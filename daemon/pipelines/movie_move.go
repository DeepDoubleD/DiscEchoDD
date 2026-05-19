package pipelines

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// MoveMovieOutputs atomic-moves each ripped/transcoded .mkv into the
// library. Three rendering modes:
//
//   - Picker drove the rip → render every file via the profile's
//     template, using selected_title_episodes for TV per-episode
//     naming when present.
//   - Profile opted into include_extras AND >1 files were ripped →
//     identify the main feature as the largest file by size and
//     route every other file into `<mainDir>/<bucket>/<label> NN.mkv`.
//   - Auto-pick single file → existing single-template render.
//
// Colliding rendered paths (movie template with multi-title picker
// pick) still get a `-titleNN` suffix to disambiguate.
//
// Shared between BDMV and the DVD-Video MakeMKV branches; both want
// identical movie/series semantics on MakeMKV-shaped outputs.
func MoveMovieOutputs(libraryRoot string, srcs []string, disc *state.Disc, prof *state.Profile) ([]string, error) {
	titleIDs := SelectedTitleIDsFromDisc(disc)
	epMap := SelectedEpisodeMapFromDisc(disc)
	// Picker-recorded season wins; otherwise fall back to the profile's
	// `season` option (DVD-Series MakeMKV seeds set it to 1). 0 stays 0
	// so output paths render `Season 00/` for movie templates that
	// reference Season — matches the legacy HandBrake-engine behaviour.
	season := SelectedSeasonFromDisc(disc)
	if season == 0 {
		season = IntOption(prof, "season", 0)
	}

	extrasMode := len(titleIDs) == 0 && len(srcs) > 1 &&
		IncludeExtrasFromProfile(prof)
	mainIdx := -1
	var mainDir string
	var srcSizes []int64
	var mainSize int64
	if extrasMode {
		srcSizes = sizeOfFiles(srcs)
		mainIdx = indexOfLargest(srcSizes)
		if mainIdx >= 0 && mainIdx < len(srcSizes) {
			mainSize = srcSizes[mainIdx]
		}
		mainFields := OutputFields{
			Title: disc.Title,
			Year:  disc.Year,
			Show:  disc.Title,
		}
		mainRel, err := RenderOutputPath(prof.OutputPathTemplate, mainFields)
		if err != nil {
			return nil, fmt.Errorf("render main template: %w", err)
		}
		mainDir = filepath.Dir(mainRel)
	}

	rendered := make([]string, len(srcs))
	extraCounters := map[string]int{}
	for i := range srcs {
		if extrasMode && i != mainIdx {
			bucket := ClassifyExtraBySizeRatio(srcSizes[i], mainSize)
			extraCounters[bucket.Folder]++
			rendered[i] = filepath.Join(mainDir, bucket.Folder,
				fmt.Sprintf("%s %02d.mkv", bucket.Label, extraCounters[bucket.Folder]))
			continue
		}
		fields := OutputFields{
			Title:         disc.Title,
			Year:          disc.Year,
			Show:          disc.Title,
			Season:        season,
			EpisodeNumber: i + 1,
		}
		if i < len(titleIDs) {
			if ea, ok := epMap[titleIDs[i]]; ok {
				if ea.Episode > 0 {
					fields.EpisodeNumber = ea.Episode
				}
				fields.EpisodeTitle = ea.EpisodeTitle
			}
		}
		rel, err := RenderOutputPath(prof.OutputPathTemplate, fields)
		if err != nil {
			return nil, fmt.Errorf("render template: %w", err)
		}
		rendered[i] = rel
	}
	counts := map[string]int{}
	for _, r := range rendered {
		counts[r]++
	}
	for i, r := range rendered {
		if counts[r] > 1 {
			ext := filepath.Ext(r)
			rendered[i] = strings.TrimSuffix(r, ext) + fmt.Sprintf("-title%02d", i+1) + ext
		}
	}
	moved := make([]string, 0, len(srcs))
	for i, src := range srcs {
		dst := filepath.Join(libraryRoot, rendered[i])
		if err := AtomicMove(src, dst); err != nil {
			return moved, err
		}
		moved = append(moved, dst)
	}
	return moved, nil
}

// sizeOfFiles stats every path and returns the resulting sizes in
// the same order. Entries we couldn't stat (e.g. test stubs that
// don't write real content) come back as 0 — callers treat that as
// "unknown size" rather than failing the move.
func sizeOfFiles(paths []string) []int64 {
	out := make([]int64, len(paths))
	for i, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			out[i] = fi.Size()
		}
	}
	return out
}

// indexOfLargest returns the index of the largest entry in sizes, or
// 0 when every entry is zero (i.e. stubbed-out test content). Used by
// extras-mode MoveMovieOutputs to identify the main feature among
// multiple ripped titles.
func indexOfLargest(sizes []int64) int {
	best := 0
	var bestSize int64
	for i, s := range sizes {
		if s > bestSize {
			bestSize = s
			best = i
		}
	}
	return best
}
