# GoVibe Commands and Scheduler

A CLI command is one declaration. Put a schedule on it and the framework also runs it, on a clock, while the app is up. The same function serves
`./app items:prune` and the scheduled run, so a task is always testable and always runnable by hand.

## A command is one declaration
```go
// routes/console.go
func Console(rt *govibe.Runtime) []govibe.Command {
	svc := itemsvc.New(itemstore.New(rt.DB)) // a nil rt.DB is fine until a query runs
	return []govibe.Command{
		{Use: "items:sync", Short: "Sync items from the upstream feed", Run: commands.SyncItems(svc)},
		{Use: "items:prune", Short: "Delete archived items", Schedule: govibe.Cron("0 3 * * *"), Run: commands.PruneItems(svc)},
	}
}

// main.go: App{..., Commands: routes.Console}
```
- Run it by hand: `./app items:sync [flags] [args]`. `./app help` lists the commands. The context ends on SIGINT or SIGTERM.
- Flags use the standard `flag` package: `Flags: func(fs *flag.FlagSet) { fs.StringVar(&day, "day", "yesterday", "which day") }`. What is left after the
  flags is `args`.
- Name commands `noun:verb`. These names are the framework's and cannot be used: `serve`, `migrate`, `routes`, `schedule`, `version`, `inspect`, `help`. An
  unnamed command, a duplicate and a command with no `Run` fail the boot.
- Registration must not open connections or read secrets: the tool and `schedule list` call `Commands` with no database to list what exists (`rt.DB` is nil then).
- A command is the CLI twin of a controller: a thin adapter in `commands/` that calls a business action or service. It imports the root `govibe` package for
  `RunFunc` and `Runtime` and nothing else of the framework, and never storage. The graph reports a command that breaks this (`layering`, `framework.import`).

## Putting a command on a schedule
```go
Schedule: govibe.Cron("*/5 * * * *")                                 // a cron expression
Schedule: govibe.Every(10 * time.Minute)                             // an interval, counted from the start of the process
Schedule: &govibe.Schedule{Spec: "@daily", Overlap: govibe.Allow, Timeout: 10 * time.Minute}
```
A scheduled run calls the same `Run` with the flags' defaults and no arguments (the flags are declared once, so overlapping runs share them).

| Spec | Meaning |
| --- | --- |
| `*/5 * * * *` | five fields: minute, hour, day of month, month, day of week (Sunday is `0` or `SUN`). If both day fields are restricted, either may match (standard cron). No `L`, `W` or `#`, and no seconds field |
| `@hourly` `@daily` `@weekly` `@monthly` `@yearly` | the usual descriptors (`@daily` is 00:00; `@weekly` is Sunday 00:00) |
| `@every 10m` | every 10 minutes from the moment the scheduler starts (a Go duration, at least `1m`) |
| `CRON_TZ=Europe/Sofia 0 9 * * *` | a time zone for this command, over `scheduler.timezone` (a bare `CRON_TZ=Zone` with no schedule is an error) |

A spec is **validated when the app boots**, by `schedule list` and by `inspect --json`: an invalid one fails with the command's name and what is wrong, never a
silently missing task. Beyond bad syntax, a spec that can never fire (`0 0 31 2 *`), an `@every` under a minute, a zone prefix that names no zone or says `Local`, and a step wider than its field (`*/90 * * * *` would run once an hour) are rejected too. The zone database is
embedded in the binary, so zones work in a minimal container.

- **Time zone.** `scheduler.timezone` (default `UTC`) is the zone every spec is read in; a `CRON_TZ=` prefix wins for that command.
- **Daylight saving.** A time that does not exist on the day the clocks go forward is skipped (nothing is caught up). When the clocks go back and an hour repeats, a spec
  that names its hours (`30 3 * * *`, `*/15 3 * * *`) runs once per reading of the clock, at the first pass; the second pass over the same readings is skipped and recorded. A spec that allows every
  hour (`30 * * * *`, `@hourly`, `0 0-23 * * *`) follows real hours and so runs in the repeated hour too. `schedule list` leaves the repeated readings out.
