package pipelines

import (
	"fmt"
	"syscall"
)

// spaceCheckMarginPct pads the needed-bytes figure before comparing
// against free space. MakeMKV's own size estimate (surfaced in its
// "may reach as much as X" MSG line) is an upper bound already, but a
// margin still absorbs container/filesystem overhead and any second
// title (menus, extras) MakeMKV writes alongside the main feature.
const spaceCheckMarginPct = 10

// CheckSpoolSpace statfs's dir and fails if the filesystem backing it
// doesn't have room for neededBytes (plus a margin). Call this right
// after picking which title(s) to rip and before the first byte is
// written -- MakeMKV itself only ever warns about insufficient space
// ("may reach as much as X while only Y free... continue?") and then
// proceeds anyway in robot mode, so an undersized spool volume used to
// silently die 10+ minutes into a rip with nothing but "no .mkv
// produced". This turns that into an immediate, readable job error.
func CheckSpoolSpace(dir string, neededBytes int64) error {
	if neededBytes <= 0 {
		return nil
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		// Diagnostic-only check: a statfs failure shouldn't block a rip
		// that might otherwise succeed.
		return nil
	}
	avail := int64(st.Bavail) * int64(st.Bsize)
	want := neededBytes + neededBytes*spaceCheckMarginPct/100
	if avail < want {
		return fmt.Errorf(
			"not enough space to rip: need ~%s, only %s free at %s -- free up space or move the spool volume before retrying",
			HumanBytes(want), HumanBytes(avail), dir,
		)
	}
	return nil
}
