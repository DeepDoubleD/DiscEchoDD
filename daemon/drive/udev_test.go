package drive_test

import (
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/drive"
)

func TestParseUevent_DiscInsert(t *testing.T) {
	// Simplified payload representative of what the kernel emits when a
	// disc is inserted into /dev/sr0. Real payloads are NUL-separated;
	// the parser splits on \n for simpler tests, then on \x00 in the
	// netlink reader.
	payload := "change@/devices/pci0000:00/0000:00:1f.2/ata1/host0/target0:0:0/0:0:0:0/block/sr0\n" +
		"ACTION=change\n" +
		"DEVPATH=/devices/pci0000:00/0000:00:1f.2/ata1/host0/target0:0:0/0:0:0:0/block/sr0\n" +
		"SUBSYSTEM=block\n" +
		"DEVNAME=sr0\n" +
		"DEVTYPE=disk\n" +
		"ID_CDROM=1\n" +
		"DISK_MEDIA_CHANGE=1\n"

	ev, ok := drive.ParseUevent(payload)
	if !ok {
		t.Fatalf("ParseUevent: want ok=true, got false")
	}
	if ev.Action != "change" {
		t.Errorf("Action: want change, got %q", ev.Action)
	}
	if ev.DevName != "sr0" {
		t.Errorf("DevName: want sr0, got %q", ev.DevName)
	}
	if ev.Subsystem != "block" {
		t.Errorf("Subsystem: want block, got %q", ev.Subsystem)
	}
	if !ev.IsOpticalMediaChange() {
		t.Errorf("IsOpticalMediaChange: want true")
	}
}

func TestParseUevent_DevNameWithPrefix(t *testing.T) {
	// udevd (eudev / systemd-udevd) emits DEVNAME as a full path
	// ("/dev/sr0") rather than the bare kernel form ("sr0"). The parser
	// must strip the prefix so downstream callers can always build a
	// device path as "/dev/" + DevName without producing "/dev//dev/sr0".
	payload := "ACTION=change\n" +
		"DEVPATH=/devices/pci0000:00/.../block/sr0\n" +
		"SUBSYSTEM=block\n" +
		"DEVNAME=/dev/sr0\n" +
		"ID_CDROM=1\n" +
		"DISK_MEDIA_CHANGE=1\n"
	ev, ok := drive.ParseUevent(payload)
	if !ok {
		t.Fatalf("ParseUevent: want ok=true")
	}
	if ev.DevName != "sr0" {
		t.Errorf("DevName: want sr0 (bare), got %q", ev.DevName)
	}
	if !ev.IsOpticalMediaChange() {
		t.Errorf("IsOpticalMediaChange: want true")
	}
}

func TestUevent_HasMedia(t *testing.T) {
	// cdrom_id sets ID_CDROM_MEDIA=1 only when a disc is loaded. On insert
	// the key is present; on eject the same media-change event omits it.
	insert := "ACTION=change\nSUBSYSTEM=block\nDEVNAME=sr0\n" +
		"ID_CDROM=1\nDISK_MEDIA_CHANGE=1\nID_CDROM_MEDIA=1\n"
	eject := "ACTION=change\nSUBSYSTEM=block\nDEVNAME=sr0\n" +
		"ID_CDROM=1\nDISK_MEDIA_CHANGE=1\n"

	ev, _ := drive.ParseUevent(insert)
	if !ev.IsOpticalMediaChange() {
		t.Fatalf("insert: want IsOpticalMediaChange true")
	}
	if !ev.HasMedia() {
		t.Errorf("insert: want HasMedia true")
	}

	ev, _ = drive.ParseUevent(eject)
	if !ev.IsOpticalMediaChange() {
		t.Fatalf("eject: want IsOpticalMediaChange true (it is still a media-change)")
	}
	if ev.HasMedia() {
		t.Errorf("eject: want HasMedia false (empty tray)")
	}
}

func TestParseUevent_NotOptical(t *testing.T) {
	payload := "ACTION=add\nSUBSYSTEM=usb\nDEVNAME=ttyUSB0\n"
	ev, ok := drive.ParseUevent(payload)
	if !ok {
		t.Fatalf("ParseUevent: want ok=true")
	}
	if ev.IsOpticalMediaChange() {
		t.Errorf("IsOpticalMediaChange: want false for non-block event")
	}
}

func TestParseUevent_Empty(t *testing.T) {
	if _, ok := drive.ParseUevent(""); ok {
		t.Errorf("empty payload: want ok=false")
	}
}

func TestParseUevent_ValueContainingEquals(t *testing.T) {
	// Regression: an OmniDrive-flashed ASUS BW-16D1HT's uevents include a
	// property whose VALUE itself contains "=" (observed live on a real
	// drive). ParseUevent must split each line on the FIRST "=" only —
	// splitting on every "=" would either corrupt the value or, in the
	// vendored go-udev library this replaced, abort parsing the entire
	// event and silently drop the disc-insert notification.
	payload := "ACTION=change\nSUBSYSTEM=block\nDEVNAME=sr1\n" +
		"ID_CDROM=1\nDISK_MEDIA_CHANGE=1\n" +
		"ID_MODEL_ENC=BW-16D1HT\\x20OmniDrive\\x20fw=v3.09\n"
	ev, ok := drive.ParseUevent(payload)
	if !ok {
		t.Fatalf("ParseUevent: want ok=true")
	}
	if !ev.IsOpticalMediaChange() {
		t.Fatalf("want IsOpticalMediaChange true")
	}
	want := "BW-16D1HT\\x20OmniDrive\\x20fw=v3.09"
	if got := ev.Properties["ID_MODEL_ENC"]; got != want {
		t.Errorf("ID_MODEL_ENC = %q, want %q (only the first '=' should split)", got, want)
	}
}
