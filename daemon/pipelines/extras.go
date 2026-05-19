package pipelines

// ExtrasBucket is one of the Jellyfin/Emby-recognised bonus-feature
// subfolders. Folder is the lowercase subfolder name (both servers
// match case-insensitively); Label is the title-cased noun used in the
// rendered filename so an extras folder reads as
//
//	featurettes/Featurette 01.mkv
//	featurettes/Featurette 02.mkv
//	behind the scenes/Behind The Scenes 01.mkv
type ExtrasBucket struct {
	Folder string
	Label  string
}

var (
	BucketTrailer        = ExtrasBucket{Folder: "trailers", Label: "Trailer"}
	BucketFeaturette     = ExtrasBucket{Folder: "featurettes", Label: "Featurette"}
	BucketBehindTheScene = ExtrasBucket{Folder: "behind the scenes", Label: "Behind The Scenes"}
)

// ClassifyExtraByDuration buckets a bonus feature by its source-title
// duration in seconds. Used by the DVD-Video pipeline, where the
// HandBrake scan gives moveOutputs direct access to each title's
// DurationSeconds. Thresholds:
//
//   - ≤ 5 min  → Trailer
//   - ≤ 30 min → Featurette
//   - > 30 min → Behind The Scenes
func ClassifyExtraByDuration(seconds int) ExtrasBucket {
	switch {
	case seconds <= 300:
		return BucketTrailer
	case seconds <= 1800:
		return BucketFeaturette
	default:
		return BucketBehindTheScene
	}
}

// ClassifyExtraBySizeRatio buckets a bonus feature by the ratio of its
// transcoded file size to the main feature's transcoded file size.
// Used by the BDMV pipeline, where moveOutputs gets the transcoded
// paths only — the per-title MakeMKV scan results aren't in scope and
// threading them through RipResult is heavier than this is worth.
//
// With a single constant-quality HEVC pass, transcoded file size scales
// roughly linearly with source duration, so a size-ratio split lands in
// the same buckets as the duration-based DVD path for the 90-min to
// 180-min main features that dominate Blu-ray catalog. mainSize ≤ 0
// (stats failed) collapses every extra into BucketFeaturette so we
// never crash on an unreadable main.
//
//   - ratio ≤ 0.05 → Trailer        (~ ≤  5 min of a 100-min main)
//   - ratio ≤ 0.25 → Featurette     (~ ≤ 25 min of a 100-min main)
//   - ratio >  0.25 → Behind The Scenes
func ClassifyExtraBySizeRatio(size, mainSize int64) ExtrasBucket {
	if mainSize <= 0 {
		return BucketFeaturette
	}
	ratio := float64(size) / float64(mainSize)
	switch {
	case ratio <= 0.05:
		return BucketTrailer
	case ratio <= 0.25:
		return BucketFeaturette
	default:
		return BucketBehindTheScene
	}
}
