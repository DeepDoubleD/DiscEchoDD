// Package settings loads environment configuration and seeds the
// database on first start: built-in profiles and any
// APPRISE_URLS notifications. The bearer token (if any) comes
// straight from DISCECHO_TOKEN — no on-disk persistence, no auto
// generation.
package settings

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// Settings is the resolved runtime configuration.
type Settings struct {
	Addr  string
	Token string
	// LibraryRoot is the env-derived parent directory (DISCECHO_LIBRARY).
	// New code should read the typed roots below; this is kept so that
	// host-disk-usage reporting still has a stable mount to stat.
	LibraryRoot          string
	LibraryMovies        string
	LibraryTV            string
	LibraryMusic         string
	LibraryGames         string
	LibraryData          string
	DataPath             string
	AutoConfirmSeconds   int
	WhipperBin           string
	AppriseBin           string
	EjectBin             string
	MetaflacBin          string
	LoudgainBin          string
	CDInfoBin            string
	CDParanoiaBin        string
	MusicBrainzBaseURL   string
	MusicBrainzUserAgent string
	TMDBKey              string
	TMDBLang             string
	SubsLang             string
	HandBrakeBin         string
	IsoInfoBin           string
	MakeMKVBin           string
	MakeMKVDataDir       string
	MakeMKVBetaKey       string
	BDInfoBin            string
	MKVMergeBin          string
	MKVExtractBin        string
	RedumperBin          string
	CHDManBin            string
	RedumpDataDir        string
	DDRescueBin          string
	VCDXRipBin           string
	IGDBClientID         string
	IGDBClientSecret     string
}

