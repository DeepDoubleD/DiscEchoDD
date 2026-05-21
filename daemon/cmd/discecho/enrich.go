package main

import (
	"encoding/json"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// igdbMinSimilarity is the strict title-match gate: an IGDB result is trusted
// as the same game only when its title clears this Jaccard similarity against
// the dat/bootcode-identified title. A wrong cover is worse than none.
const igdbMinSimilarity = 0.6

// gameDiscForIGDB reports whether the disc type is a game type IGDB enriches.
func gameDiscForIGDB(t state.DiscType) bool {
	switch t {
	case state.DiscTypePSX, state.DiscTypePS2, state.DiscTypeSAT, state.DiscTypeDC, state.DiscTypeXBOX:
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
