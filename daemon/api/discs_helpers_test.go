package api

import (
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

func TestIsGameDisc_NewTypes(t *testing.T) {
	newTypes := []state.DiscType{
		state.DiscTypeSegaCD,
		state.DiscType3DO,
		state.DiscTypePCFX,
		state.DiscTypeJaguarCD,
		state.DiscTypeCDi,
		state.DiscTypePCECD,
		state.DiscTypeNeoCD,
	}
	for _, dt := range newTypes {
		if !isGameDisc(dt) {
			t.Errorf("isGameDisc(%q) = false, want true", dt)
		}
	}
}

func TestIsGameDisc_NonGameTypes(t *testing.T) {
	nonGame := []state.DiscType{
		state.DiscTypeAudioCD,
		state.DiscTypeDVD,
		state.DiscTypeBDMV,
		state.DiscTypeUHD,
		state.DiscTypeVCD,
		state.DiscTypeData,
	}
	for _, dt := range nonGame {
		if isGameDisc(dt) {
			t.Errorf("isGameDisc(%q) = true, want false", dt)
		}
	}
}

func TestGameSystemName_NewTypes(t *testing.T) {
	cases := []struct {
		dt   state.DiscType
		want string
	}{
		{state.DiscTypeSegaCD, "Sega CD"},
		{state.DiscType3DO, "3DO"},
		{state.DiscTypePCFX, "PC-FX"},
		{state.DiscTypeJaguarCD, "Atari Jaguar CD"},
		{state.DiscTypeCDi, "Philips CD-i"},
		{state.DiscTypePCECD, "PC Engine CD"},
		{state.DiscTypeNeoCD, "Neo Geo CD"},
	}
	for _, tc := range cases {
		if got := gameSystemName(tc.dt); got != tc.want {
			t.Errorf("gameSystemName(%q) = %q, want %q", tc.dt, got, tc.want)
		}
	}
}
