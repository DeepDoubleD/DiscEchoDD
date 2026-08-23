package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jumpingmushroom/DiscEcho/daemon/api"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

func TestListDrives_ReturnsSeeded(t *testing.T) {
	h := apitestServer(t)
	seedDrive(t, h)

	req := httptest.NewRequest(http.MethodGet, "/api/drives", nil)
	w := httptest.NewRecorder()
	h.ListDrives(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body []state.Drive
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || body[0].DevPath != "/dev/sr0" {
		t.Errorf("got %+v", body)
	}
}

func TestGetDrive_NotFound(t *testing.T) {
	h := apitestServer(t)
	r := chi.NewRouter()
	r.Get("/api/drives/{id}", h.GetDrive)

	req := httptest.NewRequest(http.MethodGet, "/api/drives/nope", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status %d", w.Code)
	}
}

func TestGetDrive_OK(t *testing.T) {
	h := apitestServer(t)
	d := seedDrive(t, h)
	r := chi.NewRouter()
	r.Get("/api/drives/{id}", h.GetDrive)

	req := httptest.NewRequest(http.MethodGet, "/api/drives/"+d.ID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var got state.Drive
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != d.ID {
		t.Errorf("got %s want %s", got.ID, d.ID)
	}
}

func TestEjectDrive_FiresEjectorAndReturnsToIdle(t *testing.T) {
	h := apitestServer(t)
	d := seedDrive(t, h)

	called := false
	gotDev := ""
	h.Ejector = func(_ context.Context, dev string) error {
		called = true
		gotDev = dev
		return nil
	}

	r := chi.NewRouter()
	r.Post("/api/drives/{id}/eject", h.EjectDrive)

	req := httptest.NewRequest(http.MethodPost, "/api/drives/"+d.ID+"/eject", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d", w.Code)
	}
	if !called {
		t.Fatal("ejector not called")
	}
	if gotDev != d.DevPath {
		t.Errorf("ejector got dev %q want %q", gotDev, d.DevPath)
	}
	got, err := h.Store.GetDrive(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.DriveStateIdle {
		t.Errorf("post-eject state %s want idle", got.State)
	}
}

func TestEjectDrive_NoEjectorReturns503(t *testing.T) {
	h := apitestServer(t)
	d := seedDrive(t, h)
	// h.Ejector is nil by default in apitestServer.
	r := chi.NewRouter()
	r.Post("/api/drives/{id}/eject", h.EjectDrive)

	req := httptest.NewRequest(http.MethodPost, "/api/drives/"+d.ID+"/eject", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d want 503", w.Code)
	}
}

func TestEjectDrive_EjectorFailureRestoresIdle(t *testing.T) {
	h := apitestServer(t)
	d := seedDrive(t, h)
	h.Ejector = func(_ context.Context, _ string) error { return errors.New("boom") }
	r := chi.NewRouter()
	r.Post("/api/drives/{id}/eject", h.EjectDrive)

	req := httptest.NewRequest(http.MethodPost, "/api/drives/"+d.ID+"/eject", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status %d want 500", w.Code)
	}
	got, _ := h.Store.GetDrive(context.Background(), d.ID)
	if got.State != state.DriveStateIdle {
		t.Errorf("post-failed-eject state %s want idle", got.State)
	}
}

func TestEjectDrive_DropsOrphanDiscBoundToDrive(t *testing.T) {
	h := apitestServer(t)
	d := seedDrive(t, h)
	disc := seedDisc(t, h, d.ID) // no jobs → orphan
	h.Ejector = func(_ context.Context, _ string) error { return nil }

	// Subscribe before issuing the request to catch the disc.deleted event.
	ch, cancel := h.Broadcaster.Subscribe(8)
	defer cancel()

	r := chi.NewRouter()
	r.Post("/api/drives/{id}/eject", h.EjectDrive)
	req := httptest.NewRequest(http.MethodPost, "/api/drives/"+d.ID+"/eject", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d", w.Code)
	}

	// Disc row gone.
	if _, err := h.Store.GetDisc(context.Background(), disc.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("disc still present after eject: err=%v", err)
	}

	// disc.deleted broadcast received before drive.changed.
	got := drainBroadcast(t, ch, 2)
	var sawDelete bool
	for _, ev := range got {
		if ev.Name == "disc.deleted" {
			p, _ := ev.Payload.(map[string]any)
			if p["disc_id"] != disc.ID {
				t.Errorf("disc.deleted payload: got %+v, want disc_id=%s", p, disc.ID)
			}
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("no disc.deleted event broadcast; got %+v", got)
	}
}

// TestEjectDrive_ClearsFailedJobDisc reproduces the live bug this fixes:
// a disc whose only job failed used to survive Eject forever, leaving
// the dashboard "asking for a decision" for a drive that's now empty.
// Eject must now clear it, same as a truly jobless disc.
func TestEjectDrive_ClearsFailedJobDisc(t *testing.T) {
	h := apitestServer(t)
	d := seedDrive(t, h)
	p := seedProfile(t, h)
	disc := seedDisc(t, h, d.ID)
	if err := h.Store.CreateJob(context.Background(), &state.Job{
		DiscID: disc.ID, DriveID: d.ID, ProfileID: p.ID,
		State: state.JobStateFailed,
	}); err != nil {
		t.Fatal(err)
	}
	h.Ejector = func(_ context.Context, _ string) error { return nil }

	ch, cancel := h.Broadcaster.Subscribe(8)
	defer cancel()

	r := chi.NewRouter()
	r.Post("/api/drives/{id}/eject", h.EjectDrive)
	req := httptest.NewRequest(http.MethodPost, "/api/drives/"+d.ID+"/eject", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d", w.Code)
	}

	if _, err := h.Store.GetDisc(context.Background(), disc.ID); err == nil {
		t.Errorf("disc with failed job should be cleared on eject")
	}

	var sawDelete bool
	for _, ev := range drainBroadcast(t, ch, 2) {
		if ev.Name == "disc.deleted" {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Errorf("want disc.deleted broadcast for cleared failed-job disc")
	}
}

// TestEjectDrive_KeepsDoneDisc: a disc whose job succeeded is real
// completed work, not a dead-end prompt -- Eject must leave it alone.
func TestEjectDrive_KeepsDoneDisc(t *testing.T) {
	h := apitestServer(t)
	d := seedDrive(t, h)
	p := seedProfile(t, h)
	disc := seedDisc(t, h, d.ID)
	if err := h.Store.CreateJob(context.Background(), &state.Job{
		DiscID: disc.ID, DriveID: d.ID, ProfileID: p.ID,
		State: state.JobStateDone,
	}); err != nil {
		t.Fatal(err)
	}
	h.Ejector = func(_ context.Context, _ string) error { return nil }

	ch, cancel := h.Broadcaster.Subscribe(8)
	defer cancel()

	r := chi.NewRouter()
	r.Post("/api/drives/{id}/eject", h.EjectDrive)
	req := httptest.NewRequest(http.MethodPost, "/api/drives/"+d.ID+"/eject", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d", w.Code)
	}

	if _, err := h.Store.GetDisc(context.Background(), disc.ID); err != nil {
		t.Errorf("disc with done job should be kept; got err=%v", err)
	}
	for _, ev := range drainBroadcast(t, ch, 1) {
		if ev.Name == "disc.deleted" {
			t.Errorf("unexpected disc.deleted broadcast for done disc")
		}
	}
}

// drainBroadcast collects up to `max` events from ch with a short
// settling window. Returns whatever it gathered.
func drainBroadcast(t *testing.T, ch <-chan state.Event, max int) []state.Event {
	t.Helper()
	var out []state.Event
	deadline := time.After(200 * time.Millisecond)
	for len(out) < max {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-deadline:
			return out
		}
	}
	return out
}

func TestEjectDrive_NotFound(t *testing.T) {
	h := apitestServer(t)
	h.Ejector = func(_ context.Context, _ string) error { return nil }
	r := chi.NewRouter()
	r.Post("/api/drives/{id}/eject", h.EjectDrive)

	req := httptest.NewRequest(http.MethodPost, "/api/drives/nope/eject", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status %d", w.Code)
	}
}

// Compile-time use of the api package import for the Ejector type when
// the file would otherwise not need it directly.
var _ api.Ejector = func(context.Context, string) error { return nil }

func TestPatchDriveOffset_HappyPath(t *testing.T) {
	h := apitestServer(t)
	d := seedDrive(t, h)
	r := chi.NewRouter()
	r.Patch("/api/drives/{id}/offset", h.PatchDriveOffset)

	req := httptest.NewRequest(http.MethodPatch, "/api/drives/"+d.ID+"/offset",
		strings.NewReader(`{"read_offset": 667}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", w.Code, w.Body.String())
	}
	var got state.Drive
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ReadOffset != 667 || got.ReadOffsetSource != "manual" {
		t.Errorf("got offset=%d source=%q want 667/manual", got.ReadOffset, got.ReadOffsetSource)
	}
}

func TestPatchDriveOffset_NegativeRoundTrips(t *testing.T) {
	h := apitestServer(t)
	d := seedDrive(t, h)
	r := chi.NewRouter()
	r.Patch("/api/drives/{id}/offset", h.PatchDriveOffset)

	req := httptest.NewRequest(http.MethodPatch, "/api/drives/"+d.ID+"/offset",
		strings.NewReader(`{"read_offset": -1164}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	stored, _ := h.Store.GetDrive(context.Background(), d.ID)
	if stored.ReadOffset != -1164 {
		t.Errorf("stored offset: want -1164, got %d", stored.ReadOffset)
	}
}

func TestPatchDriveOffset_OutOfRange(t *testing.T) {
	h := apitestServer(t)
	d := seedDrive(t, h)
	r := chi.NewRouter()
	r.Patch("/api/drives/{id}/offset", h.PatchDriveOffset)

	for _, body := range []string{
		`{"read_offset": 9001}`,
		`{"read_offset": -9001}`,
	} {
		req := httptest.NewRequest(http.MethodPatch, "/api/drives/"+d.ID+"/offset",
			strings.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("body %s: status %d want 422", body, w.Code)
		}
	}
}

func TestPatchDriveOffset_MissingField(t *testing.T) {
	h := apitestServer(t)
	d := seedDrive(t, h)
	r := chi.NewRouter()
	r.Patch("/api/drives/{id}/offset", h.PatchDriveOffset)

	req := httptest.NewRequest(http.MethodPatch, "/api/drives/"+d.ID+"/offset",
		strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status %d want 422", w.Code)
	}
}

func TestPatchDriveOffset_InvalidJSON(t *testing.T) {
	h := apitestServer(t)
	d := seedDrive(t, h)
	r := chi.NewRouter()
	r.Patch("/api/drives/{id}/offset", h.PatchDriveOffset)

	req := httptest.NewRequest(http.MethodPatch, "/api/drives/"+d.ID+"/offset",
		strings.NewReader(`{not-json`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d want 400", w.Code)
	}
}

func TestPatchDriveOffset_NotFound(t *testing.T) {
	h := apitestServer(t)
	r := chi.NewRouter()
	r.Patch("/api/drives/{id}/offset", h.PatchDriveOffset)

	req := httptest.NewRequest(http.MethodPatch, "/api/drives/ghost/offset",
		strings.NewReader(`{"read_offset": 0}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status %d want 404", w.Code)
	}
}

func TestPatchDriveOffset_ConflictWhenActiveJob(t *testing.T) {
	h := apitestServer(t)
	d := seedDrive(t, h)
	p := seedProfile(t, h)
	disc := seedDisc(t, h, d.ID)
	if err := h.Store.CreateJob(context.Background(), &state.Job{
		ID:        "job-active",
		DiscID:    disc.ID,
		DriveID:   d.ID,
		ProfileID: p.ID,
		State:     state.JobStateRunning,
	}); err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	r.Patch("/api/drives/{id}/offset", h.PatchDriveOffset)
	req := httptest.NewRequest(http.MethodPatch, "/api/drives/"+d.ID+"/offset",
		strings.NewReader(`{"read_offset": 0}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("status %d want 409", w.Code)
	}
}
