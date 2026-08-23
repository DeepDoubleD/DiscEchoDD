package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/drive"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// erroringClassifier lets an insert uevent reach the classify call
// (past the tray_open update this test cares about) without needing a
// real classifier wired up; the resulting classify failure is exactly
// what a real drive reports for select/settle races and is already
// handled by the disc-flow (drive → error, no panic).
type erroringClassifier struct{}

func (erroringClassifier) Classify(_ context.Context, _ string) (state.DiscType, error) {
	return "", errors.New("no classifier configured in test")
}

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

// insertUevent mirrors what udev emits when media is loaded: a
// media-change uevent with ID_CDROM_MEDIA=1.
func insertUevent() drive.Uevent {
	return drive.Uevent{
		Action: "change", Subsystem: "block", DevName: "sr0",
		Properties: map[string]string{
			"SUBSYSTEM": "block", "ID_CDROM": "1", "ID_CDROM_MEDIA": "1",
			"DISK_MEDIA_CHANGE": "1", "DEVNAME": "sr0",
		},
	}
}

// TestDiscFlow_Insert_ClosesTrayStatus covers the other half of the
// tray-status feature: media present can only be reported with the
// tray physically closed, so an insert uevent must clear tray_open
// regardless of how the tray actually got closed (our own close-tray
// action, the drive's own button, or an auto-closing tray).
func TestDiscFlow_Insert_ClosesTrayStatus(t *testing.T) {
	store := newDiscFlowTestStore(t)
	ctx := context.Background()
	drv := &state.Drive{DevPath: "/dev/sr0", Model: "X", Bus: "sr0", State: state.DriveStateIdle, LastSeenAt: time.Now()}
	if err := store.UpsertDrive(ctx, drv); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDriveTrayOpen(ctx, drv.ID, true); err != nil {
		t.Fatal(err)
	}

	bc := state.NewBroadcaster()
	t.Cleanup(bc.Close)
	df := &discFlow{store: store, bc: bc, classifier: erroringClassifier{}, identifyDur: 5 * time.Second}
	df.handle(insertUevent())

	got, err := store.GetDrive(ctx, drv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TrayOpen {
		t.Error("want tray_open=false after an insert uevent")
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

// Physical eject (drive-button press) fires the same media-change uevent
// as the API eject path; the discflow handler must also drop the
// jobless disc bound to the drive so the dashboard's computed
// Drive.CurrentDiscID stops resolving to a phantom card.
func TestDiscFlow_Eject_DropsClearableDiscOnDrive(t *testing.T) {
	store := newDiscFlowTestStore(t)
	ctx := context.Background()
	drv := &state.Drive{DevPath: "/dev/sr0", Model: "X", Bus: "sr0", State: state.DriveStateIdle, LastSeenAt: time.Now()}
	if err := store.UpsertDrive(ctx, drv); err != nil {
		t.Fatal(err)
	}
	disc := &state.Disc{DriveID: drv.ID, Type: state.DiscTypeAudioCD, Title: "Orphan"}
	if err := store.CreateDisc(ctx, disc); err != nil {
		t.Fatal(err)
	}

	bc := state.NewBroadcaster()
	t.Cleanup(bc.Close)
	ch, cancel := bc.Subscribe(8)
	defer cancel()
	df := &discFlow{store: store, bc: bc, identifyDur: 5 * time.Second}
	df.handle(ejectUevent())

	if _, err := store.GetDisc(ctx, disc.ID); err == nil {
		t.Errorf("orphan disc still present after eject")
	}

	var sawDelete bool
	deadline := time.After(200 * time.Millisecond)
loop:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break loop
			}
			if ev.Name == "disc.deleted" {
				sawDelete = true
				break loop
			}
		case <-deadline:
			break loop
		}
	}
	if !sawDelete {
		t.Errorf("no disc.deleted broadcast for orphan disc on eject")
	}
}

// TestDiscFlow_Eject_DropsFailedDisc reproduces the live bug this
// feature fixes: physically pulling a disc whose only rip attempt
// failed used to leave its card on the dashboard forever, "asking for
// a decision" with nothing actually in the drive. A physical eject must
// now clear it, same as a jobless disc.
func TestDiscFlow_Eject_DropsFailedDisc(t *testing.T) {
	store := newDiscFlowTestStore(t)
	ctx := context.Background()
	drv := &state.Drive{DevPath: "/dev/sr0", Model: "X", Bus: "sr0", State: state.DriveStateIdle, LastSeenAt: time.Now()}
	if err := store.UpsertDrive(ctx, drv); err != nil {
		t.Fatal(err)
	}
	prof := &state.Profile{DiscType: state.DiscTypeAudioCD, Name: "p", Engine: "whipper", Format: "FLAC"}
	if err := store.CreateProfile(ctx, prof); err != nil {
		t.Fatal(err)
	}
	disc := &state.Disc{DriveID: drv.ID, Type: state.DiscTypeAudioCD, Title: "Failed Rip"}
	if err := store.CreateDisc(ctx, disc); err != nil {
		t.Fatal(err)
	}
	job := &state.Job{DiscID: disc.ID, DriveID: drv.ID, ProfileID: prof.ID}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateJobState(ctx, job.ID, state.JobStateFailed, "disc read error"); err != nil {
		t.Fatal(err)
	}

	bc := state.NewBroadcaster()
	t.Cleanup(bc.Close)
	df := &discFlow{store: store, bc: bc, identifyDur: 5 * time.Second}
	df.handle(ejectUevent())

	if _, err := store.GetDisc(ctx, disc.ID); err == nil {
		t.Errorf("failed-job disc still present after physical eject")
	}
}
