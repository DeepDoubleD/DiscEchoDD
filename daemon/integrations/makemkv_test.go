package integrations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/integrations"
)

func TestMakeMKVAdapter_WritesSettingsConf(t *testing.T) {
	dir := t.TempDir()
	adapter := integrations.MakeMKVAdapter(dir)
	if err := adapter(map[string]string{"beta_key": "T-NEW-KEY"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "settings.conf"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(b), "T-NEW-KEY") {
		t.Errorf("settings.conf: %q", string(b))
	}
}

func TestMakeMKVAdapter_EmptyKeyDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	adapter := integrations.MakeMKVAdapter(dir)
	if err := adapter(map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.conf")); !os.IsNotExist(err) {
		t.Errorf("expected no settings.conf, stat err=%v", err)
	}
}
