package scheduler

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"time"
)

// Locker serialises a task across the instances of an app.
type Locker interface {
	// TryLock takes the named lock without waiting. ok is false when another holder
	// has it; release must then not be called. release gives the lock back.
	TryLock(ctx context.Context, name string) (release func(), ok bool, err error)
}

// maxLockName is MariaDB's limit on the length of a GET_LOCK name.
const maxLockName = 64

// DBLocker is the opt-in `scheduler.lock: db`: one named MariaDB lock per task
// (GET_LOCK), held on a connection of its own for the duration of a run. A lock
// belongs to its session, so it is released when the run ends, or by the server when
// the connection drops (the lock is then lost, and a second instance may start the
// task: it is a guard against two instances, not a guarantee).
type DBLocker struct {
	DB     *sql.DB
	Prefix string // names the app, so two apps on one database do not share locks
}

// lockName is "govibe:<prefix>:<task>", shortened with a hash when it would not fit.
func (l DBLocker) lockName(task string) string {
	name := "govibe:" + task
	if l.Prefix != "" {
		name = "govibe:" + l.Prefix + ":" + task
	}
	if len(name) <= maxLockName {
		return name
	}
	sum := sha1.Sum([]byte(name))
	h := hex.EncodeToString(sum[:])[:12]
	return name[:maxLockName-len(h)-1] + ":" + h
}

// TryLock implements Locker.
func (l DBLocker) TryLock(ctx context.Context, task string) (func(), bool, error) {
	name := l.lockName(task)
	conn, err := l.DB.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	var got sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", name).Scan(&got); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("GET_LOCK %q: %w", name, err)
	}
	if !got.Valid || got.Int64 != 1 {
		_ = conn.Close() // 0: another session holds it; NULL: the server could not say
		return nil, false, nil
	}
	return func() {
		// The run's context may be over by now; releasing must not depend on it.
		rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released sql.NullInt64
		err := conn.QueryRowContext(rctx, "SELECT RELEASE_LOCK(?)", name).Scan(&released)
		if err != nil || !released.Valid || released.Int64 != 1 {
			// Do not hand a session that may still hold the lock back to the pool:
			// discard it, which closes the connection and so releases the lock.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}, true, nil
}
