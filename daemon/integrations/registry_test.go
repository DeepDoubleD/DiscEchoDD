package integrations_test

import (
	"errors"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/integrations"
)

func TestRegistry_GetAfterPut_ReturnsSnapshot(t *testing.T) {
	r := integrations.NewRegistry()
	if err := r.Put("igdb", map[string]string{"client_id": "abc"}, integrations.SourceUI, nil); err != nil {
		t.Fatal(err)
	}

	creds, source, configured := r.Get("igdb")
	if creds["client_id"] != "abc" {
		t.Errorf("creds: %v", creds)
	}
	if source != integrations.SourceUI {
		t.Errorf("source: %s want ui", source)
	}
	if !configured {
		t.Error("expected configured")
	}
}

func TestRegistry_UnknownIntegration_ReturnsUnsetEmpty(t *testing.T) {
	r := integrations.NewRegistry()
	creds, source, configured := r.Get("ghost")
	if len(creds) != 0 {
		t.Errorf("creds: %v", creds)
	}
	if source != integrations.SourceUnset {
		t.Errorf("source: %s want unset", source)
	}
	if configured {
		t.Error("ghost should not be configured")
	}
}

func TestRegistry_Put_InvokesReconfigure(t *testing.T) {
	r := integrations.NewRegistry()
	var got map[string]string
	if err := r.Put("tmdb", map[string]string{"key": "init"}, integrations.SourceEnv,
		func(creds map[string]string) error {
			got = creds
			return nil
		}); err != nil {
		t.Fatal(err)
	}

	if got["key"] != "init" {
		t.Errorf("initial Put did not invoke Reconfigure: %v", got)
	}

	if err := r.Put("tmdb", map[string]string{"key": "second"}, integrations.SourceUI, nil); err != nil {
		t.Fatal(err)
	}
	if got["key"] != "second" {
		t.Errorf("Put did not re-invoke Reconfigure with new creds: %v", got)
	}
}

func TestRegistry_Put_ReconfigureErrorPropagates(t *testing.T) {
	r := integrations.NewRegistry()
	// First Put registers the callback. Subsequent Puts use it.
	if err := r.Put("tmdb", map[string]string{"key": "ok"}, integrations.SourceEnv,
		func(creds map[string]string) error {
			if creds["key"] == "bad" {
				return errors.New("simulated reconfigure failure")
			}
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	err := r.Put("tmdb", map[string]string{"key": "bad"}, integrations.SourceUI, nil)
	if err == nil || err.Error() == "" {
		t.Errorf("want error from Reconfigure, got %v", err)
	}
}

func TestRegistry_Names_ReturnsRegisteredIntegrationsInSortedOrder(t *testing.T) {
	r := integrations.NewRegistry()
	for _, name := range []string{"tmdb", "igdb", "makemkv"} {
		if err := r.Put(name, map[string]string{}, integrations.SourceUnset, nil); err != nil {
			t.Fatal(err)
		}
	}

	names := r.Names()
	want := []string{"igdb", "makemkv", "tmdb"}
	if len(names) != len(want) {
		t.Fatalf("names: %v want %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("name[%d]: %s want %s", i, names[i], n)
		}
	}
}

func TestRegistry_DefensiveCopy_BothDirections(t *testing.T) {
	r := integrations.NewRegistry()

	// Put-side: caller mutates the input map after Put. Stored state
	// must not change.
	input := map[string]string{"client_id": "abc"}
	if err := r.Put("igdb", input, integrations.SourceUI, nil); err != nil {
		t.Fatal(err)
	}
	input["client_id"] = "tampered"
	got, _, _ := r.Get("igdb")
	if got["client_id"] != "abc" {
		t.Errorf("Put-side aliasing: stored creds changed after caller mutated input map: %v", got)
	}

	// Get-side: caller mutates the returned map. A subsequent Get must
	// still return the original value.
	got["client_id"] = "tampered-via-get"
	got2, _, _ := r.Get("igdb")
	if got2["client_id"] != "abc" {
		t.Errorf("Get-side aliasing: subsequent Get returned mutated value: %v", got2)
	}
}
