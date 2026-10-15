package devui

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"

	"github.com/petars-dev/govibe/config"
	"github.com/petars-dev/govibe/internal/devtools/schedview"
	"github.com/petars-dev/govibe/internal/scheduler"
)

var day0 = time.Date(2026, 10, 11, 18, 17, 43, 0, time.UTC)

func schedConfig(t *testing.T, yml string) config.Config {
	t.Helper()
	c, err := config.Load(fstest.MapFS{"env.yml": {Data: []byte(yml)}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// schedFixture is a real scheduler on a fake clock behind the dev routes, the way Mount wires them.
type schedFixture struct {
	r       *scheduler.Runner
	clk     *scheduler.Fake
	router  chi.Router
	release chan struct{}
	started chan struct{}
	mu      sync.Mutex
	ran     map[string]int
}

func newSchedFixture(t *testing.T, cfgYML string, start bool) *schedFixture {
	t.Helper()
	f := &schedFixture{clk: scheduler.NewFake(day0), release: make(chan struct{}), started: make(chan struct{}, 8), ran: map[string]int{}}
	f.clk.SetSettle(5 * time.Millisecond) // the digest blocks on purpose: do not wait for it
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	r, err := scheduler.New([]scheduler.Task{
		{Name: "items:sync", Short: "Sync the <items>", Spec: "0 2 * * *", Timeout: 10 * time.Minute, Run: func(context.Context) error {
			f.mu.Lock()
			f.ran["items:sync"]++
			f.mu.Unlock()
			return nil
		}},
		{Name: "reports:digest", Spec: "@every 6h", Run: func(ctx context.Context) error {
			f.started <- struct{}{}
			select {
			case <-f.release:
			case <-ctx.Done():
			}
			return nil
		}},
	}, scheduler.Options{Clock: f.clk, Log: logger})
	if err != nil {
		t.Fatal(err)
	}
	f.r = r
	if start {
		if err := r.Start(); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		select {
		case <-f.release:
		default:
			close(f.release)
		}
		r.Stop(time.Second)
	})
	cfg := schedConfig(t, cfgYML)
	status := func() schedview.Status { return schedview.Build(r, cfg, 3, f.clk.Now()) }
	f.router = chi.NewRouter()
	f.router.Get("/dev/schedule", DevSchedule(status))
	f.router.Get("/dev/schedule/data", DevScheduleData(status))
	f.router.Post("/dev/schedule/run/{name}", DevScheduleRun(r))
	return f
}

func (f *schedFixture) do(method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://localhost:8080"+target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *schedFixture) page(t *testing.T, target string) string {
	t.Helper()
	target, _, _ = strings.Cut(target, "#") // a browser keeps the fragment to itself
	rec := f.do("GET", target, nil)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("GET %s = %d %s", target, rec.Code, rec.Header().Get("Content-Type"))
	}
	return rec.Body.String()
}

func (f *schedFixture) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ran[name]
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 4000; i++ {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func mustContain(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("the page lacks %q", w)
		}
	}
}

