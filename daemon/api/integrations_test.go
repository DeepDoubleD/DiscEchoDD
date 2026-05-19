package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jumpingmushroom/DiscEcho/daemon/api"
	"github.com/jumpingmushroom/DiscEcho/daemon/integrations"
)

// fakeRegistry is a minimal IntegrationsRegistry for handler tests. It
// records every Put so assertions can verify the persistence + source
// transition.
type fakeRegistry struct {
	mu      sync.Mutex
	entries map[string]struct {
		creds  map[string]string
		source integrations.Source
	}
	reconfigureErr error
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{
		entries: map[string]struct {
			creds  map[string]string
			source integrations.Source
		}{
			"igdb":    {creds: map[string]string{}, source: integrations.SourceUnset},
			"tmdb":    {creds: map[string]string{}, source: integrations.SourceUnset},
			"makemkv": {creds: map[string]string{}, source: integrations.SourceUnset},
		},
	}
}

func (f *fakeRegistry) Get(name string) (map[string]string, integrations.Source, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.entries[name]
	if !ok {
		return map[string]string{}, integrations.SourceUnset, false
	}
	out := map[string]string{}
	configured := false
	for k, v := range e.creds {
		out[k] = v
		if v != "" {
			configured = true
		}
	}
	return out, e.source, configured
}

func (f *fakeRegistry) Put(name string, creds map[string]string, source integrations.Source, _ integrations.Reconfigure) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reconfigureErr != nil {
		return f.reconfigureErr
	}
	out := map[string]string{}
	for k, v := range creds {
		out[k] = v
	}
	f.entries[name] = struct {
		creds  map[string]string
		source integrations.Source
	}{creds: out, source: source}
	return nil
}

func (f *fakeRegistry) Names() []string { return []string{"igdb", "makemkv", "tmdb"} }

func apitestServerWithIntegrations(t *testing.T, reg api.IntegrationsRegistry) *api.Handlers {
	h := apitestServer(t)
	h.Integrations = reg
	return h
}

func TestListIntegrations_ReturnsAllInSortedOrder(t *testing.T) {
	h := apitestServerWithIntegrations(t, newFakeRegistry())
	r := chi.NewRouter()
	r.Get("/api/integrations", h.ListIntegrations)

	req := httptest.NewRequest(http.MethodGet, "/api/integrations", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", w.Code, w.Body.String())
	}
	var body []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 3 {
		t.Fatalf("want 3 entries, got %d", len(body))
	}
	wantNames := []string{"igdb", "makemkv", "tmdb"}
	for i, want := range wantNames {
		if body[i]["name"] != want {
			t.Errorf("name[%d]: %v want %s", i, body[i]["name"], want)
		}
	}
}

