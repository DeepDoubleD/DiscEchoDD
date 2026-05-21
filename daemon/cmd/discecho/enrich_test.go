package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

type stubIGDBEnrich struct {
	cands      []state.Candidate
	details    *identify.IGDBGameDetails
	configured bool
}

func (s *stubIGDBEnrich) SearchGames(context.Context, string, state.DiscType) ([]state.Candidate, error) {
	return s.cands, nil
}
func (s *stubIGDBEnrich) GameDetails(context.Context, int) (*identify.IGDBGameDetails, error) {
	return s.details, nil
}
func (s *stubIGDBEnrich) Configured() bool                { return s.configured }
func (s *stubIGDBEnrich) Reconfigure(identify.IGDBConfig) {}

func newEnrichTestStore(t *testing.T) *state.Store {
	t.Helper()
	dir := t.TempDir()
	db, err := state.Open(filepath.Join(dir, "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return state.NewStore(db)
}

func TestEnrichGameDiscFromIGDB_AppliesCoverAndMetadata(t *testing.T) {
	store := newEnrichTestStore(t)
	ctx := context.Background()
	drv := &state.Drive{DevPath: "/dev/sr0", Model: "X", Bus: "Y", State: state.DriveStateIdle, LastSeenAt: time.Now()}
	if err := store.UpsertDrive(ctx, drv); err != nil {
		t.Fatal(err)
	}
	disc := &state.Disc{
		Type: state.DiscTypePSX, DriveID: drv.ID, Title: "Gran Turismo",
		MetadataProvider: "DuckStation gamedb.yaml", MetadataID: "SCES_009.84",
		MetadataJSON: `{"system":"Sony PlayStation","serial":"SCES_009.84"}`,
	}
	if err := store.CreateDisc(ctx, disc); err != nil {
		t.Fatal(err)
	}

	df := &discFlow{
		store: store,
		bc:    state.NewBroadcaster(),
		igdb: &stubIGDBEnrich{
			configured: true,
			cands:      []state.Candidate{{Source: "IGDB", Title: "Gran Turismo", IGDBID: 999}},
			details: &identify.IGDBGameDetails{
				ID: 999, Name: "Gran Turismo", Year: 1997,
				CoverURL:  "https://images.igdb.com/cover.jpg",
				Summary:   "A racing sim.",
				Genres:    []string{"Racing", "Simulator"},
				Platforms: []string{"PlayStation"},
			},
		},
	}

	df.enrichGameDiscFromIGDB(disc.ID, disc.Type, disc.Title)

	got, err := store.GetDisc(ctx, disc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MetadataID != "SCES_009.84" {
		t.Errorf("metadata_id changed to %q (identity must be untouched)", got.MetadataID)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(got.MetadataJSON), &m); err != nil {
		t.Fatal(err)
	}
	if m["cover_url"] != "https://images.igdb.com/cover.jpg" {
		t.Errorf("cover_url = %v", m["cover_url"])
	}
	if m["summary"] != "A racing sim." {
		t.Errorf("summary = %v", m["summary"])
	}
	if m["serial"] != "SCES_009.84" {
		t.Errorf("serial lost: %v", m["serial"])
	}
}

func TestEnrichGameDiscFromIGDB_NotConfigured_NoOp(t *testing.T) {
	store := newEnrichTestStore(t)
	ctx := context.Background()
	drv := &state.Drive{DevPath: "/dev/sr0", Model: "X", Bus: "Y", State: state.DriveStateIdle, LastSeenAt: time.Now()}
	if err := store.UpsertDrive(ctx, drv); err != nil {
		t.Fatal(err)
	}
	disc := &state.Disc{Type: state.DiscTypePSX, DriveID: drv.ID, Title: "Gran Turismo", MetadataJSON: `{"serial":"X"}`}
	if err := store.CreateDisc(ctx, disc); err != nil {
		t.Fatal(err)
	}

	df := &discFlow{store: store, bc: state.NewBroadcaster(), igdb: &stubIGDBEnrich{configured: false}}
	df.enrichGameDiscFromIGDB(disc.ID, disc.Type, disc.Title)

	got, _ := store.GetDisc(ctx, disc.ID)
	if got.MetadataJSON != `{"serial":"X"}` {
		t.Errorf("metadata_json changed despite unconfigured IGDB: %q", got.MetadataJSON)
	}
}

func TestBestIGDBMatch_AcceptsAboveThreshold(t *testing.T) {
	cands := []state.Candidate{
		{Source: "IGDB", Title: "Some Other Game", IGDBID: 1},
		{Source: "IGDB", Title: "Gran Turismo", IGDBID: 2},
	}
	got, ok := bestIGDBMatch(cands, "Gran Turismo")
	if !ok {
		t.Fatal("expected a match above threshold")
	}
	if got.IGDBID != 2 {
		t.Errorf("IGDBID = %d, want 2", got.IGDBID)
	}
}

func TestBestIGDBMatch_RejectsBelowThreshold(t *testing.T) {
	cands := []state.Candidate{
		{Source: "IGDB", Title: "Completely Unrelated Title", IGDBID: 9},
	}
	if _, ok := bestIGDBMatch(cands, "Gran Turismo"); ok {
		t.Error("expected no match below the similarity gate")
	}
}

func TestBestIGDBMatch_EmptyCandidates(t *testing.T) {
	if _, ok := bestIGDBMatch(nil, "Gran Turismo"); ok {
		t.Error("expected no match for empty candidates")
	}
}

func TestMergeGameMetadata_FillsCoverWhenAbsent(t *testing.T) {
	d := &identify.IGDBGameDetails{CoverURL: "https://img/cover.jpg", Summary: "Race.", Year: 1997}
	merged, changed, err := mergeGameMetadata(`{"system":"Sony PlayStation","serial":"SCES_009.84"}`, d)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(merged), &m); err != nil {
		t.Fatal(err)
	}
	if m["cover_url"] != "https://img/cover.jpg" {
		t.Errorf("cover_url = %v", m["cover_url"])
	}
	if m["serial"] != "SCES_009.84" || m["system"] != "Sony PlayStation" {
		t.Errorf("identity fields lost: %v", m)
	}
	if m["summary"] != "Race." {
		t.Errorf("summary = %v", m["summary"])
	}
	if m["release_year"] != float64(1997) {
		t.Errorf("release_year = %v", m["release_year"])
	}
}

func TestMergeGameMetadata_PreservesExistingCover(t *testing.T) {
	d := &identify.IGDBGameDetails{CoverURL: "https://igdb/new.jpg"}
	merged, _, err := mergeGameMetadata(`{"cover_url":"https://gamedb/exact.jpg"}`, d)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(merged), &m)
	if m["cover_url"] != "https://gamedb/exact.jpg" {
		t.Errorf("existing gamedb cover must win, got %v", m["cover_url"])
	}
}

func TestMergeGameMetadata_AddsGenresAndPlatforms(t *testing.T) {
	d := &identify.IGDBGameDetails{Genres: []string{"Racing"}, Platforms: []string{"PlayStation"}}
	merged, changed, err := mergeGameMetadata("", d)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var m struct {
		Genres    []string `json:"genres"`
		Platforms []string `json:"platforms"`
	}
	_ = json.Unmarshal([]byte(merged), &m)
	if len(m.Genres) != 1 || m.Genres[0] != "Racing" {
		t.Errorf("genres = %v", m.Genres)
	}
	if len(m.Platforms) != 1 || m.Platforms[0] != "PlayStation" {
		t.Errorf("platforms = %v", m.Platforms)
	}
}

func TestMergeGameMetadata_NoIGDBData_NoChange(t *testing.T) {
	d := &identify.IGDBGameDetails{}
	merged, changed, err := mergeGameMetadata(`{"serial":"X"}`, d)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Errorf("expected no change, got %q", merged)
	}
}
