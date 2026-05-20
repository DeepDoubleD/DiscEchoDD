package api

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/settings"
)

// writeFile creates a file of n bytes under dir/name (creating dir).
func writeFile(t *testing.T, dir, name string, n int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMeasureLibraries_PerRootSizes(t *testing.T) {
	root := t.TempDir()
	movies := filepath.Join(root, "movies")
	tv := filepath.Join(root, "tv")
	writeFile(t, movies, "a.mkv", 1000)
	writeFile(t, filepath.Join(movies, "sub"), "b.mkv", 500) // nested file counts
	writeFile(t, tv, "ep.mkv", 250)
	// music root left absent on disk → Exists:false.

	s := &settings.Settings{
		LibraryMovies: movies,
		LibraryTV:     tv,
		LibraryMusic:  filepath.Join(root, "music"), // does not exist
	}

	got := measureLibraries(s)
	by := map[string]LibrarySize{}
	for _, l := range got {
		by[l.Media] = l
	}

	if by["movies"].Bytes != 1500 || !by["movies"].Exists {
		t.Errorf("movies = %+v, want 1500 bytes exists", by["movies"])
	}
	if by["tv"].Bytes != 250 || !by["tv"].Exists {
		t.Errorf("tv = %+v, want 250 bytes exists", by["tv"])
	}
	if by["music"].Exists || by["music"].Bytes != 0 {
		t.Errorf("missing music root should be exists=false bytes=0, got %+v", by["music"])
	}
	if len(got) != len(settings.AllMediaRoots) {
		t.Errorf("expected a row per media root (%d), got %d", len(settings.AllMediaRoots), len(got))
	}
}

func TestMeasureLibraries_DedupesIdenticalPaths(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	writeFile(t, shared, "x.bin", 4096)

	// movies and tv point at the exact same directory: each row reports the
	// dir size (no double-walk, no zeroing of the second row).
	s := &settings.Settings{LibraryMovies: shared, LibraryTV: shared}
	got := measureLibraries(s)
	by := map[string]LibrarySize{}
	for _, l := range got {
		by[l.Media] = l
	}
	if by["movies"].Bytes != 4096 || by["tv"].Bytes != 4096 {
		t.Errorf("identical-path rows should both report 4096, got movies=%d tv=%d", by["movies"].Bytes, by["tv"].Bytes)
	}
}

// TestLibrarySizer_RecalcConcurrent exercises the single-walk guard: a
// burst of Recalc calls plus reads must not race (run under -race).
func TestLibrarySizer_RecalcConcurrent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "movies"), "a", 10)
	ls := NewLibrarySizer(&settings.Settings{LibraryMovies: filepath.Join(root, "movies")})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ls.Recalc()
			_, _, _ = ls.Snapshot()
		}()
	}
	wg.Wait()
	// Force a synchronous walk so the snapshot is populated deterministically.
	ls.walk()
	sizes, measuredAt, _ := ls.Snapshot()
	if measuredAt.IsZero() {
		t.Fatal("expected measuredAt to be set after walk")
	}
	if len(sizes) != len(settings.AllMediaRoots) {
		t.Fatalf("expected %d rows, got %d", len(settings.AllMediaRoots), len(sizes))
	}
}
