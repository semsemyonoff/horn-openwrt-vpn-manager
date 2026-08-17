package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
)

// LockFilename is the run lock kept in the config directory. The directory is
// root-owned, unlike /tmp, so the lock cannot be pre-planted or held hostage by
// a local unprivileged process.
const LockFilename = ".run.lock"

// lockWait is how long a run waits for a lock held by another process before
// giving up. Long enough to absorb two cron entries landing in the same minute,
// short enough that invocations cannot pile up behind a wedged run.
var lockWait = 60 * time.Second

// ErrLocked reports that another vpn-manager run holds the lock.
var ErrLocked = errors.New("another vpn-manager run is in progress")

// AcquireRunLock takes an exclusive lock on dir/LockFilename and returns a
// release function.
//
// Routing and subscriptions share the route-list cache and both restart system
// services, so two overlapping runs can interleave: a cron `routing run
// --with-subscriptions` refreshing a list while a cron `subscriptions run`
// builds the config from the copy it is replacing produces an applied config
// one revision behind, with nothing in either log to show for it.
func AcquireRunLock(ctx context.Context, dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	path := filepath.Join(dir, LockFilename)
	// O_NOFOLLOW: the path is only trustworthy as long as it is not a symlink
	// planted at a writable location an operator pointed the config dir at.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}

	deadline := time.Now().Add(lockWait)
	warned := false
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if !warned {
			logx.Warn("Another vpn-manager run holds %s, waiting up to %s...", path, lockWait)
			warned = true
		}
		// Poll at a second, but never past the deadline: the remaining time is
		// the whole budget once it drops below the poll interval.
		wait := min(time.Until(deadline), time.Second)
		if wait <= 0 {
			_ = f.Close()
			return nil, fmt.Errorf("%w (lock file %s)", ErrLocked, path)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("interrupted while waiting for lock: %w", ctx.Err())
		case <-time.After(wait):
		}
	}
}