- **Overlap.** `Skip` (the default): a run that is still going when the task is due again makes the new one skip, and the log says so. `Allow` runs both.
- **Timeout.** A deadline on the run's context. A command that returns an error after it is `timeout`; one that ignores its context cannot be killed, and is reported as `overrun`
  when it finally returns success (a command that swallows the cancellation and returns nil looks the same).
- **Failures.** An error or a panic is recovered and logged with the task, trigger, status and duration (a panic with its stack); a command that ends its goroutine without returning (`t.FailNow` inside a command under test) is an `error` run; the scheduler keeps going. There are no retries.
- **Missed runs.** Nothing is caught up after downtime, a restart or a suspended machine, and nothing is run late: a timer that fires more than 30 seconds after its due time (the machine slept through the night) does not run a daily 03:00 job at noon. The due times that passed are skipped and recorded once as `missed`.
- **The history.** The same refusal again is one record with a count (`skipped ×29`), so a task that is blocked for an hour does not push the run that blocked it out of the last 20.

| Status | Meaning |
| --- | --- |
| `ok`, `error`, `panic` | the run ended that way |
| `timeout` | the deadline passed and the command returned an error |
| `overrun` | the deadline passed and the command still returned success |
| `skipped` | the previous run was still going, a wall-clock hour repeated, or another instance held the lock |
| `missed` | a due time passed while nothing was watching (not run, not caught up) |

## When it runs
- `./app serve` starts the scheduler with the server, unless `scheduler.enabled` is `false` or you pass `serve --no-schedule`. The port is bound first, so a process that cannot
  serve runs no task.
- `./app schedule run` runs **only** the scheduler, in the foreground: a worker process. It serves no HTTP, mounts no dev tools and runs no migrations (like any command, it
  expects `serve` or `migrate up` to have prepared the schema). It refuses to start when no command is scheduled or `scheduler.enabled` is `false`.
- On SIGINT or SIGTERM both stop gracefully: nothing new starts, running tasks get up to `scheduler.shutdown_timeout` to finish, then their contexts are cancelled and they get
  a few seconds more to notice. `serve` does this while the HTTP server drains, then calls `App.Shutdown`.
