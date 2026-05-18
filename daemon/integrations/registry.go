// Package integrations owns the runtime credential surfaces for IGDB,
// TMDB, and the MakeMKV beta key: it resolves stored DB values over env
// fallbacks, exposes a Reconfigure seam each integration's client plugs
// into, and is the canonical source for the UI's source-of-truth pill.
package integrations

import (
	"fmt"
	"sort"
	"sync"
)

// Source is the discriminator the UI renders as "Configured (UI)",
// "Configured (env)", or "Unset". Tracked per integration.
type Source string

const (
	SourceUnset Source = "unset"
	SourceEnv   Source = "env"
	SourceUI    Source = "ui"
)

// Reconfigure is the callback an integration's adapter provides. It is
// invoked by Put with the merged credentials whenever they change. The
// callback runs synchronously; non-nil errors bubble out of Put.
type Reconfigure func(creds map[string]string) error

// Registry holds per-integration state behind a mutex. Goroutine-safe.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*entry
}

type entry struct {
	creds       map[string]string
	source      Source
	reconfigure Reconfigure
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{entries: map[string]*entry{}}
}

// Put inserts or updates an integration. If reconfigure is non-nil it
// becomes the integration's persistent Reconfigure callback (subsequent
// Put calls with reconfigure=nil re-use it). The callback is invoked
// synchronously with a defensive copy of creds; a non-nil error from
// the callback propagates out of Put without persisting the new state.
func (r *Registry) Put(name string, creds map[string]string, source Source, reconfigure Reconfigure) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[name]
	if !ok {
		e = &entry{}
		r.entries[name] = e
	}
	if reconfigure != nil {
		e.reconfigure = reconfigure
	}
	if e.reconfigure != nil {
		// Run the callback BEFORE updating state so a failed
		// Reconfigure leaves the registry's view consistent with what
		// the client is actually using.
		if err := e.reconfigure(copyCreds(creds)); err != nil {
			return fmt.Errorf("integrations: %s reconfigure: %w", name, err)
		}
	}
	e.creds = copyCreds(creds)
	e.source = source
	return nil
}

// Get returns a copy of the integration's current credentials, its
// source discriminator, and a boolean configured flag (true when at
// least one credential field is non-empty). Unknown name returns
// (empty-map, SourceUnset, false).
func (r *Registry) Get(name string) (map[string]string, Source, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	if !ok {
		return map[string]string{}, SourceUnset, false
	}
	configured := false
	for _, v := range e.creds {
		if v != "" {
			configured = true
			break
		}
	}
	return copyCreds(e.creds), e.source, configured
}

// Names returns every registered integration name in sorted order.
// Drives the GET /api/integrations response.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.entries))
	for name := range r.entries {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func copyCreds(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
