package codegraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/petars-dev/govibe/internal/routeinfo"
)

// commandTable is the application's commands, or the fact that there is no table.
type commandTable struct {
	absent   bool
	commands []routeinfo.InspectCmd
}

// commandTable returns the commands, once per run.
func (w *world) commandTable() (*commandTable, error) {
	return w.commandsV.get(func() (*commandTable, error) {
		if w.x.opts.CommandSource == nil {
			return &commandTable{absent: true}, nil
		}
		cs, err := w.x.opts.CommandSource()
		if err != nil {
			return nil, err
		}
		cs = append([]routeinfo.InspectCmd(nil), cs...)
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].Name < cs[j].Name })
		return &commandTable{commands: cs}, nil
	})
}

// commandsLayer turns the application's CLI commands into `command` nodes: its name, the
// function that runs it, and, for a scheduled one, the schedule it declared and the zone it
// is read in. The table comes from the binary (`inspect --json`), like the route table, so a
// schedule the scheduler would refuse is not here: it never gets as far as a table.
type commandsLayer struct{}

func (commandsLayer) name() string   { return "commands" }
func (commandsLayer) deps() []string { return []string{"code"} }

// fingerprint hashes the command table, which is fixed once the app is declared: it changes
// only across a restart, when the whole graph is rebuilt anyway.
func (commandsLayer) fingerprint(w *world) (string, error) {
	ct, err := w.commandTable()
	if err != nil {
		return "", err
	}
	if ct.absent {
		return "no-commands", nil
	}
	h := sha256.New()
	for _, c := range ct.commands {
		fmt.Fprintf(h, "%s|%s|%s|%s:%d", c.Name, c.Short, c.Handler.Symbol, c.Handler.File, c.Handler.Line)
		if s := c.Schedule; s != nil {
			fmt.Fprintf(h, "|%s|%s|%s|%s|%s", s.Spec, s.Timezone, s.Overlap, s.Timeout, s.Every)
		}
		fmt.Fprintln(h)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func (commandsLayer) build(ctx context.Context, w *world) (*layerOutput, error) {
	ct, err := w.commandTable()
	if err != nil {
		return nil, err
	}
	if ct.absent {
		return &layerOutput{skipped: "no command table is available (an offline run without the application's inspect document); commands left as they were"}, nil
	}
	ci, err := w.code()
	if err != nil {
		return nil, fmt.Errorf("code index: %w", err)
	}
	set := newQuadSet("commands")
	for _, c := range ct.commands {
		id := commandID(c.Name)
		set.add(id, PredType, KindCommand)
		set.str(id, PredName, c.Name)
		if c.Short != "" {
			set.str(id, PredDescription, c.Short)
		}
		set.str(id, PredOrigin, OriginApp)
		if fi := w.resolveFunc(ci, c.Handler.Ref()); fi != nil {
			set.add(id, PredRunBy, fi.id)
		} else if sym := externalSymbol(ci, c.Handler.Ref()); sym != "" {
			// Not application code: say what the runtime sees instead of inventing a link.
			set.str(id, PredHandler, sym)
		}
		s := c.Schedule
		set.flag(id, PredScheduled, s != nil)
		if s == nil {
			continue
		}
		set.str(id, PredSchedule, s.Spec)
		set.str(id, PredTimezone, s.Timezone)
		set.str(id, PredOverlap, s.Overlap)
		if s.Timeout != "" {
			set.str(id, PredTimeout, s.Timeout)
		}
		if s.Every != "" {
			set.str(id, PredEvery, s.Every)
		}
	}
	return &layerOutput{quads: set.quads}, nil
}
