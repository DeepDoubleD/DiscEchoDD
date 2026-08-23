package pipelines

import (
	"strings"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// DriveRole is a physical drive's specialty in a multi-drive station.
// Unlike most of this package, the two roles here are grounded in
// concrete hardware facts rather than arbitrary policy:
//
//   - DriveRoleCDPS1: Redump's own compatibility list names the
//     PX-7xx/PX-Premium Plextor lineup (which a PX-716SA belongs to)
//     as certified for accurate CD dumping -- the C2/subchannel
//     reading PS1's libcrypt protection and verifiable rips need.
//   - DriveRoleBDConsole: an ASUS/Pioneer BW-16D1HT reflashed to
//     Panasonic UJ-260 firmware runs MakeMKV in "LibreDrive mode"
//     (visible in every BD rip's scan log), enabling raw BD-ROM reads
//     a stock drive can't do -- and a Plextor PX-716-series drive is a
//     DVD writer with no Blu-ray hardware at all, so it's not just a
//     preference, it's a hard capability gap for BDMV/UHD specifically.
type DriveRole string

const (
	DriveRoleCDPS1     DriveRole = "cd_ps1"
	DriveRoleBDConsole DriveRole = "bd_console"
)

// PreferredDriveRole returns the drive role a disc type should be read
// on, or "" when the type has no established preference (DVD/VCD/DATA
// all read fine on either drive in this station, so routing them would
// just add friction with no accuracy or capability benefit).
func PreferredDriveRole(dt state.DiscType) DriveRole {
	switch dt {
	case state.DiscTypeAudioCD, state.DiscTypePSX:
		return DriveRoleCDPS1
	case state.DiscTypeBDMV, state.DiscTypeUHD,
		state.DiscTypePS2, state.DiscTypeXBOX, state.DiscTypeXBOX360, state.DiscTypeSAT,
		state.DiscTypeDC, state.DiscTypeSegaCD, state.DiscType3DO,
		state.DiscTypePCFX, state.DiscTypeJaguarCD, state.DiscTypeCDi,
		state.DiscTypePCECD, state.DiscTypeNeoCD, state.DiscTypeCD32,
		state.DiscTypeFMTowns, state.DiscTypePippin:
		return DriveRoleBDConsole
	default:
		return ""
	}
}

// DriveRoleForModel classifies a drive's reported model string into a
// role, or "" when the model isn't one this station's routing knows
// about. Unrecognized drives are deliberately permissive (no role
// enforced) rather than restrictive -- a future single-drive or
// third-party setup shouldn't get blocked by a check tuned for this
// station's exact two drives.
func DriveRoleForModel(model string) DriveRole {
	upper := strings.ToUpper(model)
	switch {
	case strings.Contains(upper, "PLEXTOR"):
		return DriveRoleCDPS1
	case strings.Contains(upper, "ASUS"), strings.Contains(upper, "BW-16D1HT"),
		strings.Contains(upper, "PIONEER"), strings.Contains(upper, "UJ260"), strings.Contains(upper, "UJ-260"):
		return DriveRoleBDConsole
	default:
		return ""
	}
}

// driveRoleLabel is the human-readable phrase used in wrong-drive
// messages -- describes what the role is *for*, not the internal slug.
func driveRoleLabel(r DriveRole) string {
	switch r {
	case DriveRoleCDPS1:
		return "Audio CD / PS1"
	case DriveRoleBDConsole:
		return "Blu-ray / console"
	default:
		return string(r)
	}
}

// WrongDriveMessage reports whether discType doesn't belong in a drive
// with driveModel, and if so returns a ready-to-display explanation.
// Returns ok=false when either side has no established role (nothing
// to enforce) or the roles already match.
func WrongDriveMessage(discType state.DiscType, driveModel string) (msg string, ok bool) {
	wantRole := PreferredDriveRole(discType)
	if wantRole == "" {
		return "", false
	}
	haveRole := DriveRoleForModel(driveModel)
	if haveRole == "" || haveRole == wantRole {
		return "", false
	}
	return "wrong drive: " + driveRoleLabel(wantRole) + " disc in a " +
		driveRoleLabel(haveRole) + " drive -- this disc reads best on the " +
		driveRoleLabel(wantRole) + " drive.", true
}
