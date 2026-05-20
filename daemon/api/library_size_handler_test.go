package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/settings"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

func newTestStore(t *testing.T) *state.Store {
	t.Helper()
	db, err := state.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return state.NewStore(db)
}

func TestGetSystemLibraries_SortedWithArraySummary(t *testing.T) {
	root := t.TempDir()
	movies := filepath.Join(root, "movies")
	tv := filepath.Join(root, "tv")
	writeFile(t, movies, "big.mkv", 2000)
	writeFile(t, tv, "ep.mkv", 500)

	store := newTestStore(t)
	// libraryFSBytes (array summary) reads roots from the settings table.
	for k, v := range map[string]string{"library.movies": movies, "library.tv": tv} {
		if err := store.SetSetting(context.Background(), k, v); err != nil {
			t.Fatal(err)
		}
	}

	ls := NewLibrarySizer(&settings.Settings{LibraryMovies: movies, LibraryTV: tv})
	ls.walk()
	h := &Handlers{Store: store, LibrarySizer: ls}

	rec := httptest.NewRecorder()
	h.GetSystemLibraries(rec, httptest.NewRequest(http.MethodGet, "/api/system/libraries", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var info LibrariesInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if len(info.Libraries) != len(settings.AllMediaRoots) {
		t.Fatalf("expected %d rows, got %d", len(settings.AllMediaRoots), len(info.Libraries))
	}
	// Sorted descending by bytes: movies (2000) before tv (500).
	if info.Libraries[0].Media != "movies" || info.Libraries[1].Media != "tv" {
		t.Errorf("expected movies then tv at the top, got %q then %q",
			info.Libraries[0].Media, info.Libraries[1].Media)
	}
	if info.MeasuredAt == "" {
		t.Error("expected measured_at to be populated after a walk")
	}
	if info.Array.TotalBytes <= 0 {
		t.Errorf("expected a non-zero array total, got %d", info.Array.TotalBytes)
	}
}

func TestGetSystemLibraries_NilSizerEmpty(t *testing.T) {
	h := &Handlers{Store: newTestStore(t)}
	rec := httptest.NewRecorder()
	h.GetSystemLibraries(rec, httptest.NewRequest(http.MethodGet, "/api/system/libraries", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var info LibrariesInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if len(info.Libraries) != 0 {
		t.Errorf("nil sizer should yield no libraries, got %d", len(info.Libraries))
	}
}

func TestRecalcSystemLibraries_OK(t *testing.T) {
	h := &Handlers{
		Store:        newTestStore(t),
		LibrarySizer: NewLibrarySizer(&settings.Settings{LibraryMovies: t.TempDir()}),
	}
	rec := httptest.NewRecorder()
	h.RecalcSystemLibraries(rec, httptest.NewRequest(http.MethodPost, "/api/system/libraries/recalc", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
