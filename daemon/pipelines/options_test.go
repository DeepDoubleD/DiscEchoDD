package pipelines_test

import (
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

func TestIntOption(t *testing.T) {
	cases := []struct {
		name string
		opts map[string]any
		want int
	}{
		{"missing key → default", map[string]any{}, 99},
		{"nil options → default", nil, 99},
		{"int value", map[string]any{"k": 18}, 18},
		{"float64 value (JSON-decoded)", map[string]any{"k": float64(20)}, 20},
		{"int64 value", map[string]any{"k": int64(21)}, 21},
		{"wrong type → default", map[string]any{"k": "nope"}, 99},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pipelines.IntOption(&state.Profile{Options: c.opts}, "k", 99)
			if got != c.want {
				t.Errorf("IntOption = %d, want %d", got, c.want)
			}
		})
	}
	if got := pipelines.IntOption(nil, "k", 7); got != 7 {
		t.Errorf("nil profile: got %d, want 7", got)
	}
}

func TestStringOption(t *testing.T) {
	cases := []struct {
		name string
		opts map[string]any
		want string
	}{
		{"missing key → default", map[string]any{}, "def"},
		{"nil options → default", nil, "def"},
		{"string value", map[string]any{"k": "slow"}, "slow"},
		{"empty string → default", map[string]any{"k": ""}, "def"},
		{"wrong type → default", map[string]any{"k": 5}, "def"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pipelines.StringOption(&state.Profile{Options: c.opts}, "k", "def")
			if got != c.want {
				t.Errorf("StringOption = %q, want %q", got, c.want)
			}
		})
	}
	if got := pipelines.StringOption(nil, "k", "def"); got != "def" {
		t.Errorf("nil profile: got %q, want def", got)
	}
}

func TestIsTVProfile(t *testing.T) {
	cases := []struct {
		name string
		opts map[string]any
		want bool
	}{
		{"unset → movie", map[string]any{}, false},
		{"content_type movie", map[string]any{"content_type": "movie"}, false},
		{"content_type tv", map[string]any{"content_type": "tv"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pipelines.IsTVProfile(&state.Profile{Options: c.opts})
			if got != c.want {
				t.Errorf("IsTVProfile = %v, want %v", got, c.want)
			}
		})
	}
}

func TestLibraryRootFor(t *testing.T) {
	movie := &state.Profile{Options: map[string]any{"content_type": "movie"}}
	tv := &state.Profile{Options: map[string]any{"content_type": "tv"}}
	const kidsRoot, animeRoot = "/library/kids-cartoons", "/library/anime"

	if got := pipelines.LibraryRootFor("/library/movies", "/library/tv", kidsRoot, animeRoot, nil, movie); got != "/library/movies" {
		t.Errorf("movie profile: got %q, want /library/movies", got)
	}
	if got := pipelines.LibraryRootFor("/library/movies", "/library/tv", kidsRoot, animeRoot, nil, tv); got != "/library/tv" {
		t.Errorf("tv profile: got %q, want /library/tv", got)
	}
	// A deployment that hasn't configured LibraryTV must still work --
	// fall back to the movies root rather than writing to "".
	if got := pipelines.LibraryRootFor("/library/movies", "", kidsRoot, animeRoot, nil, tv); got != "/library/movies" {
		t.Errorf("tv profile, unconfigured TV root: got %q, want fallback to /library/movies", got)
	}

	kidsDisc := &state.Disc{MetadataJSON: `{"selected_category":"kids_cartoons"}`}
	animeDisc := &state.Disc{MetadataJSON: `{"selected_category":"anime"}`}
	bogusDisc := &state.Disc{MetadataJSON: `{"selected_category":"not_a_real_category"}`}

	if got := pipelines.LibraryRootFor("/library/movies", "/library/tv", kidsRoot, animeRoot, kidsDisc, movie); got != kidsRoot {
		t.Errorf("kids_cartoons override, movie profile: got %q, want %q", got, kidsRoot)
	}
	if got := pipelines.LibraryRootFor("/library/movies", "/library/tv", kidsRoot, animeRoot, kidsDisc, tv); got != kidsRoot {
		t.Errorf("kids_cartoons override, tv profile: got %q, want %q", got, kidsRoot)
	}
	if got := pipelines.LibraryRootFor("/library/movies", "/library/tv", kidsRoot, animeRoot, animeDisc, movie); got != animeRoot {
		t.Errorf("anime override, movie profile: got %q, want %q", got, animeRoot)
	}
	// An unrecognized category value must never silently misroute --
	// falls through to the normal movie/TV split.
	if got := pipelines.LibraryRootFor("/library/movies", "/library/tv", kidsRoot, animeRoot, bogusDisc, movie); got != "/library/movies" {
		t.Errorf("bogus category, movie profile: got %q, want /library/movies", got)
	}
	// A category override with the corresponding root left unconfigured
	// must still fall back to the normal movie/TV split rather than "".
	if got := pipelines.LibraryRootFor("/library/movies", "/library/tv", "", "", kidsDisc, movie); got != "/library/movies" {
		t.Errorf("kids_cartoons override, unconfigured kids root: got %q, want fallback to /library/movies", got)
	}
}

func TestMaxHeightFromProfile(t *testing.T) {
	cases := []struct {
		name string
		opts map[string]any
		want int
	}{
		{"unset → 0 (no cap)", map[string]any{}, 0},
		{"1080", map[string]any{"max_height": float64(1080)}, 1080},
		{"zero → 0", map[string]any{"max_height": float64(0)}, 0},
		{"negative → 0", map[string]any{"max_height": float64(-5)}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pipelines.MaxHeightFromProfile(&state.Profile{Options: c.opts})
			if got != c.want {
				t.Errorf("MaxHeightFromProfile = %d, want %d", got, c.want)
			}
		})
	}
}

func TestResolutionAndAudioArgs(t *testing.T) {
	t.Run("neither option set → no args", func(t *testing.T) {
		got := pipelines.ResolutionAndAudioArgs(&state.Profile{})
		if len(got) != 0 {
			t.Errorf("want no args, got %v", got)
		}
	})

	t.Run("max_height only", func(t *testing.T) {
		prof := &state.Profile{Options: map[string]any{"max_height": float64(1080)}}
		got := pipelines.ResolutionAndAudioArgs(prof)
		want := []string{"--maxHeight", "1080", "--maxWidth", "1920"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("arg %d: got %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("stereo_audio only", func(t *testing.T) {
		prof := &state.Profile{Options: map[string]any{"stereo_audio": true}}
		got := pipelines.ResolutionAndAudioArgs(prof)
		want := []string{"--aencoder", "av_aac", "--mixdown", "stereo", "--ab", "160"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("arg %d: got %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("both set, resolution args come first", func(t *testing.T) {
		prof := &state.Profile{Options: map[string]any{
			"max_height":   float64(1080),
			"stereo_audio": true,
		}}
		got := pipelines.ResolutionAndAudioArgs(prof)
		if len(got) != 10 {
			t.Fatalf("want 10 args, got %d: %v", len(got), got)
		}
		if got[0] != "--maxHeight" || got[4] != "--aencoder" {
			t.Errorf("unexpected arg order: %v", got)
		}
	})
}
