package pipelines

import (
	"fmt"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// SelectMovieTitles returns the user-picked titles when
// disc.metadata_json.selected_title_ids is set, otherwise falls back
// to the longest title meeting the profile's min_title_seconds floor.
// When the profile opts into include_extras AND we're on the auto-
// pick path, every other title in the [min_extra_seconds,
// main × extras_max_ratio] duration band joins the slice after the
// main title and mainID is set to the longest-title id so downstream
// code (e.g. moveMultiTitle) can route the extras into an `extras/`
// subfolder. Picker-driven flows return mainID=0 — the user is
// telling us exactly what to rip.
//
// Shared between the BDMV pipeline and the DVD-Video pipeline's
// MakeMKV branches; both want identical movie/extras semantics.
func SelectMovieTitles(titles []tools.MakeMKVTitle, prof *state.Profile, disc *state.Disc) ([]tools.MakeMKVTitle, int, error) {
	if ids := SelectedTitleIDsFromDisc(disc); len(ids) > 0 {
		picked := make([]tools.MakeMKVTitle, 0, len(ids))
		byID := make(map[int]tools.MakeMKVTitle, len(titles))
		for _, t := range titles {
			byID[t.ID] = t
		}
		for _, id := range ids {
			if t, ok := byID[id]; ok {
				picked = append(picked, t)
			}
		}
		if len(picked) == 0 {
			return nil, 0, fmt.Errorf("none of selected title IDs %v found in MakeMKV scan", ids)
		}
		return picked, 0, nil
	}
	main, err := PickLongestTitle(titles, prof)
	if err != nil {
		return nil, 0, err
	}
	out := []tools.MakeMKVTitle{main}
	mainID := 0
	if IncludeExtrasFromProfile(prof) {
		extras := SelectExtras(titles, main,
			MinExtraSecondsFromProfile(prof),
			ExtrasMaxRatioFromProfile(prof))
		if len(extras) > 0 {
			out = append(out, extras...)
			mainID = main.ID
		}
	}
	return out, mainID, nil
}

// SelectExtras picks every title in the duration band
// [minSec, main.DurationSec × maxRatio] that isn't the main itself.
// Returns matches in scan order so output ordering is deterministic.
func SelectExtras(titles []tools.MakeMKVTitle, main tools.MakeMKVTitle, minSec int, maxRatio float64) []tools.MakeMKVTitle {
	maxSec := int(float64(main.DurationSec) * maxRatio)
	out := make([]tools.MakeMKVTitle, 0)
	for _, t := range titles {
		if t.ID == main.ID {
			continue
		}
		if t.DurationSec < minSec || t.DurationSec > maxSec {
			continue
		}
		out = append(out, t)
	}
	return out
}

// PickLongestTitle selects the longest title that meets
// options.min_title_seconds. Returns an error if no title qualifies.
func PickLongestTitle(titles []tools.MakeMKVTitle, prof *state.Profile) (tools.MakeMKVTitle, error) {
	min := IntOption(prof, "min_title_seconds", 0)
	var best tools.MakeMKVTitle
	found := false
	for _, t := range titles {
		if t.DurationSec < min {
			continue
		}
		if !found || t.DurationSec > best.DurationSec {
			best = t
			found = true
		}
	}
	if !found {
		return tools.MakeMKVTitle{}, fmt.Errorf("no title with duration >= %ds", min)
	}
	return best, nil
}
