package api

import (
	"context"
	"sync"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/integrations"
	"github.com/jumpingmushroom/DiscEcho/daemon/jobs"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/settings"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// Ejector releases the tray for devPath. Production binds this to the
// system `eject` binary; tests inject a fake.
type Ejector func(ctx context.Context, devPath string) error

// Handlers bundles every dependency the API endpoints need. Constructed
// in main.go and passed to NewRouter. Methods on *Handlers implement
// individual endpoints; auth middleware sits in front of the protected
// subset.
type Handlers struct {
	Store         *state.Store
	Broadcaster   *state.Broadcaster
	Orchestrator  *jobs.Orchestrator
	Pipelines     *pipelines.Registry
	Classifier    identify.Classifier
	TMDB          identify.TMDBClient
	MusicBrainz   identify.MusicBrainzClient
	IGDB          identify.IGDBClient
	BootCodeIndex *identify.BootCodeIndex
	ActiveSampler *ActiveJobsSampler
	Token         string
	Apprise       Apprise // defined in notifications.go; nil-safe in handlers
	Settings      *settings.Settings
	// Ejector releases the tray for a drive's dev_path. Nil disables the
	// manual eject endpoint (it 503s). Production binds via tools.Eject.
	Ejector Ejector

	// Reclassify reruns the disc-flow handler against the drive whose
	// bus matches the argument. Wired in main.go to discFlow.HandleManual
	// so the reclassify endpoint can recover a drive that flipped to
	// `error` mid-classify without forcing the user to eject the disc.
	// Nil disables the endpoint (returns 503).
	Reclassify func(bus string)

	// NVENCAvailable is set at boot by tools.ProbeNVENC. It controls
	// the "GPU transcoding" Settings row and is threaded into the
	// DVD-Video / BDMV pipeline Deps (in main.go).
	NVENCAvailable bool

	// startMu serializes the "no active job?" check against job
	// submission in StartDisc. Two POST /discs/{id}/start requests can
	// arrive within milliseconds for the same disc (the dashboard
	// mounts both the mobile and desktop component trees, each with
	// its own auto-confirm timer); without this lock both pass the
	// guard and enqueue a duplicate job. The daemon is single-process,
	// so an in-process mutex fully closes the race.
	startMu sync.Mutex

	// Integrations is the live credential registry. Wired by main.go after
	// Resolve populates initial values from env + DB.
	Integrations IntegrationsRegistry

	// IGDBTestEndpoints holds override URLs so handler tests can route
	// Twitch OAuth + IGDB upstream calls to httptest servers. Zero values
	// fall back to the production endpoints.
	IGDBTestEndpoints IGDBTestEndpoints

	// TMDBTestEndpoint overrides the TMDB base URL in handler tests.
	// Zero value falls back to https://api.themoviedb.org/3.
	TMDBTestEndpoint string

	// IntegrationEnvLoader is wired by main.go and provides env-derived
	// credentials for an integration name. Used by DeleteIntegration to
	// re-resolve after clearing the DB rows.
	IntegrationEnvLoader IntegrationEnvLoader
}

// IntegrationsRegistry is the subset of *integrations.Registry the API
// layer needs. Defined as an interface so handler tests can supply a fake
// without instantiating real Reconfigure callbacks.
type IntegrationsRegistry interface {
	Get(name string) (map[string]string, integrations.Source, bool)
	Put(name string, creds map[string]string, source integrations.Source, reconfigure integrations.Reconfigure) error
	Names() []string
}

// IGDBTestEndpoints holds override URLs so handler tests can route the
// upstream call to httptest. Zero values fall back to the production
// Twitch + IGDB endpoints.
type IGDBTestEndpoints struct {
	TokenURL string
	GamesURL string
}

// IntegrationEnvLoader is wired by main.go and provides env-derived
// credentials for an integration name. Used by DeleteIntegration to
// re-resolve after clearing the DB rows.
type IntegrationEnvLoader func(name string) (map[string]string, integrations.Source)
