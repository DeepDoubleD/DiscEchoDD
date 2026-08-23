package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/drive"
	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// discFlowCooldown is the window after a job ends during which we
// ignore further media-change uevents on the same drive. Closes the
// race where the spurious mid-rip uevent fires at the instant
// HandBrake exits — by then the job is `done` so the active-job
// guard returns false, but the disc is still spinning down and
// re-classifying just wastes effort. 10 s comfortably covers
// HandBrake/makemkvcon teardown and udev's own quiesce time.
const discFlowCooldown = 10 * time.Second

// discDedupWindow is how far back we look when deduplicating game-disc rows
// by (drive_id, metadata_id). Slow drives (e.g. ASUS SDRW-08D2S-U) emit
// 2-3 media-change uevents per physical insertion; without this window each
// uevent creates a fresh disc row and queues a separate auto-rip job.
// 2 minutes is wide enough to absorb burst uevents from one insertion yet
// narrow enough that a genuine eject-and-reinsert gets a fresh row.
const discDedupWindow = 2 * time.Minute

// discFlow handles one optical-media-change uevent: classify the disc,
// pick the matching pipeline handler, run Identify, persist the disc
// row, and broadcast disc.detected / disc.identified events.
type discFlow struct {
	store       *state.Store
	bc          *state.Broadcaster
	classifier  identify.Classifier
	pipelines   *pipelines.Registry
	identifyDur time.Duration
	igdb        identify.IGDBClient
	// eject, when non-nil, physically ejects a disc that landed in the
	// wrong-role drive (see pipelines.WrongDriveMessage) before
	// handler.Identify would otherwise run against it. Nil is a no-op
	// (tests / configurations that don't wire drive-role routing).
	eject func(ctx context.Context, devPath string) error
}

// HandleManual fires the same disc-flow as a real udev uevent for the
// given drive bus identifier (e.g. "sr0"). The kernel only emits
// DISK_MEDIA_CHANGE on actual media-swap events, so a drive that
// flipped to `error` mid-classify (cold-disc spin-up race, transient
// SCSI error) has no way to recover until the user ejects and
// re-inserts. The reclassify HTTP endpoint wires this method so the
// dashboard can re-run identify against the disc that's already
// sitting in the drive.
func (df *discFlow) HandleManual(bus string) {
	df.handle(drive.Uevent{
		Action:    "change",
		Subsystem: "block",
		DevName:   bus,
		Properties: map[string]string{
			"SUBSYSTEM":         "block",
			"ID_CDROM":          "1",
			"DISK_MEDIA_CHANGE": "1",
			// Re-identify always targets the disc already sitting in the
			// drive, so assert media presence — otherwise handle()'s
			// removal branch would settle the drive idle and skip classify.
			"ID_CDROM_MEDIA": "1",
			"ACTION":         "change",
			"DEVNAME":        bus,
		},
	})
}

