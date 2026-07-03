package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// startDiscRequest is the wire format for POST /api/discs/:id/start.
// TitleIDs is optional; when present, the rip handler reads them off
// disc.metadata_json.selected_title_ids and rips exactly those
// titles instead of running its auto-pick heuristic. Empty / absent
// preserves today's behaviour.
//
// Season + EpisodeMap drive the per-episode naming path. The
// handler reads `selected_season` and `selected_title_episodes`
// from disc.metadata_json to render output paths with the canonical
// `Show - S##E## - Episode Title` shape Plex/Jellyfin auto-classify.
// EpisodeMap keys are title IDs (matching one of TitleIDs); values
// are TMDB episode numbers within the season. The daemon resolves
// each episode number to its TMDB-known title at /start time so the
// handler doesn't need to re-fetch from TMDB at rip time.
type startDiscRequest struct {
	ProfileID      string      `json:"profile_id"`
	CandidateIndex int         `json:"candidate_index"`
	TitleIDs       []int       `json:"title_ids,omitempty"`
	Season         int         `json:"season,omitempty"`
	EpisodeMap     map[int]int `json:"episode_map,omitempty"` // title id → tmdb episode number
}

// StartDisc creates a job for the given disc + profile and queues it on
// the orchestrator.
func (h *Handlers) StartDisc(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req startDiscRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if req.ProfileID == "" {
		writeError(w, http.StatusBadRequest, "profile_id required")
		return
	}

	disc, err := h.Store.GetDisc(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "disc not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Fast-path duplicate guard: skip the candidate-metadata fetch
	// below for an obvious duplicate. This check is NOT atomic with
	// Submit — the authoritative, race-safe guard is under startMu
	// just before Submit. See that block and the startMu doc comment.
	hasActive, err := h.Store.DiscHasActiveJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hasActive {
		writeError(w, http.StatusConflict, "disc already has an active job")
		return
	}
	// Authoritative duplicate guard. The fast-path check above isn't
	// atomic with Submit, so two requests racing within that window
	// both pass it. Re-check under startMu — held across Submit — so
	// exactly one job is created no matter how many requests race;
	// the losers get the same 409 as a sequential duplicate. Held
	// *before* the candidate-promotion writes below so a request that
	// loses the race 409s without having overwritten the disc's
	// title/metadata_id/metadata_json out from under the winner.
	h.startMu.Lock()
	defer h.startMu.Unlock()
	hasActive, err = h.Store.DiscHasActiveJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hasActive {
		writeError(w, http.StatusConflict, "disc already has an active job")
		return
	}

	// Promote chosen candidate by mutating the in-memory disc; the
	// orchestrator only re-reads disc metadata, not candidate index, so
	// this carries the user choice into the pipeline. M1.2 may persist
	// the choice back to the DB; for M1.1 the orchestrator's job row is
	// the source of truth once submitted.
	if req.CandidateIndex >= 0 && req.CandidateIndex < len(disc.Candidates) {
		c := disc.Candidates[req.CandidateIndex]
		// Pick the source-appropriate ID so the chosen candidate's
		// identity actually persists. MBID for MusicBrainz, TMDBID
		// for TMDB (movie/TV), IGDBID for game discs.
		var metaID string
		switch {
		case c.MBID != "":
			metaID = c.MBID
		case c.TMDBID > 0:
			metaID = strconv.Itoa(c.TMDBID)
		case c.IGDBID > 0:
			metaID = strconv.Itoa(c.IGDBID)
		default:
			// Redump / DuckStation / BootCodeIndex candidates carry no
			// MBID/TMDBID/IGDBID — the disc's stable ID is the boot code
			// already on disc.MetadataID. Preserve it; clobbering to ""
			// breaks (drive_id, metadata_id) dedup (a spurious udev
			// re-identify then spawns a duplicate disc row) and disables the
			// post-rip Redump MD5 verify.
			metaID = disc.MetadataID
		}
		// Persist the chosen identity. The orchestrator re-reads the
		// disc row inside Submit, so without this the user choice is
		// dropped and the pipeline runs on the original auto-identified
		// candidate.
		if err := h.Store.UpdateDiscMetadata(r.Context(), disc.ID, c.Title, c.Year, c.Source, metaID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// If the picked candidate is a TMDB movie, fetch its runtime
		// from `/movie/{id}` so the DVD pipeline can sanity-check the
		// scanned title duration. Best-effort: a failure here logs
		// (via the error path) but doesn't block the rip.
		if c.MediaType == "movie" && c.TMDBID > 0 && h.TMDB != nil {
			if rt, err := h.TMDB.MovieRuntime(r.Context(), c.TMDBID); err == nil && rt > 0 {
				_ = h.Store.UpdateDiscRuntime(r.Context(), disc.ID, rt)
			}
		}
		// Persist the extended pane metadata for the picked candidate so
		// disc.metadata_json is ready before the first SSE snapshot — the
		// pane renders rich data on first paint. Best-effort: a failure
		// here doesn't block the rip.
		if blob, err := h.fetchExtendedMetadata(r.Context(), disc, &c); err == nil && blob != "" {
			_ = h.Store.UpdateDiscMetadataBlob(r.Context(), disc.ID, blob)
		}
	}

	// Persist the user-picked title IDs into the disc's metadata_json
	// so the rip handler reads them at runtime. Cleared on next
	// identify (when a fresh disc is inserted into the same drive)
	// via the existing UpdateDiscMetadataBlob path. Skipped when the
	// list is empty so the default auto-pick path stays unchanged.
	if len(req.TitleIDs) > 0 {
		merged := map[string]any{}
		if disc.MetadataJSON != "" && disc.MetadataJSON != "{}" {
			_ = json.Unmarshal([]byte(disc.MetadataJSON), &merged)
		}
		merged["selected_title_ids"] = req.TitleIDs
		if req.Season > 0 {
			merged["selected_season"] = req.Season
		}
		// Resolve TMDB episode names for the picked map so the rip
		// handler can render the canonical `Show - S##E## - Episode
		// Title` naming without re-fetching from TMDB. Best-effort:
		// a TMDB error or empty season yields a number-only map so
		// the handler still gets the right S##E## naming.
		if len(req.EpisodeMap) > 0 && req.Season > 0 && h.TMDB != nil {
			merged["selected_title_episodes"] = h.resolveEpisodeMap(r.Context(), disc, req.Season, req.EpisodeMap)
		}
		body, err := json.Marshal(merged)
		if err == nil {
			if err := h.Store.UpdateDiscMetadataBlob(r.Context(), disc.ID, string(body)); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}

	// Push the picked-candidate fields out via SSE so the dashboard
	// flips the rip card to the chosen title/cover/year/runtime
	// immediately, without waiting for a page reload. Re-read the disc
	// to capture every Update* write (UpdateDiscMetadata,
	// UpdateDiscRuntime, UpdateDiscMetadataBlob) in one atomic payload.
	// Best-effort: a re-read or publish failure here doesn't block the
	// rip submit below.
	if h.Broadcaster != nil {
		if fresh, err := h.Store.GetDisc(r.Context(), disc.ID); err == nil {
			h.Broadcaster.Publish(state.Event{
				Name: "disc.changed",
				Payload: map[string]any{
					"id":                fresh.ID,
					"title":             fresh.Title,
					"year":              fresh.Year,
					"metadata_provider": fresh.MetadataProvider,
					"metadata_id":       fresh.MetadataID,
					"metadata_json":     fresh.MetadataJSON,
					"runtime_seconds":   fresh.RuntimeSeconds,
				},
			})
		}
	}

	job, err := h.Orchestrator.Submit(r.Context(), disc.ID, req.ProfileID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// ScanDisc creates a kind='scan' job that enumerates titles via
// MakeMKV (BDMV/UHD) or HandBrake (DVD) without ripping. Drive-bound
// and brief (~60s on a slow MakeMKV BD; ~10s on DVD direct-scan).
// Output lands on disc.metadata_json.scan and the dashboard's picker
// UI surfaces it when the disc.changed SSE event fires.
//
// Body: {"profile_id": "<id>"}.
// Errors:
//
//	400 missing or invalid body
//	404 disc not found
//	409 disc already has an active job
//	422 disc type doesn't support title scan (returned by Submit)
//	503 orchestrator not wired (test harness)
func (h *Handlers) ScanDisc(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req struct {
		ProfileID string `json:"profile_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if req.ProfileID == "" {
		writeError(w, http.StatusBadRequest, "profile_id required")
		return
	}
	if _, err := h.Store.GetDisc(r.Context(), id); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "disc not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.Orchestrator == nil {
		writeError(w, http.StatusServiceUnavailable, "orchestrator not configured")
		return
	}
	hasActive, err := h.Store.DiscHasActiveJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hasActive {
		writeError(w, http.StatusConflict, "disc already has an active job")
		return
	}
	job, err := h.Orchestrator.SubmitScan(r.Context(), id, req.ProfileID)
	if err != nil {
		// SubmitScan's "disc type does not support title scan" is a
		// client-side issue, not a server failure — surface as 422.
		if strings.Contains(err.Error(), "does not support title scan") {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// GetDiscEpisodes returns the TMDB episode list for the given disc's
// TV series + a season number from the query string. Used by the
// TitlePicker UI to populate the per-title episode dropdowns.
//
// Errors:
//
//	400 missing or non-integer ?season=N
//	404 disc not found
//	422 disc has no TMDB id or its top candidate isn't tv
//	502 TMDB upstream error
//
// Returns 200 with `[]` when the season is unknown to TMDB (404) or
// TMDB isn't configured — the picker hides the episode column
// gracefully on an empty list.
func (h *Handlers) GetDiscEpisodes(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	seasonStr := r.URL.Query().Get("season")
	if seasonStr == "" {
		writeError(w, http.StatusBadRequest, "season query parameter required")
		return
	}
	season, err := strconv.Atoi(seasonStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "season must be an integer")
		return
	}

	disc, err := h.Store.GetDisc(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "disc not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmdbID, err := strconv.Atoi(disc.MetadataID)
	if err != nil || tmdbID <= 0 {
		writeError(w, http.StatusUnprocessableEntity, "disc has no TMDB id")
		return
	}
	// Confirm the top candidate is tv — fetching episodes for a movie
	// returns nothing useful and would mislead the picker into showing
	// an empty episode column.
	isTV := false
	for _, c := range disc.Candidates {
		if c.TMDBID == tmdbID && c.MediaType == "tv" {
			isTV = true
			break
		}
	}
	if !isTV {
		writeError(w, http.StatusUnprocessableEntity, "disc is not a TV series")
		return
	}
	if h.TMDB == nil {
		writeJSON(w, http.StatusOK, []identify.EpisodeInfo{})
		return
	}
	episodes, err := h.TMDB.SeasonEpisodes(r.Context(), tmdbID, season)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if episodes == nil {
		episodes = []identify.EpisodeInfo{}
	}
	writeJSON(w, http.StatusOK, episodes)
}

// resolveEpisodeMap turns a {titleID: episodeNumber} map into a
// {titleID: {episode, title}} blob so the rip handler can render
// `S##E## - Episode Title` without re-hitting TMDB. The lookup is
// best-effort: when TMDB is unconfigured, the disc has no TMDB id,
// or the season fetch errors, the map falls back to number-only
// entries (the handler renders `S##E##` without the title piece).
func (h *Handlers) resolveEpisodeMap(ctx context.Context, disc *state.Disc, season int, picks map[int]int) map[string]map[string]any {
	out := make(map[string]map[string]any, len(picks))
	// Seed with number-only entries so a TMDB failure still yields a
	// usable map.
	for titleID, epNum := range picks {
		out[strconv.Itoa(titleID)] = map[string]any{"episode": epNum}
	}
	tmdbID, err := strconv.Atoi(disc.MetadataID)
	if err != nil || tmdbID <= 0 || h.TMDB == nil {
		return out
	}
	episodes, err := h.TMDB.SeasonEpisodes(ctx, tmdbID, season)
	if err != nil || len(episodes) == 0 {
		return out
	}
	byNum := make(map[int]identify.EpisodeInfo, len(episodes))
	for _, e := range episodes {
		byNum[e.Number] = e
	}
	for titleID, epNum := range picks {
		entry := map[string]any{"episode": epNum}
		if e, ok := byNum[epNum]; ok && e.Name != "" {
			entry["title"] = e.Name
		}
		out[strconv.Itoa(titleID)] = entry
	}
	return out
}

// DeleteDisc removes a disc row by id. Used by the dashboard's Skip
// affordance on awaiting-decision cards: a disc with no Job that the
// user wants to dismiss permanently (otherwise the same row is
// re-derived on every page load and the card returns).
//
// Refuses to delete a disc that has any job referencing it — the user
// can already see those discs' outcomes in History, and removing them
// would orphan job rows that still point at the disc_id.
func (h *Handlers) DeleteDisc(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	hasJob, err := h.Store.DiscHasAnyJob(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hasJob {
		writeError(w, http.StatusConflict, "disc has job history; cannot delete")
		return
	}

	if err := h.Store.DeleteDisc(r.Context(), id); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "disc not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.Broadcaster.Publish(state.Event{
		Name:    "disc.deleted",
		Payload: map[string]any{"disc_id": id},
	})

	w.WriteHeader(http.StatusNoContent)
}

// identifyRequest is the optional body for POST /api/discs/:id/identify.
// All fields are optional: an empty body re-reads the stored disc.
type identifyRequest struct {
	Query     string `json:"query,omitempty"`
	MediaType string `json:"media_type,omitempty"` // 'movie' | 'tv' | 'both' (default both)
	// Force re-runs the full classify + identify pipeline against the
	// drive the disc lives in. Used by the drive card's "Re-identify"
	// button when MusicBrainz / TMDB pick the wrong release.
	Force bool `json:"force,omitempty"`
}

// IdentifyDisc returns the disc plus its candidates. With Force=true it
// re-runs the full classify + identify pipeline against the drive the
// disc lives in (replaces candidates + metadata fields). With a non-
// empty Query it triggers a manual TMDB search. Empty body → returns
// the current stored disc + candidates.
func (h *Handlers) IdentifyDisc(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	disc, err := h.Store.GetDisc(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "disc not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if r.ContentLength == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"disc": disc, "candidates": disc.Candidates})
		return
	}

	var req identifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}

	if req.Force {
		h.forceReidentify(w, r, disc)
		return
	}

	if req.Query == "" {
		writeJSON(w, http.StatusOK, map[string]any{"disc": disc, "candidates": disc.Candidates})
		return
	}

	// Dispatch manual search to the appropriate metadata source.
	// Audio CDs → MusicBrainz; game discs → IGDB; data discs have no
	// searchable metadata; everything else (DVD, BDMV, UHD) → TMDB.
	var cands []state.Candidate
	switch {
	case disc.Type == state.DiscTypeAudioCD:
		if h.MusicBrainz == nil {
			writeError(w, http.StatusServiceUnavailable, "MusicBrainz not configured")
			return
		}
		cands, err = h.MusicBrainz.SearchByName(r.Context(), req.Query)

	case isGameDisc(disc.Type):
		if h.IGDB == nil || !h.IGDB.Configured() {
			writeError(w, http.StatusServiceUnavailable, "IGDB not configured")
			return
		}
		cands, err = h.IGDB.SearchGames(r.Context(), req.Query, disc.Type)

	case disc.Type == state.DiscTypeData:
		writeError(w, http.StatusUnprocessableEntity,
			"data discs do not support metadata search")
		return

	default: // DVD, BDMV, UHD, VCD
		if h.TMDB == nil {
			writeError(w, http.StatusServiceUnavailable, "TMDB not configured")
			return
		}
		mediaType := req.MediaType
		if mediaType == "" {
			mediaType = "both"
		}
		switch mediaType {
		case "movie":
			cands, err = h.TMDB.SearchMovie(r.Context(), req.Query)
		case "tv":
			cands, err = h.TMDB.SearchTV(r.Context(), req.Query)
		case "both":
			cands, err = h.TMDB.SearchBoth(r.Context(), req.Query)
		default:
			writeError(w, http.StatusBadRequest, "media_type must be 'movie', 'tv', or 'both'")
			return
		}
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if cands == nil {
		cands = []state.Candidate{}
	}

	disc.Candidates = cands
	if err := h.Store.UpdateDiscCandidates(r.Context(), disc.ID, cands); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"disc": disc, "candidates": cands})
}

// fetchExtendedMetadata calls the appropriate identify-source method to
// retrieve the pane payload for the picked candidate and marshals it as
// JSON. Returns "" + nil when the candidate type has no source mapping
// (data discs, unknown).
func (h *Handlers) fetchExtendedMetadata(ctx context.Context, disc *state.Disc, c *state.Candidate) (string, error) {
	var payload any
	switch {
	case c.MediaType == "movie" && c.TMDBID > 0 && h.TMDB != nil:
		m, err := h.TMDB.MovieDetails(ctx, c.TMDBID)
		if err != nil {
			return "", err
		}
		payload = m
	case c.MediaType == "tv" && c.TMDBID > 0 && h.TMDB != nil:
		m, err := h.TMDB.TVDetails(ctx, c.TMDBID)
		if err != nil {
			return "", err
		}
		payload = m
	case disc.Type == state.DiscTypeAudioCD && c.MBID != "" && h.MusicBrainz != nil:
		m, err := h.MusicBrainz.ReleaseDetails(ctx, c.MBID)
		if err != nil {
			return "", err
		}
		payload = m
	case c.IGDBID > 0 && h.IGDB != nil:
		g, err := h.IGDB.GameDetails(ctx, c.IGDBID)
		if err != nil {
			return "", err
		}
		payload = map[string]any{
			"system":    gameSystemName(disc.Type),
			"serial":    strconv.Itoa(c.IGDBID),
			"cover_url": g.CoverURL,
			"summary":   g.Summary,
			"platforms": g.Platforms,
		}
	case isGameDisc(disc.Type) && c.Source == "Redump":
		// Game discs build their blob from already-stored candidate data
		// (Redump matched at identify time). No external fetch needed.
		payload = map[string]any{
			"system": gameSystemName(disc.Type),
			"serial": disc.MetadataID,
		}
	default:
		return "", nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func isGameDisc(t state.DiscType) bool {
	switch t {
	case state.DiscTypePSX, state.DiscTypePS2, state.DiscTypeSAT, state.DiscTypeDC, state.DiscTypeXBOX,
		state.DiscTypeSegaCD, state.DiscType3DO, state.DiscTypePCFX, state.DiscTypeJaguarCD,
		state.DiscTypeCDi, state.DiscTypePCECD, state.DiscTypeNeoCD,
		state.DiscTypeCD32, state.DiscTypeFMTowns, state.DiscTypePippin:
		return true
	}
	return false
}

func gameSystemName(t state.DiscType) string {
	switch t {
	case state.DiscTypePSX:
		return "Sony PlayStation"
	case state.DiscTypePS2:
		return "Sony PlayStation 2"
	case state.DiscTypeSAT:
		return "Sega Saturn"
	case state.DiscTypeDC:
		return "Sega Dreamcast"
	case state.DiscTypeXBOX:
		return "Microsoft Xbox"
	case state.DiscTypeSegaCD:
		return "Sega CD"
	case state.DiscType3DO:
		return "3DO"
	case state.DiscTypePCFX:
		return "PC-FX"
	case state.DiscTypeJaguarCD:
		return "Atari Jaguar CD"
	case state.DiscTypeCDi:
		return "Philips CD-i"
	case state.DiscTypePCECD:
		return "PC Engine CD"
	case state.DiscTypeNeoCD:
		return "Neo Geo CD"
	case state.DiscTypeCD32:
		return "Amiga CD32"
	case state.DiscTypeFMTowns:
		return "FM Towns"
	case state.DiscTypePippin:
		return "Bandai Pippin"
	}
	return string(t)
}

// forceReidentify re-runs the classify + Identify pipeline for an
// existing disc and updates the row in place. Used by the drive card's
// Re-identify button when the prober/lookup landed on a wrong candidate
// (e.g. MusicBrainz picked the wrong release, TMDB grabbed the wrong
// title). Refuses 409 if the drive has an active job or another claim
// is already in flight.
func (h *Handlers) forceReidentify(w http.ResponseWriter, r *http.Request, disc *state.Disc) {
	ctx := r.Context()
	if disc.DriveID == "" {
		writeError(w, http.StatusUnprocessableEntity, "disc has no drive — cannot re-identify")
		return
	}
	drv, err := h.Store.GetDrive(ctx, disc.DriveID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if drv.DevPath == "" {
		writeError(w, http.StatusUnprocessableEntity, "drive has no dev_path")
		return
	}
	busy, err := h.Store.HasActiveJobOnDrive(ctx, drv.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if busy {
		writeError(w, http.StatusConflict, "drive has an active job")
		return
	}
	claimed, err := h.Store.ClaimDriveForIdentify(ctx, drv.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !claimed {
		writeError(w, http.StatusConflict, "drive already identifying")
		return
	}
	defer func() {
		// CAS release scoped to `identifying`: if a rip started on this disc
		// mid-re-identify (orchestrator flipped the drive to `ripping`), the
		// release is a no-op and we must not announce a stale `idle`. Publish
		// the idle transition only when we actually released the drive, so the
		// dashboard pill returns from "Identifying…" without a reload.
		released, uerr := h.Store.ReleaseDriveFromIdentify(context.Background(), drv.ID, state.DriveStateIdle)
		if uerr != nil {
			slog.Warn("force-reidentify: release drive state", "err", uerr, "drive_id", drv.ID)
		}
		if released && h.Broadcaster != nil {
			h.Broadcaster.Publish(state.Event{
				Name:    "drive.changed",
				Payload: map[string]any{"drive_id": drv.ID, "state": "idle"},
			})
		}
	}()
	if h.Broadcaster != nil {
		h.Broadcaster.Publish(state.Event{
			Name:    "drive.changed",
			Payload: map[string]any{"drive_id": drv.ID, "state": "identifying"},
		})
	}

	if h.Classifier == nil || h.Pipelines == nil {
		writeError(w, http.StatusServiceUnavailable, "identify not configured")
		return
	}
	dt, err := h.Classifier.Classify(ctx, drv.DevPath)
	if err != nil {
		writeError(w, http.StatusBadGateway, "classify: "+err.Error())
		return
	}
	handler, ok := h.Pipelines.Get(dt)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "no pipeline for disc type "+string(dt))
		return
	}
	fresh, cands, ierr := handler.Identify(ctx, drv)
	switch {
	case errors.Is(ierr, pipelines.ErrNoCandidates):
		// Persist whatever metadata fresh contains (often just the type)
		// and clear candidates so the UI can offer manual search.
		if cands == nil {
			cands = []state.Candidate{}
		}
	case ierr != nil:
		writeError(w, http.StatusBadGateway, "identify: "+ierr.Error())
		return
	}

	// Merge fresh fields into the existing disc row.
	if fresh != nil {
		if fresh.Type != "" && fresh.Type != disc.Type {
			if err := h.Store.UpdateDiscType(ctx, disc.ID, fresh.Type); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		disc.Type = fresh.Type
		disc.Title = fresh.Title
		disc.Year = fresh.Year
		disc.MetadataProvider = fresh.MetadataProvider
		disc.MetadataID = fresh.MetadataID
	}
	disc.Candidates = cands
	if err := h.Store.UpdateDiscMetadata(ctx, disc.ID, disc.Title, disc.Year, disc.MetadataProvider, disc.MetadataID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.Store.UpdateDiscCandidates(ctx, disc.ID, cands); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.Broadcaster != nil {
		h.Broadcaster.Publish(state.Event{
			Name:    "disc.identified",
			Payload: map[string]any{"disc": disc, "candidates": cands},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"disc": disc, "candidates": cands})
}

type setDiscTypeRequest struct {
	Type string `json:"type"`
}

// validOverrideTarget reports whether dt is a legal manual-override target:
// a disc type with a registered pipeline handler, excluding AUDIO_CD (whose
// identify/rip path is TOC + MusicBrainz + whipper, not a drive-probe the
// override flow can drive).
func (h *Handlers) validOverrideTarget(dt state.DiscType) bool {
	if dt == "" || dt == state.DiscTypeAudioCD {
		return false
	}
	if h.Pipelines == nil {
		return false
	}
	_, ok := h.Pipelines.Get(dt)
	return ok
}

// SetDiscType manually overrides a disc's classified type (the
// weak-signature safety net) and re-runs identify for the chosen type when
// the disc is still in its drive. POST /api/discs/{id}/type {"type":"PSX"}.
func (h *Handlers) SetDiscType(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	disc, err := h.Store.GetDisc(ctx, id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "disc not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var req setDiscTypeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	target := state.DiscType(strings.TrimSpace(req.Type))
	if target == disc.Type {
		writeError(w, http.StatusUnprocessableEntity, "disc is already type "+string(target))
		return
	}
	if !h.validOverrideTarget(target) {
		writeError(w, http.StatusUnprocessableEntity, "cannot override to type "+req.Type)
		return
	}

	// Persist the type first — authoritative even if re-identify can't run.
	if err := h.Store.UpdateDiscType(ctx, disc.ID, target); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	disc.Type = target

	// Type-only fallback when there's no drive to probe.
	if disc.DriveID == "" {
		h.finishTypeOverride(w, ctx, disc, []state.Candidate{})
		return
	}
	drv, err := h.Store.GetDrive(ctx, disc.DriveID)
	if err != nil {
		slog.Warn("set-disc-type: get drive failed; keeping type only", "err", err, "drive_id", disc.DriveID)
		h.finishTypeOverride(w, ctx, disc, []state.Candidate{})
		return
	}
	if drv.DevPath == "" {
		h.finishTypeOverride(w, ctx, disc, []state.Candidate{})
		return
	}
	busy, err := h.Store.HasActiveJobOnDrive(ctx, drv.ID)
	if err != nil {
		slog.Warn("set-disc-type: active-job check failed; keeping type only", "err", err, "drive_id", drv.ID)
		h.finishTypeOverride(w, ctx, disc, []state.Candidate{})
		return
	}
	if busy {
		h.finishTypeOverride(w, ctx, disc, []state.Candidate{})
		return
	}
	claimed, err := h.Store.ClaimDriveForIdentify(ctx, drv.ID)
	if err != nil {
		slog.Warn("set-disc-type: claim failed; keeping type only", "err", err, "drive_id", drv.ID)
		h.finishTypeOverride(w, ctx, disc, []state.Candidate{})
		return
	}
	if !claimed {
		h.finishTypeOverride(w, ctx, disc, []state.Candidate{})
		return
	}
	defer func() {
		// CAS release scoped to `identifying` (see forceReidentify): don't
		// stomp a rip that started mid-re-identify, and publish idle only when
		// the release actually moved the row so the UI leaves "Identifying…".
		released, uerr := h.Store.ReleaseDriveFromIdentify(context.Background(), drv.ID, state.DriveStateIdle)
		if uerr != nil {
			slog.Warn("set-disc-type: release drive state", "err", uerr, "drive_id", drv.ID)
		}
		if released && h.Broadcaster != nil {
			h.Broadcaster.Publish(state.Event{
				Name:    "drive.changed",
				Payload: map[string]any{"drive_id": drv.ID, "state": "idle"},
			})
		}
	}()
	if h.Broadcaster != nil {
		h.Broadcaster.Publish(state.Event{
			Name:    "drive.changed",
			Payload: map[string]any{"drive_id": drv.ID, "state": "identifying"},
		})
	}

	handler, ok := h.Pipelines.Get(target)
	if !ok {
		h.finishTypeOverride(w, ctx, disc, []state.Candidate{})
		return
	}
	fresh, cands, ierr := handler.Identify(ctx, drv)
	switch {
	case errors.Is(ierr, pipelines.ErrNoCandidates):
		if cands == nil {
			cands = []state.Candidate{}
		}
	case ierr != nil:
		slog.Warn("set-disc-type: identify failed; keeping type only", "err", ierr, "type", target)
		h.finishTypeOverride(w, ctx, disc, []state.Candidate{})
		return
	}
	if fresh != nil {
		disc.Title = fresh.Title
		disc.Year = fresh.Year
		disc.MetadataProvider = fresh.MetadataProvider
		disc.MetadataID = fresh.MetadataID
	}
	if cands == nil {
		cands = []state.Candidate{}
	}
	h.finishTypeOverride(w, ctx, disc, cands)
}

// finishTypeOverride persists metadata + candidates, publishes
// disc.identified, and writes the JSON response. disc.Type is already saved.
func (h *Handlers) finishTypeOverride(w http.ResponseWriter, ctx context.Context, disc *state.Disc, cands []state.Candidate) {
	disc.Candidates = cands
	if err := h.Store.UpdateDiscMetadata(ctx, disc.ID, disc.Title, disc.Year, disc.MetadataProvider, disc.MetadataID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.Store.UpdateDiscCandidates(ctx, disc.ID, cands); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.Broadcaster != nil {
		h.Broadcaster.Publish(state.Event{
			Name:    "disc.identified",
			Payload: map[string]any{"disc": disc, "candidates": cands},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"disc": disc, "candidates": cands})
}

// ListDiscHistory serves disc-keyed history, newest-first by latest
// associated job timestamp. Mirrors ListHistory's pagination shape
// (limit / offset query params) so the webui page component changes
// minimally.
func (h *Handlers) ListDiscHistory(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	rows, total, err := h.Store.ListDiscHistory(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		rows = []state.DiscHistoryRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"discs":  rows,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}
