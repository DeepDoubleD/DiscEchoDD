package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// igdbMinSimilarity is the strict title-match gate: an IGDB result is trusted
// as the same game only when its title clears this Jaccard similarity against
// the dat/bootcode-identified title. A wrong cover is worse than none.
const igdbMinSimilarity = 0.6

// igdbEnrichTimeout bounds the background enrichment round-trip (Twitch OAuth
// + IGDB search + details).
const igdbEnrichTimeout = 15 * time.Second

// gameDiscForIGDB reports whether the disc type is a game type IGDB enriches.
func gameDiscForIGDB(t state.DiscType) bool {
	switch t {
	case state.DiscTypePSX, state.DiscTypePS2, state.DiscTypeSAT, state.DiscTypeDC,
		state.DiscTypeXBOX, state.DiscTypeXBOX360, state.DiscTypeWII,
		state.DiscTypeSegaCD, state.DiscType3DO, state.DiscTypePCFX, state.DiscTypeJaguarCD,
		state.DiscTypeCDi, state.DiscTypePCECD, state.DiscTypeNeoCD,
		state.DiscTypeCD32, state.DiscTypeFMTowns, state.DiscTypePippin:
		return true
	default:
		return false
	}
}

// bestIGDBMatch returns the candidate whose title is most similar to the
// identified title, but only when it clears igdbMinSimilarity. ok=false means
// no candidate is trustworthy enough to enrich from.
func bestIGDBMatch(cands []state.Candidate, title string) (state.Candidate, bool) {
	best := -1.0
	bestIdx := -1
	for i, c := range cands {
		if s := identify.TitleSimilarity(title, c.Title); s > best {
			best, bestIdx = s, i
		}
	}
	if bestIdx < 0 || best < igdbMinSimilarity {
		return state.Candidate{}, false
	}
	return cands[bestIdx], true
}

// mergeGameMetadata merges IGDB details into an existing game metadata_json
// blob. It preserves identity-bearing / serial-exact fields already present
// (system, serial, redump_md5, an existing cover_url) and adds the IGDB rich
// fields. Returns the new JSON and whether anything changed.
func mergeGameMetadata(existingJSON string, d *identify.IGDBGameDetails) (string, bool, error) {
	meta := map[string]any{}
	if existingJSON != "" && existingJSON != "{}" {
		if err := json.Unmarshal([]byte(existingJSON), &meta); err != nil {
			return existingJSON, false, err
		}
	}
	changed := false
	// Serial-exact gamedb cover wins; only fill when none is present.
	if _, ok := meta["cover_url"]; !ok && d.CoverURL != "" {
		meta["cover_url"] = d.CoverURL
		changed = true
	}
	if d.Summary != "" {
		meta["summary"] = d.Summary
		changed = true
	}
	if len(d.Genres) > 0 {
		meta["genres"] = d.Genres
		changed = true
	}
	if len(d.Platforms) > 0 {
		meta["platforms"] = d.Platforms
		changed = true
	}
	if d.Year > 0 {
		meta["release_year"] = d.Year
		changed = true
	}
	if !changed {
		return existingJSON, false, nil
	}
	body, err := json.Marshal(meta)
	if err != nil {
		return existingJSON, false, err
	}
	return string(body), true, nil
}

// enrichGameDiscFromIGDB augments an already-identified game disc with IGDB
// cover art + rich metadata (summary, genres, platforms, release year) when
// IGDB is configured. Best-effort and asynchronous: identify has already
// published the disc card, so a failure here just leaves the card with its
// dat/bootcode metadata. Never touches identity (metadata_id/title/candidates);
// it re-reads and merges metadata_json so a concurrent Start isn't stomped.
func (df *discFlow) enrichGameDiscFromIGDB(discID string, dt state.DiscType, title string) {
	if df.igdb == nil || !df.igdb.Configured() || title == "" || !gameDiscForIGDB(dt) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), igdbEnrichTimeout)
	defer cancel()

	cands, err := df.igdb.SearchGames(ctx, title, dt)
	if err != nil {
		slog.Warn("igdb enrich: search", "err", err, "title", title)
		return
	}
	match, ok := bestIGDBMatch(cands, title)
	if !ok {
		return
	}
	details, err := df.igdb.GameDetails(ctx, match.IGDBID)
	if err != nil {
		slog.Warn("igdb enrich: details", "err", err, "igdb_id", match.IGDBID)
		return
	}
	disc, err := df.store.GetDisc(ctx, discID)
	if err != nil {
		slog.Warn("igdb enrich: get disc", "err", err, "disc_id", discID)
		return
	}
	merged, changed, err := mergeGameMetadata(disc.MetadataJSON, details)
	if err != nil {
		slog.Warn("igdb enrich: merge", "err", err, "disc_id", discID)
		return
	}
	if !changed {
		return
	}
	if err := df.store.UpdateDiscMetadataBlob(ctx, discID, merged); err != nil {
		slog.Warn("igdb enrich: persist", "err", err, "disc_id", discID)
		return
	}
	df.bc.Publish(state.Event{
		Name:    "disc.changed",
		Payload: map[string]any{"id": discID, "metadata_json": merged},
	})
	slog.Info("igdb enrich: applied", "disc_id", discID, "igdb_id", match.IGDBID)
}
