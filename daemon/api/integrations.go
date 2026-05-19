package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jumpingmushroom/DiscEcho/daemon/integrations"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// makemkvKeyRE matches both MakeMKV key shapes accepted by makemkvcon:
// `T-…` for rotating free beta keys, and `M-…` for purchased
// permanent licenses. The body is opaque to us — MakeMKV mixes
// alphabets between key types and rotates the format periodically —
// so we only gate on the prefix and length, and let makemkvcon itself
// reject malformed bodies via its own MSG codes. Validated only on
// PUT; existing rows are not re-checked.
var makemkvKeyRE = regexp.MustCompile(`^[TM]-\S{8,}$`)

type integrationListEntry struct {
	Name          string              `json:"name"`
	Source        integrations.Source `json:"source"`
	Configured    bool                `json:"configured"`
	FieldsPresent []string            `json:"fields_present"`
}

type integrationDetail struct {
	integrationListEntry
	Values map[string]string `json:"values"`
}

type integrationTestResult struct {
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

func (h *Handlers) ListIntegrations(w http.ResponseWriter, _ *http.Request) {
	if h.Integrations == nil {
		writeError(w, http.StatusServiceUnavailable, "integrations registry not configured")
		return
	}
	out := []integrationListEntry{}
	for _, name := range h.Integrations.Names() {
		creds, source, configured := h.Integrations.Get(name)
		fields := make([]string, 0, len(creds))
		for k, v := range creds {
			if v != "" {
				fields = append(fields, k)
			}
		}
		sort.Strings(fields)
		out = append(out, integrationListEntry{
			Name: name, Source: source, Configured: configured,
			FieldsPresent: fields,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handlers) GetIntegration(w http.ResponseWriter, r *http.Request) {
	if h.Integrations == nil {
		writeError(w, http.StatusServiceUnavailable, "integrations registry not configured")
		return
	}
	name := chi.URLParam(r, "name")
	if !knownIntegration(name) {
		writeError(w, http.StatusNotFound, "integration not found")
		return
	}
	creds, source, configured := h.Integrations.Get(name)
	fields := []string{}
	for k, v := range creds {
		if v != "" {
			fields = append(fields, k)
		}
	}
	sort.Strings(fields)
	writeJSON(w, http.StatusOK, integrationDetail{
		integrationListEntry: integrationListEntry{
			Name: name, Source: source, Configured: configured,
			FieldsPresent: fields,
		},
		Values: creds,
	})
}

func (h *Handlers) PutIntegration(w http.ResponseWriter, r *http.Request) {
	if h.Integrations == nil {
		writeError(w, http.StatusServiceUnavailable, "integrations registry not configured")
		return
	}
	if h.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "store not configured")
		return
	}
	name := chi.URLParam(r, "name")
	if !knownIntegration(name) {
		writeError(w, http.StatusNotFound, "integration not found")
		return
	}
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if errs := validateIntegrationPut(name, body); len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": errs})
		return
	}

	// Merge with existing creds so the Reconfigure call sees the full
	// set, not just the partial body. The store write itself stays
	// partial.
	existing, _, _ := h.Integrations.Get(name)
	merged := map[string]string{}
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range body {
		merged[k] = v
	}

	if err := h.Store.SetIntegrationCredentials(r.Context(), name, body); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.Integrations.Put(name, merged, integrations.SourceUI, nil); err != nil {
		// Reconfigure failed — roll back the DB write to keep the
		// store consistent with the live client config.
		// Rollback is best-effort: if either of these Store calls also fails,
		// the DB and the live registry end up momentarily inconsistent until
		// the next daemon restart re-resolves credentials from disk.
		_ = h.Store.ClearIntegrationCredentials(r.Context(), name)
		for k, v := range existing {
			_ = h.Store.SetIntegrationCredentials(r.Context(), name, map[string]string{k: v})
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	h.broadcastIntegrationChanged(name, integrations.SourceUI)
	h.respondWithDetail(w, name)
}

func (h *Handlers) DeleteIntegration(w http.ResponseWriter, r *http.Request) {
	if h.Integrations == nil {
		writeError(w, http.StatusServiceUnavailable, "integrations registry not configured")
		return
	}
	if h.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "store not configured")
		return
	}
	name := chi.URLParam(r, "name")
	if !knownIntegration(name) {
		writeError(w, http.StatusNotFound, "integration not found")
		return
	}
	if err := h.Store.ClearIntegrationCredentials(r.Context(), name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Re-resolve: the registry needs to fall back to env (if set) or
	// unset. The handler doesn't own env-loading; defer to the boot-
	// time IntegrationEnvLoader callback wired by main.go.
	var envCreds map[string]string
	var source integrations.Source
	if h.IntegrationEnvLoader != nil {
		envCreds, source = h.IntegrationEnvLoader(name)
	} else {
		envCreds, source = map[string]string{}, integrations.SourceUnset
	}
	if err := h.Integrations.Put(name, envCreds, source, nil); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	h.broadcastIntegrationChanged(name, source)
	h.respondWithDetail(w, name)
}

func (h *Handlers) TestIntegration(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "makemkv" {
		writeError(w, http.StatusNotImplemented,
			"no upstream test available; key is validated on next BDMV/UHD rip")
		return
	}
	if !knownIntegration(name) {
		writeError(w, http.StatusNotFound, "integration not found")
		return
	}
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var result integrationTestResult
	switch name {
	case "igdb":
		result = testIGDB(ctx, body, h.IGDBTestEndpoints)
	case "tmdb":
		base := h.TMDBTestEndpoint
		if base == "" {
			base = "https://api.themoviedb.org/3"
		}
		result = testTMDB(ctx, body, base)
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handlers) respondWithDetail(w http.ResponseWriter, name string) {
	creds, source, configured := h.Integrations.Get(name)
	fields := []string{}
	for k, v := range creds {
		if v != "" {
			fields = append(fields, k)
		}
	}
	sort.Strings(fields)
	writeJSON(w, http.StatusOK, integrationDetail{
		integrationListEntry: integrationListEntry{
			Name: name, Source: source, Configured: configured,
			FieldsPresent: fields,
		},
		Values: creds,
	})
}

func (h *Handlers) broadcastIntegrationChanged(name string, source integrations.Source) {
	if h.Broadcaster == nil {
		return
	}
	h.Broadcaster.Publish(state.Event{
		Name: "integrations.changed",
		Payload: map[string]any{
			"name":   name,
			"source": string(source),
		},
	})
}

func knownIntegration(name string) bool {
	switch name {
	case "igdb", "tmdb", "makemkv":
		return true
	}
	return false
}

type validationError struct {
	Field string `json:"field"`
	Msg   string `json:"msg"`
}

func validateIntegrationPut(name string, body map[string]string) []validationError {
	var errs []validationError
	switch name {
	case "igdb":
		_, hasID := body["client_id"]
		_, hasSecret := body["client_secret"]
		if hasID || hasSecret {
			if body["client_id"] == "" {
				errs = append(errs, validationError{Field: "client_id", Msg: "required"})
			}
			if body["client_secret"] == "" {
				errs = append(errs, validationError{Field: "client_secret", Msg: "required"})
			}
		}
	case "tmdb":
		if _, ok := body["key"]; ok && body["key"] == "" {
			errs = append(errs, validationError{Field: "key", Msg: "required"})
		}
	case "makemkv":
		if v, ok := body["beta_key"]; ok && !makemkvKeyRE.MatchString(v) {
			errs = append(errs, validationError{Field: "beta_key",
				Msg: "must start with T- (free beta key) or M- (purchased license)"})
		}
	}
	return errs
}

// testIGDB runs a one-shot Twitch OAuth + IGDB /games round-trip with
// candidate credentials. Always returns a result struct (never an
// error); the test endpoint's HTTP 200 carries the {ok, error?,
// http_status?} payload.
func testIGDB(ctx context.Context, body map[string]string, endpoints IGDBTestEndpoints) integrationTestResult {
	clientID := body["client_id"]
	clientSecret := body["client_secret"]
	if clientID == "" || clientSecret == "" {
		return integrationTestResult{OK: false, Error: "client_id and client_secret are required"}
	}
	tokenURL := endpoints.TokenURL
	if tokenURL == "" {
		tokenURL = "https://id.twitch.tv/oauth2/token"
	}
	gamesURL := endpoints.GamesURL
	if gamesURL == "" {
		gamesURL = "https://api.igdb.com/v4/games"
	}

	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"grant_type":    {"client_credentials"},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
		strings.NewReader(form))
	if err != nil {
		return integrationTestResult{OK: false, Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return integrationTestResult{OK: false, Error: "network timeout — check container DNS / outbound HTTPS"}
		}
		return integrationTestResult{OK: false, Error: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusTooManyRequests {
		return integrationTestResult{OK: false, Error: "Rate-limited; retry in a moment", HTTPStatus: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return integrationTestResult{
			OK: false, Error: http.StatusText(resp.StatusCode), HTTPStatus: resp.StatusCode,
		}
	}
	var tokenBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenBody); err != nil || tokenBody.AccessToken == "" {
		return integrationTestResult{OK: false, Error: "token response missing access_token"}
	}

	gamesReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, gamesURL,
		strings.NewReader("fields name; limit 1;"))
	gamesReq.Header.Set("Authorization", "Bearer "+tokenBody.AccessToken)
	gamesReq.Header.Set("Client-ID", clientID)
	gamesReq.Header.Set("Accept", "application/json")
	gamesResp, err := http.DefaultClient.Do(gamesReq)
	if err != nil {
		return integrationTestResult{OK: false, Error: err.Error()}
	}
	defer func() { _ = gamesResp.Body.Close() }()
	if gamesResp.StatusCode < 200 || gamesResp.StatusCode >= 300 {
		return integrationTestResult{
			OK: false, Error: http.StatusText(gamesResp.StatusCode), HTTPStatus: gamesResp.StatusCode,
		}
	}
	return integrationTestResult{OK: true}
}

func testTMDB(ctx context.Context, body map[string]string, baseURL string) integrationTestResult {
	key := body["key"]
	if key == "" {
		return integrationTestResult{OK: false, Error: "key is required"}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+"/configuration?api_key="+key, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return integrationTestResult{OK: false, Error: "network timeout — check container DNS / outbound HTTPS"}
		}
		return integrationTestResult{OK: false, Error: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return integrationTestResult{OK: true}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return integrationTestResult{
			OK:         false,
			Error:      "Rate-limited; retry in a moment",
			HTTPStatus: resp.StatusCode,
		}
	}
	return integrationTestResult{
		OK:         false,
		Error:      http.StatusText(resp.StatusCode),
		HTTPStatus: resp.StatusCode,
	}
}
