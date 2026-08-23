package pipelines

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// MKVSubtitleTool is the slice of tools.MKVToolNix used to lift
// text-based subtitle tracks out of a transcoded .mkv into standalone
// sidecar files. tests can supply a fake.
type MKVSubtitleTool interface {
	Tracks(ctx context.Context, path string) ([]tools.MKVTrack, error)
	ExtractTrack(ctx context.Context, srcPath string, trackID int, destPath string) error
}

// ExtractTextSubtitlesFromProfile reports whether the profile wants
// text-based subtitle tracks pulled out as sidecar files alongside the
// movie/episode, e.g. "Movie (2016).eng.srt" next to "Movie (2016).mkv"
// -- the naming Jellyfin/Plex both auto-detect and let a viewer toggle
// at playback, same as an embedded track, without burning anything in.
// Bitmap tracks (PGS/VobSub) aren't affected; they stay muxed in the
// video, since there's no lossless way to turn a bitmap into text.
func ExtractTextSubtitlesFromProfile(prof *state.Profile) bool {
	return BoolOption(prof, "extract_text_subtitles", false)
}

// ExtractTextSubtitleSidecars identifies the text-based subtitle
// tracks in mkvPath (already muxed there by HandBrake's --all-subtitles)
// and pulls each one out to "<mkvPath-without-ext>.<lang>.<ext>". A
// second track in the same language gets a ".2", ".3", ... suffix
// before the extension so multiple English tracks (e.g. dialogue +
// signs/songs on an anime disc) don't collide.
//
// A single track's extraction failing is logged via sink and skipped
// rather than failing the whole step -- the video itself already moved
// successfully; a missing sidecar is a minor loss, not a job failure.
// Returns the sidecar paths written.
func ExtractTextSubtitleSidecars(ctx context.Context, mkv MKVSubtitleTool, mkvPath string, sink EventSink) ([]string, error) {
	tracks, err := mkv.Tracks(ctx, mkvPath)
	if err != nil {
		return nil, fmt.Errorf("identify tracks: %w", err)
	}
	ext := filepath.Ext(mkvPath)
	base := strings.TrimSuffix(mkvPath, ext)

	var sidecars []string
	langCount := map[string]int{}
	for _, t := range tracks {
		if !t.IsTextSubtitle() {
			continue
		}
		langCount[t.Language]++
		suffix := t.Language
		if n := langCount[t.Language]; n > 1 {
			suffix = fmt.Sprintf("%s.%d", t.Language, n)
		}
		dest := fmt.Sprintf("%s.%s.%s", base, suffix, t.SidecarExt())
		if err := mkv.ExtractTrack(ctx, mkvPath, t.ID, dest); err != nil {
			if sink != nil {
				sink.OnLog(state.LogLevelWarn, "subtitle sidecar: track %d (%s) extract failed: %v", t.ID, t.Language, err)
			}
			continue
		}
		if sink != nil {
			sink.OnLog(state.LogLevelInfo, "subtitle sidecar: %s", filepath.Base(dest))
		}
		sidecars = append(sidecars, dest)
	}
	return sidecars, nil
}
