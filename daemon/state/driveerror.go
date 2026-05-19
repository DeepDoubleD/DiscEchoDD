package state

import "strings"

// DriveErrorTip returns a human-readable tip explaining a known
// drive-error pattern, or the empty string when no tip applies.
// Surfaces in the UI under the raw error message.
func DriveErrorTip(errMsg string) string {
	if errMsg == "" {
		return ""
	}
	lower := strings.ToLower(errMsg)
	switch {
	case strings.Contains(lower, "cd-info") && strings.Contains(lower, "exit status"):
		// cd-info's exit-1 can come from a dirty/scratched surface, a
		// transient spin-up race that exhausted the classifier's retry
		// budget, or a disc the drive can't physically read (XGD
		// originals need Kreon firmware). The tip leads with the
		// recoverable causes since most failures we see are dirty discs
		// or chilled / slow drives — XGD is rare and gets a "less
		// commonly" framing so users don't run off to flash firmware
		// for a dirty pressed CD.
		return "The drive couldn't read this disc. Most often: dirty or scratched surface (clean the disc and try again), or the drive is spinning the disc down before cd-info finishes (eject and re-insert — the second spin-up is usually faster). Less commonly: an original Xbox game disc requires Kreon-firmware drives (https://kreon.dev) since XGD media is unreadable on stock optical drives."
	case strings.Contains(lower, "deadline exceeded"):
		return "The drive is reading this disc very slowly — the identify step timed out before cd-info or isoinfo could finish. Try ejecting and re-inserting (often the second spin-up is faster), clean the disc surface, or try a different drive."
	case strings.Contains(lower, "makemkv") && isMakeMKVKeyError(lower):
		// MakeMKV's beta key expires roughly every 60 days. Without it,
		// makemkvcon refuses to scan or rip and the error surfaces from
		// the rip step. Profiles whose engine is "MakeMKV" or
		// "MakeMKV+HandBrake" depend on a valid key — the legacy
		// HandBrake-engine DVD profiles still rip via dvdbackup and are
		// unaffected.
		return "MakeMKV's beta key looks expired or missing. Open Settings → Integrations → MakeMKV and paste a fresh key from https://forum.makemkv.com/forum/viewtopic.php?t=1053, then retry the rip. The legacy HandBrake-engine DVD profiles (DVD-Movie, DVD-Movie + Extras, DVD-Series) rip via dvdbackup and don't need a MakeMKV key — they remain usable in the meantime."
	}
	return ""
}

// isMakeMKVKeyError matches the substrings makemkvcon prints when the
// beta key is missing, expired, or unregistered. Kept narrow so a
// generic "expired" string in some other tool's output doesn't trip
// the tip.
func isMakeMKVKeyError(lower string) bool {
	return strings.Contains(lower, "evaluation") ||
		strings.Contains(lower, "trial period") ||
		strings.Contains(lower, "expired") ||
		strings.Contains(lower, "not registered") ||
		strings.Contains(lower, "register makemkv")
}
