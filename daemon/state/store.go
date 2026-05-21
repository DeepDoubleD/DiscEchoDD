package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned by Get* methods when no row matches.
var ErrNotFound = errors.New("state: not found")

// Store is the typed CRUD layer over the DB. All tables route through here.
type Store struct {
	db *DB
}

func NewStore(db *DB) *Store { return &Store{db: db} }

// DB exposes the underlying *DB for tests and the broadcaster bootstrap.
// Production callers should use the typed methods on Store.
func (s *Store) DB() *DB { return s.db }

// NewID returns a fresh UUIDv4 string (lower-case, 36 chars).
func NewID() string { return uuid.NewString() }

// timestamp formats t in the canonical RFC3339Nano UTC string.
func timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// parseTime parses "" as a zero time, otherwise RFC3339Nano.
func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, s)
}

// parseTimePtr returns nil for "", otherwise a pointer to the parsed
// time. Used for nullable timestamp columns where the wire JSON expects
// `null` rather than the zero time.
func parseTimePtr(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// timestampPtr returns "" for nil, otherwise the formatted timestamp.
func timestampPtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return timestamp(*t)
}

// ---- shared scanners ------------------------------------------------------

type rowScanner interface {
	Scan(dest ...any) error
}

// ---- JSON helpers ---------------------------------------------------------

func marshalCandidates(cs []Candidate) (string, error) {
	if cs == nil {
		cs = []Candidate{}
	}
	b, err := json.Marshal(cs)
	return string(b), err
}

func unmarshalCandidates(s string) ([]Candidate, error) {
	if s == "" {
		return nil, nil
	}
	var cs []Candidate
	if err := json.Unmarshal([]byte(s), &cs); err != nil {
		return nil, err
	}
	return cs, nil
}

func marshalOptions(opts map[string]any) (string, error) {
	if opts == nil {
		opts = map[string]any{}
	}
	b, err := json.Marshal(opts)
	return string(b), err
}

