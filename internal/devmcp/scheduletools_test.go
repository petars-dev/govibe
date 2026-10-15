package devmcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/config"
	"github.com/petars-dev/govibe/internal/devtools/schedview"
	"github.com/petars-dev/govibe/internal/scheduler"
)

var scheduleDay0 = time.Date(2026, 10, 11, 18, 17, 43, 0, time.UTC)

// scheduledOptions is a real scheduler on a fake clock, started, with one run behind it.
func scheduledOptions(t *testing.T) Options {
	t.Helper()
	clk := scheduler.NewFake(scheduleDay0)
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	r, err := scheduler.New([]scheduler.Task{
		{Name: "items:sync", Short: "Sync the items", Spec: "0 2 * * *", Timeout: 10 * time.Minute, Run: func(context.Context) error { return nil }},
		{Name: "reports:digest", Spec: "@every 6h", Overlap: scheduler.Allow, Run: func(context.Context) error { return errors.New("smtp is down") }},
	}, scheduler.Options{Clock: clk, Log: logger})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Stop(time.Second) })
	clk.Advance(7 * time.Hour)
	cfg, err := config.Load(fstest.MapFS{"env.yml": {Data: []byte("")}})
	if err != nil {
		t.Fatal(err)
	}
	return Options{Schedule: func() schedview.Status { return schedview.Build(r, cfg, 4, clk.Now()) }}
}

func TestScheduleStatusReportsWhatIsScheduledAndHowItWentWithoutRunningAnything(t *testing.T) {
	h := NewHandler(nil, nil, nil, scheduledOptions(t))
	res := call(t, h, "schedule_status", nil).ok(t, "schedule_status")
	// Decode through the document type: what the tool returns is what the dev page's JSON carries.
	raw, _ := json.Marshal(res.StructuredContent)
	var st schedview.Status
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if st.State != schedview.StateRunning || st.Timezone != "UTC" || st.Lock != "none" || st.Scheduled != 2 || st.Commands != 4 || len(st.Tasks) != 2 {
		t.Fatalf("status = %+v", st)
	}
	sync, digest := st.Tasks[0], st.Tasks[1]
	if sync.Name != "items:sync" || sync.Schedule != "0 2 * * *" || sync.Timezone != "UTC" || sync.Overlap != "skip" || sync.Timeout != "10m" || sync.Next == nil || sync.Next.Format(time.DateTime) != "2026-10-12 02:00:00" || sync.NextIn != "in 42m" {
		t.Errorf("sync = %+v", sync)
	}
	if digest.Overlap != "allow" || digest.Every != "6h" || len(digest.Runs) != 1 || digest.Runs[0].Status != "error" || digest.Runs[0].Detail != "smtp is down" || digest.Runs[0].Trigger != "schedule" {
		t.Errorf("digest = %+v", digest)
	}
	// Reading the status ran nothing: the sync task has no run at all.
	if len(sync.Runs) != 0 {
		t.Errorf("schedule_status must never run a command: %+v", sync.Runs)
	}
}

func TestScheduleStatusCanBeLimitedToOneCommand(t *testing.T) {
	h := NewHandler(nil, nil, nil, scheduledOptions(t))
	res := call(t, h, "schedule_status", map[string]any{"name": "reports:digest"}).ok(t, "one command")
	tasks, _ := res.StructuredContent["tasks"].([]any)
	if len(tasks) != 1 || tasks[0].(map[string]any)["name"] != "reports:digest" {
		t.Errorf("tasks = %v", tasks)
	}
	bad := call(t, h, "schedule_status", map[string]any{"name": "nope"})
	if !bad.IsError || !strings.Contains(bad.text(), `no scheduled command named "nope"`) || !strings.Contains(bad.text(), "items:sync, reports:digest") {
		t.Errorf("an unknown command names the scheduled ones: %q", bad.text())
	}
}

func TestScheduleStatusWithNoSchedulerSaysSoAndHowToDeclareOne(t *testing.T) {
	for name, opt := range map[string]Options{
		"no reader":       {},
		"nothing to read": {Schedule: func() schedview.Status { return schedview.Build(nil, config.Config{}, 2, scheduleDay0) }},
	} {
		h := NewHandler(nil, nil, nil, opt)
		res := call(t, h, "schedule_status", nil).ok(t, name)
		tasks, ok := res.StructuredContent["tasks"].([]any)
		if res.StructuredContent["state"] != "none" || !ok || len(tasks) != 0 {
			t.Errorf("%s: %v", name, res.StructuredContent)
		}
		if how, _ := res.StructuredContent["how_to_declare"].(string); !strings.Contains(how, "govibe.Cron(") || !strings.Contains(how, "routes/console.go") {
			t.Errorf("%s: how to declare: %q", name, how)
		}
	}
	h := NewHandler(nil, nil, nil, Options{})
	if res := call(t, h, "schedule_status", map[string]any{"name": "x"}); !res.IsError || !strings.Contains(res.text(), "no command is scheduled") {
		t.Errorf("a name asked of an empty scheduler: %q", res.text())
	}
}

func TestScheduleStatusIsAReadOnlyToolTheServerAnnounces(t *testing.T) {
	h := NewHandler(nil, nil, nil, Options{})
	res, perr := rpc(t, h, "tools/list", map[string]any{})
	if perr != nil {
		t.Fatal(perr.Message)
	}
	var out struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Properties map[string]any `json:"properties"`
			} `json:"inputSchema"`
			Annotations struct {
				ReadOnlyHint bool `json:"readOnlyHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, tl := range out.Tools {
		if tl.Name != "schedule_status" {
			continue
		}
		found = true
		if !tl.Annotations.ReadOnlyHint || len(tl.Description) < 80 || tl.InputSchema.Properties["name"] == nil {
			t.Errorf("schedule_status = %+v", tl)
		}
		if _, scoped := tl.InputSchema.Properties["scope"]; scoped {
			t.Error("schedule_status is not a graph tool: it takes no scope")
		}
		for _, w := range []string{"never runs a command", "last 20 runs", "how to declare"} {
			if !strings.Contains(tl.Description, w) {
				t.Errorf("the description should say %q: %s", w, tl.Description)
			}
		}
	}
	if !found {
		t.Fatal("schedule_status is not offered, though the server has no store: it needs none")
	}
}

// The framework publishes its tools in the manifest the project's graph is labelled with.
func TestTheManifestListsScheduleStatus(t *testing.T) {
	tools, err := Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools {
		if tl.Name == "schedule_status" {
			if tl.Description == "" {
				t.Error("no description")
			}
			return
		}
	}
	t.Errorf("Tools does not list schedule_status: %v", tools)
}
