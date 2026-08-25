package api

import (
	"fmt"
	"text/template"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// OptionType is a tag for option-blob value types.
type OptionType string

const (
	OptString OptionType = "string"
	OptInt    OptionType = "int"
	OptBool   OptionType = "bool"
)

// OptionSchema declares one valid key in a profile's options blob.
type OptionSchema struct {
	Type     OptionType
	Required bool
}

// EngineSchema declares the constraints for one engine string. The
// daemon validates incoming profiles against the engine-keyed map at
// CreateProfile/UpdateProfile time.
//
// Containers and VideoCodecs are the typed fields that drive the
// mockup-shaped editor; Formats stays as a fallback while the
// deprecated Profile.Format is kept (one-release window).
type EngineSchema struct {
	Formats     []string
	Containers  []string
	VideoCodecs []string
	Options     map[string]OptionSchema
	StepCount   int
}

// engineSchemas is the canonical map. Hand-edited; changes here also
// need a parallel update in webui/src/lib/profile_schema.ts. Server is
// authoritative — clients drift, but bad input gets rejected.
var engineSchemas = map[string]EngineSchema{
	"whipper": {
		Formats:     []string{"FLAC"},
		Containers:  []string{"FLAC"},
		VideoCodecs: []string{},
		Options: map[string]OptionSchema{
			// embed_cover_art: download release-level cover art from
			// the MusicBrainz Cover Art Archive and embed it into each
			// ripped FLAC via metaflac. Best-effort: a 404 / network
			// failure / missing metaflac binary logs a WARN and does
			// not fail the rip.
			"embed_cover_art": {Type: OptBool},
			// replaygain_album_mode: write per-track + album-mode
			// ReplayGain 2.0 tags onto the rip via loudgain. Best-effort
			// same as above. Album-mode requires loudgain to see every
			// track of the album in one invocation; the pipeline gathers
			// them automatically before the call.
			"replaygain_album_mode": {Type: OptBool},
		},
		StepCount: 6,
	},
	"MakeMKV": {
		Formats:     []string{"MKV"},
		Containers:  []string{"MKV"},
		VideoCodecs: []string{"copy"},
		Options: map[string]OptionSchema{
			"min_title_seconds": {Type: OptInt},
			"keep_all_tracks":   {Type: OptBool},
			"show_title_picker": {Type: OptBool}, // route through pre-rip title picker
			"include_extras":    {Type: OptBool}, // auto-include bonus-material titles in the rip
			"min_extra_seconds": {Type: OptInt},  // shortest title duration considered an extra (default 60)
			"extras_max_ratio":  {Type: OptInt},  // longest extra as % of main duration (default 90)
			// dvd_selection_mode ("main_feature" | "per_title") is read by
			// IsMovieProfile in the dvdvideo pipeline; the passthrough
			// movie profile seeds it. season feeds per-title output paths.
			"dvd_selection_mode": {Type: OptString},
			"season":             {Type: OptInt},
			// extract_text_subtitles pulls text-based subtitle tracks
			// (ASS/SSA/SRT) out of the moved .mkv as sidecar files via
			// mkvextract; content_type ("movie" | "tv") picks LibraryRoot
			// vs LibraryTV at move time. Both apply even without a
			// transcode step.
			"extract_text_subtitles": {Type: OptBool},
			"content_type":           {Type: OptString},
		},
		StepCount: 6,
	},
	"MakeMKV+HandBrake": {
		Formats:     []string{"MKV"},
		Containers:  []string{"MKV"},
		VideoCodecs: []string{"x265", "x264", "nvenc_h265", "nvenc_h264", "av1", "copy"},
		Options: map[string]OptionSchema{
			"min_title_seconds": {Type: OptInt},
			"keep_all_tracks":   {Type: OptBool},
			"show_title_picker": {Type: OptBool}, // route through pre-rip title picker
			"include_extras":    {Type: OptBool},
			"min_extra_seconds": {Type: OptInt},
			"extras_max_ratio":  {Type: OptInt},
			// HandBrake-step knobs: quality_rf + encoder_preset are read by
			// transcodeMakeMKVRips; dvd_selection_mode by IsMovieProfile;
			// season by the per-title (series) output path. The seeded
			// extras + series profiles set all four.
			"dvd_selection_mode": {Type: OptString},
			"quality_rf":         {Type: OptInt},
			"encoder_preset":     {Type: OptString},
			"season":             {Type: OptInt},
			// max_height caps output resolution (e.g. 1080), preserving
			// aspect ratio, never upscaling. stereo_audio encodes every
			// kept audio track (--all-audio keeps all of them, e.g.
			// Japanese + English) down to 2.0 AAC. extract_text_subtitles
			// pulls text-based subtitle tracks out as sidecar files.
			// content_type ("movie" | "tv") picks LibraryRoot vs LibraryTV
			// at move time.
			"max_height":             {Type: OptInt},
			"stereo_audio":           {Type: OptBool},
			"extract_text_subtitles": {Type: OptBool},
			"content_type":           {Type: OptString},
		},
		StepCount: 7,
	},
	"HandBrake": {
		Formats:     []string{"MP4", "MKV"},
		Containers:  []string{"MP4", "MKV"},
		VideoCodecs: []string{"x265", "x264", "nvenc_h265", "nvenc_h264", "av1"},
		Options: map[string]OptionSchema{
			"min_title_seconds":      {Type: OptInt},
			"season":                 {Type: OptInt},
			"dvd_selection_mode":     {Type: OptString},
			"quality_rf":             {Type: OptInt},
			"encoder_preset":         {Type: OptString},
			"show_title_picker":      {Type: OptBool},
			"include_extras":         {Type: OptBool},
			"min_extra_seconds":      {Type: OptInt},
			"extras_max_ratio":       {Type: OptInt},
			"max_height":             {Type: OptInt},
			"stereo_audio":           {Type: OptBool},
			"extract_text_subtitles": {Type: OptBool},
			"content_type":           {Type: OptString},
		},
		StepCount: 7,
	},
	"redumper+chdman": {
		Formats:     []string{"CHD"},
		Containers:  []string{"CHD"},
		VideoCodecs: []string{},
		Options:     map[string]OptionSchema{},
		StepCount:   7,
	},
	"redumper": {
		Formats:     []string{"ISO"},
		Containers:  []string{"ISO"},
		VideoCodecs: []string{},
		Options:     map[string]OptionSchema{},
		StepCount:   5,
	},
	"ddrescue": {
		Formats:     []string{"ISO"},
		Containers:  []string{"ISO"},
		VideoCodecs: []string{},
		Options:     map[string]OptionSchema{},
		StepCount:   6,
	},
	"vcdimager": {
		Formats:     []string{"MPEG"},
		Containers:  []string{"MPG"},
		VideoCodecs: []string{},
		Options:     map[string]OptionSchema{},
		StepCount:   6,
	},
}

// DiscTypeEngines is the curated allow-list of engines per disc type.
// ValidateProfile rejects an engine that isn't listed for the profile's
// disc type. The first entry is the recommended default the editor
// pre-fills when a new profile's disc type is chosen. A disc type absent
// from this map imposes no engine constraint (defensive — every type the
// editor offers is listed here).
var DiscTypeEngines = map[state.DiscType][]string{
	state.DiscTypeAudioCD:  {"whipper"},
	state.DiscTypeDVD:      {"MakeMKV+HandBrake", "MakeMKV", "HandBrake"},
	state.DiscTypeBDMV:     {"MakeMKV+HandBrake", "MakeMKV"},
	state.DiscTypeUHD:      {"MakeMKV", "MakeMKV+HandBrake"},
	state.DiscTypePSX:      {"redumper+chdman"},
	state.DiscTypePS2:      {"redumper+chdman"},
	state.DiscTypeSAT:      {"redumper+chdman"},
	state.DiscTypeDC:       {"redumper+chdman"},
	state.DiscTypeXBOX:     {"redumper"},
	state.DiscTypeXBOX360:  {"redumper"},
	state.DiscTypeWII:      {"redumper"},
	state.DiscTypeWIIU:     {"redumper"},
	state.DiscTypePS3:      {"ps3dumper-cli"},
	state.DiscTypeSegaCD:   {"redumper+chdman"},
	state.DiscType3DO:      {"redumper+chdman"},
	state.DiscTypePCFX:     {"redumper+chdman"},
	state.DiscTypeJaguarCD: {"redumper+chdman"},
	state.DiscTypeCDi:      {"redumper+chdman"},
	state.DiscTypePCECD:    {"redumper+chdman"},
	state.DiscTypeNeoCD:    {"redumper+chdman"},
	state.DiscTypeCD32:     {"redumper+chdman"},
	state.DiscTypeFMTowns:  {"redumper+chdman"},
	state.DiscTypePippin:   {"redumper+chdman"},
	state.DiscTypeData:     {"ddrescue"},
	state.DiscTypeVCD:      {"vcdimager"},
}

// QualityTier is a resolved (constant-quality RF, encoder speed-preset)
// pair for one codec at one named quality tier.
type QualityTier struct {
	RF     int
	Preset string
}

// QualityTierSlugs are the valid quality_preset values for an
// encode-bearing profile. "" is the value for lossless/passthrough
// engines; "custom" means the user supplies raw quality_rf +
// encoder_preset directly (no tier resolution).
var QualityTierSlugs = []string{"archival", "high", "balanced", "space-saver", "custom"}

// QualityTiers maps video codec → tier slug → resolved encode settings.
// Picking a tier in the editor writes options.quality_rf +
// options.encoder_preset from this table (the webui mirror resolves the
// same values); the pipeline reads those two options unchanged. "custom"
// is intentionally absent — it stores raw user values instead. RF values
// follow the codec-equivalence rule of thumb (x265 ≈ x264+2, NVENC needs
// a few more, AV1 on its own scale) and are tunable.
var QualityTiers = map[string]map[string]QualityTier{
	"x264": {
		"archival":    {RF: 16, Preset: "slower"},
		"high":        {RF: 18, Preset: "slow"},
		"balanced":    {RF: 21, Preset: "medium"},
		"space-saver": {RF: 23, Preset: "fast"},
	},
	"x265": {
		"archival":    {RF: 18, Preset: "slower"},
		"high":        {RF: 20, Preset: "slow"},
		"balanced":    {RF: 23, Preset: "medium"},
		"space-saver": {RF: 25, Preset: "fast"},
	},
	"nvenc_h264": {
		"archival":    {RF: 18, Preset: "slower"},
		"high":        {RF: 21, Preset: "slow"},
		"balanced":    {RF: 24, Preset: "medium"},
		"space-saver": {RF: 27, Preset: "fast"},
	},
	"nvenc_h265": {
		"archival":    {RF: 18, Preset: "slower"},
		"high":        {RF: 21, Preset: "slow"},
		"balanced":    {RF: 24, Preset: "medium"},
		"space-saver": {RF: 27, Preset: "fast"},
	},
	"av1": {
		"archival":    {RF: 20, Preset: "slower"},
		"high":        {RF: 24, Preset: "slow"},
		"balanced":    {RF: 28, Preset: "medium"},
		"space-saver": {RF: 32, Preset: "fast"},
	},
}

// HDRPipelines lists the valid hdr_pipeline values. Empty is allowed
// (per-engine default — no HDR concept for audio/data engines).
var HDRPipelines = []string{"", "passthrough", "hdr10plus", "tone-map-sdr", "strip"}

// DrivePolicies lists the valid drive_policy values. Only "any" is
// enforced today: no scheduler code reads Profile.DrivePolicy, so the
// "drv-N" pin values are a no-op (and could never match a real drive,
// whose ID is a UUID). They're retained as reserved slugs for a future
// drive-pinning feature; the profile editor no longer surfaces the
// control so users aren't misled into thinking a pin takes effect.
var DrivePolicies = []string{"any", "drv-1", "drv-2", "drv-3"}

// ValidationError is one field-level issue with a submitted profile.
type ValidationError struct {
	Field string `json:"field"`
	Msg   string `json:"msg"`
}

// ValidateProfile checks p against engineSchemas. Returns a slice of
// field-specific errors (empty when valid).
//
// Rules:
//   - Name + DiscType + Engine required.
//   - Engine must exist in engineSchemas.
//   - Engine must be allowed for the disc type (DiscTypeEngines).
//   - QualityPreset must be a tier slug (QualityTierSlugs) or "";
//     encode-bearing profiles (real video codec, not "copy") require a
//     tier.
//   - Container must be in schema.Containers (or, during the
//     deprecation window, Format must be in schema.Formats when
//     Container is empty).
//   - VideoCodec must be in schema.VideoCodecs when the engine has
//     any (audio/data engines have an empty list — VideoCodec must
//     itself be empty there).
//   - HDRPipeline must be in HDRPipelines.
//   - DrivePolicy must be in DrivePolicies (or "" — defaulted to "any").
//   - Each Options key must exist in schema.Options; the value's
//     runtime type must match (JSON-decoded ints become float64 — we
//     accept both for OptInt so the API works as users expect).
//   - OutputPathTemplate must parse via text/template.
func ValidateProfile(p *state.Profile) []ValidationError {
	var errs []ValidationError

	if p.Name == "" {
		errs = append(errs, ValidationError{Field: "name", Msg: "required"})
	}
	if p.DiscType == "" {
		errs = append(errs, ValidationError{Field: "disc_type", Msg: "required"})
	}
	if p.Engine == "" {
		errs = append(errs, ValidationError{Field: "engine", Msg: "required"})
		return errs
	}
	schema, ok := engineSchemas[p.Engine]
	if !ok {
		errs = append(errs, ValidationError{
			Field: "engine",
			Msg:   fmt.Sprintf("unknown engine %q", p.Engine),
		})
		return errs
	}

	if allowed, known := DiscTypeEngines[p.DiscType]; known && !contains(allowed, p.Engine) {
		errs = append(errs, ValidationError{
			Field: "engine",
			Msg:   fmt.Sprintf("disc type %s does not support engine %q (allowed: %v)", p.DiscType, p.Engine, allowed),
		})
	}

	switch {
	case p.Container != "":
		if !contains(schema.Containers, p.Container) {
			errs = append(errs, ValidationError{
				Field: "container",
				Msg:   fmt.Sprintf("engine %s requires container in %v, got %q", p.Engine, schema.Containers, p.Container),
			})
		}
	case p.Format != "":
		if !contains(schema.Formats, p.Format) {
			errs = append(errs, ValidationError{
				Field: "format",
				Msg:   fmt.Sprintf("engine %s requires format in %v, got %q", p.Engine, schema.Formats, p.Format),
			})
		}
	default:
		errs = append(errs, ValidationError{
			Field: "container",
			Msg:   fmt.Sprintf("engine %s requires container in %v", p.Engine, schema.Containers),
		})
	}

	if p.VideoCodec != "" {
		if len(schema.VideoCodecs) == 0 {
			errs = append(errs, ValidationError{
				Field: "video_codec",
				Msg:   fmt.Sprintf("engine %s does not accept a video codec", p.Engine),
			})
		} else if !contains(schema.VideoCodecs, p.VideoCodec) {
			errs = append(errs, ValidationError{
				Field: "video_codec",
				Msg:   fmt.Sprintf("engine %s requires video codec in %v, got %q", p.Engine, schema.VideoCodecs, p.VideoCodec),
			})
		}
	}

	// quality_preset is a tier slug (or "" for lossless/passthrough
	// engines). Encode-bearing profiles (a real video codec, not the
	// "copy" passthrough) must carry a tier so the field can never be
	// the empty/garbage string the free-text version allowed.
	if p.QualityPreset != "" && !contains(QualityTierSlugs, p.QualityPreset) {
		errs = append(errs, ValidationError{
			Field: "quality_preset",
			Msg:   fmt.Sprintf("quality_preset must be one of %v, got %q", QualityTierSlugs, p.QualityPreset),
		})
	}
	if p.VideoCodec != "" && p.VideoCodec != "copy" && p.QualityPreset == "" {
		errs = append(errs, ValidationError{
			Field: "quality_preset",
			Msg:   "quality_preset is required for encoding profiles",
		})
	}

	if !contains(HDRPipelines, p.HDRPipeline) {
		errs = append(errs, ValidationError{
			Field: "hdr_pipeline",
			Msg:   fmt.Sprintf("hdr_pipeline must be one of %v, got %q", HDRPipelines[1:], p.HDRPipeline),
		})
	}

	if p.DrivePolicy != "" && !contains(DrivePolicies, p.DrivePolicy) {
		errs = append(errs, ValidationError{
			Field: "drive_policy",
			Msg:   fmt.Sprintf("drive_policy must be one of %v, got %q", DrivePolicies, p.DrivePolicy),
		})
	}

	for k, v := range p.Options {
		opt, known := schema.Options[k]
		if !known {
			errs = append(errs, ValidationError{
				Field: "options." + k,
				Msg:   fmt.Sprintf("unknown option for engine %s", p.Engine),
			})
			continue
		}
		if !valueMatchesType(v, opt.Type) {
			errs = append(errs, ValidationError{
				Field: "options." + k,
				Msg:   fmt.Sprintf("expected %s", opt.Type),
			})
		}
	}
	if p.OutputPathTemplate != "" {
		if _, err := template.New("output").Parse(p.OutputPathTemplate); err != nil {
			errs = append(errs, ValidationError{
				Field: "output_path_template",
				Msg:   err.Error(),
			})
		}
	}
	return errs
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func valueMatchesType(v any, t OptionType) bool {
	switch t {
	case OptString:
		_, ok := v.(string)
		return ok
	case OptInt:
		// JSON decodes numbers as float64. Accept both.
		switch n := v.(type) {
		case int:
			return true
		case float64:
			return n == float64(int(n)) // whole number
		}
		return false
	case OptBool:
		_, ok := v.(bool)
		return ok
	}
	return false
}
