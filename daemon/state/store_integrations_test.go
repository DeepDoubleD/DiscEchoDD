package state_test

import (
	"context"
	"testing"
)

func TestStore_IntegrationCredentials_RoundTrip(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.SetIntegrationCredentials(ctx, "igdb", map[string]string{
		"client_id":     "abc",
		"client_secret": "xyz",
	}); err != nil {
		t.Fatalf("set: %v", err)
	}

	got, err := s.GetIntegrationCredentials(ctx, "igdb")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got["client_id"] != "abc" || got["client_secret"] != "xyz" {
		t.Errorf("got %+v want client_id=abc client_secret=xyz", got)
	}
}

func TestStore_IntegrationCredentials_Empty(t *testing.T) {
	s := openStore(t)
	got, err := s.GetIntegrationCredentials(context.Background(), "igdb")
	if err != nil {
		t.Fatalf("get on empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty map, got %+v", got)
	}
}

func TestStore_IntegrationCredentials_Partial(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if err := s.SetIntegrationCredentials(ctx, "tmdb", map[string]string{"key": "abc"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetIntegrationCredentials(ctx, "tmdb")
	if got["key"] != "abc" {
		t.Errorf("partial set: got %+v", got)
	}
	if _, ok := got["lang"]; ok {
		t.Errorf("partial set: lang should be absent, got %q", got["lang"])
	}
}

func TestStore_ClearIntegrationCredentials_RemovesAllUnderPrefix(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if err := s.SetIntegrationCredentials(ctx, "igdb", map[string]string{
		"client_id": "abc", "client_secret": "xyz",
	}); err != nil {
		t.Fatal(err)
	}
	// Pollute with an unrelated setting that must NOT be deleted.
	if err := s.SetSetting(ctx, "library.movies", "/library/movies"); err != nil {
		t.Fatal(err)
	}

	if err := s.ClearIntegrationCredentials(ctx, "igdb"); err != nil {
		t.Fatalf("clear: %v", err)
	}

	got, _ := s.GetIntegrationCredentials(ctx, "igdb")
	if len(got) != 0 {
		t.Errorf("after clear: want empty, got %+v", got)
	}
	v, err := s.GetSetting(ctx, "library.movies")
	if err != nil || v != "/library/movies" {
		t.Errorf("unrelated setting was clobbered: err=%v v=%q", err, v)
	}
}

func TestStore_SetIntegrationCredentials_PartialDoesNotClobber(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if err := s.SetIntegrationCredentials(ctx, "igdb", map[string]string{
		"client_id": "abc", "client_secret": "xyz",
	}); err != nil {
		t.Fatal(err)
	}
	// Partial set: only client_id. client_secret should remain.
	if err := s.SetIntegrationCredentials(ctx, "igdb", map[string]string{
		"client_id": "new-id",
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetIntegrationCredentials(ctx, "igdb")
	if got["client_id"] != "new-id" {
		t.Errorf("client_id: want new-id, got %q", got["client_id"])
	}
	if got["client_secret"] != "xyz" {
		t.Errorf("client_secret clobbered: got %q", got["client_secret"])
	}
}

func TestStore_IntegrationCredentials_RejectsWildcardName(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// Seed a real integration so a poisoned name has something to over-match.
	if err := s.SetIntegrationCredentials(ctx, "igdb", map[string]string{
		"client_id": "abc",
	}); err != nil {
		t.Fatal(err)
	}

	for _, badName := range []string{"ig%b", "igd_", `igdb\`} {
		if err := s.ClearIntegrationCredentials(ctx, badName); err == nil {
			t.Errorf("ClearIntegrationCredentials(%q): want error, got nil", badName)
		}
		if err := s.SetIntegrationCredentials(ctx, badName, map[string]string{"k": "v"}); err == nil {
			t.Errorf("SetIntegrationCredentials(%q): want error, got nil", badName)
		}
		if _, err := s.GetIntegrationCredentials(ctx, badName); err == nil {
			t.Errorf("GetIntegrationCredentials(%q): want error, got nil", badName)
		}
	}

	// The seeded igdb credential must still be intact after the rejections.
	got, _ := s.GetIntegrationCredentials(ctx, "igdb")
	if got["client_id"] != "abc" {
		t.Errorf("igdb credentials clobbered by rejected-name calls: %+v", got)
	}
}