func TestGetIntegration_ReturnsSecretsCleartext(t *testing.T) {
	fr := newFakeRegistry()
	_ = fr.Put("igdb", map[string]string{
		"client_id": "abc", "client_secret": "secret-xyz",
	}, integrations.SourceUI, nil)

	h := apitestServerWithIntegrations(t, fr)
	r := chi.NewRouter()
	r.Get("/api/integrations/{name}", h.GetIntegration)

	req := httptest.NewRequest(http.MethodGet, "/api/integrations/igdb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	values := body["values"].(map[string]any)
	if values["client_secret"] != "secret-xyz" {
		t.Errorf("secret not returned cleartext: %v", values["client_secret"])
	}
	if body["source"] != "ui" {
		t.Errorf("source: %v want ui", body["source"])
	}
}

func TestPutIntegration_PersistsCredsAndFlipsSourceToUI(t *testing.T) {
	fr := newFakeRegistry()
	h := apitestServerWithIntegrations(t, fr)
	r := chi.NewRouter()
	r.Put("/api/integrations/{name}", h.PutIntegration)

	req := httptest.NewRequest(http.MethodPut, "/api/integrations/tmdb",
		strings.NewReader(`{"key":"new-tmdb-key","lang":"fr-FR"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", w.Code, w.Body.String())
	}
	creds, source, _ := fr.Get("tmdb")
	if creds["key"] != "new-tmdb-key" || creds["lang"] != "fr-FR" {
		t.Errorf("creds: %v", creds)
	}
	if source != integrations.SourceUI {
		t.Errorf("source: %s want ui", source)
	}
}

func TestPutIntegration_ValidatesIGDBPair(t *testing.T) {
	h := apitestServerWithIntegrations(t, newFakeRegistry())
	r := chi.NewRouter()
	r.Put("/api/integrations/{name}", h.PutIntegration)

	// client_id without client_secret → 422.
	req := httptest.NewRequest(http.MethodPut, "/api/integrations/igdb",
		strings.NewReader(`{"client_id":"only"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status %d want 422 (%s)", w.Code, w.Body.String())
	}
}

func TestPutIntegration_ValidatesMakeMKVKeyShape(t *testing.T) {
	cases := []struct {
		name     string
		key      string
		wantCode int
	}{
		{"reject unprefixed", "not-a-key", http.StatusUnprocessableEntity},
		{"reject X-prefix", "X-foo", http.StatusUnprocessableEntity},
		{"accept T- beta", "T-sJ5R5BKxhD671U9s0teXbyP19MhCkkkB7rmnNbb1aEHaqveiVqyI3RXGMHDXhoyNUC", http.StatusOK},
		{"accept M- purchased", "M-abcdef0123456789ABCDEF0123456789abcdef==", http.StatusOK},
		{"accept M- with base64url chars", "M-_6P312gXv-abcDEF0123456789==", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := apitestServerWithIntegrations(t, newFakeRegistry())
			r := chi.NewRouter()
			r.Put("/api/integrations/{name}", h.PutIntegration)

			body, _ := json.Marshal(map[string]string{"beta_key": tc.key})
			req := httptest.NewRequest(http.MethodPut, "/api/integrations/makemkv",
				strings.NewReader(string(body)))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.wantCode {
				t.Errorf("status %d want %d (%s)", w.Code, tc.wantCode, w.Body.String())
			}
		})
	}
}

func TestDeleteIntegration_ClearsAndFlipsSource(t *testing.T) {
	fr := newFakeRegistry()
	_ = fr.Put("igdb", map[string]string{
		"client_id": "abc", "client_secret": "xyz",
	}, integrations.SourceUI, nil)

	h := apitestServerWithIntegrations(t, fr)
	r := chi.NewRouter()
	r.Delete("/api/integrations/{name}", h.DeleteIntegration)

	req := httptest.NewRequest(http.MethodDelete, "/api/integrations/igdb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	creds, source, _ := fr.Get("igdb")
	if len(creds) != 0 {
		t.Errorf("creds after delete: %v", creds)
	}
	if source != integrations.SourceUnset {
		t.Errorf("source: %s want unset (no env fallback in fake)", source)
	}
}

func TestPostIntegrationTest_MakeMKV_Returns501(t *testing.T) {
	h := apitestServerWithIntegrations(t, newFakeRegistry())
	r := chi.NewRouter()
	r.Post("/api/integrations/{name}/test", h.TestIntegration)

	req := httptest.NewRequest(http.MethodPost, "/api/integrations/makemkv/test",
		strings.NewReader(`{"beta_key":"T-X"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("status %d want 501", w.Code)
	}
}

func TestPostIntegrationTest_TMDB_UpstreamOK(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"images":{"base_url":"x"}}`))
	}))
	defer upstream.Close()

	h := apitestServerWithIntegrations(t, newFakeRegistry())
	h.TMDBTestEndpoint = upstream.URL
	r := chi.NewRouter()
	r.Post("/api/integrations/{name}/test", h.TestIntegration)

	req := httptest.NewRequest(http.MethodPost, "/api/integrations/tmdb/test",
		strings.NewReader(`{"key":"any"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", w.Code, w.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["ok"] != true {
		t.Errorf("ok: %v want true", body["ok"])
	}
}

func TestPostIntegrationTest_TMDB_Upstream401(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"status_message":"Invalid API key"}`, http.StatusUnauthorized)
	}))
	defer upstream.Close()

	h := apitestServerWithIntegrations(t, newFakeRegistry())
	h.TMDBTestEndpoint = upstream.URL
	r := chi.NewRouter()
	r.Post("/api/integrations/{name}/test", h.TestIntegration)

	req := httptest.NewRequest(http.MethodPost, "/api/integrations/tmdb/test",
		strings.NewReader(`{"key":"bad"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d want 200 (test endpoint always 200 with ok flag)", w.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["ok"] != false {
		t.Errorf("ok: %v want false", body["ok"])
	}
	if body["http_status"] != float64(401) {
		t.Errorf("http_status: %v want 401", body["http_status"])
	}
}

func TestPutIntegration_RollbackOnReconfigureFailure(t *testing.T) {
	fr := newFakeRegistry()
	fr.reconfigureErr = context.DeadlineExceeded
	h := apitestServerWithIntegrations(t, fr)
	r := chi.NewRouter()
	r.Put("/api/integrations/{name}", h.PutIntegration)

	ctx := context.Background()
	// Seed both the store and the registry so the rollback has the prior
	// value to restore (the handler reads existing creds from the registry,
	// not the store).
	if err := h.Store.SetIntegrationCredentials(ctx, "tmdb", map[string]string{"key": "pre-existing"}); err != nil {
		t.Fatal(err)
	}
	// Prime the registry with the same value before wiring the error so
	// Integrations.Get returns the pre-existing cred during rollback.
	fr.mu.Lock()
	fr.entries["tmdb"] = struct {
		creds  map[string]string
		source integrations.Source
	}{creds: map[string]string{"key": "pre-existing"}, source: integrations.SourceUI}
	fr.mu.Unlock()

	req := httptest.NewRequest(http.MethodPut, "/api/integrations/tmdb",
		strings.NewReader(`{"key":"x"}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Errorf("status %d want 502", w.Code)
	}

	// DB must be unchanged: the failed PUT must not have persisted "x".
	creds, err := h.Store.GetIntegrationCredentials(ctx, "tmdb")
	if err != nil {
		t.Fatal(err)
	}
	if creds["key"] != "pre-existing" {
		t.Errorf("DB after failed PUT: key=%q, want %q", creds["key"], "pre-existing")
	}
}
