package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireRunLock_secondCallerIsRejected(t *testing.T) {
	dir := t.TempDir()

	release, err := AcquireRunLock(context.Background(), dir)
	if err != nil {
		t.Fatalf("first AcquireRunLock: %v", err)
	}
	defer release()

	prev := lockWait
	lockWait = 50 * time.Millisecond
	defer func() { lockWait = prev }()

	if _, err := AcquireRunLock(context.Background(), dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("second AcquireRunLock error = %v, want ErrLocked", err)
	}
}

func TestAcquireRunLock_releaseAllowsNextRun(t *testing.T) {
	dir := t.TempDir()

	release, err := AcquireRunLock(context.Background(), dir)
	if err != nil {
		t.Fatalf("first AcquireRunLock: %v", err)
	}
	release()

	release2, err := AcquireRunLock(context.Background(), dir)
	if err != nil {
		t.Fatalf("AcquireRunLock after release: %v", err)
	}
	release2()

	if _, err := os.Stat(filepath.Join(dir, LockFilename)); err != nil {
		t.Errorf("lock file missing after release: %v", err)
	}
}

// TestAcquireRunLock_honoursContext pins that a waiting run gives up when the
// process is interrupted instead of sitting out the full lockWait.
func TestAcquireRunLock_honoursContext(t *testing.T) {
	dir := t.TempDir()

	release, err := AcquireRunLock(context.Background(), dir)
	if err != nil {
		t.Fatalf("first AcquireRunLock: %v", err)
	}
	defer release()

	prev := lockWait
	lockWait = time.Minute
	defer func() { lockWait = prev }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := AcquireRunLock(ctx, dir); err == nil {
		t.Fatal("expected an error when the context expires while waiting")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Errorf("waited %s, expected the context to cut it short", elapsed)
	}
}
