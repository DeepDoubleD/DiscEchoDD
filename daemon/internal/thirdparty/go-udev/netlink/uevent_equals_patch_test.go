package netlink

import "testing"

// libudevHeaderPrefix is the fixed 40-byte libudev-monitor header this
// package's parseUdevEvent reads from: "libudev\x00" (8) + magic
// 0xfeedcafe big-endian (4) + 3 more native-order uint32 header fields
// (12) + payload offset = 40 (4, native/little-endian) + 2 more
// trailing header uint32 fields (8) = 40 bytes total, with the payload
// starting immediately after. Lifted verbatim from upstream's own
// TestParseUdevEvent sample (same package, uevent_test.go) -- only
// the payload past byte 40 differs per test below.
var libudevHeaderPrefix = []byte("libudev\x00\xfe\xed\xca\xfe(\x00\x00\x00(\x00\x00\x00\xd5\x03\x00\x00\x8a\xfa\x90\xc8\x00\x00\x00\x00\x02\x00\x04\x00\x10\x80\x00\x00")

// TestParseUdevEvent_ValueContainingEquals is the regression test for
// the live bug this vendored copy patches: upstream's parseUdevEvent
// used bytes.Split(envs, "=") and rejected the WHOLE event (losing
// every property, not just the offending one) the instant a single env
// VALUE contained a literal "=". Confirmed live against a real
// OmniDrive-flashed ASUS BW-16D1HT, whose uevents trip this
// consistently -- crashing the daemon's udev watcher and silently
// dropping real disc-insert events during the multi-second reconnect
// window it takes to recover.
func TestParseUdevEvent_ValueContainingEquals(t *testing.T) {
	payload := "ACTION=change\x00DEVPATH=/devices/pci0000:00/sr0\x00SUBSYSTEM=block\x00" +
		"ID_REVISION=06=00\x00ID_CDROM_MEDIA=1\x00DISK_MEDIA_CHANGE=1\x00"
	raw := append(append([]byte(nil), libudevHeaderPrefix...), payload...)

	ev, err := parseUdevEvent(raw)
	if err != nil {
		t.Fatalf("parseUdevEvent should tolerate a value containing '=', got err: %v", err)
	}
	if ev == nil {
		t.Fatal("parseUdevEvent returned nil event with nil error")
	}
	if got := ev.Env["ID_REVISION"]; got != "06=00" {
		t.Errorf(`Env["ID_REVISION"] = %q, want "06=00" (split on first "=" only)`, got)
	}
	// Every other property in the same event must still come through --
	// upstream's bug lost ALL of them, not just the offending line.
	if got := ev.Env["ID_CDROM_MEDIA"]; got != "1" {
		t.Errorf(`Env["ID_CDROM_MEDIA"] = %q, want "1"`, got)
	}
	if ev.Action != CHANGE {
		t.Errorf("Action = %q, want change", ev.Action)
	}
}

// TestParseUdevEvent_StillRejectsGenuinelyMalformedEnv confirms the
// patch didn't loosen validation entirely -- a line with no "=" at all
// (not just extra ones) is still a real parse error.
func TestParseUdevEvent_StillRejectsGenuinelyMalformedEnv(t *testing.T) {
	payload := "ACTION=change\x00NOEQUALSSIGNHERE\x00"
	raw := append(append([]byte(nil), libudevHeaderPrefix...), payload...)

	if _, err := parseUdevEvent(raw); err == nil {
		t.Error("want error for an env line with no '=' at all")
	}
}