func TestTheSchedulePageShowsTasksNextRunsAndHistory(t *testing.T) {
	f := newSchedFixture(t, "", true)
	f.clk.Advance(7 * time.Hour) // the digest started at 00:17:43 and is still running
	waitUntil(t, "the digest to start", func() bool { return len(f.started) == 1 })
	body := f.page(t, "/dev/schedule")

	mustContain(t, body,
		"Scheduler running", `class="state running"`,
		"timezone <code>UTC</code>", "lock <code>none</code>", "every instance of the app runs every due task", "2 scheduled of 3 commands",
		`id="t-items:sync"`, "items:sync", "Sync the &lt;items&gt;", // the description is escaped
		`<span class="mono">0 2 * * *</span>`, "2026-10-12 02:00:00", "in 42m",
		"@every 6h", "2026-10-12 06:17:43",
		"<td>10m</td>",
		`action="/dev/schedule/run/items:sync"`, `action="/dev/schedule/run/reports:digest"`, ">Run now<",
		"Recent runs of <code>items:sync</code>", "Recent runs of <code>reports:digest</code>",
		`<a class="tab active" href="/dev/schedule">Schedule</a>`, `href="/dev/db">DB</a>`,
	)
	// The digest is in flight: its row says so and the page refreshes itself until it ends.
	mustContain(t, body, `<meta http-equiv="refresh" content="3">`)
	if !strings.Contains(body, "<td>1</td>") {
		t.Error("a run in flight is counted")
	}
	// No history yet for the sync task: said, with where history lives.
	mustContain(t, body, "No run yet. History is kept in memory")

	// Let the digest end: the page then shows its run, stops refreshing.
	close(f.release)
	waitUntil(t, "the digest to finish", func() bool { return len(f.r.Infos(f.clk.Now())[1].Runs) == 1 })
	body = f.page(t, "/dev/schedule")
	mustContain(t, body, `<span class="badge ok">ok</span>`, "<td>schedule</td>", "2026-10-12 00:17:43")
	if strings.Contains(body, "http-equiv=\"refresh\"") {
		t.Error("nothing is running, so the page must not refresh")
	}
}

func TestTheSchedulePageShowsFailuresAndSkips(t *testing.T) {
	f := newSchedFixture(t, "", true)
	f.clk.Advance(13 * time.Hour) // digest due at 00:17, 06:17, 12:17: the first blocks, the others are skipped
	waitUntil(t, "the first run", func() bool { return len(f.started) >= 1 })
	body := f.page(t, "/dev/schedule")
	mustContain(t, body, `<span class="badge skipped">skipped</span>`, "the previous run is still going")
}

func TestTheSchedulePageSaysWhyTheSchedulerIsNotRunning(t *testing.T) {
	notStarted := newSchedFixture(t, "", false)
	body := notStarted.page(t, "/dev/schedule")
	mustContain(t, body, "Scheduler not started here", `class="state stopped"`, "serve --no-schedule", "schedule run",
		"6h after start") // an @every task that is not started has no date, only its interval
	if strings.Contains(body, "2026-10-12 06:17:43") {
		t.Error("an @every task that has not started has no next run to show")
	}

	disabled := newSchedFixture(t, "scheduler:\n  enabled: false\n  timezone: Europe/Sofia\n  lock: db\n", true)
	body = disabled.page(t, "/dev/schedule")
	mustContain(t, body, "Scheduler disabled", `class="state disabled"`, "scheduler.enabled is false", "timezone <code>Europe/Sofia</code>", "lock <code>db</code>", "one instance runs a task at a time")
}

func TestTheSchedulePageWithNothingScheduledSaysHowToDeclareOne(t *testing.T) {
	status := func() schedview.Status { return schedview.Build(nil, config.Config{}, 2, day0) }
	rec := httptest.NewRecorder()
	DevSchedule(status).ServeHTTP(rec, httptest.NewRequest("GET", "/dev/schedule", nil))
	body := rec.Body.String()
	mustContain(t, body, "Nothing scheduled", "No command declares a Schedule", "routes/console.go",
		"govibe.Cron(&#34;0 2 * * *&#34;)", "0 scheduled of 2 commands", "./app schedule list")
	// The sample is neutral: nothing of a shop.
	for _, bad := range []string{"orders", "shop", "customer"} {
		if strings.Contains(strings.ToLower(body), bad) {
			t.Errorf("the page names a sample domain (%q)", bad)
		}
	}
	if strings.Contains(body, "<table>") {
		t.Error("no table without tasks")
	}
}

