package identify_test

import (
	"context"
	"os"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
)

func TestParseIsoInfo(t *testing.T) {
	out, err := os.ReadFile("testdata/isoinfo-arrival.txt")
	if err != nil {
		t.Fatal(err)
	}
	info, err := identify.ParseIsoInfoOutput(string(out))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.VolumeLabel != "ARRIVAL" {
		t.Errorf("volume label: got %q", info.VolumeLabel)
	}
}

func TestParseIsoInfo_Blank(t *testing.T) {
	out, err := os.ReadFile("testdata/isoinfo-blank.txt")
	if err != nil {
		t.Fatal(err)
	}
	info, err := identify.ParseIsoInfoOutput(string(out))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.VolumeLabel != "" {
		t.Errorf("blank disc label: got %q", info.VolumeLabel)
	}
}

func TestParseIsoInfo_NoVolumeIdLine(t *testing.T) {
	_, err := identify.ParseIsoInfoOutput("garbage output\nwith no useful fields\n")
	if err == nil {
		t.Errorf("want error when Volume id: line missing")
	}
}

func TestDVDProber_ExecFailureSurfaces(t *testing.T) {
	p := identify.NewDVDProber(identify.DVDProberConfig{IsoInfoBin: "/usr/bin/false"})
	_, err := p.Probe(context.Background(), "/dev/null")
	if err == nil {
		t.Errorf("want error from /usr/bin/false")
	}
}

// fakeBin writes an executable shell script named name into a fresh
// PATH-only temp dir and points PATH at it (prepended to the real PATH,
// so any other real binaries the prober shells out to still resolve).
func fakeBin(t *testing.T, name, script string) {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/" + name
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

// TestDVDProber_UDFFallback covers the case that broke live: a UDF-only
// Blu-ray (no ISO9660 bridge) makes isoinfo exit 1, but udevadm's
// blkid-derived ID_FS_LABEL still reads it fine. The prober must not
// fail the whole identify pipeline over that — see daemon/identify/classify.go's
// mediaIsBluRay, which trusts the same udev property.
func TestDVDProber_UDFFallback(t *testing.T) {
	fakeBin(t, "isoinfo", "echo 'CD-ROM is NOT in ISO 9660 format' >&2\nexit 1\n")
	fakeBin(t, "udevadm", "cat <<'EOF'\nID_FS_TYPE=udf\nID_FS_LABEL=V_FOR_VENDETTA\nEOF\n")

	p := identify.NewDVDProber(identify.DVDProberConfig{IsoInfoBin: "isoinfo"})
	info, err := p.Probe(context.Background(), "/dev/sr0")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if info.VolumeLabel != "V_FOR_VENDETTA" {
		t.Errorf("volume label: got %q", info.VolumeLabel)
	}
}

// TestDVDProber_UDFFallback_NoLabel covers a genuinely unreadable disc:
// isoinfo fails AND udevadm reports no filesystem at all (no ID_FS_LABEL
// line), so the original isoinfo error must still surface rather than be
// swallowed into a fabricated blank label.
func TestDVDProber_UDFFallback_NoLabel(t *testing.T) {
	fakeBin(t, "isoinfo", "echo 'CD-ROM is NOT in ISO 9660 format' >&2\nexit 1\n")
	fakeBin(t, "udevadm", "echo 'DEVNAME=/dev/sr0'\n")

	p := identify.NewDVDProber(identify.DVDProberConfig{IsoInfoBin: "isoinfo"})
	_, err := p.Probe(context.Background(), "/dev/sr0")
	if err == nil {
		t.Errorf("want error when udevadm reports no ID_FS_LABEL")
	}
}