func unmarshalOptions(s string) (map[string]any, error) {
	if s == "" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// ---- DRIVES ----------------------------------------------------------------

// GetDrive fetches a drive by ID.
func (s *Store) GetDrive(ctx context.Context, id string) (*Drive, error) {
	row := s.db.Conn().QueryRowContext(ctx, `
		SELECT id, model, bus, dev_path, state, last_seen_at, notes, last_error,
		       read_offset, read_offset_source
		FROM drives WHERE id = ?`, id)
	return scanDrive(row)
}

// ListDrives returns all drives ordered by dev_path.
func (s *Store) ListDrives(ctx context.Context) ([]Drive, error) {
	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT id, model, bus, dev_path, state, last_seen_at, notes, last_error,
		       read_offset, read_offset_source
		FROM drives ORDER BY dev_path`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Drive
	for rows.Next() {
		d, err := scanDrive(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// UpsertDrive inserts a new drive, or updates the existing one keyed by
// dev_path. d.ID is filled in on insert.
func (s *Store) UpsertDrive(ctx context.Context, d *Drive) error {
	if d.ID == "" {
		d.ID = NewID()
	}
	_, err := s.db.Conn().ExecContext(ctx, `
		INSERT INTO drives (id, model, bus, dev_path, state, last_seen_at, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(dev_path) DO UPDATE SET
		  model = excluded.model,
		  bus = excluded.bus,
		  state = excluded.state,
		  last_seen_at = excluded.last_seen_at,
		  notes = excluded.notes`,
		d.ID, d.Model, d.Bus, d.DevPath, string(d.State),
		timestamp(d.LastSeenAt), d.Notes)
	return err
}

// ResetIdentifyingDrives clears any drive left in the `identifying`
// state, transitioning it back to `idle`. Returns the number of rows
// updated.
//
// A daemon crash, container restart, or classify-ctx timeout whose
// cleanup write was itself made with the timed-out ctx (and silently
// failed) can leave a drive stuck in `identifying`. ClaimDriveForIdentify
// only transitions from `idle` or `error`, so a stuck row means every
// subsequent uevent hits "already identifying" and the daemon is deaf
// until the row is corrected. Call this at startup so the next uevent
// can claim the drive cleanly.
func (s *Store) ResetIdentifyingDrives(ctx context.Context) (int, error) {
	res, err := s.db.Conn().ExecContext(ctx,
		`UPDATE drives SET state = ?, last_seen_at = ? WHERE state = ?`,
		string(DriveStateIdle), timestamp(time.Now()), string(DriveStateIdentifying))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// UpdateDriveState sets the drive's state and refreshes last_seen_at.
// When transitioning to idle the per-drive error context is no longer
// relevant — clear last_error so the dashboard does not keep showing a
// stale error banner.
func (s *Store) UpdateDriveState(ctx context.Context, id string, state DriveState) error {
	var res sql.Result
	var err error
	if state == DriveStateIdle {
		res, err = s.db.Conn().ExecContext(ctx,
			`UPDATE drives SET state = ?, last_seen_at = ?, last_error = '' WHERE id = ?`,
			string(state), timestamp(time.Now()), id)
	} else {
		res, err = s.db.Conn().ExecContext(ctx,
			`UPDATE drives SET state = ?, last_seen_at = ? WHERE id = ?`,
			string(state), timestamp(time.Now()), id)
	}
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateDriveLastError stores the raw error message from the most recent
// classify failure. Call this before UpdateDriveState(…, DriveStateError)
// so the dashboard can surface the reason. The error is cleared
// automatically when the drive returns to idle via UpdateDriveState.
func (s *Store) UpdateDriveLastError(ctx context.Context, id, errMsg string) error {
	res, err := s.db.Conn().ExecContext(ctx,
		`UPDATE drives SET last_error = ? WHERE id = ?`, errMsg, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimDriveForIdentify atomically transitions the drive into the
// `identifying` state only if it's currently `idle` or `error`. Returns
// (true, nil) when the claim succeeds — the caller owns the identify
// slot. Returns (false, nil) when another caller is already identifying
// or the drive is busy with a later state (ripping, transcoding, …);
// the caller must drop the uevent.
//
// This closes the race the v0.1.4 active-job guard left open: between
// the first `disc inserted` uevent (which kicks off identify but
// doesn't have a job yet) and the second uevent a few seconds later,
// `HasActiveJobOnDrive` returns false for both, so both proceed to
// identify and both create separate Disc rows. Hollywood DVDs emit
// multiple media-change uevents per insertion as the drive settles,
// which made the duplicate Disc rows reliably reproducible.
func (s *Store) ClaimDriveForIdentify(ctx context.Context, id string) (bool, error) {
	res, err := s.db.Conn().ExecContext(ctx,
		`UPDATE drives SET state = ?, last_seen_at = ?
		 WHERE id = ? AND state IN ('idle','error')`,
		string(DriveStateIdentifying), timestamp(time.Now()), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ReleaseDriveFromIdentify transitions a drive out of the `identifying`
// state set by ClaimDriveForIdentify, but only while it is still
// identifying. Returns true if the row moved.
//
// The CAS matters because the discFlow identify pass and the orchestrator's
// rip run concurrently and both write drive state. A spurious udev uevent can
// claim the drive for identify, the user then starts a rip (orchestrator flips
// the drive to `ripping`), and the finishing identify pass would otherwise
// stomp `ripping` back to `idle` — the "drive shows IDLE with Eject/Re-identify
// offered mid-rip" desync. Scoping the release to `state = 'identifying'` makes
// it a no-op once another flow owns the drive.
func (s *Store) ReleaseDriveFromIdentify(ctx context.Context, id string, to DriveState) (bool, error) {
	res, err := s.db.Conn().ExecContext(ctx,
		`UPDATE drives SET state = ? WHERE id = ? AND state = 'identifying'`,
		string(to), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func scanDrive(r rowScanner) (*Drive, error) {
	var d Drive
	var state, lastSeenStr string
	if err := r.Scan(&d.ID, &d.Model, &d.Bus, &d.DevPath, &state, &lastSeenStr,
		&d.Notes, &d.LastError, &d.ReadOffset, &d.ReadOffsetSource); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	d.State = DriveState(state)
	t, err := parseTime(lastSeenStr)
	if err != nil {
		return nil, fmt.Errorf("parse last_seen_at: %w", err)
	}
	d.LastSeenAt = t
	d.LastErrorTip = DriveErrorTip(d.LastError)
	return &d, nil
}

// UpdateDriveReadOffset persists a drive's calibration. source is one of
// 'manual' (user typed) or 'auto' (`whipper offset find`). Returns
// ErrNotFound if id has no row.
//
// UpsertDrive deliberately omits these columns from its UPDATE branch so
// the device-rescan on boot does not clobber the user's calibration —
// this updater is the only call site that ever touches them.
func (s *Store) UpdateDriveReadOffset(ctx context.Context, id string, offset int, source string) error {
	res, err := s.db.Conn().ExecContext(ctx,
		`UPDATE drives SET read_offset = ?, read_offset_source = ? WHERE id = ?`,
		offset, source, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- DISCS ----------------------------------------------------------------

// CreateDisc inserts a new disc row. d.ID and d.CreatedAt are filled in
// if zero. Discs are immutable after creation in M1.1; updates land in
// future milestones via separate dedicated methods.
func (s *Store) CreateDisc(ctx context.Context, d *Disc) error {
	if d.ID == "" {
		d.ID = NewID()
	}
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	candJSON, err := marshalCandidates(d.Candidates)
	if err != nil {
		return fmt.Errorf("marshal candidates: %w", err)
	}
	metaBlob := d.MetadataJSON
	if metaBlob == "" {
		metaBlob = "{}"
	}
	_, err = s.db.Conn().ExecContext(ctx, `
		INSERT INTO discs (id, drive_id, type, title, year, runtime_seconds,
		                   size_bytes_raw, toc_hash, metadata_provider, metadata_id,
		                   candidates_json, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, nullString(d.DriveID), string(d.Type), d.Title, d.Year, d.RuntimeSeconds,
		d.SizeBytesRaw, d.TOCHash, d.MetadataProvider, d.MetadataID,
		candJSON, metaBlob, timestamp(d.CreatedAt))
	return err
}

// GetDiscByDriveTOC returns the most-recently-created disc on driveID
// with the given non-empty toc_hash, or ErrNotFound when none exists.
// Callers (notably discflow) use this on detect to reuse an existing
// disc row instead of inserting a new one on every retry — see
// migration 009 for the backfill that collapsed pre-existing dupes.
// An empty driveID or tocHash short-circuits to ErrNotFound because
// neither identifies a single physical disc.
func (s *Store) GetDiscByDriveTOC(ctx context.Context, driveID, tocHash string) (*Disc, error) {
	if driveID == "" || tocHash == "" {
		return nil, ErrNotFound
	}
	row := s.db.Conn().QueryRowContext(ctx, `
		SELECT id, COALESCE(drive_id, ''), type, title, year, runtime_seconds,
		       size_bytes_raw, toc_hash, metadata_provider, metadata_id,
		       candidates_json, metadata_json, created_at
		FROM discs WHERE drive_id = ? AND toc_hash = ?
		ORDER BY created_at DESC LIMIT 1`, driveID, tocHash)
	return scanDisc(row)
}

// GetDiscByDriveAndMetadataID returns the most-recently-created disc on
// driveID with the given non-empty metadata_id, created within the past
// `within` duration. Used by discflow.persistDisc as a game-disc dedup
// fallback when TOCHash is empty: slow drives emit 2-3 media-change
// uevents per physical insertion, each one creating a fresh disc row
// unless deduped. An empty driveID or metaID short-circuits to ErrNotFound.
func (s *Store) GetDiscByDriveAndMetadataID(ctx context.Context, driveID, metaID string, within time.Duration) (*Disc, error) {
	if driveID == "" || metaID == "" {
		return nil, ErrNotFound
	}
	cutoff := timestamp(time.Now().Add(-within))
	row := s.db.Conn().QueryRowContext(ctx, `
		SELECT id, COALESCE(drive_id, ''), type, title, year, runtime_seconds,
		       size_bytes_raw, toc_hash, metadata_provider, metadata_id,
		       candidates_json, metadata_json, created_at
		FROM discs
		WHERE drive_id = ? AND metadata_id = ? AND created_at >= ?
		ORDER BY created_at DESC LIMIT 1`, driveID, metaID, cutoff)
	return scanDisc(row)
}

// GetDisc fetches a disc by ID, including its candidates.
func (s *Store) GetDisc(ctx context.Context, id string) (*Disc, error) {
	row := s.db.Conn().QueryRowContext(ctx, `
		SELECT id, COALESCE(drive_id, ''), type, title, year, runtime_seconds,
		       size_bytes_raw, toc_hash, metadata_provider, metadata_id,
		       candidates_json, metadata_json, created_at
		FROM discs WHERE id = ?`, id)
	return scanDisc(row)
}

// ListRecentDiscs returns the N most-recently-created discs across all
// drives, newest first. Used by /api/state and the SSE bootstrap so the
// UI can resolve disc titles for the active and recent jobs without an
// extra round-trip per job.
func (s *Store) ListRecentDiscs(ctx context.Context, limit int) ([]Disc, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT id, COALESCE(drive_id, ''), type, title, year, runtime_seconds,
		       size_bytes_raw, toc_hash, metadata_provider, metadata_id,
		       candidates_json, metadata_json, created_at
		FROM discs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Disc
	for rows.Next() {
		d, err := scanDisc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// CurrentDiscByDrive returns the most-recent disc per drive that has no
// terminal job yet (i.e. the user has not resolved it via rip / skip /
// retry-failure). A disc with a `done` / `failed` / `cancelled` /
// `interrupted` job is treated as resolved and excluded — re-rip flows
// happen against the existing row, and the active-job branch on the
// drive card already covers that case.
//
// Returned map is drive_id → disc_id. Drives with no unresolved disc
// are omitted. Used by buildSnapshot to populate Drive.CurrentDiscID so
// the dashboard drive card can render "disc present" for an
// awaiting-decision disc, not just for an actively-ripping job.
func (s *Store) CurrentDiscByDrive(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT drive_id, id FROM discs
		WHERE drive_id IS NOT NULL AND drive_id != ''
		  AND NOT EXISTS (
		    SELECT 1 FROM jobs j
		    WHERE j.disc_id = discs.id
		      AND j.state IN ('done','failed','cancelled','interrupted')
		  )
		ORDER BY drive_id, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var driveID, discID string
		if err := rows.Scan(&driveID, &discID); err != nil {
			return nil, err
		}
		// Keep only the first (most recent) per drive.
		if _, ok := out[driveID]; !ok {
			out[driveID] = discID
		}
	}
	return out, rows.Err()
}

// ListDiscsForDrive returns discs that were inserted in the given drive,
// most recent first.
func (s *Store) ListDiscsForDrive(ctx context.Context, driveID string) ([]Disc, error) {
	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT id, COALESCE(drive_id, ''), type, title, year, runtime_seconds,
		       size_bytes_raw, toc_hash, metadata_provider, metadata_id,
		       candidates_json, metadata_json, created_at
		FROM discs WHERE drive_id = ? ORDER BY created_at DESC`, driveID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Disc
	for rows.Next() {
		d, err := scanDisc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// UpdateDiscMetadata persists the title/year/provider/metadata_id
// fields for a disc. Used when the user picks a candidate via
// /discs/{id}/start so the chosen identity reaches the orchestrator
// (which re-reads the row before dispatching the job).
func (s *Store) UpdateDiscMetadata(ctx context.Context, id, title string, year int, provider, metadataID string) error {
	res, err := s.db.Conn().ExecContext(ctx, `
		UPDATE discs SET title = ?, year = ?, metadata_provider = ?, metadata_id = ?
		WHERE id = ?`, title, year, provider, metadataID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateDiscMetadataBlob writes the per-disc-type extended metadata
// JSON onto an existing disc row. Used at /api/discs/{id}/start after
// the user picks a candidate so the pane has rich data from the first
// paint without round-tripping back to TMDB / MusicBrainz at view
// time. blob is the raw JSON string (UTF-8). An empty string is
// stored as "{}" so the column's NOT NULL constraint is satisfied
// and the webui can safely parse the result.
func (s *Store) UpdateDiscMetadataBlob(ctx context.Context, id string, blob string) error {
	if blob == "" {
		blob = "{}"
	}
	res, err := s.db.Conn().ExecContext(ctx,
		`UPDATE discs SET metadata_json = ? WHERE id = ?`, blob, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateDiscRuntime persists the runtime (in seconds) for a disc.
// Called from /discs/{id}/start after the daemon fetches TMDB's
// `/movie/{id}` for the chosen candidate. The DVD pipeline reads
// this column as a sanity check against the scanned title duration.
func (s *Store) UpdateDiscRuntime(ctx context.Context, id string, runtimeSec int) error {
	res, err := s.db.Conn().ExecContext(ctx,
		`UPDATE discs SET runtime_seconds = ? WHERE id = ?`, runtimeSec, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DiscHasAnyJob reports whether any job (in any state) references the
// disc. Used by the Skip / delete affordance to refuse removing discs
// that already have job history — a delete would leave those job rows
// pointing at a non-existent disc_id.
func (s *Store) DiscHasAnyJob(ctx context.Context, discID string) (bool, error) {
	if discID == "" {
		return false, nil
	}
	var n int
	err := s.db.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jobs WHERE disc_id = ?`, discID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// DiscHasActiveJob reports whether the disc currently has a job in any
// non-terminal state (queued / identifying / running / paused). Used by
// POST /api/discs/{id}/start to refuse a duplicate start when a previous
// click already created a job — the orchestrator serialises per-drive
// but the API itself has no idempotency, so a fast double-click (or
// auto-confirm + manual click racing) otherwise enqueues two jobs for
// the same disc.
func (s *Store) DiscHasActiveJob(ctx context.Context, discID string) (bool, error) {
	if discID == "" {
		return false, nil
	}
	var n int
	err := s.db.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM jobs
		WHERE disc_id = ?
		  AND state IN ('queued','identifying','running','paused')`,
		discID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// DeleteDisc removes a disc row by id. Returns ErrNotFound when no row
// matches. Does NOT cascade-delete jobs — callers must check via
// DiscHasAnyJob first.
func (s *Store) DeleteDisc(ctx context.Context, id string) error {
	res, err := s.db.Conn().ExecContext(ctx,
		`DELETE FROM discs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateDiscCandidates replaces the candidates JSON for an existing disc.
// Used by the identify endpoint when the user supplies a manual TMDB
// query and we want to persist the new candidate list.
func (s *Store) UpdateDiscCandidates(ctx context.Context, id string, cands []Candidate) error {
	body, err := marshalCandidates(cands)
	if err != nil {
		return fmt.Errorf("marshal candidates: %w", err)
	}
	res, err := s.db.Conn().ExecContext(ctx,
		`UPDATE discs SET candidates_json = ? WHERE id = ?`, body, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// nullString returns a sql.NullString that's NULL on the empty string,
// used for nullable FK columns. SQLite ON DELETE SET NULL needs real
// NULLs to work; an empty-string value would never match.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func scanDisc(r rowScanner) (*Disc, error) {
	var d Disc
	var dtype, candJSON, metaJSON, createdStr string
	if err := r.Scan(
		&d.ID, &d.DriveID, &dtype, &d.Title, &d.Year, &d.RuntimeSeconds,
		&d.SizeBytesRaw, &d.TOCHash, &d.MetadataProvider, &d.MetadataID,
		&candJSON, &metaJSON, &createdStr,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	d.Type = DiscType(dtype)
	cs, err := unmarshalCandidates(candJSON)
	if err != nil {
		return nil, fmt.Errorf("unmarshal candidates: %w", err)
	}
	d.Candidates = cs
	d.MetadataJSON = metaJSON
	t, err := parseTime(createdStr)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	d.CreatedAt = t
	return &d, nil
}

// ---- PROFILES -------------------------------------------------------------

// CreateProfile inserts a new profile. p.ID, p.CreatedAt, p.UpdatedAt
// are filled in if zero.
func (s *Store) CreateProfile(ctx context.Context, p *Profile) error {
	if p.ID == "" {
		p.ID = NewID()
	}
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = now
	}
	optsJSON, err := marshalOptions(p.Options)
	if err != nil {
		return fmt.Errorf("marshal options: %w", err)
	}
	if p.DrivePolicy == "" {
		p.DrivePolicy = "any"
	}
	_, err = s.db.Conn().ExecContext(ctx, `
		INSERT INTO profiles (id, disc_type, name, engine, format, preset,
		                      container, video_codec, quality_preset,
		                      hdr_pipeline, drive_policy,
		                      options_json, output_path_template, enabled,
		                      step_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, string(p.DiscType), p.Name, p.Engine, p.Format, p.Preset,
		p.Container, p.VideoCodec, p.QualityPreset,
		p.HDRPipeline, p.DrivePolicy,
		optsJSON, p.OutputPathTemplate, boolToInt(p.Enabled),
		p.StepCount, timestamp(p.CreatedAt), timestamp(p.UpdatedAt))
	return err
}

// GetProfile fetches a profile by ID.
func (s *Store) GetProfile(ctx context.Context, id string) (*Profile, error) {
	row := s.db.Conn().QueryRowContext(ctx, `
		SELECT id, disc_type, name, engine, format, preset,
		       container, video_codec, quality_preset,
		       hdr_pipeline, drive_policy,
		       options_json, output_path_template, enabled,
		       step_count, created_at, updated_at
		FROM profiles WHERE id = ?`, id)
	return scanProfile(row)
}

// ListProfiles returns all profiles.
func (s *Store) ListProfiles(ctx context.Context) ([]Profile, error) {
	return s.queryProfiles(ctx, `
		SELECT id, disc_type, name, engine, format, preset,
		       container, video_codec, quality_preset,
		       hdr_pipeline, drive_policy,
		       options_json, output_path_template, enabled,
		       step_count, created_at, updated_at
		FROM profiles ORDER BY name`)
}

// ListProfilesByDiscType filters profiles to those matching the given
// disc type. Used by the orchestrator when picking a default for a
// freshly identified disc.
func (s *Store) ListProfilesByDiscType(ctx context.Context, dt DiscType) ([]Profile, error) {
	return s.queryProfiles(ctx, `
		SELECT id, disc_type, name, engine, format, preset,
		       container, video_codec, quality_preset,
		       hdr_pipeline, drive_policy,
		       options_json, output_path_template, enabled,
		       step_count, created_at, updated_at
		FROM profiles WHERE disc_type = ? ORDER BY name`, string(dt))
}

// UpdateProfile rewrites every mutable column. The ID and CreatedAt
// stay; UpdatedAt is refreshed.
func (s *Store) UpdateProfile(ctx context.Context, p *Profile) error {
	p.UpdatedAt = time.Now()
	optsJSON, err := marshalOptions(p.Options)
	if err != nil {
		return fmt.Errorf("marshal options: %w", err)
	}
	if p.DrivePolicy == "" {
		p.DrivePolicy = "any"
	}
	res, err := s.db.Conn().ExecContext(ctx, `
		UPDATE profiles SET disc_type = ?, name = ?, engine = ?, format = ?,
		                    preset = ?, container = ?, video_codec = ?,
		                    quality_preset = ?, hdr_pipeline = ?,
		                    drive_policy = ?,
		                    options_json = ?, output_path_template = ?,
		                    enabled = ?, step_count = ?, updated_at = ?
		WHERE id = ?`,
		string(p.DiscType), p.Name, p.Engine, p.Format, p.Preset,
		p.Container, p.VideoCodec, p.QualityPreset, p.HDRPipeline,
		p.DrivePolicy,
		optsJSON, p.OutputPathTemplate, boolToInt(p.Enabled),
		p.StepCount, timestamp(p.UpdatedAt), p.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteProfile removes a profile. ON DELETE RESTRICT on jobs.profile_id
// will cause this to error if any job references the profile.
func (s *Store) DeleteProfile(ctx context.Context, id string) error {
	res, err := s.db.Conn().ExecContext(ctx, `DELETE FROM profiles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) queryProfiles(ctx context.Context, q string, args ...any) ([]Profile, error) {
	rows, err := s.db.Conn().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Profile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func scanProfile(r rowScanner) (*Profile, error) {
	var p Profile
	var dtype, optsJSON, createdStr, updatedStr string
	var enabled int
	if err := r.Scan(
		&p.ID, &dtype, &p.Name, &p.Engine, &p.Format, &p.Preset,
		&p.Container, &p.VideoCodec, &p.QualityPreset,
		&p.HDRPipeline, &p.DrivePolicy,
		&optsJSON, &p.OutputPathTemplate, &enabled,
		&p.StepCount, &createdStr, &updatedStr,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.DiscType = DiscType(dtype)
	p.Enabled = enabled != 0
	opts, err := unmarshalOptions(optsJSON)
	if err != nil {
		return nil, fmt.Errorf("unmarshal options: %w", err)
	}
	p.Options = opts
	if p.CreatedAt, err = parseTime(createdStr); err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	if p.UpdatedAt, err = parseTime(updatedStr); err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	return &p, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- JOBS -----------------------------------------------------------------

// CreateJob inserts a new job AND eagerly materializes its eight
// job_steps rows. Steps marked Skipped in j.Steps land as
// JobStepStateSkipped; the rest start at JobStepStatePending. The whole
// insert is one tx so callers never observe a half-created job. j.ID
// and j.CreatedAt fill in if zero.
func (s *Store) CreateJob(ctx context.Context, j *Job) error {
	if j.ID == "" {
		j.ID = NewID()
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = time.Now()
	}
	if j.State == "" {
		j.State = JobStateQueued
	}
	if j.Kind == "" {
		j.Kind = JobKindRip
	}
	switch j.Kind {
	case JobKindRip, JobKindTranscode, JobKindScan:
	default:
		return fmt.Errorf("CreateJob: invalid kind %q", j.Kind)
	}

	// Step materialisation. rip-kind jobs (monolithic + split rip-half)
	// always emit the full canonical 8-step row list; entries in j.Steps
	// act as a skipSet so split-rip handlers can mark the transcode-half
	// steps as 'skipped'. transcode-kind child jobs emit only the
	// 4-step transcode subset.
	skipSet := map[StepID]bool{}
	for _, st := range j.Steps {
		if st.State == JobStepStateSkipped {
			skipSet[st.Step] = true
		}
	}
	var stepIDs []StepID
	switch j.Kind {
	case JobKindTranscode:
		stepIDs = CanonicalTranscodeSteps()
	case JobKindScan:
		stepIDs = CanonicalScanSteps()
	default:
		stepIDs = CanonicalSteps()
	}

	tx, err := s.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO jobs (id, disc_id, drive_id, profile_id, kind, parent_job_id,
		                  spool_path, state, active_step,
		                  progress, speed, eta_seconds, elapsed_seconds, output_bytes,
		                  started_at, finished_at, error_message, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.DiscID, nullString(j.DriveID), j.ProfileID,
		string(j.Kind), nullString(j.ParentJobID), j.SpoolPath,
		string(j.State), string(j.ActiveStep),
		j.Progress, j.Speed, j.ETASeconds, j.ElapsedSeconds, j.OutputBytes,
		timestampPtr(j.StartedAt), timestampPtr(j.FinishedAt),
		j.ErrorMessage, timestamp(j.CreatedAt),
	); err != nil {
		return err
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO job_steps (job_id, step, state, attempt_count,
		                       started_at, finished_at, notes_json)
		VALUES (?, ?, ?, 0, '', '', '{}')`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	steps := make([]JobStep, 0, len(stepIDs))
	for _, sid := range stepIDs {
		stState := JobStepStatePending
		if skipSet[sid] {
			stState = JobStepStateSkipped
		}
		if _, err := stmt.ExecContext(ctx, j.ID, string(sid), string(stState)); err != nil {
			return err
		}
		steps = append(steps, JobStep{Step: sid, State: stState})
	}
	j.Steps = steps

	return tx.Commit()
}

// GetJob fetches a job and its steps. Returns ErrNotFound if missing.
func (s *Store) GetJob(ctx context.Context, id string) (*Job, error) {
	row := s.db.Conn().QueryRowContext(ctx, `
		SELECT id, disc_id, COALESCE(drive_id, ''), profile_id, state, active_step,
		       progress, speed, eta_seconds, elapsed_seconds, output_bytes,
		       started_at, finished_at, error_message, created_at, active_substep,
		       kind, COALESCE(parent_job_id, ''), spool_path
		FROM jobs WHERE id = ?`, id)
	j, err := scanJob(row)
	if err != nil {
		return nil, err
	}
	steps, err := s.ListJobSteps(ctx, id)
	if err != nil {
		return nil, err
	}
	j.Steps = steps
	return j, nil
}

// JobFilter narrows ListJobs.
type JobFilter struct {
	State   JobState // empty = no filter
	DriveID string   // empty = no filter
	Limit   int      // 0 → 50, capped at 200
	Offset  int      // 0 = beginning
}

// ListJobs returns jobs matching f, ordered by created_at DESC. Steps
// are NOT loaded; callers wanting steps use GetJob per-id.
func (s *Store) ListJobs(ctx context.Context, f JobFilter) ([]Job, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}

	q := `SELECT id, disc_id, COALESCE(drive_id, ''), profile_id, state, active_step,
	             progress, speed, eta_seconds, elapsed_seconds, output_bytes,
	             started_at, finished_at, error_message, created_at, active_substep,
	             kind, COALESCE(parent_job_id, ''), spool_path
	      FROM jobs`
	var args []any
	var conds []string
	if f.State != "" {
		conds = append(conds, "state = ?")
		args = append(args, string(f.State))
	}
	if f.DriveID != "" {
		conds = append(conds, "drive_id = ?")
		args = append(args, f.DriveID)
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, f.Limit, f.Offset)

	rows, err := s.db.Conn().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

// ListActiveAndRecentJobs returns currently-active jobs plus the N
// most-recent finished jobs, used for /api/state's first-paint payload.
// Active = state NOT IN ('done','failed','cancelled').
func (s *Store) ListActiveAndRecentJobs(ctx context.Context, recentLimit int) ([]Job, error) {
	if recentLimit < 0 {
		recentLimit = 0
	}

	activeRows, err := s.db.Conn().QueryContext(ctx, `
		SELECT id, disc_id, COALESCE(drive_id, ''), profile_id, state, active_step,
		       progress, speed, eta_seconds, elapsed_seconds, output_bytes,
		       started_at, finished_at, error_message, created_at, active_substep,
		       kind, COALESCE(parent_job_id, ''), spool_path
		FROM jobs
		WHERE state NOT IN ('done','failed','cancelled')
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = activeRows.Close() }()
	var out []Job
	for activeRows.Next() {
		j, err := scanJob(activeRows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	if err := activeRows.Err(); err != nil {
		return nil, err
	}

	if recentLimit > 0 {
		recentRows, err := s.db.Conn().QueryContext(ctx, `
			SELECT id, disc_id, COALESCE(drive_id, ''), profile_id, state, active_step,
			       progress, speed, eta_seconds, elapsed_seconds, output_bytes,
			       started_at, finished_at, error_message, created_at, active_substep,
			       kind, COALESCE(parent_job_id, ''), spool_path
			FROM jobs
			WHERE state IN ('done','failed','cancelled','interrupted')
			ORDER BY COALESCE(NULLIF(finished_at,''), created_at) DESC
			LIMIT ?`, recentLimit)
		if err != nil {
			return nil, err
		}
		defer func() { _ = recentRows.Close() }()
		for recentRows.Next() {
			j, err := scanJob(recentRows)
			if err != nil {
				return nil, err
			}
			out = append(out, *j)
		}
		if err := recentRows.Err(); err != nil {
			return nil, err
		}
	}
	// Hydrate steps for every job so the desktop pipeline stepper and
	// the mobile job rows can render the correct dot colors without an
	// extra round-trip. Without this, /api/state and the SSE snapshot
	// always return step_count=0 and the stepper renders empty.
	//
	// Single batched query: at recentLimit=50 the prior per-job loop did
	// 51 round-trips, fired on every /api/state and every SSE bootstrap.
	if err := s.hydrateJobSteps(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// hydrateJobSteps fills in jobs[i].Steps for every job in the slice
// using one IN (...) query. Empty slice is a no-op.
func (s *Store) hydrateJobSteps(ctx context.Context, jobs []Job) error {
	if len(jobs) == 0 {
		return nil
	}
	ids := make([]any, len(jobs))
	for i, j := range jobs {
		ids[i] = j.ID
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	q := `SELECT job_id, step, state, attempt_count, started_at, finished_at, notes_json
	      FROM job_steps WHERE job_id IN (` + placeholders + `) ORDER BY job_id, id`
	rows, err := s.db.Conn().QueryContext(ctx, q, ids...)
	if err != nil {
		return fmt.Errorf("hydrate steps: %w", err)
	}
	defer func() { _ = rows.Close() }()

	byJob := make(map[string][]JobStep, len(jobs))
	for rows.Next() {
		var jobID, step, stateStr, startedStr, finishedStr, notesJSON string
		var attempt int
		if err := rows.Scan(&jobID, &step, &stateStr, &attempt, &startedStr, &finishedStr, &notesJSON); err != nil {
			return fmt.Errorf("hydrate steps scan: %w", err)
		}
		st := JobStep{
			Step:         StepID(step),
			State:        JobStepState(stateStr),
			AttemptCount: attempt,
		}
		if st.StartedAt, err = parseTimePtr(startedStr); err != nil {
			return fmt.Errorf("parse started_at: %w", err)
		}
		if st.FinishedAt, err = parseTimePtr(finishedStr); err != nil {
			return fmt.Errorf("parse finished_at: %w", err)
		}
		if notesJSON != "" && notesJSON != "{}" {
			var notes map[string]any
			if err := json.Unmarshal([]byte(notesJSON), &notes); err != nil {
				return fmt.Errorf("unmarshal notes_json: %w", err)
			}
			st.Notes = notes
		}
		byJob[jobID] = append(byJob[jobID], st)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range jobs {
		jobs[i].Steps = byJob[jobs[i].ID]
	}
	return nil
}

// LifecycleStates returns disc_id → DiscLifecycleState for the given
// disc IDs using one batched SELECT per table (discs.type, then jobs)
// and an in-memory per-disc reduction via DeriveLifecycleState.
// Empty/nil input returns an empty map. Mirrors the hydrateJobSteps
// batching pattern so buildSnapshot doesn't fan out an N+1 across
// the snapshot's disc list.
func (s *Store) LifecycleStates(ctx context.Context, discIDs []string) (map[string]DiscLifecycleState, error) {
	if len(discIDs) == 0 {
		return map[string]DiscLifecycleState{}, nil
	}

	idArgs := make([]any, len(discIDs))
	for i, id := range discIDs {
		idArgs[i] = id
	}
	placeholders := strings.Repeat("?,", len(idArgs))
	placeholders = placeholders[:len(placeholders)-1]

	// Pull disc type alongside the id — DeriveLifecycleState branches
	// on HasTranscodeHalf, which is a disc-type predicate.
	typeRows, err := s.db.Conn().QueryContext(ctx,
		`SELECT id, type FROM discs WHERE id IN (`+placeholders+`)`, idArgs...)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: disc types: %w", err)
	}
	types := make(map[string]DiscType, len(discIDs))
	for typeRows.Next() {
		var id, tStr string
		if err := typeRows.Scan(&id, &tStr); err != nil {
			_ = typeRows.Close()
			return nil, fmt.Errorf("lifecycle: scan disc type: %w", err)
		}
		types[id] = DiscType(tStr)
	}
	if err := typeRows.Err(); err != nil {
		_ = typeRows.Close()
		return nil, err
	}
	if err := typeRows.Close(); err != nil {
		return nil, err
	}

	// Every job (any state) for these discs. DeriveLifecycleState only
	// reads kind/state/parent_job_id/created_at, so leave the rest off.
	jobRows, err := s.db.Conn().QueryContext(ctx,
		`SELECT id, disc_id, kind, state, COALESCE(parent_job_id, ''), created_at
		   FROM jobs WHERE disc_id IN (`+placeholders+`)`, idArgs...)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: jobs: %w", err)
	}
	defer func() { _ = jobRows.Close() }()
	byDisc := make(map[string][]Job, len(discIDs))
	for jobRows.Next() {
		var id, discID, kindStr, stateStr, parentID, createdStr string
		if err := jobRows.Scan(&id, &discID, &kindStr, &stateStr, &parentID, &createdStr); err != nil {
			return nil, fmt.Errorf("lifecycle: scan job: %w", err)
		}
		created, err := parseTime(createdStr)
		if err != nil {
			return nil, fmt.Errorf("lifecycle: parse created_at: %w", err)
		}
		byDisc[discID] = append(byDisc[discID], Job{
			ID:          id,
			DiscID:      discID,
			Kind:        JobKind(kindStr),
			State:       JobState(stateStr),
			ParentJobID: parentID,
			CreatedAt:   created,
		})
	}
	if err := jobRows.Err(); err != nil {
		return nil, err
	}

	out := make(map[string]DiscLifecycleState, len(discIDs))
	for _, id := range discIDs {
		out[id] = DeriveLifecycleState(types[id], byDisc[id])
	}
	return out, nil
}

// HasActiveJobOnDrive reports whether the given drive currently has a
// job in queued / running / identifying state. Used by the udev event
// handler to drop mid-rip media-change events that would otherwise
// collide with the active job's exclusive hold on /dev/sr0.
func (s *Store) HasActiveJobOnDrive(ctx context.Context, driveID string) (bool, error) {
	if driveID == "" {
		return false, nil
	}
	var n int
	err := s.db.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM jobs
		WHERE drive_id = ? AND state IN ('queued','running','identifying')`,
		driveID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// SetActiveStep writes jobs.active_step without touching progress /
// speed / eta. Called by the persistent sink at OnStepStart so the
// dashboard's pipeline-stepper highlight tracks the running step even
// before any progress event fires.
func (s *Store) SetActiveStep(ctx context.Context, jobID string, step StepID) error {
	_, err := s.db.Conn().ExecContext(ctx,
		`UPDATE jobs SET active_step = ? WHERE id = ?`,
		string(step), jobID)
	return err
}

// HasRecentJobOnDrive reports whether any job on the drive finished
// within the last `cooldown`. Defence-in-depth against the race where
// a spurious mid-rip media-change uevent fires at the *exact* instant
// the current job transitions to done — HasActiveJobOnDrive returns
// false, the guard lets the re-classify through, and the kernel disc
// disturbance trashes whatever the orchestrator was about to do next.
func (s *Store) HasRecentJobOnDrive(ctx context.Context, driveID string, cooldown time.Duration) (bool, error) {
	if driveID == "" || cooldown <= 0 {
		return false, nil
	}
	cutoff := timestamp(time.Now().Add(-cooldown))
	var n int
	err := s.db.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM jobs
		WHERE drive_id = ?
		  AND state IN ('done','failed','cancelled','interrupted')
		  AND finished_at >= ?`,
		driveID, cutoff).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// UpdateJobState transitions a job, optionally bumping started_at /
// finished_at on the relevant transitions. error_message is set on
// JobStateFailed transitions; pass "" otherwise.
func (s *Store) UpdateJobState(ctx context.Context, id string, st JobState, errMsg string) error {
	now := timestamp(time.Now())
	q := `UPDATE jobs SET state = ?`
	args := []any{string(st)}
	switch st {
	case JobStateRunning:
		q += `, started_at = COALESCE(NULLIF(started_at, ''), ?)`
		args = append(args, now)
	case JobStateDone, JobStateFailed, JobStateCancelled, JobStateInterrupted:
		q += `, finished_at = ?`
		args = append(args, now)
	}
	if st == JobStateFailed {
		q += `, error_message = ?`
		args = append(args, errMsg)
	}
	q += ` WHERE id = ?`
	args = append(args, id)
	res, err := s.db.Conn().ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CancelJobIfActive flips a job to cancelled only when it is not already
// in a terminal state. The cancel-fallback paths (Orchestrator.Cancel /
// Compute.Cancel) reach a bare UpdateJobState write when the in-memory
// cancels map has no entry — which is exactly the window where a worker
// has just finished and written done/failed but its map entry is gone.
// An unconditional write there clobbers the terminal state, so a
// completed rip would show as cancelled. Guarding on the state column
// makes the late Stop a no-op. A missing job is likewise a no-op:
// cancelling something that no longer exists is benign.
func (s *Store) CancelJobIfActive(ctx context.Context, id string) error {
	_, err := s.db.Conn().ExecContext(ctx, `
		UPDATE jobs SET state = ?, finished_at = ?
		WHERE id = ?
		  AND state NOT IN ('done','failed','cancelled','interrupted')`,
		string(JobStateCancelled), timestamp(time.Now()), id)
	return err
}

// UpdateJobProgress writes the volatile progress fields. Cheap; called
// by the orchestrator at most once per second per job.
func (s *Store) UpdateJobProgress(ctx context.Context, id string, activeStep StepID,
	pct float64, speed string, etaSeconds, elapsedSeconds int) error {
	res, err := s.db.Conn().ExecContext(ctx, `
		UPDATE jobs SET active_step = ?, progress = ?, speed = ?,
		                eta_seconds = ?, elapsed_seconds = ?
		WHERE id = ?`,
		string(activeStep), pct, speed, etaSeconds, elapsedSeconds, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateJobSubStep records the redumper sub-phase (DUMP, REFINE, SPLIT
// etc.) so the dashboard can show something useful while the rip step
// is in a long quiet phase. Empty substep clears the field.
func (s *Store) UpdateJobSubStep(ctx context.Context, id, substep string) error {
	res, err := s.db.Conn().ExecContext(ctx,
		`UPDATE jobs SET active_substep = ? WHERE id = ?`, substep, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordOutputBytes writes the move step's final output size onto a
// job row. Called from each pipeline after the move step succeeds so
// the LIBRARY SIZE widget can sum across all done jobs.
func (s *Store) RecordOutputBytes(ctx context.Context, jobID string, bytes int64) error {
	res, err := s.db.Conn().ExecContext(ctx,
		`UPDATE jobs SET output_bytes = ? WHERE id = ?`, bytes, jobID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ActiveSpoolReferences returns the set of spool dir basenames the
// store currently considers in-use. Spool dir name comes from two
// places: (a) rip-kind jobs in pre-terminal states own the spool dir
// named after their ID, (b) transcode-kind jobs in any state that
// might still consume the spool (queued/running/failed/interrupted —
// the failed+interrupted set is what powers the retry-transcode
// flow) reference the spool dir via their spool_path basename.
// Cancelled jobs are excluded — cancel is user intent to abandon.
//
// Returned ripIDs and transcodeSpoolBasenames are the names to keep;
// anything else under the spool root is fair game for GC.
func (s *Store) ActiveSpoolReferences(ctx context.Context) (ripIDs []string, transcodeSpoolBasenames []string, err error) {
	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT id FROM jobs
		WHERE kind = 'rip'
		  AND state IN ('queued','identifying','running','paused')`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, nil, err
		}
		ripIDs = append(ripIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	rows2, err := s.db.Conn().QueryContext(ctx, `
		SELECT spool_path FROM jobs
		WHERE kind = 'transcode'
		  AND spool_path != ''
		  AND state IN ('queued','identifying','running','paused','failed','interrupted')`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows2.Close() }()
	for rows2.Next() {
		var p string
		if err := rows2.Scan(&p); err != nil {
			return nil, nil, err
		}
		// Strip to basename so callers can match against on-disk dir names.
		// The trailing-slash + path-separator scan handles both forward
		// and back slashes for cross-platform safety.
		for i := len(p) - 1; i >= 0; i-- {
			if p[i] == '/' || p[i] == '\\' {
				p = p[i+1:]
				break
			}
		}
		if p != "" {
			transcodeSpoolBasenames = append(transcodeSpoolBasenames, p)
		}
	}
	if err := rows2.Err(); err != nil {
		return nil, nil, err
	}
	return ripIDs, transcodeSpoolBasenames, nil
}

// ResetTranscodeJob resets a transcode-kind job back to queued so the
// compute worker re-runs it from the existing spool. Used by the
// retry-transcode endpoint after a failed or interrupted transcode.
// Clears job-level volatile fields (active_step, progress, speed,
// error_message, started_at/finished_at) and flips every non-skipped
// step row back to pending with the timestamps cleared. Validates
// kind=transcode and state in {failed, interrupted}; returns
// ErrInvalidJobKindForRetry / ErrInvalidJobStateForRetry otherwise.
//
// One transaction so the UI never sees a half-reset job.
func (s *Store) ResetTranscodeJob(ctx context.Context, id string) error {
	tx, err := s.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var kindStr, stateStr string
	if err := tx.QueryRowContext(ctx,
		`SELECT kind, state FROM jobs WHERE id = ?`, id).
		Scan(&kindStr, &stateStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if JobKind(kindStr) != JobKindTranscode {
		return ErrInvalidJobKindForRetry
	}
	switch JobState(stateStr) {
	case JobStateFailed, JobStateInterrupted:
		// OK to retry.
	default:
		return ErrInvalidJobStateForRetry
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE jobs
		   SET state         = 'queued',
		       active_step   = '',
		       active_substep= '',
		       progress      = 0,
		       speed         = '',
		       eta_seconds   = 0,
		       elapsed_seconds = 0,
		       started_at    = '',
		       finished_at   = '',
		       error_message = ''
		 WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE job_steps
		   SET state       = 'pending',
		       started_at  = '',
		       finished_at = '',
		       attempt_count = attempt_count + 1
		 WHERE job_id = ? AND state != 'skipped'`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ErrInvalidJobKindForRetry is returned by ResetTranscodeJob when the
// requested job isn't kind=transcode.
var ErrInvalidJobKindForRetry = errors.New("state: job is not a transcode job")

// ErrInvalidJobStateForRetry is returned by ResetTranscodeJob when the
// requested job isn't in a retryable terminal state.
var ErrInvalidJobStateForRetry = errors.New("state: job is not in a retryable state")

// MarkInterruptedJobs flips every job in {queued, identifying, running}
// to interrupted. Used at daemon startup so crashed-mid-rip jobs are
// visible in the UI for resolution. Returns the count flipped.
//
// Also flips any orphan job_steps still in 'running' for those jobs to
// 'failed' (with finished_at stamped). Without this, the job-detail
// pipeline view shows a stale "running" indicator on the step that was
// active when the daemon crashed. The job's parent state already carries
// the "interrupted" context for the user.
func (s *Store) MarkInterruptedJobs(ctx context.Context) (int, error) {
	now := timestamp(time.Now())
	tx, err := s.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		UPDATE jobs
		SET state = 'interrupted', finished_at = ?
		WHERE state IN ('queued','identifying','running')`, now)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()

	if _, err := tx.ExecContext(ctx, `
		UPDATE job_steps
		SET state = 'failed', finished_at = ?
		WHERE state = 'running'
		  AND job_id IN (SELECT id FROM jobs WHERE state = 'interrupted')`, now); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}

func scanJob(r rowScanner) (*Job, error) {
	var j Job
	var st, activeStep, startedStr, finishedStr, createdStr, kindStr string
	if err := r.Scan(
		&j.ID, &j.DiscID, &j.DriveID, &j.ProfileID, &st, &activeStep,
		&j.Progress, &j.Speed, &j.ETASeconds, &j.ElapsedSeconds, &j.OutputBytes,
		&startedStr, &finishedStr, &j.ErrorMessage, &createdStr, &j.ActiveSubStep,
		&kindStr, &j.ParentJobID, &j.SpoolPath,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	j.State = JobState(st)
	j.ActiveStep = StepID(activeStep)
	j.Kind = JobKind(kindStr)
	var err error
	if j.StartedAt, err = parseTimePtr(startedStr); err != nil {
		return nil, fmt.Errorf("parse started_at: %w", err)
	}
	if j.FinishedAt, err = parseTimePtr(finishedStr); err != nil {
		return nil, fmt.Errorf("parse finished_at: %w", err)
	}
	if j.CreatedAt, err = parseTime(createdStr); err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	return &j, nil
}

// ---- JOB STEPS (read-side; mutators in next commit) -----------------------

// ListJobSteps returns every job_step row for a job, in insertion order
// (= canonical step sequence, since CreateJob inserts them that way).
func (s *Store) ListJobSteps(ctx context.Context, jobID string) ([]JobStep, error) {
	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT step, state, attempt_count, started_at, finished_at, notes_json
		FROM job_steps WHERE job_id = ?
		ORDER BY id`, jobID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []JobStep
	for rows.Next() {
		st, err := scanJobStep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *st)
	}
	return out, rows.Err()
}

func scanJobStep(r rowScanner) (*JobStep, error) {
	var st JobStep
	var step, stateStr, startedStr, finishedStr, notesJSON string
	if err := r.Scan(&step, &stateStr, &st.AttemptCount, &startedStr, &finishedStr, &notesJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	st.Step = StepID(step)
	st.State = JobStepState(stateStr)
	var err error
	if st.StartedAt, err = parseTimePtr(startedStr); err != nil {
		return nil, fmt.Errorf("parse started_at: %w", err)
	}
	if st.FinishedAt, err = parseTimePtr(finishedStr); err != nil {
		return nil, fmt.Errorf("parse finished_at: %w", err)
	}
	if notesJSON != "" && notesJSON != "{}" {
		var notes map[string]any
		if err := json.Unmarshal([]byte(notesJSON), &notes); err != nil {
			return nil, fmt.Errorf("unmarshal notes_json: %w", err)
		}
		st.Notes = notes
	}
	return &st, nil
}

// UpdateJobStepState transitions a step's state. Sets started_at on
// transitions to running; sets finished_at on transitions to
// {done, skipped, failed}. Bumps attempt_count on every transition to
// running (so "running again after failure" reads as a retry).
func (s *Store) UpdateJobStepState(ctx context.Context, jobID string, step StepID, st JobStepState) error {
	now := timestamp(time.Now())
	q := `UPDATE job_steps SET state = ?`
	args := []any{string(st)}
	switch st {
	case JobStepStateRunning:
		q += `, started_at = COALESCE(NULLIF(started_at, ''), ?), attempt_count = attempt_count + 1`
		args = append(args, now)
	case JobStepStateDone, JobStepStateSkipped, JobStepStateFailed:
		q += `, finished_at = ?`
		args = append(args, now)
	}
	q += ` WHERE job_id = ? AND step = ?`
	args = append(args, jobID, string(step))
	res, err := s.db.Conn().ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// AppendJobStepNotes merges the given map into the step's notes_json.
// Concurrent appenders may race; M1.1's pipeline is single-writer per
// step so this is acceptable.
func (s *Store) AppendJobStepNotes(ctx context.Context, jobID string, step StepID, extra map[string]any) error {
	if len(extra) == 0 {
		return nil
	}
	tx, err := s.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var current string
	row := tx.QueryRowContext(ctx,
		`SELECT notes_json FROM job_steps WHERE job_id = ? AND step = ?`,
		jobID, string(step))
	if err := row.Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	merged := map[string]any{}
	if current != "" {
		if err := json.Unmarshal([]byte(current), &merged); err != nil {
			return fmt.Errorf("unmarshal notes_json: %w", err)
		}
	}
	for k, v := range extra {
		merged[k] = v
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("marshal notes_json: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE job_steps SET notes_json = ? WHERE job_id = ? AND step = ?`,
		string(encoded), jobID, string(step)); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- LOG LINES ------------------------------------------------------------

// AppendLogLine inserts one row.
func (s *Store) AppendLogLine(ctx context.Context, l LogLine) error {
	_, err := s.db.Conn().ExecContext(ctx, `
		INSERT INTO log_lines (job_id, t, step, level, message)
		VALUES (?, ?, ?, ?, ?)`,
		l.JobID, timestamp(l.T), string(l.Step), string(l.Level), l.Message)
	return err
}

// TailLogLines returns the most recent n log lines for a job, oldest
// first (the same order they'd appear in a follow-the-tail UI). n<=0
// defaults to 200.
func (s *Store) TailLogLines(ctx context.Context, jobID string, n int) ([]LogLine, error) {
	if n <= 0 {
		n = 200
	}
	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT job_id, t, step, level, message FROM log_lines
		WHERE job_id = ? ORDER BY id DESC LIMIT ?`, jobID, n)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var reversed []LogLine
	for rows.Next() {
		var l LogLine
		var tStr, step, level string
		if err := rows.Scan(&l.JobID, &tStr, &step, &level, &l.Message); err != nil {
			return nil, err
		}
		l.Step = StepID(step)
		l.Level = LogLevel(level)
		t, err := parseTime(tStr)
		if err != nil {
			return nil, fmt.Errorf("parse t: %w", err)
		}
		l.T = t
		reversed = append(reversed, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed, nil
}

// LogFilter narrows ListLogLines. Empty Step matches every line. Limit
// is clamped to (0, 2000]; Offset is clamped to >= 0.
type LogFilter struct {
	Step   StepID
	Limit  int
	Offset int
}

// ListLogLines returns log lines for a job in insertion order
// (oldest → newest), optionally filtered by step, paginated by
// limit/offset. Also returns the total matching count (ignoring the
// limit/offset window) so the webui can size its paging.
func (s *Store) ListLogLines(ctx context.Context, jobID string, f LogFilter) ([]LogLine, int, error) {
	if f.Limit <= 0 {
		f.Limit = 500
	}
	if f.Limit > 2000 {
		f.Limit = 2000
	}
	if f.Offset < 0 {
		f.Offset = 0
	}

	args := []any{jobID}
	where := "WHERE job_id = ?"
	if f.Step != "" {
		where += " AND step = ?"
		args = append(args, string(f.Step))
	}

	var total int
	if err := s.db.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM log_lines `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, f.Limit, f.Offset)
	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT job_id, t, step, level, message FROM log_lines
		`+where+` ORDER BY id ASC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []LogLine
	for rows.Next() {
		var l LogLine
		var tStr, step, level string
		if err := rows.Scan(&l.JobID, &tStr, &step, &level, &l.Message); err != nil {
			return nil, 0, err
		}
		l.Step = StepID(step)
		l.Level = LogLevel(level)
		t, err := parseTime(tStr)
		if err != nil {
			return nil, 0, fmt.Errorf("parse t: %w", err)
		}
		l.T = t
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// triggersInclude reports whether `triggers` (a comma-separated list)
// contains the given trigger token.
func triggersInclude(triggers, trigger string) bool {
	for _, t := range strings.Split(triggers, ",") {
		if strings.TrimSpace(t) == trigger {
			return true
		}
	}
	return false
}

// ---- NOTIFICATIONS --------------------------------------------------------

// CreateNotification inserts a new notification.
func (s *Store) CreateNotification(ctx context.Context, n *Notification) error {
	if n.ID == "" {
		n.ID = NewID()
	}
	now := time.Now()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	if n.UpdatedAt.IsZero() {
		n.UpdatedAt = now
	}
	if n.Triggers == "" {
		n.Triggers = "done,failed"
	}
	_, err := s.db.Conn().ExecContext(ctx, `
		INSERT INTO notifications (id, name, url, tags, triggers, enabled,
		                           created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.Name, n.URL, n.Tags, n.Triggers, boolToInt(n.Enabled),
		timestamp(n.CreatedAt), timestamp(n.UpdatedAt))
	return err
}

// GetNotification fetches a notification by ID.
func (s *Store) GetNotification(ctx context.Context, id string) (*Notification, error) {
	row := s.db.Conn().QueryRowContext(ctx, `
		SELECT id, name, url, tags, triggers, enabled, created_at, updated_at
		FROM notifications WHERE id = ?`, id)
	return scanNotification(row)
}

// ListNotifications returns every notification ordered by name.
func (s *Store) ListNotifications(ctx context.Context) ([]Notification, error) {
	return s.queryNotifications(ctx, `
		SELECT id, name, url, tags, triggers, enabled, created_at, updated_at
		FROM notifications ORDER BY name`)
}

// ListNotificationsForTrigger returns enabled notifications whose
// triggers list contains the given token. SQL pre-filter is by LIKE
// (cheap) then post-checked in Go to avoid false positives like
// "donezo" matching "done".
func (s *Store) ListNotificationsForTrigger(ctx context.Context, trigger string) ([]Notification, error) {
	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT id, name, url, tags, triggers, enabled, created_at, updated_at
		FROM notifications
		WHERE enabled = 1 AND triggers LIKE '%' || ? || '%'`, trigger)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		if triggersInclude(n.Triggers, trigger) {
			out = append(out, *n)
		}
	}
	return out, rows.Err()
}

// UpdateNotification rewrites every mutable column. ID + CreatedAt stay.
func (s *Store) UpdateNotification(ctx context.Context, n *Notification) error {
	n.UpdatedAt = time.Now()
	res, err := s.db.Conn().ExecContext(ctx, `
		UPDATE notifications SET name = ?, url = ?, tags = ?, triggers = ?,
		                         enabled = ?, updated_at = ?
		WHERE id = ?`,
		n.Name, n.URL, n.Tags, n.Triggers, boolToInt(n.Enabled),
		timestamp(n.UpdatedAt), n.ID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteNotification removes a notification.
func (s *Store) DeleteNotification(ctx context.Context, id string) error {
	res, err := s.db.Conn().ExecContext(ctx, `DELETE FROM notifications WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) queryNotifications(ctx context.Context, q string, args ...any) ([]Notification, error) {
	rows, err := s.db.Conn().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

func scanNotification(r rowScanner) (*Notification, error) {
	var n Notification
	var enabled int
	var createdStr, updatedStr string
	if err := r.Scan(&n.ID, &n.Name, &n.URL, &n.Tags, &n.Triggers, &enabled, &createdStr, &updatedStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	n.Enabled = enabled != 0
	var err error
	if n.CreatedAt, err = parseTime(createdStr); err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	if n.UpdatedAt, err = parseTime(updatedStr); err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	return &n, nil
}

// ---- SETTINGS -------------------------------------------------------------

// GetSetting returns the value for key. ErrNotFound if missing.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	row := s.db.Conn().QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, key)
	var v string
	if err := row.Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return v, nil
}

// GetAllSettings returns every key-value pair as a map.
func (s *Store) GetAllSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.Conn().QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// GetBool returns the value for key parsed as bool. Returns (false, nil)
// if the key is missing or unparseable — callers treat absent settings as false.
func (s *Store) GetBool(ctx context.Context, key string) (bool, error) {
	v, err := s.GetSetting(ctx, key)
	if err != nil || v == "" {
		return false, nil
	}
	b, perr := strconv.ParseBool(v)
	if perr != nil {
		return false, nil
	}
	return b, nil
}

// GetInt returns the value for key parsed as int. Returns (0, nil)
// if the key is missing or unparseable.
func (s *Store) GetInt(ctx context.Context, key string) (int, error) {
	v, err := s.GetSetting(ctx, key)
	if err != nil || v == "" {
		return 0, nil
	}
	n, perr := strconv.Atoi(v)
	if perr != nil {
		return 0, nil
	}
	return n, nil
}

// SetSetting upserts (key, value).
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.Conn().ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, timestamp(time.Now()))
	return err
}

// ---- HISTORY (query layer over jobs + discs) ------------------------------

// HistoryFilter narrows ListHistory.
type HistoryFilter struct {
	Type   DiscType
	From   time.Time
	To     time.Time
	Limit  int
	Offset int
}

// HistoryRow is one finished job + the disc it ripped.
type HistoryRow struct {
	Job  Job  `json:"job"`
	Disc Disc `json:"disc"`
}

// ListHistory returns finished jobs (state in done/failed/cancelled)
// joined with their disc, ordered by finished_at DESC.
func (s *Store) ListHistory(ctx context.Context, f HistoryFilter) ([]HistoryRow, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}

	q := `
		SELECT
		  j.id, j.disc_id, COALESCE(j.drive_id, ''), j.profile_id, j.state, j.active_step,
		  j.progress, j.speed, j.eta_seconds, j.elapsed_seconds, j.output_bytes,
		  j.started_at, j.finished_at, j.error_message, j.created_at, j.active_substep,
		  j.kind, COALESCE(j.parent_job_id, ''), j.spool_path,
		  d.id, COALESCE(d.drive_id, ''), d.type, d.title, d.year, d.runtime_seconds,
		  d.size_bytes_raw, d.toc_hash, d.metadata_provider, d.metadata_id,
		  d.candidates_json, d.metadata_json, d.created_at
		FROM jobs j
		JOIN discs d ON j.disc_id = d.id
		WHERE j.state IN ('done','failed','cancelled','interrupted')
	`
	args := []any{}
	if f.Type != "" {
		q += " AND d.type = ?"
		args = append(args, string(f.Type))
	}
	if !f.From.IsZero() {
		q += " AND j.finished_at >= ?"
		args = append(args, timestamp(f.From))
	}
	if !f.To.IsZero() {
		q += " AND j.finished_at <= ?"
		args = append(args, timestamp(f.To))
	}
	q += " ORDER BY j.finished_at DESC LIMIT ? OFFSET ?"
	args = append(args, f.Limit, f.Offset)

	rows, err := s.db.Conn().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []HistoryRow
	for rows.Next() {
		var (
			j                                              Job
			d                                              Disc
			jState, jActive, jStarted, jFinished, jCreated string
			jKind                                          string
			dType, dCands, dMeta, dCreated                 string
		)
		if err := rows.Scan(
			&j.ID, &j.DiscID, &j.DriveID, &j.ProfileID, &jState, &jActive,
			&j.Progress, &j.Speed, &j.ETASeconds, &j.ElapsedSeconds, &j.OutputBytes,
			&jStarted, &jFinished, &j.ErrorMessage, &jCreated, &j.ActiveSubStep,
			&jKind, &j.ParentJobID, &j.SpoolPath,
			&d.ID, &d.DriveID, &dType, &d.Title, &d.Year, &d.RuntimeSeconds,
			&d.SizeBytesRaw, &d.TOCHash, &d.MetadataProvider, &d.MetadataID,
			&dCands, &dMeta, &dCreated,
		); err != nil {
			return nil, err
		}
		j.State = JobState(jState)
		j.ActiveStep = StepID(jActive)
		j.Kind = JobKind(jKind)
		var err error
		if j.StartedAt, err = parseTimePtr(jStarted); err != nil {
			return nil, fmt.Errorf("parse j.started_at: %w", err)
		}
		if j.FinishedAt, err = parseTimePtr(jFinished); err != nil {
			return nil, fmt.Errorf("parse j.finished_at: %w", err)
		}
		if j.CreatedAt, err = parseTime(jCreated); err != nil {
			return nil, fmt.Errorf("parse j.created_at: %w", err)
		}
		d.Type = DiscType(dType)
		if d.Candidates, err = unmarshalCandidates(dCands); err != nil {
			return nil, fmt.Errorf("unmarshal candidates: %w", err)
		}
		d.MetadataJSON = dMeta
		if d.CreatedAt, err = parseTime(dCreated); err != nil {
			return nil, fmt.Errorf("parse d.created_at: %w", err)
		}
		out = append(out, HistoryRow{Job: j, Disc: d})
	}
	return out, rows.Err()
}

// CountHistory returns the count of rows matching the filter.
func (s *Store) CountHistory(ctx context.Context, f HistoryFilter) (int, error) {
	q := `SELECT COUNT(*) FROM jobs j WHERE j.state IN ('done','failed','cancelled','interrupted')`
	args := []any{}
	if f.Type != "" {
		q = `SELECT COUNT(*) FROM jobs j JOIN discs d ON j.disc_id = d.id
		     WHERE j.state IN ('done','failed','cancelled','interrupted') AND d.type = ?`
		args = append(args, string(f.Type))
	}
	if !f.From.IsZero() {
		q += " AND j.finished_at >= ?"
		args = append(args, timestamp(f.From))
	}
	if !f.To.IsZero() {
		q += " AND j.finished_at <= ?"
		args = append(args, timestamp(f.To))
	}
	row := s.db.Conn().QueryRowContext(ctx, q, args...)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// ListDiscHistory returns disc-as-unit history rows, newest-first by
// latest associated job. Discs with zero jobs are excluded (those are
// awaiting-decision discs, surfaced by AwaitingDecisionList instead).
//
// limit / offset use the same pagination shape as ListHistory.
// Returns (rows, total, error) — total counts ALL discs with at least
// one job (for the UI pager), not just the returned page.
func (s *Store) ListDiscHistory(ctx context.Context, limit, offset int) ([]DiscHistoryRow, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	err := s.db.Conn().QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT disc_id) FROM jobs
		WHERE disc_id IS NOT NULL AND disc_id != ''`).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("disc history count: %w", err)
	}
	if total == 0 {
		return nil, 0, nil
	}

	// One row per disc; sort by latest job timestamp. We pull the
	// disc IDs first, then hydrate (disc fields + lifecycle + journey
	// summary) in batched follow-up queries.
	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT j.disc_id, MAX(j.created_at) AS latest_at
		FROM jobs j
		WHERE j.disc_id IS NOT NULL AND j.disc_id != ''
		GROUP BY j.disc_id
		ORDER BY latest_at DESC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("disc history page: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var discIDs []string
	for rows.Next() {
		var id, latestAt string
		if err := rows.Scan(&id, &latestAt); err != nil {
			return nil, 0, fmt.Errorf("disc history scan: %w", err)
		}
		discIDs = append(discIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(discIDs) == 0 {
		return nil, total, nil
	}

	discs, err := s.discsByIDs(ctx, discIDs)
	if err != nil {
		return nil, 0, err
	}
	states, err := s.LifecycleStates(ctx, discIDs)
	if err != nil {
		return nil, 0, err
	}
	summaries, err := s.discJourneySummaries(ctx, discIDs)
	if err != nil {
		return nil, 0, err
	}

	out := make([]DiscHistoryRow, 0, len(discIDs))
	for _, id := range discIDs {
		d := discs[id]
		d.LifecycleState = states[id]
		row := DiscHistoryRow{Disc: d}
		if sum, ok := summaries[id]; ok {
			row.LatestRipJobID = sum.LatestRipJobID
			row.LatestTranscodeJobID = sum.LatestTranscodeJobID
			row.FirstAttemptAt = sum.FirstAttemptAt
			row.CompletedAt = sum.CompletedAt
			row.OutputBytes = sum.OutputBytes
			row.OutputPaths = sum.OutputPaths
			row.AttemptSummary = sum.AttemptSummary
		}
		out = append(out, row)
	}
	return out, total, nil
}

// discJourneySummary collects the per-disc fields ListDiscHistory needs.
// Internal helper struct — never serialised; the caller maps it onto
// DiscHistoryRow.
type discJourneySummary struct {
	LatestRipJobID       string
	LatestTranscodeJobID string
	FirstAttemptAt       time.Time
	CompletedAt          *time.Time
	OutputBytes          int64
	OutputPaths          []string
	AttemptSummary       string
}

// discJourneySummaries returns one summary per disc ID. Issues one
// batched SELECT against jobs + one against job_steps for move-step
// notes. Discs with no jobs are absent from the result.
func (s *Store) discJourneySummaries(ctx context.Context, discIDs []string) (map[string]discJourneySummary, error) {
	if len(discIDs) == 0 {
		return map[string]discJourneySummary{}, nil
	}
	args := make([]any, len(discIDs))
	for i, id := range discIDs {
		args[i] = id
	}
	placeholders := strings.Repeat("?,", len(args))
	placeholders = placeholders[:len(placeholders)-1]

	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT id, disc_id, COALESCE(kind, 'rip'), state, COALESCE(parent_job_id, ''),
		       created_at, finished_at, output_bytes
		  FROM jobs WHERE disc_id IN (`+placeholders+`)
		  ORDER BY disc_id, created_at`, args...)
	if err != nil {
		return nil, fmt.Errorf("journey jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type bucket struct {
		latestRip       *Job
		latestTranscode *Job
		first           time.Time
		completed       *time.Time
		bytes           int64
		ok              int
		failed          int
		cancelled       int
		interrupted     int
	}
	out := map[string]bucket{}

	for rows.Next() {
		var (
			id, discID, kindStr, stateStr, parentID string
			createdStr, finishedStr                 string
			outBytes                                int64
		)
		if err := rows.Scan(&id, &discID, &kindStr, &stateStr, &parentID,
			&createdStr, &finishedStr, &outBytes); err != nil {
			return nil, fmt.Errorf("journey scan: %w", err)
		}
		created, err := parseTime(createdStr)
		if err != nil {
			return nil, fmt.Errorf("journey parse created: %w", err)
		}
		finished, err := parseTimePtr(finishedStr)
		if err != nil {
			return nil, fmt.Errorf("journey parse finished: %w", err)
		}
		j := Job{
			ID:          id,
			DiscID:      discID,
			Kind:        JobKind(kindStr),
			State:       JobState(stateStr),
			ParentJobID: parentID,
			CreatedAt:   created,
			FinishedAt:  finished,
			OutputBytes: outBytes,
		}
		b := out[discID]
		if b.first.IsZero() || j.CreatedAt.Before(b.first) {
			b.first = j.CreatedAt
		}
		kind := j.Kind
		if kind == "" {
			kind = JobKindRip
		}
		switch kind {
		case JobKindRip:
			if b.latestRip == nil || j.CreatedAt.After(b.latestRip.CreatedAt) {
				jj := j
				b.latestRip = &jj
			}
		case JobKindTranscode:
			if b.latestTranscode == nil || j.CreatedAt.After(b.latestTranscode.CreatedAt) {
				jj := j
				b.latestTranscode = &jj
			}
		}
		switch j.State {
		case JobStateDone:
			b.ok++
			b.bytes += j.OutputBytes
			if j.FinishedAt != nil {
				b.completed = j.FinishedAt
			}
		case JobStateFailed:
			b.failed++
		case JobStateCancelled:
			b.cancelled++
		case JobStateInterrupted:
			b.interrupted++
		}
		out[discID] = b
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	pathsByDisc, err := s.movePathsForDiscs(ctx, discIDs)
	if err != nil {
		return nil, err
	}

	summaries := make(map[string]discJourneySummary, len(out))
	for discID, b := range out {
		sum := discJourneySummary{
			FirstAttemptAt: b.first,
			CompletedAt:    b.completed,
			OutputBytes:    b.bytes,
			OutputPaths:    pathsByDisc[discID],
		}
		if b.latestRip != nil {
			sum.LatestRipJobID = b.latestRip.ID
		}
		if b.latestTranscode != nil {
			sum.LatestTranscodeJobID = b.latestTranscode.ID
		}
		totalAttempts := b.ok + b.failed + b.cancelled + b.interrupted
		if totalAttempts > 1 {
			parts := []string{}
			if b.failed > 0 {
				parts = append(parts, fmt.Sprintf("%d failures", b.failed))
			}
			if b.cancelled > 0 {
				parts = append(parts, fmt.Sprintf("%d cancelled", b.cancelled))
			}
			if b.interrupted > 0 {
				parts = append(parts, fmt.Sprintf("%d interrupted", b.interrupted))
			}
			if b.ok > 0 {
				parts = append(parts, fmt.Sprintf("%d success", b.ok))
			}
			sum.AttemptSummary = strings.Join(parts, ", ")
		}
		summaries[discID] = sum
	}
	return summaries, nil
}

// movePathsForDiscs returns disc_id → list of file paths produced by
// the latest done move step on the disc's journey. Empty for discs
// whose journey hasn't moved any files yet.
func (s *Store) movePathsForDiscs(ctx context.Context, discIDs []string) (map[string][]string, error) {
	if len(discIDs) == 0 {
		return map[string][]string{}, nil
	}
	args := make([]any, len(discIDs))
	for i, id := range discIDs {
		args[i] = id
	}
	placeholders := strings.Repeat("?,", len(args))
	placeholders = placeholders[:len(placeholders)-1]
	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT j.disc_id, s.notes_json
		  FROM job_steps s
		  JOIN jobs j ON j.id = s.job_id
		 WHERE j.disc_id IN (`+placeholders+`)
		   AND s.step = 'move' AND s.state = 'done'
		 ORDER BY j.disc_id, s.id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("move paths: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]string{}
	for rows.Next() {
		var discID, notesJSON string
		if err := rows.Scan(&discID, &notesJSON); err != nil {
			return nil, fmt.Errorf("move paths scan: %w", err)
		}
		if _, ok := out[discID]; ok {
			continue
		}
		if notesJSON == "" || notesJSON == "{}" {
			continue
		}
		var notes map[string]any
		if err := json.Unmarshal([]byte(notesJSON), &notes); err != nil {
			continue
		}
		raw, ok := notes["paths"].([]any)
		if !ok {
			continue
		}
		paths := make([]string, 0, len(raw))
		for _, p := range raw {
			if s, ok := p.(string); ok {
				paths = append(paths, s)
			}
		}
		out[discID] = paths
	}
	return out, rows.Err()
}

// discsByIDs returns id → Disc for the given disc IDs in a single
// SELECT. Used by ListDiscHistory and any other batched disc loader.
func (s *Store) discsByIDs(ctx context.Context, ids []string) (map[string]Disc, error) {
	if len(ids) == 0 {
		return map[string]Disc{}, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	placeholders := strings.Repeat("?,", len(args))
	placeholders = placeholders[:len(placeholders)-1]
	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT id, COALESCE(drive_id, ''), type, title, year, runtime_seconds,
		       size_bytes_raw, toc_hash, metadata_provider, metadata_id,
		       candidates_json, metadata_json, created_at
		  FROM discs WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("discsByIDs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]Disc{}
	for rows.Next() {
		d, err := scanDisc(rows)
		if err != nil {
			return nil, err
		}
		out[d.ID] = *d
	}
	return out, rows.Err()
}

// Retention buckets group terminal jobs by outcome. A successful rip is
// state 'done'; everything else terminal counts as a failure for retention
// purposes. Both require a non-NULL finished_at.
var (
	retentionSuccessStates = []string{"done"}
	retentionFailedStates  = []string{"failed", "cancelled", "interrupted"}
)

// RetentionPolicy is the per-outcome retention configuration. For each
// bucket, Days caps age (delete entries older than N days) and Count caps
// quantity (keep only the N most recent). 0 means "no limit" for that knob;
// both may be set, in which case an entry is pruned if it trips either.
type RetentionPolicy struct {
	SuccessDays  int
	SuccessCount int
	FailedDays   int
	FailedCount  int
}

// active reports whether any knob would prune something.
func (p RetentionPolicy) active() bool {
	return p.SuccessDays > 0 || p.SuccessCount > 0 || p.FailedDays > 0 || p.FailedCount > 0
}

// RetentionResult reports how much a prune removed (or would remove).
type RetentionResult struct {
	SuccessDeleted int
	FailedDeleted  int
	DiscsDeleted   int
}

// Total is the number of history (job) entries removed across buckets.
func (r RetentionResult) Total() int { return r.SuccessDeleted + r.FailedDeleted }

// retentionWhere builds the WHERE body selecting the jobs to prune for one
// bucket, plus its bind args. Returns ("", nil) when the bucket has no active
// limit. The state list is composed of constant literals (no injection). The
// age and count arms are OR'd: an entry is selected if it is older than the
// cutoff OR it falls outside the newest Count of the bucket.
func retentionWhere(states []string, days, count int, now time.Time) (string, []any) {
	inList := "'" + strings.Join(states, "','") + "'"
	var arms []string
	var args []any
	if days > 0 {
		cutoff := now.Add(-time.Duration(days) * 24 * time.Hour)
		arms = append(arms, "finished_at < ?")
		args = append(args, cutoff.UTC().Format(time.RFC3339Nano))
	}
	if count > 0 {
		arms = append(arms,
			"id NOT IN (SELECT id FROM jobs WHERE state IN ("+inList+
				") AND finished_at IS NOT NULL ORDER BY finished_at DESC, id DESC LIMIT ?)")
		args = append(args, count)
	}
	if len(arms) == 0 {
		return "", nil
	}
	return "state IN (" + inList + ") AND finished_at IS NOT NULL AND (" +
		strings.Join(arms, " OR ") + ")", args
}

// retentionOrphanDiscSQL drops discs left with no job after a prune, but
// keeps a drive's most-recent disc (the proxy for "still physically
// inserted") so the awaiting-decision card doesn't point at a deleted row.
// Verbatim guard from ClearHistory / DeleteJobAndOrphans — the old
// PruneHistoryBefore omitted it (latent bug).
const retentionOrphanDiscSQL = `
	DELETE FROM discs
	WHERE NOT EXISTS (SELECT 1 FROM jobs WHERE jobs.disc_id = discs.id)
	  AND (
	    discs.drive_id IS NULL OR discs.drive_id = ''
	    OR EXISTS (
	      SELECT 1 FROM discs d2
	      WHERE d2.drive_id = discs.drive_id
	        AND d2.created_at > discs.created_at
	    )
	  )`

// PruneHistory deletes terminal jobs that exceed the policy's per-outcome
// age/count limits, then drops the discs left orphaned (guarded). FK cascades
// remove job_steps and log_lines. Files on disk are never touched. One tx.
func (s *Store) PruneHistory(ctx context.Context, p RetentionPolicy, now time.Time) (RetentionResult, error) {
	var res RetentionResult
	if !p.active() {
		return res, nil
	}
	tx, err := s.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	buckets := []struct {
		states []string
		days   int
		count  int
		out    *int
	}{
		{retentionSuccessStates, p.SuccessDays, p.SuccessCount, &res.SuccessDeleted},
		{retentionFailedStates, p.FailedDays, p.FailedCount, &res.FailedDeleted},
	}
	for _, b := range buckets {
		where, args := retentionWhere(b.states, b.days, b.count, now)
		if where == "" {
			continue
		}
		r, err := tx.ExecContext(ctx, "DELETE FROM jobs WHERE "+where, args...)
		if err != nil {
			return res, fmt.Errorf("delete jobs: %w", err)
		}
		n, _ := r.RowsAffected()
		*b.out = int(n)
	}

	r, err := tx.ExecContext(ctx, retentionOrphanDiscSQL)
	if err != nil {
		return res, fmt.Errorf("delete orphan discs: %w", err)
	}
	n, _ := r.RowsAffected()
	res.DiscsDeleted = int(n)

	if err := tx.Commit(); err != nil {
		return res, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

// CountWouldPrune reports how many job entries each bucket would lose under
// the policy, without deleting anything. DiscsDeleted is not computed (a
// dry-run can't cheaply predict which discs become fully orphaned); callers
// use the job counts for the UI preview.
func (s *Store) CountWouldPrune(ctx context.Context, p RetentionPolicy, now time.Time) (RetentionResult, error) {
	var res RetentionResult
	buckets := []struct {
		states []string
		days   int
		count  int
		out    *int
	}{
		{retentionSuccessStates, p.SuccessDays, p.SuccessCount, &res.SuccessDeleted},
		{retentionFailedStates, p.FailedDays, p.FailedCount, &res.FailedDeleted},
	}
	for _, b := range buckets {
		where, args := retentionWhere(b.states, b.days, b.count, now)
		if where == "" {
			continue
		}
		var n int
		if err := s.db.Conn().QueryRowContext(ctx,
			"SELECT COUNT(*) FROM jobs WHERE "+where, args...).Scan(&n); err != nil {
			return res, fmt.Errorf("count would-prune: %w", err)
		}
		*b.out = n
	}
	return res, nil
}

// HistoryBucketTotals returns the count of terminal job entries in each
// bucket (the denominator for the retention preview).
func (s *Store) HistoryBucketTotals(ctx context.Context) (success, failed int, err error) {
	for _, b := range []struct {
		states []string
		out    *int
	}{
		{retentionSuccessStates, &success},
		{retentionFailedStates, &failed},
	} {
		inList := "'" + strings.Join(b.states, "','") + "'"
		if err = s.db.Conn().QueryRowContext(ctx,
			"SELECT COUNT(*) FROM jobs WHERE state IN ("+inList+
				") AND finished_at IS NOT NULL").Scan(b.out); err != nil {
			return 0, 0, fmt.Errorf("count bucket totals: %w", err)
		}
	}
	return success, failed, nil
}

// DeleteJobAndOrphans removes one job (and its FK-cascaded step + log
// rows), then drops the disc if no other job references it. Used by
// DELETE /api/jobs/:id for single-row history pruning; callers are
// responsible for refusing non-terminal states before invoking this.
func (s *Store) DeleteJobAndOrphans(ctx context.Context, jobID string) error {
	tx, err := s.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var discID string
	if err := tx.QueryRowContext(ctx,
		`SELECT disc_id FROM jobs WHERE id = ?`, jobID).Scan(&discID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lookup disc_id: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM jobs WHERE id = ?`, jobID); err != nil {
		return fmt.Errorf("delete job: %w", err)
	}

	// Drop the disc only if no other job references it AND it isn't the
	// drive's most-recent disc (the proxy for "still physically
	// inserted"). Without the newest-disc guard, deleting the last
	// history entry for a disc that's currently in the drive nukes the
	// row out from under the awaiting-decision card — the next
	// /start or /identify against that disc id returns 404. Disc rows
	// with a NULL/empty drive_id (orphaned by drive removal) still get
	// pruned the usual way.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM discs
		WHERE id = ?
		  AND NOT EXISTS (SELECT 1 FROM jobs WHERE jobs.disc_id = discs.id)
		  AND (
		    discs.drive_id IS NULL OR discs.drive_id = ''
		    OR EXISTS (
		      SELECT 1 FROM discs d2
		      WHERE d2.drive_id = discs.drive_id
		        AND d2.created_at > discs.created_at
		    )
		  )`, discID); err != nil {
		return fmt.Errorf("delete orphan disc: %w", err)
	}

	return tx.Commit()
}

// ClearHistory deletes every finished job (done/failed/cancelled), the
// job_steps and log_lines they own (via FK cascade), and the disc rows
// left with no job at all once those jobs are gone. In-progress jobs
// (queued/identifying/running/paused) and their discs are untouched,
// as are discs that never had a job (e.g. one still awaiting a
// decision). Ripped files on disk are not touched. Returns the number
// of jobs deleted. Runs in a single transaction.
func (s *Store) ClearHistory(ctx context.Context) (int, error) {
	tx, err := s.db.Conn().BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Count the finished jobs up front — once the deletes (and their FK
	// cascades) run, RowsAffected can't see cascade-deleted rows.
	var deleted int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM jobs
		WHERE state IN ('done','failed','cancelled','interrupted')`).Scan(&deleted); err != nil {
		return 0, fmt.Errorf("count finished jobs: %w", err)
	}

	// Drop discs whose every job is finished — they had history and have
	// no active rip. The jobs.disc_id ON DELETE CASCADE removes those
	// discs' jobs, job_steps and log_lines. The first EXISTS clause
	// keeps discs that never had a job (one still awaiting a decision).
	// The drive_id / newest-disc clause keeps the drive's currently-
	// inserted disc alive even when its only jobs are finished — the
	// alternative is the awaiting-decision card on the dashboard
	// pointing at a row that no longer exists, and the next /start
	// returning 404.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM discs
		WHERE EXISTS (SELECT 1 FROM jobs j WHERE j.disc_id = discs.id)
		  AND NOT EXISTS (
		    SELECT 1 FROM jobs j WHERE j.disc_id = discs.id
		      AND j.state IN ('queued','identifying','running','paused'))
		  AND (
		    discs.drive_id IS NULL OR discs.drive_id = ''
		    OR EXISTS (
		      SELECT 1 FROM discs d2
		      WHERE d2.drive_id = discs.drive_id
		        AND d2.created_at > discs.created_at
		    )
		  )`); err != nil {
		return 0, fmt.Errorf("delete history discs: %w", err)
	}

	// Mop up finished jobs on discs that were KEPT (a disc with both a
	// finished job and an active re-rip).
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM jobs
		WHERE state IN ('done','failed','cancelled','interrupted')`); err != nil {
		return 0, fmt.Errorf("delete finished jobs: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return deleted, nil
}

// Stats computes the dashboard's top-widgets payload. Library
// total_bytes is left zero — that's filled in by the API layer via
// statfs against the library roots (the daemon's state package
// shouldn't reach for the OS filesystem). ActiveJobs.Delta1h and
// Spark24h are similarly zero-filled here and stitched in by the API
// layer's in-memory active-jobs sampler.
func (s *Store) Stats(ctx context.Context, now time.Time) (Stats, error) {
	var out Stats
	if err := s.statsActive(ctx, &out.ActiveJobs); err != nil {
		return out, err
	}
	if err := s.statsTodayRipped(ctx, now, &out.TodayRipped); err != nil {
		return out, err
	}
	if err := s.statsLibrary(ctx, now, &out.Library); err != nil {
		return out, err
	}
	if err := s.statsFailures(ctx, now, &out.Failures7d); err != nil {
		return out, err
	}
	return out, nil
}

func (s *Store) statsActive(ctx context.Context, out *ActiveJobsStat) error {
	row := s.db.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM jobs
		WHERE state NOT IN ('done','failed','cancelled','interrupted')`)
	if err := row.Scan(&out.Value); err != nil {
		return err
	}
	out.Spark24h = make([]int, 24)
	return nil
}

func (s *Store) statsTodayRipped(ctx context.Context, now time.Time, out *TodayRippedStat) error {
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Format(time.RFC3339)
	row := s.db.Conn().QueryRowContext(ctx, `
		SELECT COALESCE(SUM(output_bytes), 0), COUNT(*)
		FROM jobs WHERE state='done' AND finished_at >= ?`, startOfToday)
	if err := row.Scan(&out.Bytes, &out.Titles); err != nil {
		return err
	}
	spark, err := s.dailyByteSeries(ctx, now, 7)
	if err != nil {
		return err
	}
	out.Spark7dBytes = spark
	return nil
}

func (s *Store) statsLibrary(ctx context.Context, now time.Time, out *LibraryStat) error {
	row := s.db.Conn().QueryRowContext(ctx, `
		SELECT COALESCE(SUM(output_bytes), 0)
		FROM jobs WHERE state='done'`)
	if err := row.Scan(&out.UsedBytes); err != nil {
		return err
	}
	// Build a cumulative-at-end-of-day series for the last 30 days.
	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT finished_at, output_bytes
		FROM jobs WHERE state='done' ORDER BY finished_at ASC`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	type pt struct {
		t time.Time
		b int64
	}
	var all []pt
	for rows.Next() {
		var ts string
		var b int64
		if err := rows.Scan(&ts, &b); err != nil {
			return err
		}
		t, _ := time.Parse(time.RFC3339, ts)
		all = append(all, pt{t, b})
	}
	out.Spark30dUsed = make([]int64, 30)
	dayEnd := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
	for i := 0; i < 30; i++ {
		thisDayEnd := dayEnd.AddDate(0, 0, -(29 - i))
		var sum int64
		for _, p := range all {
			if !p.t.After(thisDayEnd) {
				sum += p.b
			}
		}
		out.Spark30dUsed[i] = sum
	}
	return nil
}

func (s *Store) statsFailures(ctx context.Context, now time.Time, out *Failures7dStat) error {
	cutCurr := now.AddDate(0, 0, -7).Format(time.RFC3339)
	cutPrev := now.AddDate(0, 0, -14).Format(time.RFC3339)

	row := s.db.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM jobs
		WHERE state IN ('failed','cancelled','interrupted')
		  AND finished_at >= ?`, cutCurr)
	if err := row.Scan(&out.Value); err != nil {
		return err
	}
	row = s.db.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM jobs
		WHERE state IN ('failed','cancelled','interrupted')
		  AND finished_at >= ? AND finished_at < ?`, cutPrev, cutCurr)
	if err := row.Scan(&out.Previous); err != nil {
		return err
	}

	out.Spark30d = make([]int, 30)
	cut30 := now.AddDate(0, 0, -30).Format(time.RFC3339)
	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT date(finished_at), COUNT(*)
		FROM jobs WHERE state IN ('failed','cancelled','interrupted')
		  AND finished_at >= ?
		GROUP BY date(finished_at)`, cut30)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var day string
		var n int
		if err := rows.Scan(&day, &n); err != nil {
			return err
		}
		idx := dayOffsetIndex(now, day, 30)
		if idx >= 0 && idx < 30 {
			out.Spark30d[idx] = n
		}
	}
	return nil
}

// dailyByteSeries returns the per-day SUM(output_bytes) over the last
// `days` calendar days as a slice of length `days` (oldest first).
// Missing days are zero-filled.
func (s *Store) dailyByteSeries(ctx context.Context, now time.Time, days int) ([]int64, error) {
	cut := now.AddDate(0, 0, -days).Format(time.RFC3339)
	rows, err := s.db.Conn().QueryContext(ctx, `
		SELECT date(finished_at), SUM(output_bytes)
		FROM jobs WHERE state='done' AND finished_at >= ?
		GROUP BY date(finished_at)`, cut)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]int64, days)
	for rows.Next() {
		var day string
		var n int64
		if err := rows.Scan(&day, &n); err != nil {
			return nil, err
		}
		idx := dayOffsetIndex(now, day, days)
		if idx >= 0 && idx < days {
			out[idx] = n
		}
	}
	return out, nil
}

// dayOffsetIndex maps a 'YYYY-MM-DD' day string to its bucket index in
// a window of `days` days ending today. days-1 is today; 0 is the
// oldest day in the window. Returns -1 if the day is outside the
// window or unparseable.
func dayOffsetIndex(now time.Time, ymd string, days int) int {
	t, err := time.Parse("2006-01-02", ymd)
	if err != nil {
		return -1
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	bucket := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
	diff := int(today.Sub(bucket).Hours() / 24)
	idx := (days - 1) - diff
	if idx < 0 || idx >= days {
		return -1
	}
	return idx
}

// Conn exposes the underlying *sql.DB. Used by the API layer's
// active-jobs sampler, which needs a single COUNT query without going
// through the full Stats aggregator.
func (s *Store) Conn() *sql.DB {
	return s.db.Conn()
}
