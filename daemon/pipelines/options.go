package pipelines

import (
	"strconv"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// IntOption reads an integer-valued key from a profile's Options blob,
// returning def when the key is absent or not a whole number. JSON
// decoding turns numbers into float64, so both int and whole-valued
// float64 are accepted — callers don't have to care which shape the
// value arrived in.
func IntOption(prof *state.Profile, key string, def int) int {
	if prof == nil {
		return def
	}
	switch n := prof.Options[key].(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return def
}

// StringOption reads a string-valued key from a profile's Options
// blob, returning def when the key is absent, empty, or not a string.
func StringOption(prof *state.Profile, key, def string) string {
	if prof == nil {
		return def
	}
	if s, ok := prof.Options[key].(string); ok && s != "" {
		return s
	}
	return def
}

// BoolOption reads a boolean-valued key from a profile's Options blob,
// returning def when the key is absent or not a bool.
func BoolOption(prof *state.Profile, key string, def bool) bool {
	if prof == nil {
		return def
	}
	if b, ok := prof.Options[key].(bool); ok {
		return b
	}
	return def
}

// IncludeExtrasFromProfile reports whether a profile's include_extras
// option is set. When true and the title picker didn't override, the
// handler auto-includes bonus-material titles alongside the main
// feature in the rip.
func IncludeExtrasFromProfile(prof *state.Profile) bool {
	return BoolOption(prof, "include_extras", false)
}

// MinExtraSecondsFromProfile returns the shortest title duration
// considered an extra (default 60s). Titles shorter than this are
// navigation loops / transitions and are dropped from the extras set.
func MinExtraSecondsFromProfile(prof *state.Profile) int {
	return IntOption(prof, "min_extra_seconds", 60)
}

// ExtrasMaxRatioFromProfile returns the longest-extra-as-percent-of-
// main-feature cap (default 90 → 0.9). Titles longer than this ratio
// of the main feature are typically alternate cuts / commentary
// dupes and are dropped to avoid duplicate full-length encodes.
// Returned as a float fraction in [0, 1].
func ExtrasMaxRatioFromProfile(prof *state.Profile) float64 {
	pct := IntOption(prof, "extras_max_ratio", 90)
	if pct <= 0 || pct > 100 {
		pct = 90
	}
	return float64(pct) / 100.0
}

// MaxHeightFromProfile returns the profile's resolution cap in pixels
// (e.g. 1080), or 0 when unset/not positive -- 0 means "no cap, keep
// the disc's native resolution".
func MaxHeightFromProfile(prof *state.Profile) int {
	h := IntOption(prof, "max_height", 0)
	if h <= 0 {
		return 0
	}
	return h
}

// StereoAudioFromProfile reports whether every audio track should be
// encoded down to a 2.0 stereo mix, rather than passed through at its
// native channel layout.
func StereoAudioFromProfile(prof *state.Profile) bool {
	return BoolOption(prof, "stereo_audio", false)
}

// stereoAudioEncoder is the HandBrake audio encoder paired with
// --mixdown stereo. Stereo mixdown requires an actual encode pass (a
// codec-copy track can't be remixed), so this can't be "copy" -- av_aac
// is ffmpeg's built-in AAC encoder, always present in an ffmpeg-linked
// HandBrake build, unlike platform encoders like ca_aac (macOS-only).
const stereoAudioEncoder = "av_aac"

// stereoAudioBitrateKbps is a solid, unremarkable default for a 2.0 AAC
// mix -- not exposed as a profile option to keep the option surface
// small; revisit if someone actually needs a different bitrate.
const stereoAudioBitrateKbps = 160

// ResolutionAndAudioArgs returns the extra HandBrake CLI args a
// profile's max_height / stereo_audio options add on top of a
// pipeline's existing --all-audio (kept as-is: multiple audio tracks,
// e.g. Japanese + English, must survive so a player can switch between
// them -- this only changes how each surviving track is encoded, never
// how many are kept).
func ResolutionAndAudioArgs(prof *state.Profile) []string {
	var args []string
	if h := MaxHeightFromProfile(prof); h > 0 {
		args = append(args, "--maxHeight", strconv.Itoa(h), "--maxWidth", strconv.Itoa(h*16/9))
	}
	if StereoAudioFromProfile(prof) {
		args = append(args, "--aencoder", stereoAudioEncoder, "--mixdown", "stereo",
			"--ab", strconv.Itoa(stereoAudioBitrateKbps))
	}
	return args
}