// Load reads env vars, seeds default rows, and returns a *Settings.
// If getenv is nil, os.Getenv is used.
func Load(getenv func(string) string, store *state.Store, version string) (*Settings, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	s := &Settings{
		Addr:                 firstNonEmpty(getenv("DISCECHO_ADDR"), ":8088"),
		LibraryRoot:          firstNonEmpty(getenv("DISCECHO_LIBRARY"), "/library"),
		DataPath:             firstNonEmpty(getenv("DISCECHO_DATA"), "/var/lib/discecho"),
		WhipperBin:           firstNonEmpty(getenv("DISCECHO_WHIPPER_BIN"), "whipper"),
		AppriseBin:           firstNonEmpty(getenv("DISCECHO_APPRISE_BIN"), "apprise"),
		EjectBin:             firstNonEmpty(getenv("DISCECHO_EJECT_BIN"), "eject"),
		MetaflacBin:          firstNonEmpty(getenv("DISCECHO_METAFLAC_BIN"), "metaflac"),
		LoudgainBin:          firstNonEmpty(getenv("DISCECHO_LOUDGAIN_BIN"), "loudgain"),
		CDInfoBin:            firstNonEmpty(getenv("DISCECHO_CDINFO_BIN"), "cd-info"),
		CDParanoiaBin:        firstNonEmpty(getenv("DISCECHO_CDPARANOIA_BIN"), "cdparanoia"),
		MusicBrainzBaseURL:   firstNonEmpty(getenv("DISCECHO_MB_BASE_URL"), "https://musicbrainz.org"),
		MusicBrainzUserAgent: fmt.Sprintf("DiscEcho/%s ( https://github.com/jumpingmushroom/DiscEcho )", version),
		TMDBKey:              getenv("DISCECHO_TMDB_KEY"),
		TMDBLang:             firstNonEmpty(getenv("DISCECHO_TMDB_LANG"), "en-US"),
		SubsLang:             firstNonEmpty(getenv("DISCECHO_SUBS_LANG"), "eng"),
		HandBrakeBin:         firstNonEmpty(getenv("DISCECHO_HANDBRAKE_BIN"), "HandBrakeCLI"),
		IsoInfoBin:           firstNonEmpty(getenv("DISCECHO_ISOINFO_BIN"), "isoinfo"),
		MakeMKVBin:           firstNonEmpty(getenv("DISCECHO_MAKEMKV_BIN"), "makemkvcon"),
		MakeMKVDataDir:       firstNonEmpty(getenv("DISCECHO_MAKEMKV_DATA"), filepath.Join(firstNonEmpty(getenv("DISCECHO_DATA"), "/var/lib/discecho"), "MakeMKV")),
		MakeMKVBetaKey:       getenv("DISCECHO_MAKEMKV_BETA_KEY"),
		BDInfoBin:            firstNonEmpty(getenv("DISCECHO_BDINFO_BIN"), "bd_info"),
		MKVMergeBin:          firstNonEmpty(getenv("DISCECHO_MKVMERGE_BIN"), "mkvmerge"),
		MKVExtractBin:        firstNonEmpty(getenv("DISCECHO_MKVEXTRACT_BIN"), "mkvextract"),
		RedumperBin:          firstNonEmpty(getenv("DISCECHO_REDUMPER_BIN"), "redumper"),
		CHDManBin:            firstNonEmpty(getenv("DISCECHO_CHDMAN_BIN"), "chdman"),
		RedumpDataDir:        firstNonEmpty(getenv("DISCECHO_REDUMP_DIR"), filepath.Join(firstNonEmpty(getenv("DISCECHO_DATA"), "/var/lib/discecho"), "redump")),
		DDRescueBin:          firstNonEmpty(getenv("DISCECHO_DDRESCUE_BIN"), "ddrescue"),
		VCDXRipBin:           firstNonEmpty(getenv("DISCECHO_VCDXRIP_BIN"), "vcdxrip"),
		IGDBClientID:         getenv("DISCECHO_IGDB_CLIENT_ID"),
		IGDBClientSecret:     getenv("DISCECHO_IGDB_CLIENT_SECRET"),
	}

	if v := getenv("DISCECHO_AUTO_CONFIRM_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			s.AutoConfirmSeconds = n
		}
	}
	if s.AutoConfirmSeconds == 0 {
		s.AutoConfirmSeconds = 8
	}

	s.Token = getenv("DISCECHO_TOKEN")

	if err := WriteMakeMKVBetaKey(s.MakeMKVDataDir, s.MakeMKVBetaKey); err != nil {
		return nil, fmt.Errorf("makemkv beta key: %w", err)
	}

	ctx := context.Background()
	if err := seedProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed profile: %w", err)
	}
	if err := seedDVDProfiles(ctx, store); err != nil {
		return nil, fmt.Errorf("seed DVD profiles: %w", err)
	}
	if err := seedBDMVProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed BDMV profile: %w", err)
	}
	if err := seedBlurayDDProfiles(ctx, store); err != nil {
		return nil, fmt.Errorf("seed Blu-Ray DD profiles: %w", err)
	}
	if err := seedDVDDDProfiles(ctx, store); err != nil {
		return nil, fmt.Errorf("seed DVD DD profiles: %w", err)
	}
	if err := seedUHDProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed UHD profile: %w", err)
	}
	if err := seedPSXProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed PSX profile: %w", err)
	}
	if err := seedPS2Profile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed PS2 profile: %w", err)
	}
	if err := seedSaturnProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed Saturn profile: %w", err)
	}
	if err := seedDCProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed DC profile: %w", err)
	}
	if err := seedXboxProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed Xbox profile: %w", err)
	}
	if err := seedXbox360Profile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed Xbox 360 profile: %w", err)
	}
	if err := seedSegaCDProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed SegaCD profile: %w", err)
	}
	if err := seed3DOProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed 3DO profile: %w", err)
	}
	if err := seedPCFXProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed PCFX profile: %w", err)
	}
	if err := seedJaguarCDProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed JaguarCD profile: %w", err)
	}
	if err := seedCDiProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed CDi profile: %w", err)
	}
	if err := seedPCECDProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed PCEngineCD profile: %w", err)
	}
	if err := seedNeoCDProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed NeoGeoCD profile: %w", err)
	}
	if err := seedCD32Profile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed CD32 profile: %w", err)
	}
	if err := seedFMTownsProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed FMTowns profile: %w", err)
	}
	if err := seedPippinProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed Pippin profile: %w", err)
	}
	if err := seedDataProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed Data profile: %w", err)
	}
	if err := seedVCDProfile(ctx, store); err != nil {
		return nil, fmt.Errorf("seed VCD profile: %w", err)
	}
	if err := seedNotifications(ctx, store, getenv("DISCECHO_APPRISE_URLS")); err != nil {
		return nil, fmt.Errorf("seed notifications: %w", err)
	}
	if err := seedRetentionDefault(ctx, store); err != nil {
		return nil, fmt.Errorf("seed retention default: %w", err)
	}
	if err := seedLibraryRoots(ctx, store, getenv, s.LibraryRoot); err != nil {
		return nil, fmt.Errorf("seed library roots: %w", err)
	}
	s.LibraryMovies = resolveLibraryRoot(ctx, store, getenv, "library.movies", "DISCECHO_LIBRARY_MOVIES", s.LibraryRoot, "movies")
	s.LibraryTV = resolveLibraryRoot(ctx, store, getenv, "library.tv", "DISCECHO_LIBRARY_TV", s.LibraryRoot, "tv")
	s.LibraryMusic = resolveLibraryRoot(ctx, store, getenv, "library.music", "DISCECHO_LIBRARY_MUSIC", s.LibraryRoot, "music")
	s.LibraryGames = resolveLibraryRoot(ctx, store, getenv, "library.games", "DISCECHO_LIBRARY_GAMES", s.LibraryRoot, "games")
	s.LibraryData = resolveLibraryRoot(ctx, store, getenv, "library.data", "DISCECHO_LIBRARY_DATA", s.LibraryRoot, "data")
	return s, nil
}