func (df *discFlow) handle(ev drive.Uevent) {
	if !ev.IsOpticalMediaChange() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), df.identifyDur)
	defer cancel()

	devPath := "/dev/" + ev.DevName

	drv, err := df.findDriveByDevPath(ctx, devPath)
	if err != nil {
		slog.Warn("disc-flow: no drive registered", "dev", devPath, "err", err)
		return
	}
	// Drives emit spurious media-change uevents during a long rip when
	// HandBrake / makemkvcon hammer the SCSI bus. Re-classifying then
	// races the running step's exclusive hold on /dev/sr0 and the
	// classifier always fails ("cd-info: exit status 1"), which then
	// flips the drive to Error and kills the in-flight job. Bail out
	// early if there's already a job in flight for this drive. Checked
	// before the eject branch below so a hardware eject mid-rip can't
	// force a busy drive idle.
	if busy, err := df.store.HasActiveJobOnDrive(ctx, drv.ID); err != nil {
		slog.Warn("disc-flow: HasActiveJobOnDrive", "err", err)
	} else if busy {
		slog.Info("disc-flow: drive busy, ignoring media-change", "dev", devPath, "drive_id", drv.ID)
		return
	}
	// A media-change uevent fires on eject as well as insert. cdrom_id sets
	// ID_CDROM_MEDIA=1 only when a disc is loaded; its absence means the tray
	// was emptied. Classifying an empty tray fails ("cd-info: exit status 1")
	// and would wrongly flip the drive to Error, so settle it to idle (which
	// also clears any stale last_error) and stop here.
	if !ev.HasMedia() {
		slog.Info("disc removed", "dev", devPath, "drive_id", drv.ID)
		if err := df.store.UpdateDriveState(ctx, drv.ID, state.DriveStateIdle); err != nil {
			slog.Warn("disc-flow: settle drive idle on eject", "err", err, "drive_id", drv.ID)
		}
		// Mirror the API eject path: drop the disc bound to this drive if
		// it's safe (no job in flight) and sensible (jobless, failed,
		// cancelled, or interrupted -- not a completed rip) to clear, so
		// the dashboard's computed Drive.CurrentDiscID stops resolving to
		// a phantom "asking for a decision" card after a physical eject.
		df.dropClearableDiscOnDrive(ctx, drv.ID)
		df.bc.Publish(state.Event{
			Name:    "drive.changed",
			Payload: map[string]any{"drive_id": drv.ID, "state": "idle"},
		})
		return
	}
	slog.Info("disc inserted", "dev", devPath)
	// Media present can only be reported with the tray closed -- true
	// regardless of whether it got there via our own close-tray action,
	// the physical drive button, or the tray auto-closing on some
	// drives when media is detected.
	if err := df.store.UpdateDriveTrayOpen(ctx, drv.ID, false); err != nil {
		slog.Warn("disc-flow: update tray_open on insert", "err", err, "drive_id", drv.ID)
	}
	if recent, err := df.store.HasRecentJobOnDrive(ctx, drv.ID, discFlowCooldown); err != nil {
		slog.Warn("disc-flow: HasRecentJobOnDrive", "err", err)
	} else if recent {
		slog.Info("disc-flow: drive in post-job cooldown, ignoring media-change", "dev", devPath, "drive_id", drv.ID)
		return
	}
	// Atomic CAS: only proceed if the drive is currently idle/error.
	// Closes the race where multiple media-change uevents fire in quick
	// succession (Hollywood DVDs emit 2–3 per insertion as the drive
	// settles) and would otherwise each kick off an independent identify
	// + create a duplicate Disc row.
	claimed, err := df.store.ClaimDriveForIdentify(ctx, drv.ID)
	if err != nil {
		slog.Warn("disc-flow: ClaimDriveForIdentify", "err", err)
		return
	}
	if !claimed {
		slog.Info("disc-flow: drive already identifying, ignoring media-change", "dev", devPath, "drive_id", drv.ID)
		return
	}
	df.bc.Publish(state.Event{
		Name:    "drive.changed",
		Payload: map[string]any{"drive_id": drv.ID, "state": "identifying"},
	})

	dt, err := df.classifier.Classify(ctx, devPath)
	if err != nil {
		// A totally unreadable disc on a BD/console (OmniDrive-flashed)
		// drive isn't necessarily broken media -- confirmed live against
		// a real Wii disc: a stock read gets nothing at all (not even a
		// TOC; cd-info fails outright), unlike every other format this
		// classifier handles, which get at least a partial stock read.
		// Without this fallback the disc silently vanishes into a drive
		// error with no card at all, leaving no way to manually flag it
		// as Wii/GameCube -- the same OVERRIDE_TYPES dropdown every other
		// hard-to-classify format (CD32, FM Towns, Pippin) already uses.
		// A non-BDConsole drive (e.g. the Plextor) keeps the old
		// behaviour: a totally unreadable disc there is a real error,
		// not an OmniDrive-only-format candidate.
		if pipelines.DriveRoleForModel(drv.Model) == pipelines.DriveRoleBDConsole {
			slog.Info("classify failed on BD/console drive; creating DATA card for manual override",
				"dev", devPath, "err", err)
			disc := &state.Disc{Type: state.DiscTypeData, DriveID: drv.ID}
			if perr := df.persistDisc(ctx, disc, nil); perr != nil {
				slog.Warn("persist unreadable-disc fallback", "err", perr)
				df.recordDriveError(drv.ID, err.Error())
				df.releaseDriveState(drv.ID, state.DriveStateError)
				return
			}
			df.bc.Publish(state.Event{Name: "disc.detected", Payload: map[string]any{"disc": disc}})
			df.bc.Publish(state.Event{
				Name:    "disc.identified",
				Payload: map[string]any{"disc": disc, "candidates": []state.Candidate{}},
			})
			if df.releaseDriveState(drv.ID, state.DriveStateIdle) {
				df.bc.Publish(state.Event{
					Name:    "drive.changed",
					Payload: map[string]any{"drive_id": drv.ID, "state": "idle"},
				})
			}
			return
		}
		slog.Warn("classify failed", "dev", devPath, "err", err)
		df.recordDriveError(drv.ID, err.Error())
		df.releaseDriveState(drv.ID, state.DriveStateError)
		return
	}
	// Drive-role routing: some disc types have an established best
	// drive in this station (see pipelines.WrongDriveMessage) -- a
	// PS1/audio CD wants the Redump-certified Plextor, a BD/console
	// disc needs the LibreDrive-flashed ASUS (the Plextor has no
	// Blu-ray hardware at all for BDMV/UHD). Catch the mismatch here,
	// before handler.Identify spins up a scan on hardware that either
	// can't read the disc at all or won't produce a certifiable dump,
	// and eject with an explanation instead.
	if msg, mismatch := pipelines.WrongDriveMessage(dt, drv.Model); mismatch {
		slog.Info("disc-flow: wrong drive for disc type, ejecting",
			"dev", devPath, "disc_type", dt, "drive_model", drv.Model)
		if df.eject != nil {
			if ejErr := df.eject(ctx, devPath); ejErr != nil {
				slog.Warn("disc-flow: eject wrong-drive disc failed", "err", ejErr)
			}
		}
		df.recordDriveError(drv.ID, msg)
		if df.releaseDriveState(drv.ID, state.DriveStateIdle) {
			df.bc.Publish(state.Event{
				Name:    "drive.changed",
				Payload: map[string]any{"drive_id": drv.ID, "state": "idle"},
			})
		}
		return
	}

	handler, ok := df.pipelines.Get(dt)
	if !ok {
		slog.Info("no handler for disc type; skipping", "type", dt)
		df.releaseDriveState(drv.ID, state.DriveStateIdle)
		return
	}

	disc, candidates, err := handler.Identify(ctx, drv)
	switch {
	case errors.Is(err, pipelines.ErrNoCandidates):
		// Persist the disc record anyway so the UI can show "no matches".
		if disc != nil {
			disc.DriveID = drv.ID
			if perr := df.persistDisc(ctx, disc, nil); perr != nil {
				slog.Warn("persist disc (no cands)", "err", perr)
				df.releaseDriveState(drv.ID, state.DriveStateError)
				return
			}
			df.bc.Publish(state.Event{Name: "disc.detected", Payload: map[string]any{"disc": disc}})
			df.bc.Publish(state.Event{
				Name:    "disc.identified",
				Payload: map[string]any{"disc": disc, "candidates": []state.Candidate{}},
			})
		}
		df.releaseDriveState(drv.ID, state.DriveStateIdle)
		return
	case err != nil:
		slog.Warn("identify failed", "err", err)
		df.releaseDriveState(drv.ID, state.DriveStateError)
		return
	}

	disc.DriveID = drv.ID
	if err := df.persistDisc(ctx, disc, candidates); err != nil {
		slog.Warn("persist disc", "err", err)
		df.releaseDriveState(drv.ID, state.DriveStateError)
		return
	}
	df.bc.Publish(state.Event{Name: "disc.detected", Payload: map[string]any{"disc": disc}})
	df.bc.Publish(state.Event{
		Name:    "disc.identified",
		Payload: map[string]any{"disc": disc, "candidates": candidates},
	})
	// Best-effort async IGDB enrichment (cover + summary/genres/year). The
	// card is already published; this pushes a disc.changed when it lands.
	go df.enrichGameDiscFromIGDB(disc.ID, disc.Type, disc.Title)
	// Identify is done. The disc is now waiting for the user to pick a
	// candidate and start a job; the drive itself is no longer doing
	// any work, so flip it back to idle. Leaving it in `identifying`
	// makes the dashboard lie ("Identifying disc…") and blocks future
	// uevents from being processed cleanly.
	//
	// Only publish the idle transition if we actually released the drive
	// from `identifying`. A rip started on this disc mid-identify (the user
	// clicks Start while a spurious udev uevent is still re-identifying)
	// flips the drive to `ripping` via the orchestrator; the CAS release is
	// then a no-op and we must not announce `idle`, or the dashboard shows
	// the drive idle with Eject/Re-identify offered while a rip runs.
	if df.releaseDriveState(drv.ID, state.DriveStateIdle) {
		df.bc.Publish(state.Event{
			Name:    "drive.changed",
			Payload: map[string]any{"drive_id": drv.ID, "state": "idle"},
		})
	}
}

