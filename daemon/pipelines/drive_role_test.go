package pipelines_test

import (
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

func TestPreferredDriveRole(t *testing.T) {
	cases := []struct {
		dt   state.DiscType
		want pipelines.DriveRole
	}{
		// CD-form-factor: Plextor, for the same C2/subchannel accuracy
		// reason PSX gets it -- not just "is it a game".
		{state.DiscTypeAudioCD, pipelines.DriveRoleCDPS1},
		{state.DiscTypePSX, pipelines.DriveRoleCDPS1},
		{state.DiscTypeSAT, pipelines.DriveRoleCDPS1},
		{state.DiscTypeDC, pipelines.DriveRoleCDPS1},
		{state.DiscTypeSegaCD, pipelines.DriveRoleCDPS1},
		{state.DiscType3DO, pipelines.DriveRoleCDPS1},
		{state.DiscTypePCFX, pipelines.DriveRoleCDPS1},
		{state.DiscTypeJaguarCD, pipelines.DriveRoleCDPS1},
		{state.DiscTypeCDi, pipelines.DriveRoleCDPS1},
		{state.DiscTypePCECD, pipelines.DriveRoleCDPS1},
		{state.DiscTypeNeoCD, pipelines.DriveRoleCDPS1},
		{state.DiscTypeCD32, pipelines.DriveRoleCDPS1},
		{state.DiscTypeFMTowns, pipelines.DriveRoleCDPS1},
		{state.DiscTypePippin, pipelines.DriveRoleCDPS1},
		{state.DiscTypeVCD, pipelines.DriveRoleCDPS1},
		// DVD/BD-form-factor: OmniDrive-flashed drives only.
		{state.DiscTypeBDMV, pipelines.DriveRoleBDConsole},
		{state.DiscTypeUHD, pipelines.DriveRoleBDConsole},
		{state.DiscTypePS2, pipelines.DriveRoleBDConsole},
		{state.DiscTypeXBOX, pipelines.DriveRoleBDConsole},
		{state.DiscTypeXBOX360, pipelines.DriveRoleBDConsole},
		{state.DiscTypePS3, pipelines.DriveRoleBDConsole},
		// No established preference: a plain movie DVD (or unclassified
		// data disc) works fine on either drive, so nothing is enforced.
		{state.DiscTypeDVD, ""},
		{state.DiscTypeData, ""},
	}
	for _, c := range cases {
		if got := pipelines.PreferredDriveRole(c.dt); got != c.want {
			t.Errorf("PreferredDriveRole(%s) = %q, want %q", c.dt, got, c.want)
		}
	}
}

func TestDriveRoleForModel(t *testing.T) {
	cases := []struct {
		model string
		want  pipelines.DriveRole
	}{
		{"PLEXTOR DVDR PX-716A", pipelines.DriveRoleCDPS1},
		{"PLEXTOR DVDR PX-716SA", pipelines.DriveRoleCDPS1},
		{"ASUS BW-16D1HT", pipelines.DriveRoleBDConsole},
		{"PIONEER BD-RW BDR-XD05", pipelines.DriveRoleBDConsole},
		// Unrecognized model: no role enforced, not restrictive.
		{"HL-DT-ST BD-RE  WH14NS40", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := pipelines.DriveRoleForModel(c.model); got != c.want {
			t.Errorf("DriveRoleForModel(%q) = %q, want %q", c.model, got, c.want)
		}
	}
}

func TestWrongDriveMessage(t *testing.T) {
	// PSX in the BD/console (ASUS) drive: mismatch.
	msg, ok := pipelines.WrongDriveMessage(state.DiscTypePSX, "ASUS BW-16D1HT")
	if !ok {
		t.Fatal("want mismatch for PSX in ASUS drive")
	}
	if msg == "" {
		t.Error("want a non-empty message")
	}

	// PSX in the CD/PS1 (Plextor) drive: no mismatch.
	if _, ok := pipelines.WrongDriveMessage(state.DiscTypePSX, "PLEXTOR DVDR PX-716A"); ok {
		t.Error("want no mismatch for PSX in Plextor drive")
	}

	// BDMV in the Plextor drive: mismatch (and a hard capability gap,
	// not just a preference -- PX-716 has no Blu-ray hardware at all).
	if _, ok := pipelines.WrongDriveMessage(state.DiscTypeBDMV, "PLEXTOR DVDR PX-716A"); !ok {
		t.Error("want mismatch for BDMV in Plextor drive")
	}

	// DVD has no established role: never a mismatch, regardless of drive.
	if _, ok := pipelines.WrongDriveMessage(state.DiscTypeDVD, "ASUS BW-16D1HT"); ok {
		t.Error("want no mismatch for DVD (no preferred role) in any drive")
	}

	// Unrecognized drive model: no enforcement even for a role-bearing
	// disc type -- a third-party drive shouldn't get blocked.
	if _, ok := pipelines.WrongDriveMessage(state.DiscTypePSX, "HL-DT-ST BD-RE  WH14NS40"); ok {
		t.Error("want no mismatch when the drive model is unrecognized")
	}
}
