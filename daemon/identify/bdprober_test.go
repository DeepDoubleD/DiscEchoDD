package identify_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
)

func TestParseBDInfo_BDMV(t *testing.T) {
	b, err := os.ReadFile("testdata/bdinfo-bdmv.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := identify.ParseBDInfoOutput(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if got.HasAACS2 {
		t.Errorf("BDMV: HasAACS2=true, want false")
	}
	if got.AACSEncrypted != true {
		t.Errorf("BDMV: AACSEncrypted=false, want true")
	}
}

func TestParseBDInfo_UHD(t *testing.T) {
	b, err := os.ReadFile("testdata/bdinfo-uhd.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := identify.ParseBDInfoOutput(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasAACS2 {
		t.Errorf("UHD: HasAACS2=false, want true")
	}
}

func TestParseBDInfo_Empty(t *testing.T) {
	if _, err := identify.ParseBDInfoOutput(""); err == nil {
		t.Errorf("want error on empty input")
	}
}

// TestParseBDInfo_DiscName covers the real fix for the bug that let a
// UDF-only Blu-ray with a generic placeholder volume label ("VOLUME_ID")
// auto-match an unrelated TMDB title: bd_info's "Disc library metadata"
// section, read from the disc's own bdmt_eng.xml, carries the real
// title even when the ISO9660/UDF volume label doesn't.
func TestParseBDInfo_DiscName(t *testing.T) {
	b, err := os.ReadFile("testdata/bdinfo-discname.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := identify.ParseBDInfoOutput(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if got.DiscName != "V For Vendetta" {
		t.Errorf("DiscName: got %q, want %q", got.DiscName, "V For Vendetta")
	}
	if got.VolumeID != "VOLUME_ID" {
		t.Errorf("VolumeID: got %q", got.VolumeID)
	}
}

func TestParseBDInfo_NoDiscNameSection(t *testing.T) {
	b, err := os.ReadFile("testdata/bdinfo-bdmv.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := identify.ParseBDInfoOutput(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if got.DiscName != "" {
		t.Errorf("DiscName: got %q, want empty when disc has no library metadata", got.DiscName)
	}
}

// fakeBDProber (defined in classify_test.go, same package) is reused here.

func TestBDSearchLabel_PrefersDiscName(t *testing.T) {
	bd := &fakeBDProber{info: &identify.BDInfo{DiscName: "V For Vendetta"}}
	got := identify.BDSearchLabel(context.Background(), bd, "/dev/sr0", "VOLUME_ID")
	if got != "V For Vendetta" {
		t.Errorf("got %q, want disc name to win over the volume label", got)
	}
}

func TestBDSearchLabel_FallsBackToVolumeLabel(t *testing.T) {
	cases := []struct {
		name string
		bd   identify.BDProber
	}{
		{"nil prober", nil},
		{"probe error", &fakeBDProber{err: errors.New("bd_info: exit status 1")}},
		{"empty disc name", &fakeBDProber{info: &identify.BDInfo{DiscName: ""}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := identify.BDSearchLabel(context.Background(), c.bd, "/dev/sr0", "ARRIVAL")
			if got != "ARRIVAL" {
				t.Errorf("got %q, want fallback to volume label %q", got, "ARRIVAL")
			}
		})
	}
}

func TestNewBDProber_DefaultBin(t *testing.T) {
	p := identify.NewBDProber(identify.BDProberConfig{})
	if p == nil {
		t.Fatal("nil prober")
	}
	// Calling against /dev/null should fail cleanly (not panic).
	_, err := p.Probe(context.Background(), "/dev/null")
	if err == nil {
		t.Errorf("want error from /dev/null")
	}
}
