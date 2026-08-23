package pipelines_test

import (
	"strings"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
)

// TestCheckSpoolSpace_OK exercises the common case against the real
// filesystem via t.TempDir(): a small, obviously-satisfiable request
// must pass on any CI runner.
func TestCheckSpoolSpace_OK(t *testing.T) {
	dir := t.TempDir()
	if err := pipelines.CheckSpoolSpace(dir, 1024); err != nil {
		t.Fatalf("want nil error for a trivially small request, got %v", err)
	}
}

// TestCheckSpoolSpace_ZeroNeeded covers titles with an unknown
// (zero/negative) SizeBytes -- MakeMKV scan info that doesn't report a
// size shouldn't block a rip the check can't actually evaluate.
func TestCheckSpoolSpace_ZeroNeeded(t *testing.T) {
	dir := t.TempDir()
	if err := pipelines.CheckSpoolSpace(dir, 0); err != nil {
		t.Fatalf("want nil error for neededBytes=0, got %v", err)
	}
}

// TestCheckSpoolSpace_InsufficientSpace reproduces the live failure
// this check exists to catch: a rip whose title size exceeds free
// space at the destination now fails fast with a readable error
// instead of MakeMKV silently dying to ENOSPC 10+ minutes in.
func TestCheckSpoolSpace_InsufficientSpace(t *testing.T) {
	dir := t.TempDir()
	// No real filesystem has an exabyte free; this must always exceed
	// available space regardless of the runner's actual disk size.
	const impossiblyLarge = 1 << 60
	err := pipelines.CheckSpoolSpace(dir, impossiblyLarge)
	if err == nil {
		t.Fatal("want error for a request far exceeding free space, got nil")
	}
	if !strings.Contains(err.Error(), "not enough space") {
		t.Errorf("error = %q, want it to mention insufficient space", err.Error())
	}
}
