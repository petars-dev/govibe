package scheduler

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// liveDB opens the server the tests may use: GOVIBE_TEST_MARIADB_DSN, a throwaway
// server (GET_LOCK writes nothing, but the tests still never default to a real one).
func liveDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("GOVIBE_TEST_MARIADB_DSN")
	if dsn == "" {
		t.Skip("GOVIBE_TEST_MARIADB_DSN is not set: skipping the GET_LOCK tests against a real server")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("GOVIBE_TEST_MARIADB_DSN is set but the server does not answer: %v", err)
	}
	return db
}

func TestDBLockerAgainstAServer(t *testing.T) {
	db := liveDB(t)
	ctx := context.Background()
	l := DBLocker{DB: db, Prefix: "locktest"}

	release, ok, err := l.TryLock(ctx, "task")
	if err != nil || !ok {
		t.Fatalf("first TryLock: ok=%v err=%v", ok, err)
	}
	// A second holder, on another connection of the same pool, is refused at once.
	start := time.Now()
	if _, ok2, err := l.TryLock(ctx, "task"); err != nil || ok2 {
		t.Fatalf("second TryLock while held: ok=%v err=%v", ok2, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("TryLock must not wait for the lock")
	}
	// Another task's lock is independent, and so is another app's lock of the same task name.
	if rel, ok, err := l.TryLock(ctx, "other"); err != nil || !ok {
		t.Fatalf("another task: ok=%v err=%v", ok, err)
	} else {
		rel()
	}
	if rel, ok, err := (DBLocker{DB: db, Prefix: "another-app"}).TryLock(ctx, "task"); err != nil || !ok {
		t.Fatalf("another app: ok=%v err=%v", ok, err)
	} else {
		rel()
	}

	release()
	rel, ok, err := l.TryLock(ctx, "task")
	if err != nil || !ok {
		t.Fatalf("after release: ok=%v err=%v", ok, err)
	}
	rel()

	// A name past MariaDB's 64-character limit still locks (it is shortened, not rejected).
	long := "a-very-long-task-name-that-goes-on-and-on-and-on-and-on-and-on-and-on-and-on"
	rel, ok, err = l.TryLock(ctx, long)
	if err != nil || !ok {
		t.Fatalf("long name: ok=%v err=%v", ok, err)
	}
	rel()

	// Locks are given back to the server, not left on a pooled connection: after all of
	// the above, none of them is still held.
	var held sql.NullInt64
	if err := db.QueryRow("SELECT IS_USED_LOCK(?)", l.lockName("task")).Scan(&held); err != nil || held.Valid {
		t.Errorf("a lock is still held after release (holder %v, err %v)", held, err)
	}
}

// Two runners on one database are two instances of an app: the task runs on one of
// them and is skipped, with the reason, on the other.
func TestTwoInstancesRunATaskOnce(t *testing.T) {
	db := liveDB(t)
	release := make(chan struct{})
	var mu sync.Mutex
	runs := 0

	task := Task{Name: "once", Spec: "@every 10m", Run: func(context.Context) error {
		mu.Lock()
		runs++
		mu.Unlock()
		<-release
		return nil
	}}
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	clk.SetSettle(20 * time.Millisecond)
	mk := func() *Runner {
		r, err := New([]Task{task}, Options{Clock: clk, Locker: DBLocker{DB: db, Prefix: "twoinst"}, Log: quiet()})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Start(); err != nil {
			t.Fatal(err)
		}
		return r
	}
	a, b := mk(), mk()
	clk.Advance(10 * time.Minute) // both are due; both try the lock
	eventually(t, "the first run to start", func() bool { mu.Lock(); defer mu.Unlock(); return runs >= 1 })
	skipped := 0
	for _, r := range []*Runner{a, b} {
		eventually(t, "each instance to have a record", func() bool { return len(r.Infos(clk.Now())[0].Runs) > 0 || r.Infos(clk.Now())[0].Running > 0 })
		for _, run := range r.Infos(clk.Now())[0].Runs {
			if run.Status == StatusSkipped {
				skipped++
			}
		}
	}
	mu.Lock()
	n := runs
	mu.Unlock()
	if n != 1 || skipped != 1 {
		t.Fatalf("the task ran %d times and was skipped %d times across two instances; want 1 and 1", n, skipped)
	}
	close(release)
	stopAndMove(a, clk)
	stopAndMove(b, clk)
}

// An instant task: if the lock were given back when the run ends, the second instance would
// take it and run the same due time again. With the hold, exactly one instance runs it.
func TestTwoInstancesRunAQuickTaskOnce(t *testing.T) {
	db := liveDB(t)
	var mu sync.Mutex
	runs := 0
	task := Task{Name: "quick", Spec: "@every 10m", Run: func(context.Context) error {
		mu.Lock()
		runs++
		mu.Unlock()
		return nil
	}}
	clk := NewFake(utc(2026, 10, 11, 12, 0, 0))
	mk := func() *Runner {
		r, err := New([]Task{task}, Options{Clock: clk, Locker: DBLocker{DB: db, Prefix: "quickinst"}, Log: quiet()})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Start(); err != nil {
			t.Fatal(err)
		}
		return r
	}
	a, b := mk(), mk()
	clk.Advance(10 * time.Minute)
	mu.Lock()
	n := runs
	mu.Unlock()
	if n != 1 {
		t.Fatalf("a quick task ran %d times across two instances for one due time; want 1", n)
	}
	clk.Advance(10 * time.Minute) // the hold ended long ago: the next due time is taken again
	mu.Lock()
	n = runs
	mu.Unlock()
	if n != 2 {
		t.Errorf("the next due time: %d runs in all, want 2", n)
	}
	stopAndMove(a, clk)
	stopAndMove(b, clk)
	var held sql.NullInt64
	if err := db.QueryRow("SELECT IS_USED_LOCK(?)", (DBLocker{Prefix: "quickinst"}).lockName("quick")).Scan(&held); err != nil || held.Valid {
		t.Errorf("a lock is still held after Stop (holder %v, err %v)", held, err)
	}
}
