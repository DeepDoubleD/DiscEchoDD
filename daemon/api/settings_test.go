package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetSettings_ReturnsKV(t *testing.T) {
	h := apitestServer(t)
	if err := h.Store.SetSetting(context.Background(), "library_path", "/srv/lib"); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.SetSetting(context.Background(), "default_audiocd_profile", "p1"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()
	h.GetSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["library_path"] != "/srv/lib" || got["default_audiocd_profile"] != "p1" {
		t.Errorf("got %+v", got)
	}
}

func TestSettings_Put_RetentionValid(t *testing.T) {
	h := apitestServer(t)
	body := mustMarshal(t, map[string]any{
		"retention.forever": false, "retention.success.days": 30, "retention.failed.count": 50,
	})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	if v, _ := h.Store.GetSetting(context.Background(), "retention.success.days"); v != "30" {
		t.Fatalf("success.days: %q", v)
	}
	if v, _ := h.Store.GetSetting(context.Background(), "retention.failed.count"); v != "50" {
		t.Fatalf("failed.count: %q", v)
	}
	if v, _ := h.Store.GetSetting(context.Background(), "retention.forever"); v != "false" {
		t.Fatalf("forever: %q", v)
	}
}

func TestSettings_Put_RetentionForeverFalse_AllKnobsZero_422(t *testing.T) {
	h := apitestServer(t)
	// Explicitly zero every knob (overriding the seeded failed.days=14) so
	// nothing would ever be pruned — that's the meaningless case we reject.
	body := mustMarshal(t, map[string]any{
		"retention.forever":       false,
		"retention.success.days":  0,
		"retention.success.count": 0,
		"retention.failed.days":   0,
		"retention.failed.count":  0,
	})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 422 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
}

func TestSettings_Put_RetentionForeverFalse_SeededDefaults_OK(t *testing.T) {
	h := apitestServer(t)
	// Migration 020 seeds failed.days=14, so turning off forever with no
	// knob in the patch still has an active limit and is accepted.
	body := mustMarshal(t, map[string]any{"retention.forever": false})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
}

func TestSettings_Put_RetentionForeverTrue_OK(t *testing.T) {
	h := apitestServer(t)
	body := mustMarshal(t, map[string]any{"retention.forever": true})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
}

func TestSettings_Put_RetentionNegative_422(t *testing.T) {
	h := apitestServer(t)
	body := mustMarshal(t, map[string]any{"retention.failed.days": -1})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 422 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
}

func TestSettings_Put_LibraryPathValid(t *testing.T) {
	h := apitestServer(t)
	body := mustMarshal(t, map[string]any{"library.path": "/srv/media"})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	v, _ := h.Store.GetSetting(context.Background(), "library.path")
	if v != "/srv/media" {
		t.Fatalf("library.path: %q", v)
	}
	// Legacy compat: writing library.path also fans out to the typed
	// roots. Future clients should write library.<media> directly.
	for media, want := range map[string]string{
		"library.movies": "/srv/media/movies",
		"library.tv":     "/srv/media/tv",
		"library.music":  "/srv/media/music",
		"library.games":  "/srv/media/games",
		"library.data":   "/srv/media/data",
	} {
		got, _ := h.Store.GetSetting(context.Background(), media)
		if got != want {
			t.Errorf("%s = %q, want %q", media, got, want)
		}
	}
}

func TestSettings_Put_LibraryRoots_Typed(t *testing.T) {
	h := apitestServer(t)
	body := mustMarshal(t, map[string]any{
		"library.movies": "/srv/movies",
		"library.tv":     "/srv/tv",
		"library.music":  "/srv/music",
		"library.games":  "/srv/games",
		"library.data":   "/srv/data",
	})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	for k, want := range map[string]string{
		"library.movies": "/srv/movies",
		"library.tv":     "/srv/tv",
		"library.music":  "/srv/music",
		"library.games":  "/srv/games",
		"library.data":   "/srv/data",
	} {
		got, _ := h.Store.GetSetting(context.Background(), k)
		if got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestSettings_Put_LibraryRoot_Relative_422(t *testing.T) {
	h := apitestServer(t)
	body := mustMarshal(t, map[string]any{"library.movies": "media/films"})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 422 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
}

func TestSettings_Put_LibraryPathRelative_422(t *testing.T) {
	h := apitestServer(t)
	body := mustMarshal(t, map[string]any{"library.path": "media"})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 422 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
}

func TestSettings_Put_LibraryPathEmpty_422(t *testing.T) {
	h := apitestServer(t)
	body := mustMarshal(t, map[string]any{"library.path": "   "})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 422 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
}

func TestSettings_Put_OperationMode_Batch(t *testing.T) {
	h := apitestServer(t)
	body := mustMarshal(t, map[string]any{"operation.mode": "batch"})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	v, _ := h.Store.GetSetting(context.Background(), "operation.mode")
	if v != "batch" {
		t.Errorf("operation.mode = %q", v)
	}
}

func TestSettings_Put_OperationMode_Manual(t *testing.T) {
	h := apitestServer(t)
	body := mustMarshal(t, map[string]any{"operation.mode": "manual"})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	v, _ := h.Store.GetSetting(context.Background(), "operation.mode")
	if v != "manual" {
		t.Errorf("operation.mode = %q", v)
	}
}

func TestSettings_Put_OperationMode_Invalid_422(t *testing.T) {
	h := apitestServer(t)
	body := mustMarshal(t, map[string]any{"operation.mode": "bogus"})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 422 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
}

func TestSettings_Put_EjectOnFinish(t *testing.T) {
	h := apitestServer(t)
	body := mustMarshal(t, map[string]any{"rip.eject_on_finish": false})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	v, _ := h.Store.GetSetting(context.Background(), "rip.eject_on_finish")
	if v != "false" {
		t.Errorf("rip.eject_on_finish = %q", v)
	}
}

func TestSettings_Put_EjectOnFinish_NonBool_422(t *testing.T) {
	h := apitestServer(t)
	body := mustMarshal(t, map[string]any{"rip.eject_on_finish": "no"})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 422 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
}

func TestSettings_Put_UnknownKey_422(t *testing.T) {
	h := apitestServer(t)
	body := mustMarshal(t, map[string]any{"unknown.key": "value"})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 422 {
		t.Fatalf("status: %d", rec.Code)
	}
}

func TestSettings_Put_BroadcastsSettingsChanged(t *testing.T) {
	h := apitestServer(t)
	ch, cancel := h.Broadcaster.Subscribe(4)
	defer cancel()
	body := mustMarshal(t, map[string]any{"library.path": "/srv/media"})
	req := authedReq(t, http.MethodPut, "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.PutSettings(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: %d", rec.Code)
	}
	select {
	case ev := <-ch:
		if ev.Name != "settings.changed" {
			t.Fatalf("event: %q", ev.Name)
		}
	default:
		t.Fatal("expected settings.changed SSE event")
	}
}
