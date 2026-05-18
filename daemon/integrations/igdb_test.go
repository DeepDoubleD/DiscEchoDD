package integrations_test

import (
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/integrations"
)

type captureIGDB struct {
	got identify.IGDBConfig
}

func (c *captureIGDB) Reconfigure(cfg identify.IGDBConfig) { c.got = cfg }

func TestIGDBAdapter_PassesCredsThrough(t *testing.T) {
	cap := &captureIGDB{}
	adapter := integrations.IGDBAdapter(cap)
	err := adapter(map[string]string{"client_id": "x", "client_secret": "y"})
	if err != nil {
		t.Fatal(err)
	}
	if cap.got.ClientID != "x" || cap.got.ClientSecret != "y" {
		t.Errorf("got %+v", cap.got)
	}
}

func TestIGDBAdapter_EmptyCredsZeroesConfig(t *testing.T) {
	cap := &captureIGDB{}
	cap.Reconfigure(identify.IGDBConfig{ClientID: "stale", ClientSecret: "stale"})
	adapter := integrations.IGDBAdapter(cap)
	_ = adapter(map[string]string{})
	if cap.got.ClientID != "" || cap.got.ClientSecret != "" {
		t.Errorf("expected empty config to zero credentials, got %+v", cap.got)
	}
}
