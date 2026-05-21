package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/drive"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

func newDiscFlowTestStore(t *testing.T) *state.Store {
	t.Helper()
	dir := t.TempDir()
	db, err := state.Open(filepath.Join(dir, "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return state.NewStore(db)
}

// ejectUevent mirrors what udev emits when the tray is emptied: a real
// media-change (DISK_MEDIA_CHANGE=1, ID_CDROM=1) but with ID_CDROM_MEDIA
// absent because no disc is loaded.
func ejectUevent() drive.Uevent {
	return drive.Uevent{
		Action: "change", Subsystem: "block", DevName: "sr0",
		Properties: map[string]string{
			"SUBSYSTEM": "block", "ID_CDROM": "1",
			"DISK_MEDIA_CHANGE": "1", "DEVNAME": "sr0",
		},
	}
}

// Ejecting a disc fires a media-change uevent that carries no media. The
// daemon must settle the drive to idle and clear any stale error — not run
// classify against an empty tray (which fails with "cd-info: exit status 1"
// and used to flip the drive into the Error state).
func TestDiscFlow_Eject_SettlesIdleAndClearsError(t *testing.T) {
	store := newDiscFlowTestStore(t)
	ctx := context.Background()
	drv := &state.Drive{DevPath: "/dev/sr0", Model: "X", Bus: "sr0", State: state.DriveStateError, LastSeenAt: time.Now()}
	if err := store.UpsertDrive(ctx, drv); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDriveLastError(ctx, drv.ID, "cd-info: exit status 1"); err != nil {
		t.Fatal(err)
	}

	df := &discFlow{store: store, bc: state.NewBroadcaster(), identifyDur: 5 * time.Second}
	df.handle(ejectUevent())

	got, err := store.GetDrive(ctx, drv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.DriveStateIdle {
		t.Errorf("state = %q, want idle", got.State)
	}
	if got.LastError != "" {
		t.Errorf("last_error = %q, want cleared", got.LastError)
	}
}
