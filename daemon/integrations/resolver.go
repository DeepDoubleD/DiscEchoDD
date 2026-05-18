// Package integrations: resolver merges DB-stored credentials over
// env-derived defaults so the boot path uses one code path for both
// surfaces. Lives alongside Registry (registry.go) so adapter code
// only needs to import one package.
package integrations

import (
	"context"
	"fmt"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// Resolve returns the active credentials for an integration plus the
// source they came from. Precedence:
//   - DB: if Store.GetIntegrationCredentials returns at least one
//     non-empty value, those credentials win and source is SourceUI.
//   - env: otherwise, if the supplied env map has any non-empty value,
//     it is returned and source is SourceEnv.
//   - else: empty map, SourceUnset.
//
// envCreds is whatever the caller has pre-extracted from
// `settings.Settings` (the package doesn't import settings to avoid a
// cycle).
func Resolve(ctx context.Context, store *state.Store, name string, envCreds map[string]string) (map[string]string, Source, error) {
	dbCreds, err := store.GetIntegrationCredentials(ctx, name)
	if err != nil {
		return nil, SourceUnset, fmt.Errorf("integrations: load %s: %w", name, err)
	}
	if hasAnyValue(dbCreds) {
		return dbCreds, SourceUI, nil
	}
	if hasAnyValue(envCreds) {
		out := make(map[string]string, len(envCreds))
		for k, v := range envCreds {
			out[k] = v
		}
		return out, SourceEnv, nil
	}
	return map[string]string{}, SourceUnset, nil
}

func hasAnyValue(m map[string]string) bool {
	for _, v := range m {
		if v != "" {
			return true
		}
	}
	return false
}