// MediaRoot enumerates the typed library subtrees. Order matches what
// the settings UI renders and what api/system.go advertises.
type MediaRoot string

const (
	MediaMovies MediaRoot = "movies"
	MediaTV     MediaRoot = "tv"
	MediaMusic  MediaRoot = "music"
	MediaGames  MediaRoot = "games"
	MediaData   MediaRoot = "data"
)

// AllMediaRoots is the canonical iteration order.
var AllMediaRoots = []MediaRoot{MediaMovies, MediaTV, MediaMusic, MediaGames, MediaData}

// LibraryFor returns the configured root for one media type. Falls back
// to LibraryRoot/<media> if the typed field is empty (which only happens
// before Load() runs, e.g. in tests that build Settings by hand).
func (s *Settings) LibraryFor(m MediaRoot) string {
	if s == nil {
		return ""
	}
	switch m {
	case MediaMovies:
		if s.LibraryMovies != "" {
			return s.LibraryMovies
		}
	case MediaTV:
		if s.LibraryTV != "" {
			return s.LibraryTV
		}
	case MediaMusic:
		if s.LibraryMusic != "" {
			return s.LibraryMusic
		}
	case MediaGames:
		if s.LibraryGames != "" {
			return s.LibraryGames
		}
	case MediaData:
		if s.LibraryData != "" {
			return s.LibraryData
		}
	}
	if s.LibraryRoot != "" {
		return filepath.Join(s.LibraryRoot, string(m))
	}
	return ""
}

// LibraryRootsMap is a snapshot of the 5 typed roots, suitable for
// serving in /api/system/integrations.
func (s *Settings) LibraryRootsMap() map[string]string {
	out := make(map[string]string, len(AllMediaRoots))
	for _, m := range AllMediaRoots {
		out[string(m)] = s.LibraryFor(m)
	}
	return out
}

