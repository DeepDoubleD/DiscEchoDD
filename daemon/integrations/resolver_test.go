package integrations_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/integrations"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

func openStore(t *testing.T) *state.Store {
	t.Helper()
	dir := t.TempDir()
	db, err := state.Open(filepath.Join(dir, "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return state.NewStore(db)
}

func TestResolve_DBWinsWhenSet(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	_ = s.SetIntegrationCredentials(ctx, "igdb", map[string]string{
		"client_id": "from-db", "client_secret": "db-secret",
	})

	env := map[string]string{"client_id": "from-env", "client_secret": "env-secret"}
	creds, source, err := integrations.Resolve(ctx, s, "igdb", env)
	if err != nil {
		t.Fatal(err)
	}
	if creds["client_id"] != "from-db" {
		t.Errorf("client_id: %q want from-db", creds["client_id"])
	}
	if source != integrations.SourceUI {
		t.Errorf("source: %s want ui", source)
	}
}

func TestResolve_EnvUsedWhenDBEmpty(t *testing.T) {
	s := openStore(t)
	env := map[string]string{"key": "tmdb-env-key", "lang": "fr-FR"}
	creds, source, err := integrations.Resolve(context.Background(), s, "tmdb", env)
	if err != nil {
		t.Fatal(err)
	}
	if creds["key"] != "tmdb-env-key" {
		t.Errorf("key: %q", creds["key"])
	}
	if creds["lang"] != "fr-FR" {
		t.Errorf("lang: %q", creds["lang"])
	}
	if source != integrations.SourceEnv {
		t.Errorf("source: %s want env", source)
	}
}

func TestResolve_UnsetWhenBothEmpty(t *testing.T) {
	s := openStore(t)
	creds, source, err := integrations.Resolve(context.Background(), s, "tmdb", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 0 {
		t.Errorf("creds: %v", creds)
	}
	if source != integrations.SourceUnset {
		t.Errorf("source: %s want unset", source)
	}
}

func TestResolve_EnvAllEmptyStringsCountsAsUnset(t *testing.T) {
	s := openStore(t)
	env := map[string]string{"key": "", "lang": ""}
	_, source, err := integrations.Resolve(context.Background(), s, "tmdb", env)
	if err != nil {
		t.Fatal(err)
	}
	if source != integrations.SourceUnset {
		t.Errorf("source: %s want unset (env values all empty)", source)
	}
}
