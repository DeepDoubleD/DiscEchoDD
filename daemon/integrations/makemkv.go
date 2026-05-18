package integrations

import "github.com/jumpingmushroom/DiscEcho/daemon/settings"

// MakeMKVAdapter returns a Reconfigure that rewrites
// <dataDir>/settings.conf with the supplied beta_key (and mirrors it
// into the .MakeMKV/ subdirectory that makemkvcon actually loads).
// Empty key is a no-op; non-fatal error from the writer propagates so
// the API surface can return a 502 if the disk is read-only.
func MakeMKVAdapter(dataDir string) Reconfigure {
	return func(creds map[string]string) error {
		return settings.WriteMakeMKVBetaKey(dataDir, creds["beta_key"])
	}
}