// resolveLibraryRoot picks (in order): stored KV row > env override >
// derived <root>/<media>. Caller has already loaded the env-derived
// LibraryRoot so the third branch is always populated.
func resolveLibraryRoot(ctx context.Context, store *state.Store, getenv func(string) string, kvKey, envVar, root, media string) string {
	if v, err := store.GetSetting(ctx, kvKey); err == nil {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(getenv(envVar)); v != "" {
		return v
	}
	if root == "" {
		return ""
	}
	return filepath.Join(root, media)
}

// seedLibraryRoots writes a stored KV row for each typed root that
// doesn't already have one. The seeded value uses the env override if
// set, otherwise <root>/<media>. Existing rows are left untouched —
// upgrades from "library.path" deployments seed the 5 from the parent.
func seedLibraryRoots(ctx context.Context, store *state.Store, getenv func(string) string, root string) error {
	envFor := map[MediaRoot]string{
		MediaMovies: "DISCECHO_LIBRARY_MOVIES",
		MediaTV:     "DISCECHO_LIBRARY_TV",
		MediaMusic:  "DISCECHO_LIBRARY_MUSIC",
		MediaGames:  "DISCECHO_LIBRARY_GAMES",
		MediaData:   "DISCECHO_LIBRARY_DATA",
	}
	// Single-root upgrade: legacy "library.path" wins if no per-media
	// rows exist yet. After this seed it acts only as a default source.
	legacy, _ := store.GetSetting(ctx, "library.path")
	legacy = strings.TrimSpace(legacy)
	for _, m := range AllMediaRoots {
		key := "library." + string(m)
		if v, err := store.GetSetting(ctx, key); err == nil && strings.TrimSpace(v) != "" {
			continue
		}
		var value string
		switch {
		case strings.TrimSpace(getenv(envFor[m])) != "":
			value = strings.TrimSpace(getenv(envFor[m]))
		case legacy != "":
			value = filepath.Join(legacy, string(m))
		case root != "":
			value = filepath.Join(root, string(m))
		default:
			continue
		}
		if err := store.SetSetting(ctx, key, value); err != nil {
			return err
		}
	}
	return nil
}

const (
	cdFlacProfileName                = "CD-FLAC"
	dvdMovieProfileName              = "DVD-Movie"
	dvdMovieExtrasProfileName        = "DVD-Movie + Extras"
	dvdSeriesProfileName             = "DVD-Series"
	dvdMovieMakeMKVProfileName       = "DVD-Movie (MakeMKV)"
	dvdMovieExtrasMakeMKVProfileName = "DVD-Movie + Extras (MakeMKV)"
	dvdSeriesMakeMKVProfileName      = "DVD-Series (MakeMKV)"
	bdProfileName                    = "BD-1080p"
	bdExtrasProfileName              = "BD-1080p + Extras"
	blurayDDMovieProfileName         = "Blu-Ray DD (Movies)"
	blurayDDTVProfileName            = "Blu-Ray DD (TV)"
	dvdDDMovieProfileName            = "DVD DD (Movies)"
	dvdDDTVProfileName               = "DVD DD (TV)"
	uhdProfileName                   = "UHD-Remux"
	psxProfileName                   = "PSX-CHD"
	ps2ProfileName                   = "PS2-CHD"
	saturnProfileName                = "Saturn-CHD"
	dcProfileName                    = "DC-CHD"
	xboxProfileName                  = "XBOX-ISO"
	xbox360ProfileName               = "XBOX360-ISO"
	dataProfileName                  = "Data-ISO"
	vcdProfileName                   = "VCD-MPEG"
	segaCDProfileName                = "SegaCD-CHD"
	threeDOProfileName               = "3DO-CHD"
	pcfxProfileName                  = "PCFX-CHD"
	jaguarCDProfileName              = "JaguarCD-CHD"
	cdiProfileName                   = "CDi-CHD"
	pceCDProfileName                 = "PCEngineCD-CHD"
	neoCDProfileName                 = "NeoGeoCD-CHD"
	cd32ProfileName                  = "CD32-CHD"
	fmTownsProfileName               = "FMTowns-CHD"
	pippinProfileName                = "Pippin-CHD"
)

func seedDVDProfiles(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeDVD)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, p := range existing {
		have[p.Name] = true
	}

	now := time.Now()
	if !have[dvdMovieProfileName] {
		p := &state.Profile{
			DiscType:      state.DiscTypeDVD,
			Name:          dvdMovieProfileName,
			Engine:        "HandBrake",
			Format:        "MKV",
			Preset:        "high",
			Container:     "MKV",
			VideoCodec:    "x264",
			QualityPreset: "high",
			DrivePolicy:   "any",
			Options: map[string]any{
				"dvd_selection_mode": "main_feature",
				"quality_rf":         18,
				"encoder_preset":     "slow",
			},
			OutputPathTemplate: `{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv`,
			Enabled:            true,
			StepCount:          7,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := store.CreateProfile(ctx, p); err != nil {
			return err
		}
	}
	if !have[dvdMovieExtrasProfileName] {
		p := &state.Profile{
			DiscType:      state.DiscTypeDVD,
			Name:          dvdMovieExtrasProfileName,
			Engine:        "HandBrake",
			Format:        "MKV",
			Preset:        "high",
			Container:     "MKV",
			VideoCodec:    "x264",
			QualityPreset: "high",
			DrivePolicy:   "any",
			Options: map[string]any{
				"dvd_selection_mode": "main_feature",
				"quality_rf":         18,
				"encoder_preset":     "slow",
				"include_extras":     true,
				"min_extra_seconds":  60,
				"extras_max_ratio":   90,
			},
			OutputPathTemplate: `{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv`,
			Enabled:            true,
			StepCount:          7,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := store.CreateProfile(ctx, p); err != nil {
			return err
		}
	}
	if !have[dvdSeriesProfileName] {
		p := &state.Profile{
			DiscType:      state.DiscTypeDVD,
			Name:          dvdSeriesProfileName,
			Engine:        "HandBrake",
			Format:        "MKV",
			Preset:        "high",
			Container:     "MKV",
			VideoCodec:    "x264",
			QualityPreset: "high",
			DrivePolicy:   "any",
			Options: map[string]any{
				"min_title_seconds":  300,
				"season":             1,
				"dvd_selection_mode": "per_title",
				"quality_rf":         18,
				"encoder_preset":     "slow",
			},
			OutputPathTemplate: `{{.Show}}/Season {{printf "%02d" .Season}}/{{.Show}} - S{{printf "%02d" .Season}}E{{printf "%02d" .EpisodeNumber}}{{if .EpisodeTitle}} - {{.EpisodeTitle}}{{end}}.mkv`,
			Enabled:            true,
			StepCount:          7,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := store.CreateProfile(ctx, p); err != nil {
			return err
		}
	}
	if !have[dvdMovieMakeMKVProfileName] {
		p := &state.Profile{
			DiscType:      state.DiscTypeDVD,
			Name:          dvdMovieMakeMKVProfileName,
			Engine:        "MakeMKV",
			Format:        "MKV",
			Preset:        "",
			Container:     "MKV",
			VideoCodec:    "copy",
			QualityPreset: "",
			DrivePolicy:   "any",
			Options: map[string]any{
				"dvd_selection_mode": "main_feature",
			},
			OutputPathTemplate: `{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv`,
			Enabled:            true,
			StepCount:          6,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := store.CreateProfile(ctx, p); err != nil {
			return err
		}
	}
	if !have[dvdMovieExtrasMakeMKVProfileName] {
		p := &state.Profile{
			DiscType:      state.DiscTypeDVD,
			Name:          dvdMovieExtrasMakeMKVProfileName,
			Engine:        "MakeMKV+HandBrake",
			Format:        "MKV",
			Preset:        "high",
			Container:     "MKV",
			VideoCodec:    "x265",
			QualityPreset: "high",
			DrivePolicy:   "any",
			Options: map[string]any{
				"dvd_selection_mode": "main_feature",
				"quality_rf":         20,
				"encoder_preset":     "slow",
				"include_extras":     true,
				"min_extra_seconds":  60,
				"extras_max_ratio":   90,
			},
			OutputPathTemplate: `{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv`,
			Enabled:            true,
			StepCount:          7,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := store.CreateProfile(ctx, p); err != nil {
			return err
		}
	}
	if !have[dvdSeriesMakeMKVProfileName] {
		p := &state.Profile{
			DiscType:      state.DiscTypeDVD,
			Name:          dvdSeriesMakeMKVProfileName,
			Engine:        "MakeMKV+HandBrake",
			Format:        "MKV",
			Preset:        "high",
			Container:     "MKV",
			VideoCodec:    "x265",
			QualityPreset: "high",
			DrivePolicy:   "any",
			Options: map[string]any{
				"min_title_seconds":  300,
				"season":             1,
				"dvd_selection_mode": "per_title",
				"quality_rf":         20,
				"encoder_preset":     "slow",
			},
			OutputPathTemplate: `{{.Show}}/Season {{printf "%02d" .Season}}/{{.Show}} - S{{printf "%02d" .Season}}E{{printf "%02d" .EpisodeNumber}}{{if .EpisodeTitle}} - {{.EpisodeTitle}}{{end}}.mkv`,
			Enabled:            true,
			StepCount:          7,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := store.CreateProfile(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

func seedProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeAudioCD)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == cdFlacProfileName {
			return nil
		}
	}
	now := time.Now()
	p := &state.Profile{
		DiscType:           state.DiscTypeAudioCD,
		Name:               cdFlacProfileName,
		Engine:             "whipper",
		Format:             "FLAC",
		Preset:             "",
		Container:          "FLAC",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `{{.Artist}}/{{.Album}} ({{.Year}})/{{printf "%02d" .TrackNumber}} - {{.Title}}.flac`,
		Enabled:            true,
		StepCount:          6,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	return store.CreateProfile(ctx, p)
}

// seedRetentionDefault sets retention.forever = "true" on first boot if not already set.
func seedRetentionDefault(ctx context.Context, store *state.Store) error {
	if v, err := store.GetSetting(ctx, "retention.forever"); err == nil && v != "" {
		return nil
	}
	return store.SetSetting(ctx, "retention.forever", "true")
}

func seedNotifications(ctx context.Context, store *state.Store, urls string) error {
	if urls == "" {
		return nil
	}
	existing, err := store.ListNotifications(ctx)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	for i, u := range strings.Split(urls, ",") {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		n := &state.Notification{
			Name:     fmt.Sprintf("env-%d", i+1),
			URL:      u,
			Triggers: "done,failed",
			Enabled:  true,
		}
		if err := store.CreateNotification(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// WriteMakeMKVBetaKey writes <dataDir>/settings.conf with
// app_Key = "<key>" and mirrors it into <dataDir>/.MakeMKV/settings.conf
// (the path makemkvcon actually loads on every invocation). Safe to
// call repeatedly — overwrites in place. Empty key is a no-op (returns
// nil without touching the filesystem) so callers can plug in an
// unset credential without a guard.
func WriteMakeMKVBetaKey(dataDir, key string) error {
	if key == "" {
		return nil
	}
	if dataDir == "" {
		return fmt.Errorf("makemkv data dir not set")
	}
	content := fmt.Sprintf("app_Key = %q\n", key)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dataDir, err)
	}
	confPath := filepath.Join(dataDir, "settings.conf")
	if err := os.WriteFile(confPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", confPath, err)
	}
	// makemkvcon always loads its config from `$HOME/.MakeMKV/settings.conf`
	// — a directory whose name starts with a dot. Our managed dataDir is
	// the user-friendly `MakeMKV/` (no dot) so KEYDB.cfg and friends are
	// visible to humans listing the appdata mount. The runtime image
	// invokes makemkvcon with `HOME=<parent>`, so we also write the
	// same settings.conf into `<parent>/.MakeMKV/`. v0.2.0 tried a
	// symlink, but makemkvcon's first scan had already materialised
	// `.MakeMKV/` as a real directory (it writes `_private_data.tar`
	// and `update.conf` there), and the symlink branch bailed out on
	// a non-symlink target — so the key never reached makemkvcon.
	// Writing the second copy is unconditional and idempotent.
	parent := filepath.Dir(dataDir)
	if parent == "" || parent == "." {
		return nil
	}
	dotDir := filepath.Join(parent, ".MakeMKV")
	if err := os.MkdirAll(dotDir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dotDir, err)
	}
	dotConf := filepath.Join(dotDir, "settings.conf")
	if err := os.WriteFile(dotConf, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", dotConf, err)
	}
	return nil
}

func seedBDMVProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeBDMV)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, p := range existing {
		have[p.Name] = true
	}
	now := time.Now()
	if !have[bdProfileName] {
		if err := store.CreateProfile(ctx, &state.Profile{
			DiscType:           state.DiscTypeBDMV,
			Name:               bdProfileName,
			Engine:             "MakeMKV+HandBrake",
			Format:             "MKV",
			Preset:             "high",
			Container:          "MKV",
			VideoCodec:         "x265",
			QualityPreset:      "high",
			HDRPipeline:        "passthrough",
			DrivePolicy:        "any",
			Options:            map[string]any{"min_title_seconds": 3600, "keep_all_tracks": false},
			OutputPathTemplate: `{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv`,
			Enabled:            true,
			StepCount:          7,
			CreatedAt:          now,
			UpdatedAt:          now,
		}); err != nil {
			return err
		}
	}
	if !have[bdExtrasProfileName] {
		if err := store.CreateProfile(ctx, &state.Profile{
			DiscType:      state.DiscTypeBDMV,
			Name:          bdExtrasProfileName,
			Engine:        "MakeMKV+HandBrake",
			Format:        "MKV",
			Preset:        "high",
			Container:     "MKV",
			VideoCodec:    "x265",
			QualityPreset: "high",
			HDRPipeline:   "passthrough",
			DrivePolicy:   "any",
			Options: map[string]any{
				"min_title_seconds": 3600,
				"keep_all_tracks":   false,
				"include_extras":    true,
				"min_extra_seconds": 60,
				"extras_max_ratio":  90,
			},
			OutputPathTemplate: `{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv`,
			Enabled:            true,
			StepCount:          7,
			CreatedAt:          now,
			UpdatedAt:          now,
		}); err != nil {
			return err
		}
	}
	return nil
}

// seedBlurayDDProfiles adds the user's personal Blu-Ray defaults:
// NVENC on the Quadro, archival-tier quality (RF 18 / "slower" —
// closes most of the gap with software x265 at NVENC's speed and
// concurrency), 1080p cap, every audio track kept but downmixed to
// 2.0 (Japanese + English etc. all survive, just no multichannel),
// and every text subtitle track pulled to a sidecar file so Jellyfin
// can switch tracks with nothing burned in. "DD" in the name marks
// these as the user's own tuned defaults, distinct from the generic
// BD-1080p/NVENC starter profiles.
func seedBlurayDDProfiles(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeBDMV)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, p := range existing {
		have[p.Name] = true
	}
	now := time.Now()
	if !have[blurayDDMovieProfileName] {
		if err := store.CreateProfile(ctx, &state.Profile{
			DiscType:      state.DiscTypeBDMV,
			Name:          blurayDDMovieProfileName,
			Engine:        "MakeMKV+HandBrake",
			Format:        "MKV",
			Preset:        "archival",
			Container:     "MKV",
			VideoCodec:    "nvenc_h265",
			QualityPreset: "archival",
			HDRPipeline:   "passthrough",
			DrivePolicy:   "any",
			Options: map[string]any{
				"content_type":           "movie",
				"min_title_seconds":      3600,
				"quality_rf":             18,
				"encoder_preset":         "slower",
				"max_height":             1080,
				"stereo_audio":           true,
				"extract_text_subtitles": true,
			},
			OutputPathTemplate: `{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv`,
			Enabled:            true,
			StepCount:          7,
			CreatedAt:          now,
			UpdatedAt:          now,
		}); err != nil {
			return err
		}
	}
	if !have[blurayDDTVProfileName] {
		if err := store.CreateProfile(ctx, &state.Profile{
			DiscType:      state.DiscTypeBDMV,
			Name:          blurayDDTVProfileName,
			Engine:        "MakeMKV+HandBrake",
			Format:        "MKV",
			Preset:        "archival",
			Container:     "MKV",
			VideoCodec:    "nvenc_h265",
			QualityPreset: "archival",
			HDRPipeline:   "passthrough",
			DrivePolicy:   "any",
			Options: map[string]any{
				"content_type":           "tv",
				"min_title_seconds":      600,
				"season":                 1,
				"show_title_picker":      true,
				"quality_rf":             18,
				"encoder_preset":         "slower",
				"max_height":             1080,
				"stereo_audio":           true,
				"extract_text_subtitles": true,
			},
			OutputPathTemplate: `{{.Show}}/Season {{printf "%02d" .Season}}/{{.Show}} - S{{printf "%02d" .Season}}E{{printf "%02d" .EpisodeNumber}}{{if .EpisodeTitle}} - {{.EpisodeTitle}}{{end}}.mkv`,
			Enabled:            true,
			StepCount:          7,
			CreatedAt:          now,
			UpdatedAt:          now,
		}); err != nil {
			return err
		}
	}
	return nil
}

// seedDVDDDProfiles is seedBlurayDDProfiles' DVD counterpart — same
// NVENC/archival/1080p/stereo/subtitle-sidecar defaults, routed
// through the DVD pipeline's MakeMKV+HandBrake engine instead of
// BDMV's.
func seedDVDDDProfiles(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeDVD)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, p := range existing {
		have[p.Name] = true
	}
	now := time.Now()
	if !have[dvdDDMovieProfileName] {
		if err := store.CreateProfile(ctx, &state.Profile{
			DiscType:      state.DiscTypeDVD,
			Name:          dvdDDMovieProfileName,
			Engine:        "MakeMKV+HandBrake",
			Format:        "MKV",
			Preset:        "archival",
			Container:     "MKV",
			VideoCodec:    "nvenc_h265",
			QualityPreset: "archival",
			DrivePolicy:   "any",
			Options: map[string]any{
				"content_type":           "movie",
				"dvd_selection_mode":     "main_feature",
				"quality_rf":             18,
				"encoder_preset":         "slower",
				"max_height":             1080,
				"stereo_audio":           true,
				"extract_text_subtitles": true,
			},
			OutputPathTemplate: `{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv`,
			Enabled:            true,
			StepCount:          7,
			CreatedAt:          now,
			UpdatedAt:          now,
		}); err != nil {
			return err
		}
	}
	if !have[dvdDDTVProfileName] {
		if err := store.CreateProfile(ctx, &state.Profile{
			DiscType:      state.DiscTypeDVD,
			Name:          dvdDDTVProfileName,
			Engine:        "MakeMKV+HandBrake",
			Format:        "MKV",
			Preset:        "archival",
			Container:     "MKV",
			VideoCodec:    "nvenc_h265",
			QualityPreset: "archival",
			DrivePolicy:   "any",
			Options: map[string]any{
				"content_type":           "tv",
				"dvd_selection_mode":     "per_title",
				"min_title_seconds":      600,
				"season":                 1,
				"quality_rf":             18,
				"encoder_preset":         "slower",
				"max_height":             1080,
				"stereo_audio":           true,
				"extract_text_subtitles": true,
			},
			OutputPathTemplate: `{{.Show}}/Season {{printf "%02d" .Season}}/{{.Show}} - S{{printf "%02d" .Season}}E{{printf "%02d" .EpisodeNumber}}{{if .EpisodeTitle}} - {{.EpisodeTitle}}{{end}}.mkv`,
			Enabled:            true,
			StepCount:          7,
			CreatedAt:          now,
			UpdatedAt:          now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func seedUHDProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeUHD)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == uhdProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypeUHD,
		Name:               uhdProfileName,
		Engine:             "MakeMKV",
		Format:             "MKV",
		Preset:             "",
		Container:          "MKV",
		VideoCodec:         "copy",
		QualityPreset:      "",
		HDRPipeline:        "passthrough",
		DrivePolicy:        "any",
		Options:            map[string]any{"min_title_seconds": 3600, "keep_all_tracks": true},
		OutputPathTemplate: `{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}) [UHD].mkv`,
		Enabled:            true,
		StepCount:          6,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedPSXProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypePSX)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == psxProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypePSX,
		Name:               psxProfileName,
		Engine:             "redumper+chdman",
		Format:             "CHD",
		Preset:             "",
		Container:          "CHD",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `PlayStation/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`,
		Enabled:            true,
		StepCount:          7,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedPS2Profile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypePS2)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == ps2ProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypePS2,
		Name:               ps2ProfileName,
		Engine:             "redumper+chdman",
		Format:             "CHD",
		Preset:             "",
		Container:          "CHD",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `PlayStation 2/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`,
		Enabled:            true,
		StepCount:          7,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedSaturnProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeSAT)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == saturnProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypeSAT,
		Name:               saturnProfileName,
		Engine:             "redumper+chdman",
		Format:             "CHD",
		Preset:             "",
		Container:          "CHD",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `Sega Saturn/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`,
		Enabled:            true,
		StepCount:          6,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedDCProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeDC)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == dcProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypeDC,
		Name:               dcProfileName,
		Engine:             "redumper+chdman",
		Format:             "CHD",
		Preset:             "",
		Container:          "CHD",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `Dreamcast/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`,
		Enabled:            true,
		StepCount:          6,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedXboxProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeXBOX)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == xboxProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypeXBOX,
		Name:               xboxProfileName,
		Engine:             "redumper",
		Format:             "ISO",
		Preset:             "",
		Container:          "ISO",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `Xbox/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).iso`,
		Enabled:            true,
		StepCount:          5,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedXbox360Profile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeXBOX360)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == xbox360ProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypeXBOX360,
		Name:               xbox360ProfileName,
		Engine:             "redumper",
		Format:             "ISO",
		Preset:             "",
		Container:          "ISO",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `Xbox 360/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).iso`,
		Enabled:            true,
		StepCount:          5,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedDataProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeData)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == dataProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypeData,
		Name:               dataProfileName,
		Engine:             "ddrescue",
		Format:             "ISO",
		Preset:             "",
		Container:          "ISO",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `{{.Title}}/{{.Title}} [{{.ShortHash}}].iso`,
		Enabled:            true,
		StepCount:          6,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

