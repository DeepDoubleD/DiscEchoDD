package tools

import (
	"slices"
	"testing"
)

// The rip command MUST carry --progress=-same. makemkvcon's robot mode emits
// no progress output by default, so dropping this flag silently regresses the
// rip bar to a permanent 0% with no ETA. Regression guard for that.
func TestMakeMKVRipArgs_IncludesProgressFlag(t *testing.T) {
	args := makeMKVRipArgs("/dev/sr0", 1, "/out")

	if !slices.Contains(args, "--progress=-same") {
		t.Fatalf("rip args missing --progress=-same, got %v", args)
	}
	for _, want := range []string{"-r", "--decrypt", "--noscan", "mkv", "dev:/dev/sr0", "1", "/out"} {
		if !slices.Contains(args, want) {
			t.Errorf("rip args missing %q, got %v", want, args)
		}
	}
}