// persistDisc inserts a new disc row, or — when the drive already has a
// matching disc — refreshes that existing row's metadata fields and rebinds
// disc.ID to it. The caller then publishes events with the canonical
// (possibly preexisting) ID so downstream listeners (and the disc-decision
// UI) attach a job to the reused row rather than spawning yet another
// duplicate.
//
// Dedup is two-tiered:
//   - Tier 1 (TOC hash): audio CDs and data discs that compute a content hash.
//   - Tier 2 (metadata_id within discDedupWindow): game discs (PSX/PS2/SAT/DC/XBOX)
//     that lack a TOC hash but carry a stable boot code / product number / title ID.
//     Slow drives emit 2-3 uevents per insertion; this tier prevents each from
//     creating its own disc row.
//
// candidates can be nil for the no-candidates branch; in that case we
// don't overwrite the existing row's candidates JSON.
func (df *discFlow) persistDisc(ctx context.Context, disc *state.Disc, candidates []state.Candidate) error {
	if disc.DriveID == "" {
		return df.store.CreateDisc(ctx, disc)
	}
	// Tier 1: TOC hash (audio CDs, data discs).
	if disc.TOCHash != "" {
		existing, err := df.store.GetDiscByDriveTOC(ctx, disc.DriveID, disc.TOCHash)
		if err == nil {
			return df.reuseDiscRow(ctx, existing, disc, candidates)
		}
		if !errors.Is(err, state.ErrNotFound) {
			return err
		}
	}
	// Tier 2: metadata_id within a short window (game discs).
	// Same drive + same metadata_id within discDedupWindow is the same
	// physical disc. Without this, the slow ASUS SDRW-08D2S-U drive's
	// 3-uevent-per-insertion behaviour creates 3 disc rows.
	if disc.MetadataID != "" {
		existing, err := df.store.GetDiscByDriveAndMetadataID(ctx, disc.DriveID, disc.MetadataID, discDedupWindow)
		if err == nil {
			return df.reuseDiscRow(ctx, existing, disc, candidates)
		}
		if !errors.Is(err, state.ErrNotFound) {
			return err
		}
	}
	return df.store.CreateDisc(ctx, disc)
}

