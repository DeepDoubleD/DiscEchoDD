package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/drive"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
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

// stubClassifier always reports the same disc type -- used for the
// drive-role routing tests, which need Classify to succeed rather than
// error out before the routing check runs.
type stubClassifier struct{ dt state.DiscType }

func (s stubClassifier) Classify(_ context.Context, _ string) (state.DiscType, error) {
	return s.dt, nil
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

// TestDiscFlow_Insert_WrongDriveEjectsAndSkipsIdentify covers drive-role
// routing: a PSX disc classified in the BD/console (ASUS) drive must be
// physically ejected, get a "wrong drive" last_error explaining where
// it belongs, and never reach handler.Identify -- proven here by there
// being no registered PSX handler at all, so a mismatched Identify call
// would panic/fail loudly rather than silently succeed.
func TestDiscFlow_Insert_WrongDriveEjectsAndSkipsIdentify(t *testing.T) {
	store := newDiscFlowTestStore(t)
	ctx := context.Background()
	drv := &state.Drive{DevPath: "/dev/sr0", Model: "ASUS BW-16D1HT", Bus: "sr0", State: state.DriveStateIdle, LastSeenAt: time.Now()}
	if err := store.UpsertDrive(ctx, drv); err != nil {
		t.Fatal(err)
	}

	var ejected []string
	bc := state.NewBroadcaster()
	t.Cleanup(bc.Close)
	df := &discFlow{
		store:       store,
		bc:          bc,
		classifier:  stubClassifier{dt: state.DiscTypePSX},
		pipelines:   pipelines.NewRegistry(), // no PSX handler registered
		identifyDur: 5 * time.Second,
		eject: func(_ context.Context, devPath string) error {
			ejected = append(ejected, devPath)
			return nil
		},
	}
	df.handle(insertUevent())

	if len(ejected) != 1 || ejected[0] != "/dev/sr0" {
		t.Errorf("eject calls = %v, want one call for /dev/sr0", ejected)
	}
	got, err := store.GetDrive(ctx, drv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.DriveStateIdle {
		t.Errorf("drive state = %q, want idle", got.State)
	}
	if !strings.HasPrefix(got.LastError, "wrong drive:") {
		t.Errorf("last_error = %q, want a wrong-drive message", got.LastError)
	}
}

// TestDiscFlow_Insert_MatchingDriveProceedsToIdentify is the control
// case: a PSX disc in the Plextor (CD/PS1-role) drive must NOT be
// ejected by drive-role routing, and should reach handler.Identify
// normally.
func TestDiscFlow_Insert_MatchingDriveProceedsToIdentify(t *testing.T) {
	store := newDiscFlowTestStore(t)
	ctx := context.Background()
	drv := &state.Drive{DevPath: "/dev/sr0", Model: "PLEXTOR DVDR PX-716A", Bus: "sr0", State: state.DriveStateIdle, LastSeenAt: time.Now()}
	if err := store.UpsertDrive(ctx, drv); err != nil {
		t.Fatal(err)
	}

	var ejected []string
	reg := pipelines.NewRegistry()
	reg.Register(&stubPSXHandler{})
	bc := state.NewBroadcaster()
	t.Cleanup(bc.Close)
	df := &discFlow{
		store:       store,
		bc:          bc,
		classifier:  stubClassifier{dt: state.DiscTypePSX},
		pipelines:   reg,
		identifyDur: 5 * time.Second,
		eject: func(_ context.Context, devPath string) error {
			ejected = append(ejected, devPath)
			return nil
		},
	}
	df.handle(insertUevent())

	if len(ejected) != 0 {
		t.Errorf("eject calls = %v, want none for a disc in its correct drive", ejected)
	}
	got, err := store.GetDrive(ctx, drv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastError != "" {
		t.Errorf("last_error = %q, want empty (no wrong-drive message)", got.LastError)
	}
}

// stubPSXHandler is the minimal pipelines.Handler needed to prove
// Identify actually ran in TestDiscFlow_Insert_MatchingDriveProceedsToIdentify.
type stubPSXHandler struct{}

func (stubPSXHandler) DiscType() state.DiscType { return state.DiscTypePSX }
func (stubPSXHandler) Identify(_ context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	return &state.Disc{Type: state.DiscTypePSX, Title: "Test PSX Disc"}, []state.Candidate{
		{Source: "test", Title: "Test PSX Disc", Confidence: 90},
	}, nil
}
func (stubPSXHandler) Plan(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan { return nil }
func (stubPSXHandler) Run(_ context.Context, _ *state.Drive, _ *state.Disc, _ *state.Profile, _ pipelines.EventSink) error {
	return nil
}
