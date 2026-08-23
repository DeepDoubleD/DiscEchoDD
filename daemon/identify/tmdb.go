package identify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// TMDBConfig configures a TMDB client.
type TMDBConfig struct {
	APIKey     string       // empty → all Search calls return ([], nil) without HTTP
	BaseURL    string       // default https://api.themoviedb.org/3
	Language   string       // default en-US
	HTTPClient *http.Client // default {Timeout: 30s}
}

// TMDBClient looks up movie / tv release candidates by free-text query.
type TMDBClient interface {
	SearchMovie(ctx context.Context, query string) ([]state.Candidate, error)
	SearchTV(ctx context.Context, query string) ([]state.Candidate, error)
	// preferMediaType ("movie", "tv", or "") breaks ties between
	// candidates that score identically on title similarity -- see the
	// implementation's doc comment for why that matters.
	SearchBoth(ctx context.Context, query string, preferMediaType string) ([]state.Candidate, error)
	// MovieRuntime fetches `/movie/{id}` and returns the runtime in
	// seconds. Returns (0, nil) when the API is not configured or
	// TMDB doesn't know the runtime; only network / decode errors
	// produce non-nil error.
	MovieRuntime(ctx context.Context, tmdbID int) (int, error)
	// MovieDetails / TVDetails fetch the extended record used by the
	// dashboard metadata pane (plot, director, cast, studio, genres,
	// rating, poster URL). Persisted into disc.metadata_json at
	// /api/discs/{id}/start so the pane has rich data on first paint.
	MovieDetails(ctx context.Context, tmdbID int) (DiscMetadata, error)
	TVDetails(ctx context.Context, tmdbID int) (DiscMetadata, error)
	// SeasonEpisodes fetches /tv/{id}/season/{n} and returns the
	// episode list for that season. Used by the scan-job orchestrator
	// path so the picker UI can offer per-title episode mapping for
	// TV box sets. Returns (nil, nil) when the API is unconfigured
	// or the show/season pair isn't known (HTTP 404).
	SeasonEpisodes(ctx context.Context, tvID, seasonNumber int) ([]EpisodeInfo, error)
	// Reconfigure atomically swaps the active config (APIKey + Language).
	// BaseURL / HTTPClient defaults are re-applied for zero fields.
	Reconfigure(cfg TMDBConfig)
}

// EpisodeInfo is one episode from a TMDB season response, in the
// minimal shape the picker UI + handlers need.
type EpisodeInfo struct {
	Number   int    `json:"number"`
	Name     string `json:"name"`
	Runtime  int    `json:"runtime_sec,omitempty"`
	Overview string `json:"overview,omitempty"`
	AirDate  string `json:"air_date,omitempty"`
}

// DiscMetadata is the extended display payload the pane needs.
// Persisted onto discs.metadata_json at /api/discs/{id}/start. Field
// names match the JSON the webui expects so json.Marshal yields the
// wire shape directly.
type DiscMetadata struct {
	Plot      string   `json:"plot,omitempty"`
	Director  string   `json:"director,omitempty"`
	Cast      []string `json:"cast,omitempty"`
	Studio    string   `json:"studio,omitempty"`
	Genres    []string `json:"genres,omitempty"`
	Rating    float64  `json:"rating,omitempty"`
	PosterURL string   `json:"poster_url,omitempty"`
}

// posterImageBase is the TMDB CDN base for w342 poster images. w342 is
// the "card thumbnail" size — adequate for the 56-pixel header thumb
// and large enough that retina screens don't pixelate.
const posterImageBase = "https://image.tmdb.org/t/p/w342"

const tmdbCandidateCap = 5

// NewTMDBClient constructs a TMDBClient.
func NewTMDBClient(c TMDBConfig) TMDBClient {
	if c.BaseURL == "" {
		c.BaseURL = "https://api.themoviedb.org/3"
	}
	if c.Language == "" {
		c.Language = "en-US"
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &tmdbClient{cfg: c}
}

type tmdbClient struct {
	mu  sync.RWMutex
	cfg TMDBConfig
}

// Reconfigure atomically swaps the active credentials. Safe to call
// while other goroutines are mid-request — snapshot() copies the
// config before the HTTP round-trip so the swap window is tiny.
func (c *tmdbClient) Reconfigure(cfg TMDBConfig) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.themoviedb.org/3"
	}
	if cfg.Language == "" {
		cfg.Language = "en-US"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg = cfg
}

// snapshot returns a copy of the current config without holding the
// lock across an HTTP round-trip. Every request path calls this once
// at entry and uses the local copy.
func (c *tmdbClient) snapshot() TMDBConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