// reuseDiscRow refreshes an existing disc row's metadata from a fresh
// identify pass and rebinds the in-memory disc to the existing ID so
// jobs.disc_id references remain coherent. candidates can be nil, in which
// case the existing row's candidates JSON is left untouched.
func (df *discFlow) reuseDiscRow(ctx context.Context, existing, disc *state.Disc, candidates []state.Candidate) error {
	// Found a prior row for this physical disc. Refresh the metadata
	// fields from the fresh identify pass so a re-identify (after the
	// user picks a different MB release, or after TMDB enriches a TV
	// series later) sticks. Reuse the existing ID so jobs.disc_id
	// references stay coherent.
	if err := df.store.UpdateDiscMetadata(ctx, existing.ID, disc.Title, disc.Year, disc.MetadataProvider, disc.MetadataID); err != nil {
		return err
	}
	if disc.MetadataJSON != "" {
		if err := df.store.UpdateDiscMetadataBlob(ctx, existing.ID, disc.MetadataJSON); err != nil {
			return err
		}
	}
	if candidates != nil {
		if err := df.store.UpdateDiscCandidates(ctx, existing.ID, candidates); err != nil {
			return err
		}
	}
	disc.ID = existing.ID
	disc.CreatedAt = existing.CreatedAt
	return nil
}