func TestRunNowRunsTheTaskThroughTheSchedulerAndShowsInTheHistory(t *testing.T) {
	f := newSchedFixture(t, "scheduler:\n  enabled: false\n", false) // works with the scheduler off too
	rec := f.do("POST", "/dev/schedule/run/items:sync", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dev/schedule?ran=items%3Async#t-items:sync" {
		t.Fatalf("a form post is answered with a redirect back to the page: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	waitUntil(t, "the manual run", func() bool { return f.count("items:sync") == 1 && len(f.r.Infos(f.clk.Now())[0].Runs) == 1 })

	body := f.page(t, rec.Header().Get("Location"))
	mustContain(t, body, "Started items:sync.", `<span class="badge ok">ok</span>`, "<td>manual</td>")
	if run := f.r.Infos(f.clk.Now())[0].Runs[0]; run.Trigger != scheduler.TriggerManual || run.Status != scheduler.StatusOK {
		t.Errorf("run = %+v", run)
	}
}

func TestRunNowAnswersJSONForScripts(t *testing.T) {
	f := newSchedFixture(t, "", true)
	accept := map[string]string{"Accept": "application/json"}

	rec := f.do("POST", "/dev/schedule/run/items:sync", accept)
	if rec.Code != http.StatusAccepted || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("started: %d %s", rec.Code, rec.Body)
	}
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out["status"] != "started" || out["task"] != "items:sync" {
		t.Errorf("body %s (%v)", rec.Body, err)
	}
	waitUntil(t, "the run", func() bool { return f.count("items:sync") == 1 })

	// Still running: refused, not started again, not a 500.
	if rec := f.do("POST", "/dev/schedule/run/reports:digest", accept); rec.Code != http.StatusAccepted {
		t.Fatalf("digest: %d %s", rec.Code, rec.Body)
	}
	<-f.started
	rec = f.do("POST", "/dev/schedule/run/reports:digest", accept)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "already running") {
		t.Errorf("a task that is running must be refused with 409: %d %s", rec.Code, rec.Body)
	}
	// The same refusal from the page's form is a message on the page, not an error page.
	rec = f.do("POST", "/dev/schedule/run/reports:digest", nil)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "refused=reports%3Adigest") {
		t.Fatalf("form refusal: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	body := f.page(t, rec.Header().Get("Location"))
	mustContain(t, body, "reports:digest is still running (overlap: skip), so it was not started again.")

	// No such task.
	if rec := f.do("POST", "/dev/schedule/run/nope", accept); rec.Code != http.StatusNotFound {
		t.Errorf("unknown task: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do("POST", "/dev/schedule/run/nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown task from a form: %d", rec.Code)
	}
	// It is a POST: a GET must not run anything.
	if rec := f.do("GET", "/dev/schedule/run/items:sync", nil); rec.Code == http.StatusOK || rec.Code == http.StatusAccepted || f.count("items:sync") != 1 {
		t.Errorf("GET ran or served something: %d (runs %d)", rec.Code, f.count("items:sync"))
	}
}

// The endpoint runs the app's code, so a page on another site must not be able to post to it.
func TestRunNowRefusesCrossSiteRequests(t *testing.T) {
	f := newSchedFixture(t, "", true)
	for name, hdr := range map[string]map[string]string{
		"Sec-Fetch-Site cross-site": {"Sec-Fetch-Site": "cross-site"},
		"Sec-Fetch-Site same-site":  {"Sec-Fetch-Site": "same-site"},
		"another Origin":            {"Origin": "https://evil.example"},
		"a null Origin":             {"Origin": "null"},
	} {
		if rec := f.do("POST", "/dev/schedule/run/items:sync", hdr); rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", name, rec.Code)
		}
	}
	if n := f.count("items:sync"); n != 0 {
		t.Fatalf("a refused request ran the task %d time(s)", n)
	}
	// What the page's own form sends, and what curl sends, is accepted.
	for name, hdr := range map[string]map[string]string{
		"same origin":     {"Sec-Fetch-Site": "same-origin", "Origin": "http://localhost:8080"},
		"typed address":   {"Sec-Fetch-Site": "none"},
		"no headers":      nil,
		"matching Origin": {"Origin": "http://localhost:8080"},
	} {
		before := f.count("items:sync")
		if rec := f.do("POST", "/dev/schedule/run/items:sync", hdr); rec.Code != http.StatusSeeOther {
			t.Errorf("%s: %d, want the redirect", name, rec.Code)
		}
		waitUntil(t, name, func() bool { return f.count("items:sync") == before+1 })
	}
}

func TestTheNoticeOnThePageIsOnlyEverATaskName(t *testing.T) {
	f := newSchedFixture(t, "", true)
	body := f.page(t, "/dev/schedule?ran="+url.QueryEscape("<script>alert(1)</script>")+"&refused=x")
	if strings.Contains(body, "alert(1)") || strings.Contains(body, `class="notice"`) {
		t.Error("the query string must not put its own words on the page")
	}
}

func TestTheDataEndpointIsTheSameDocument(t *testing.T) {
	f := newSchedFixture(t, "", true)
	rec := f.do("GET", "/dev/schedule/data", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	var st schedview.Status
	dec := json.NewDecoder(rec.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&st); err != nil {
		t.Fatal(err)
	}
	if st.State != "running" || st.Scheduled != 2 || st.Commands != 3 || len(st.Tasks) != 2 || st.Tasks[0].Name != "items:sync" || st.Tasks[0].Next == nil {
		t.Errorf("status = %+v", st)
	}
}

func TestTheDevIndexHasASchedulerCard(t *testing.T) {
	card := func(st schedview.Status) string {
		body := devIndexBody(t, DevInfo{AppName: "demo", Schedule: func() schedview.Status { return st }}, nil)
		return between(body, `<h2><a href="/dev/schedule">`, "</div>")
	}
	f := newSchedFixture(t, "", true)
	running := schedview.Build(f.r, schedConfig(t, ""), 3, f.clk.Now())
	got := card(running)
	for _, w := range []string{"Scheduler</a>", "2 scheduled of 3 commands", ", running", "Next: <code>reports:digest</code> in 6h"} {
		if !strings.Contains(got, w) {
			t.Errorf("running card lacks %q:\n%s", w, got)
		}
	}
	// the soonest of the two is the digest (00:17), not the sync (02:00)
	stopped := newSchedFixture(t, "", false)
	if got := card(schedview.Build(stopped.r, schedConfig(t, ""), 3, stopped.clk.Now())); !strings.Contains(got, "not started in this process") || !strings.Contains(got, "Next: <code>items:sync</code>") {
		t.Errorf("stopped card:\n%s", got)
	}
	if got := card(schedview.Build(stopped.r, schedConfig(t, "scheduler:\n  enabled: false\n"), 3, stopped.clk.Now())); !strings.Contains(got, "disabled (<code>scheduler.enabled</code> is false)") {
		t.Errorf("disabled card:\n%s", got)
	}
	got = card(schedview.Build(nil, config.Config{}, 1, day0))
	if !strings.Contains(got, "No command is scheduled") || !strings.Contains(got, "routes/console.go") {
		t.Errorf("empty card:\n%s", got)
	}
	// The tab bar of the front door has the Schedule tab, and an app that is not told has no card.
	body := devIndexBody(t, DevInfo{AppName: "demo"}, nil)
	mustContain(t, body, `<a class="tab" href="/dev/schedule">Schedule</a>`)
	if strings.Contains(body, "Scheduler</a>") {
		t.Error("no card when the page is not given a scheduler reader")
	}
}

func (f *schedFixture) doHost(host, target string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "http://"+host+target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// DNS rebinding: a page on a hostile name that resolves to 127.0.0.1 is "same-origin" with the
// dev server as far as the browser's own headers go. The endpoint also wants a host that is the
// machine itself.
func TestRunNowRefusesAHostThatIsNotThisMachine(t *testing.T) {
	f := newSchedFixture(t, "", true)
	rebound := map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "http://evil.example:8080"}
	for _, host := range []string{"evil.example:8080", "evil.example", "192.168.1.20:8080", "0.0.0.0:8080", "localhost.evil.example"} {
		rec := f.doHost(host, "/dev/schedule/run/items:sync", rebound)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "localhost") {
			t.Errorf("host %q: %d %q, want 403 saying to use localhost", host, rec.Code, rec.Body.String())
		}
	}
	if n := f.count("items:sync"); n != 0 {
		t.Fatalf("a refused request ran the task %d time(s)", n)
	}
	for _, host := range []string{"localhost:8080", "localhost", "LOCALHOST:8080", "127.0.0.1:18080", "127.1.2.3", "[::1]:8080", "[::1]", "app.localhost:8080"} {
		before := f.count("items:sync")
		if rec := f.doHost(host, "/dev/schedule/run/items:sync", nil); rec.Code != http.StatusSeeOther {
			t.Errorf("host %q: %d, want the redirect", host, rec.Code)
		}
		waitUntil(t, host, func() bool { return f.count("items:sync") == before+1 })
	}
}

func TestLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "localhost:3000": true, "127.0.0.1": true, "127.0.0.1:8080": true, "::1": true, "[::1]:8080": true,
		"foo.localhost:80": true, "": false, "example.com": false, "example.com:80": false, "10.0.0.5:8080": false,
		"localhost.example.com": false, "127.0.0.1.nip.io": false, "[fe80::1]:80": false,
	} {
		if got := loopbackHost(host); got != want {
			t.Errorf("loopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// "Run now" works for any name a command can have: the page escapes it as one path segment
// and the handler unescapes it (a name with a space, a slash, a question mark, an accent or a
// percent sign used to answer 404, or ran another task).
func TestRunNowReachesCommandsWithUnusualNames(t *testing.T) {
	names := []string{"café:sync", "a/b", "q?x#y", "sp ace", "items%3Async", "p%2Fq", "plain:name", "<b>x</b>"}
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	clk := scheduler.NewFake(day0)
	var mu sync.Mutex
	ran := map[string]int{}
	var tasks []scheduler.Task
	for _, n := range names {
		tasks = append(tasks, scheduler.Task{Name: n, Spec: "@daily", Run: func(context.Context) error {
			mu.Lock()
			ran[n]++
			mu.Unlock()
			return nil
		}})
	}
	r, err := scheduler.New(tasks, scheduler.Options{Clock: clk, Log: logger})
	if err != nil {
		t.Fatal(err)
	}
	status := func() schedview.Status { return schedview.Build(r, schedConfig(t, ""), len(names), clk.Now()) }
	router := chi.NewRouter()
	router.Get("/dev/schedule", DevSchedule(status))
	router.Post("/dev/schedule/run/{name}", DevScheduleRun(r))

	page := httptest.NewRecorder()
	router.ServeHTTP(page, httptest.NewRequest("GET", "http://localhost:8080/dev/schedule", nil))
	actions := regexp.MustCompile(`action="(/dev/schedule/run/[^"]*)"`).FindAllStringSubmatch(page.Body.String(), -1)
	if len(actions) != len(names) {
		t.Fatalf("%d forms for %d tasks", len(actions), len(names))
	}
	for i, a := range actions {
		// html/template writes the attribute value escaped for HTML; a browser unescapes it
		// before it posts, so do the same.
		target := html.UnescapeString(a[1])
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("POST", "http://localhost:8080"+target, nil))
		if rec.Code != http.StatusSeeOther {
			t.Errorf("%q: POST %s = %d, want the redirect", names[i], target, rec.Code)
		}
	}
	waitUntil(t, "every unusually named task to run", func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, n := range names {
			if ran[n] != 1 {
				return false
			}
		}
		return true
	})
}

// The "refused" notice is only shown while the task is running: an address cannot make the
// page say so.
func TestTheRefusedNoticeOnlyShowsForARunningTask(t *testing.T) {
	f := newSchedFixture(t, "", true)
	rec := f.do("GET", "/dev/schedule?refused=items:sync", nil)
	if strings.Contains(rec.Body.String(), "is still running") {
		t.Error("items:sync is not running, so the page must not say it is")
	}
}