// MovieRuntime fetches `/movie/{id}` to read the canonical runtime
// in minutes, returns it in seconds. Search endpoints don't include
// runtime, so this is called on a per-pick basis when the user
// starts a rip.
func (c *tmdbClient) MovieRuntime(ctx context.Context, tmdbID int) (int, error) {
	snap := c.snapshot()
	if snap.APIKey == "" || tmdbID <= 0 {
		return 0, nil
	}
	endpoint := fmt.Sprintf("/movie/%d", tmdbID)
	u, err := url.Parse(strings.TrimRight(snap.BaseURL, "/") + endpoint)
	if err != nil {
		return 0, fmt.Errorf("build url: %w", err)
	}
	q := u.Query()
	q.Set("api_key", snap.APIKey)
	q.Set("language", snap.Language)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := snap.HTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return 0, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return 0, fmt.Errorf("tmdb movie/%d: status %d: %s", tmdbID, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var detail struct {
		Runtime int `json:"runtime"` // minutes; may be 0 or null for unknown
	}
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return 0, fmt.Errorf("decode movie response: %w", err)
	}
	return detail.Runtime * 60, nil
}

// SeasonEpisodes fetches `/tv/{id}/season/{n}` and returns the
// episode list. Used by the title-picker flow so the user can map
// each ripped title to the correct episode (and the move-step
// template renders the canonical `Show - S##E## - Episode Title`
// shape Plex/Jellyfin auto-classify). Returns (nil, nil) when the
// API isn't configured, the tv/season pair is unknown (404), or no
// episodes are returned — callers fall back to bare title naming
// in those cases.
func (c *tmdbClient) SeasonEpisodes(ctx context.Context, tvID, seasonNumber int) ([]EpisodeInfo, error) {
	snap := c.snapshot()
	if snap.APIKey == "" || tvID <= 0 || seasonNumber < 0 {
		return nil, nil
	}
	endpoint := fmt.Sprintf("/tv/%d/season/%d", tvID, seasonNumber)
	u, err := url.Parse(strings.TrimRight(snap.BaseURL, "/") + endpoint)
	if err != nil {
		return nil, fmt.Errorf("build url: %w", err)
	}
	q := u.Query()
	q.Set("api_key", snap.APIKey)
	q.Set("language", snap.Language)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := snap.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("tmdb tv/%d/season/%d: status %d: %s", tvID, seasonNumber, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var detail struct {
		Episodes []struct {
			EpisodeNumber int    `json:"episode_number"`
			Name          string `json:"name"`
			Runtime       int    `json:"runtime"` // minutes
			Overview      string `json:"overview"`
			AirDate       string `json:"air_date"`
		} `json:"episodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return nil, fmt.Errorf("decode season response: %w", err)
	}
	out := make([]EpisodeInfo, 0, len(detail.Episodes))
	for _, e := range detail.Episodes {
		out = append(out, EpisodeInfo{
			Number:   e.EpisodeNumber,
			Name:     e.Name,
			Runtime:  e.Runtime * 60,
			Overview: e.Overview,
			AirDate:  e.AirDate,
		})
	}
	return out, nil
}

// MovieDetails returns the extended pane metadata for a TMDB movie.
func (c *tmdbClient) MovieDetails(ctx context.Context, tmdbID int) (DiscMetadata, error) {
	return c.details(ctx, fmt.Sprintf("/movie/%d", tmdbID), "Director")
}

// TVDetails is the TV-show equivalent of MovieDetails. Treats
// "Creator" as the Director equivalent so the pane shows one human
// name regardless of media type.
func (c *tmdbClient) TVDetails(ctx context.Context, tmdbID int) (DiscMetadata, error) {
	return c.details(ctx, fmt.Sprintf("/tv/%d", tmdbID), "Creator")
}

// tmdbDetailsResponse decodes the slice of fields we care about from
// /movie/{id} and /tv/{id} responses. Both endpoints share the same
// genre / production_companies / credits shape, so one struct suffices.
type tmdbDetailsResponse struct {
	Overview    string  `json:"overview"`
	VoteAverage float64 `json:"vote_average"`
	PosterPath  string  `json:"poster_path"`
	Genres      []struct {
		Name string `json:"name"`
	} `json:"genres"`
	ProductionCompanies []struct {
		Name string `json:"name"`
	} `json:"production_companies"`
	Credits struct {
		Crew []struct {
			Name string `json:"name"`
			Job  string `json:"job"`
		} `json:"crew"`
		Cast []struct {
			Name  string `json:"name"`
			Order int    `json:"order"`
		} `json:"cast"`
	} `json:"credits"`
}

func (c *tmdbClient) details(ctx context.Context, endpoint, directorJob string) (DiscMetadata, error) {
	snap := c.snapshot()
	if snap.APIKey == "" {
		return DiscMetadata{}, nil
	}
	u, err := url.Parse(strings.TrimRight(snap.BaseURL, "/") + endpoint)
	if err != nil {
		return DiscMetadata{}, fmt.Errorf("build url: %w", err)
	}
	q := u.Query()
	q.Set("api_key", snap.APIKey)
	q.Set("language", snap.Language)
	q.Set("append_to_response", "credits")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return DiscMetadata{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := snap.HTTPClient.Do(req)
	if err != nil {
		return DiscMetadata{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return DiscMetadata{}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return DiscMetadata{}, fmt.Errorf("tmdb details: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var raw tmdbDetailsResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return DiscMetadata{}, fmt.Errorf("decode details: %w", err)
	}

	out := DiscMetadata{
		Plot:   raw.Overview,
		Rating: raw.VoteAverage,
	}
	for _, g := range raw.Genres {
		out.Genres = append(out.Genres, g.Name)
	}
	if len(raw.ProductionCompanies) > 0 {
		out.Studio = raw.ProductionCompanies[0].Name
	}
	for _, cm := range raw.Credits.Cast {
		out.Cast = append(out.Cast, cm.Name)
		if len(out.Cast) >= 10 {
			break
		}
	}
	for _, cm := range raw.Credits.Crew {
		if cm.Job == directorJob {
			out.Director = cm.Name
			break
		}
	}
	if raw.PosterPath != "" {
		out.PosterURL = posterImageBase + raw.PosterPath
	}
	return out, nil
}

func (c *tmdbClient) SearchMovie(ctx context.Context, query string) ([]state.Candidate, error) {
	out, err := c.search(ctx, "/search/movie", "movie", query, parseTMDBMovie)
	if err != nil {
		return nil, err
	}
	applyRankConfidence(out, query, "")
	return out, nil
}

func (c *tmdbClient) SearchTV(ctx context.Context, query string) ([]state.Candidate, error) {
	out, err := c.search(ctx, "/search/tv", "tv", query, parseTMDBTV)
	if err != nil {
		return nil, err
	}
	applyRankConfidence(out, query, "")
	return out, nil
}

// SearchBoth runs movie + tv searches in parallel, merges them by
// popularity-derived sort key (kept in Confidence at this stage),
// caps at 5, then assigns final rank-based confidence.
//
// preferMediaType ("movie", "tv", or "" for no preference) breaks ties
// between candidates that score identically on title similarity --
// e.g. a disc labeled "WATCHMEN" matches the 2009 movie, the 2019 HBO
// series, and a 2001 short at the same 100% text similarity, and
// TMDB's own popularity field is recency-biased enough that the newest
// one can outrank a far more iconic older film. Callers that only ever
// mean one media type for their disc format (BDMV/UHD are single-disc
// formats essentially never used for a full TV season) pass that
// preference; DVD, which has real movie AND real season/box-set
// profiles with no way to tell which from the label alone, passes "".
func (c *tmdbClient) SearchBoth(ctx context.Context, query string, preferMediaType string) ([]state.Candidate, error) {
	if c.snapshot().APIKey == "" {
		return nil, nil
	}
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		out      []state.Candidate
		movieErr error
		tvErr    error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		cands, err := c.search(ctx, "/search/movie", "movie", query, parseTMDBMovie)
		mu.Lock()
		out = append(out, cands...)
		movieErr = err
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		cands, err := c.search(ctx, "/search/tv", "tv", query, parseTMDBTV)
		mu.Lock()
		out = append(out, cands...)
		tvErr = err
		mu.Unlock()
	}()
	wg.Wait()

	if movieErr != nil && tvErr != nil {
		return nil, fmt.Errorf("tmdb both: movie=%w; tv=%v", movieErr, tvErr)
	}

	// Pre-cap ordering: similarity to the query first, preferred media
	// type second, popularity (which still lives in Confidence) as
	// final tiebreaker. Keeps the best-matching candidates when there
	// are more than tmdbCandidateCap results across the two endpoints,
	// instead of dropping them in favour of irrelevant-but-popular
	// titles.
	sort.SliceStable(out, func(i, j int) bool {
		if query != "" {
			si := TitleSimilarity(query, out[i].Title)
			sj := TitleSimilarity(query, out[j].Title)
			if si != sj {
				return si > sj
			}
		}
		if preferMediaType != "" && out[i].MediaType != out[j].MediaType {
			if out[i].MediaType == preferMediaType {
				return true
			}
			if out[j].MediaType == preferMediaType {
				return false
			}
		}
		return out[i].Confidence > out[j].Confidence
	})
	if len(out) > tmdbCandidateCap {
		out = out[:tmdbCandidateCap]
	}
	applyRankConfidence(out, query, preferMediaType)
	return out, nil
}

// rankConfidence maps a candidate's rank position to a confidence
// value, used only when there's no query to measure similarity against
// (see applyRankConfidence). Top match gets 100; subsequent matches
// step down in 20-point increments with a floor of 20.
func rankConfidence(rank int) int {
	switch {
	case rank <= 0:
		return 100
	case rank == 1:
		return 80
	case rank == 2:
		return 60
	case rank == 3:
		return 40
	default:
		return 20
	}
}

// applyRankConfidence sorts cands by title-vs-query similarity desc,
// breaks ties on the existing Confidence (which still holds the TMDB
// popularity-derived score at this point), then overwrites each
// Confidence with round(TitleSimilarity*100).
//
// A rank-based ladder (top match always 100, regardless of how similar
// it actually is) previously lived here; it let a disc with a
// low-signal label — e.g. a UDF Blu-ray whose volume label is the
// literal placeholder "VOLUME_ID" — auto-match a completely unrelated
// title at a false 100% confidence, because that title happened to
// outrank the others via incidental token overlap ("volume"). Real
// similarity is the only honest confidence signal: a weak match must
// report a low number so the UI/user can tell it's a guess, not a hit.
//
// preferMediaType breaks ties between candidates tied on similarity --
// see SearchBoth's doc comment for why that's needed. "" for callers
// (SearchMovie/SearchTV) whose candidates are already all one media
// type, where it would be a no-op anyway.
//
// Passing an empty query falls back to the old rank ladder — for tests
// and callers that don't have a natural query (none in production
// today), since TitleSimilarity can't score against nothing. Stable
// sort preserves the relative order of full ties.
func applyRankConfidence(cands []state.Candidate, query string, preferMediaType string) {
	sort.SliceStable(cands, func(i, j int) bool {
		if query != "" {
			si := TitleSimilarity(query, cands[i].Title)
			sj := TitleSimilarity(query, cands[j].Title)
			if si != sj {
				return si > sj
			}
		}
		if preferMediaType != "" && cands[i].MediaType != cands[j].MediaType {
			if cands[i].MediaType == preferMediaType {
				return true
			}
			if cands[j].MediaType == preferMediaType {
				return false
			}
		}
		return cands[i].Confidence > cands[j].Confidence
	})
	for i := range cands {
		if query == "" {
			cands[i].Confidence = rankConfidence(i)
			continue
		}
		cands[i].Confidence = int(math.Round(TitleSimilarity(query, cands[i].Title) * 100))
	}
}

type tmdbSearchResponse struct {
	Results []json.RawMessage `json:"results"`
}

type tmdbMovie struct {
	ID          int     `json:"id"`
	Title       string  `json:"title"`
	ReleaseDate string  `json:"release_date"`
	Popularity  float64 `json:"popularity"`
}

type tmdbTV struct {
	ID           int     `json:"id"`
	Name         string  `json:"name"`
	FirstAirDate string  `json:"first_air_date"`
	Popularity   float64 `json:"popularity"`
}

func parseTMDBMovie(raw json.RawMessage) (state.Candidate, error) {
	var m tmdbMovie
	if err := json.Unmarshal(raw, &m); err != nil {
		return state.Candidate{}, err
	}
	return state.Candidate{
		Source:     "TMDB",
		Title:      m.Title,
		Year:       parseYear(m.ReleaseDate),
		Confidence: int(math.Round(math.Min(m.Popularity/10, 100))),
		TMDBID:     m.ID,
		MediaType:  "movie",
	}, nil
}

func parseTMDBTV(raw json.RawMessage) (state.Candidate, error) {
	var t tmdbTV
	if err := json.Unmarshal(raw, &t); err != nil {
		return state.Candidate{}, err
	}
	return state.Candidate{
		Source:     "TMDB",
		Title:      t.Name,
		Year:       parseYear(t.FirstAirDate),
		Confidence: int(math.Round(math.Min(t.Popularity/10, 100))),
		TMDBID:     t.ID,
		MediaType:  "tv",
	}, nil
}

// search runs one TMDB endpoint and parses with the given parser. 404
// → empty + nil; other non-2xx → error.
func (c *tmdbClient) search(
	ctx context.Context,
	endpoint, mediaType, query string,
	parser func(json.RawMessage) (state.Candidate, error),
) ([]state.Candidate, error) {
	snap := c.snapshot()
	if snap.APIKey == "" {
		return nil, nil
	}
	u, err := url.Parse(strings.TrimRight(snap.BaseURL, "/") + endpoint)
	if err != nil {
		return nil, fmt.Errorf("build url: %w", err)
	}
	q := u.Query()
	q.Set("api_key", snap.APIKey)
	q.Set("query", query)
	q.Set("language", snap.Language)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := snap.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("tmdb %s: status %d: %s", mediaType, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var raw tmdbSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	out := make([]state.Candidate, 0, len(raw.Results))
	for _, r := range raw.Results {
		c, err := parser(r)
		if err != nil {
			continue
		}
		out = append(out, c)
	}
	if len(out) > tmdbCandidateCap {
		out = out[:tmdbCandidateCap]
	}
	return out, nil
}