- **Several instances.** Each instance runs every due task. Either run the scheduler on one instance (`serve --no-schedule` on the rest), or set `scheduler.lock: db`: a named
  MariaDB lock per task (`GET_LOCK`), taken without waiting and held, on a connection of its own, for the duration of a run. A task that cannot take it is skipped ("another instance holds
  the lock"). A finished scheduled run keeps the lock until 10 seconds after its due time, so a second instance a few milliseconds behind finds the due time taken and does not run it again (a quick task would otherwise run on both); a manual
  run gives it back at once. It is a guard against two instances running a task, not a guarantee: if the database connection drops mid-run the lock is lost, and instances whose clocks differ by more than the hold can still both run. It needs a database (`serve`
  refuses to start without one) and it is **never on by default**, even when a database is configured. The log line printed when the scheduler starts says which is in force.

## Seeing what is scheduled
```text
$ ./app schedule list
NAME         SCHEDULE      TIMEZONE  NEXT RUN             OVERLAP  TIMEOUT  DESCRIPTION
items:prune  0 3 * * *     UTC       2026-10-12 03:00:00  skip     -        Delete archived items
items:poll   @every 10m    UTC       10m after start      skip     2m       Poll the upstream feed
2 scheduled of 3 commands. Scheduler enabled; timezone UTC.
```
`--json` prints one document, and `--next N` lists the next N due times of every task. The listing is **computed from the declarations and the current configuration**: it needs no
database, starts nothing, and does not ask a running server, so it works in production. An `@every` task shows "10m after start" because it counts from the start of the process.

The live state is in a running process:
- `/dev/schedule` (dev builds): the scheduler's state, every task with its next run, and the last 20 runs of each (status, trigger, due, started, duration, detail), with a
  **Run now** button that goes through the same code path as the scheduler and the CLI (overlap policy, timeout, lock and recovery all apply). It refuses a request from another site and one addressed to a host that is not this
  machine (open the panel through `localhost`, `127.0.0.1` or `[::1]`): the dev server listens on every interface, and the button runs your code. `GET /dev/schedule/data` is the same facts as JSON.
- The `schedule_status` MCP tool gives an agent the same document.
- In production the history is the log: one structured line per run.
```text
time=2026-10-12 03:00:00 level=info msg=task finished app=myapp duration=1.2s status=ok task=items:prune trigger=schedule
time=2026-10-12 03:10:41 level=error msg=task failed app=myapp detail=upstream: 503 duration=3.1s status=error task=items:poll trigger=schedule
```
History is kept in memory (20 runs per task) and is not persisted.

## In the project graph and the gate
Commands are in the graph: a `command` node per command (name, description, schedule, zone, overlap, timeout) linked to the function that runs it (`run_by`). `graph_context` and
`graph_impact` on a table list the commands that reach it, scheduled ones first, with whether each reads or writes it, because a scheduled command that writes a table runs with
nobody watching. `/dev/graph/reports/commands` is the generated table, the app map draws commands with their schedules, and `docs.command_undocumented` (info) reports a command no doc
mentions: name it in backticks (`` `items:prune` ``, or as typed `` `./app items:prune` ``). A name with a colon is recognised in running text; a name without one (`cleanup`) needs the typed form, because a plain word in backticks is just a word.

## Testing your commands
Run one by name, the way a person does:
```go
err := govibe.Run(ctx, newApp(), []string{"items:prune"}, &out, &errOut)
```
Prove a schedule with a fake clock. `govibetest.Boot` starts the scheduler on a clock the test moves:
```go
func TestPruneRunsNightly(t *testing.T) {
	srv := govibetest.Boot(t, newApp(), govibetest.WithTime(time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)))
	srv.Clock.AdvanceTo(time.Date(2026, 3, 2, 3, 0, 0, 0, time.UTC)) // runs "items:prune" at exactly 03:00
	if runs := srv.Runs("items:prune"); len(runs) != 1 || runs[0].Status != "ok" {
		t.Fatalf("runs = %+v", runs)
	}
}
```
- Nothing scheduled runs until the test advances the clock; the clock starts at `govibetest.Start` (2026-01-01 00:00 UTC) unless `WithTime` says otherwise.
- `Advance` and `AdvanceTo` fire every due time on the way, in order, and return once the runs they started have finished, so the test can assert at once. The wait is real time, up to five
  seconds per due run; a test that blocks a command on purpose (to test overlap) passes `govibetest.WithSettle(10 * time.Millisecond)`.
- `Boot` opens no database, so a `scheduler.lock` of `db` in your `env.yml` is ignored there, and `scheduler.enabled: false` is respected. An invalid schedule fails the test at `Boot`.
- Your command bodies take what they need from the services you construct in `Console`, so a test passes fakes the usual way.

## Settings
| Key | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `scheduler.enabled` | `SCHEDULER_ENABLED` | `true` | `serve` starts the scheduler; `false` leaves every scheduled command to the CLI (and `schedule run` refuses) |
| `scheduler.timezone` | `SCHEDULER_TIMEZONE` | `UTC` | the IANA zone specs are read in; a `CRON_TZ=` prefix overrides it per command |
| `scheduler.shutdown_timeout` | `SCHEDULER_SHUTDOWN_TIMEOUT` | `30s` | how long a stopping process waits for running tasks before cancelling them: a Go duration (`30s`, `2m`) or a number of seconds; `0` does not wait |
| `scheduler.lock` | `SCHEDULER_LOCK` | `none` | `none`, or `db` for one MariaDB lock per task across instances |

## What v1 does not do
No seconds field, no retries, no persisted run history, no catch-up of missed runs, no run-on-start, no jitter, and no lock for databases other than MariaDB. A job queue with retries is a
separate feature.