// dropClearableDiscOnDrive deletes the disc bound to driveID if
// ClearableDiscOnDrive says it's safe and sensible to (see its doc
// comment), and broadcasts disc.deleted so the webui drops it from its
// $discs map. No-op when nothing matches. Idempotent — safe to race
// against the API EjectDrive path (which calls the same cleanup helper
// in daemon/api on the user's button click).
func (df *discFlow) dropClearableDiscOnDrive(ctx context.Context, driveID string) {
	discID, err := df.store.ClearableDiscOnDrive(ctx, driveID)
	if err != nil {
		slog.Warn("disc-flow: lookup clearable disc on eject", "err", err, "drive_id", driveID)
		return
	}
	if discID == "" {
		return
	}
	if err := df.store.DeleteDisc(ctx, discID); err != nil && !errors.Is(err, state.ErrNotFound) {
		slog.Warn("disc-flow: delete clearable disc on eject", "err", err, "disc_id", discID)
		return
	}
	df.bc.Publish(state.Event{
		Name:    "disc.deleted",
		Payload: map[string]any{"disc_id": discID},
	})
}

// recordDriveError persists the raw error message from a classify
// failure so the dashboard can surface it on the drive card. Uses a
// fresh background context for the same reason releaseDriveState does —
// the identify ctx may already be cancelled.
func (df *discFlow) recordDriveError(driveID, errMsg string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := df.store.UpdateDriveLastError(ctx, driveID, errMsg); err != nil {
		slog.Warn("disc-flow: UpdateDriveLastError", "err", err, "drive_id", driveID)
	}
}

// releaseDriveState writes the drive's terminal state for this handle()
// invocation using a fresh background context, and reports whether the
// transition actually happened. The identify ctx (df.identifyDur, 30s) is
// cancelled the moment classify or identify times out, and ExecContext on the
// original ctx then returns context.Canceled before the SQL runs — silently
// leaving the drive stuck in `identifying` and locking the daemon out of every
// later uevent on that drive. Always use a clean context for the cleanup write.
//
// The write is a CAS scoped to `state = 'identifying'`: if a rip started on
// this disc mid-identify (orchestrator set `ripping`), the release is a no-op
// and returns false so the caller skips announcing a stale `idle`/`error`.
func (df *discFlow) releaseDriveState(driveID string, st state.DriveState) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	released, err := df.store.ReleaseDriveFromIdentify(ctx, driveID, st)
	if err != nil {
		slog.Warn("disc-flow: release drive state", "err", err, "drive_id", driveID, "target_state", st)
	}
	return released
}

func (df *discFlow) findDriveByDevPath(ctx context.Context, dev string) (*state.Drive, error) {
	drives, err := df.store.ListDrives(ctx)
	if err != nil {
		return nil, err
	}
	for i := range drives {
		if drives[i].DevPath == dev {
			return &drives[i], nil
		}
	}
	return nil, errors.New("no drive with dev_path " + dev)
}
