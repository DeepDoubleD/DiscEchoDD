package state

import (
	"context"
	"fmt"
	"strings"
)

// validateIntegrationName rejects names containing SQL LIKE wildcards
// (% _ \) so a poisoned name can't cause ClearIntegrationCredentials to
// over-match rows it shouldn't touch.
func validateIntegrationName(name string) error {
	if strings.ContainsAny(name, `%_\`) {
		return fmt.Errorf("state: integration name %q contains SQL LIKE wildcards", name)
	}
	return nil
}

// integrationKeyPrefix builds the settings-key prefix for one integration.
// Concrete examples: "integrations.igdb." + "client_id".
func integrationKeyPrefix(name string) string {
	return "integrations." + name + "."
}

// GetIntegrationCredentials returns every credential field stored under
// the given integration name. Missing integration → empty map, nil error.
// Field names are returned without the prefix (e.g. "client_id", not
// "integrations.igdb.client_id").
func (s *Store) GetIntegrationCredentials(ctx context.Context, name string) (map[string]string, error) {
	if err := validateIntegrationName(name); err != nil {
		return nil, err
	}
	all, err := s.GetAllSettings(ctx)
	if err != nil {
		return nil, err
	}
	prefix := integrationKeyPrefix(name)
	out := map[string]string{}
	for k, v := range all {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		field := strings.TrimPrefix(k, prefix)
		if field == "" {
			continue
		}
		out[field] = v
	}
	return out, nil
}

// SetIntegrationCredentials upserts each (field, value) pair under the
// given integration name. Fields not present in fields are left
// untouched — this is a partial update by design so the UI can ship
// only the edited field without re-sending masked secrets it never had
// access to.
func (s *Store) SetIntegrationCredentials(ctx context.Context, name string, fields map[string]string) error {
	if err := validateIntegrationName(name); err != nil {
		return err
	}
	prefix := integrationKeyPrefix(name)
	for field, value := range fields {
		if field == "" {
			continue
		}
		if err := s.SetSetting(ctx, prefix+field, value); err != nil {
			return err
		}
	}
	return nil
}

// ClearIntegrationCredentials deletes every row under the integration's
// key prefix. Idempotent: clearing an already-empty integration is a
// no-op (nil error). Unrelated settings under other prefixes are
// untouched.
func (s *Store) ClearIntegrationCredentials(ctx context.Context, name string) error {
	if err := validateIntegrationName(name); err != nil {
		return err
	}
	prefix := integrationKeyPrefix(name)
	// SQLite LIKE is case-insensitive by default for ASCII; the prefix
	// is lowercase ASCII so that's fine. The literal % is the only
	// wildcard.
	_, err := s.db.Conn().ExecContext(ctx,
		`DELETE FROM settings WHERE key LIKE ?`, prefix+"%")
	return err
}
