package integrations_test

import (
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/integrations"
)

type captureTMDB struct {
	got identify.TMDBConfig
}

func (c *captureTMDB) Reconfigure(cfg identify.TMDBConfig) { c.got = cfg }

func TestTMDBAdapter_PassesCredsThrough(t *testing.T) {
	cap := &captureTMDB{}
	adapter := integrations.TMDBAdapter(cap)
	if err := adapter(map[string]string{"key": "abc", "lang": "de-DE"}); err != nil {
		t.Fatal(err)
	}
	if cap.got.APIKey != "abc" || cap.got.Language != "de-DE" {
		t.Errorf("got %+v", cap.got)
	}
}

func TestTMDBAdapter_DefaultsLangWhenAbsent(t *testing.T) {
	cap := &captureTMDB{}
	adapter := integrations.TMDBAdapter(cap)
	if err := adapter(map[string]string{"key": "abc"}); err != nil {
		t.Fatal(err)
	}
	if cap.got.Language != "en-US" {
		t.Errorf("language: %q want en-US", cap.got.Language)
	}
}
