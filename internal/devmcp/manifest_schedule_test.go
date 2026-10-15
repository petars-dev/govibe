package devmcp

import (
	"testing"

	"github.com/petars-dev/govibe/internal/routeinfo"
)

// The app manifest says which commands are scheduled, and how the scheduler is set.
func TestTheAppManifestCarriesSchedules(t *testing.T) {
	doc := &routeinfo.Inspect{
		Commands: []routeinfo.InspectCmd{
			{Name: "items:prune", Short: "Delete archived items", Handler: routeinfo.InspectRef{Symbol: "x/commands.Prune"},
				Schedule: &routeinfo.InspectSchedule{Spec: "0 3 * * *", Timezone: "UTC", Overlap: "skip", Timeout: "10m0s"}},
			{Name: "items:sync", Short: "Sync", Handler: routeinfo.InspectRef{Symbol: "x/commands.Sync"}},
		},
		Scheduler: routeinfo.InspectScheduler{Enabled: true, Timezone: "Europe/Sofia", Lock: "db"},
	}
	m := appManifestOf(doc)
	if len(m.Commands) != 2 || m.Commands[0].Schedule == nil || m.Commands[0].Schedule.Spec != "0 3 * * *" || m.Commands[0].Schedule.Timeout != "10m0s" || m.Commands[1].Schedule != nil {
		t.Errorf("commands = %+v", m.Commands)
	}
	if !m.Scheduler.Enabled || m.Scheduler.Timezone != "Europe/Sofia" || m.Scheduler.Lock != "db" {
		t.Errorf("scheduler = %+v", m.Scheduler)
	}
}