// seedVCDProfile creates the default Video CD profile. vcdxrip extracts
// the MPEG tracks as-is; there is no encode, so video_codec stays empty
// (vcdimager accepts no codec — same as the ddrescue data engine) and
// quality_preset is empty. OutputPathTemplate renders a per-title
// directory the handler drops avseqNN.mpg files into.
func seedVCDProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeVCD)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == vcdProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypeVCD,
		Name:               vcdProfileName,
		Engine:             "vcdimager",
		Format:             "MPEG",
		Preset:             "",
		Container:          "MPG",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `{{.Title}}`,
		Enabled:            true,
		StepCount:          6,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedSegaCDProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeSegaCD)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == segaCDProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypeSegaCD,
		Name:               segaCDProfileName,
		Engine:             "redumper+chdman",
		Format:             "CHD",
		Preset:             "",
		Container:          "CHD",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `Sega CD/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`,
		Enabled:            true,
		StepCount:          7,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seed3DOProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscType3DO)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == threeDOProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscType3DO,
		Name:               threeDOProfileName,
		Engine:             "redumper+chdman",
		Format:             "CHD",
		Preset:             "",
		Container:          "CHD",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `3DO/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`,
		Enabled:            true,
		StepCount:          7,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedPCFXProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypePCFX)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == pcfxProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypePCFX,
		Name:               pcfxProfileName,
		Engine:             "redumper+chdman",
		Format:             "CHD",
		Preset:             "",
		Container:          "CHD",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `PC-FX/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`,
		Enabled:            true,
		StepCount:          7,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedJaguarCDProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeJaguarCD)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == jaguarCDProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypeJaguarCD,
		Name:               jaguarCDProfileName,
		Engine:             "redumper+chdman",
		Format:             "CHD",
		Preset:             "",
		Container:          "CHD",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `Atari Jaguar CD/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`,
		Enabled:            true,
		StepCount:          7,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedCDiProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeCDi)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == cdiProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypeCDi,
		Name:               cdiProfileName,
		Engine:             "redumper+chdman",
		Format:             "CHD",
		Preset:             "",
		Container:          "CHD",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `Philips CD-i/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`,
		Enabled:            true,
		StepCount:          7,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedPCECDProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypePCECD)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == pceCDProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypePCECD,
		Name:               pceCDProfileName,
		Engine:             "redumper+chdman",
		Format:             "CHD",
		Preset:             "",
		Container:          "CHD",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `PC Engine CD/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`,
		Enabled:            true,
		StepCount:          7,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedNeoCDProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeNeoCD)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == neoCDProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypeNeoCD,
		Name:               neoCDProfileName,
		Engine:             "redumper+chdman",
		Format:             "CHD",
		Preset:             "",
		Container:          "CHD",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `Neo Geo CD/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`,
		Enabled:            true,
		StepCount:          7,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedCD32Profile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeCD32)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == cd32ProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypeCD32,
		Name:               cd32ProfileName,
		Engine:             "redumper+chdman",
		Format:             "CHD",
		Preset:             "",
		Container:          "CHD",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `Amiga CD32/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`,
		Enabled:            true,
		StepCount:          7,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedFMTownsProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypeFMTowns)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == fmTownsProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypeFMTowns,
		Name:               fmTownsProfileName,
		Engine:             "redumper+chdman",
		Format:             "CHD",
		Preset:             "",
		Container:          "CHD",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `FM Towns/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`,
		Enabled:            true,
		StepCount:          7,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func seedPippinProfile(ctx context.Context, store *state.Store) error {
	existing, err := store.ListProfilesByDiscType(ctx, state.DiscTypePippin)
	if err != nil {
		return err
	}
	for _, p := range existing {
		if p.Name == pippinProfileName {
			return nil
		}
	}
	now := time.Now()
	return store.CreateProfile(ctx, &state.Profile{
		DiscType:           state.DiscTypePippin,
		Name:               pippinProfileName,
		Engine:             "redumper+chdman",
		Format:             "CHD",
		Preset:             "",
		Container:          "CHD",
		QualityPreset:      "",
		DrivePolicy:        "any",
		Options:            map[string]any{},
		OutputPathTemplate: `Bandai Pippin/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`,
		Enabled:            true,
		StepCount:          7,
		CreatedAt:          now,
		UpdatedAt:          now,
	})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
