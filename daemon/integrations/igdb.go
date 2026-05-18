package integrations

import "github.com/jumpingmushroom/DiscEcho/daemon/identify"

// IGDBReconfigurer is the slice of identify.IGDBClient the adapter
// actually needs. Lets tests pass a minimal capture struct instead of
// constructing a full client.
type IGDBReconfigurer interface {
	Reconfigure(cfg identify.IGDBConfig)
}

// IGDBAdapter returns a Reconfigure that maps the credential field
// names ("client_id", "client_secret") to the typed identify.IGDBConfig
// the client expects. Missing fields are passed as empty strings —
// IGDBClient.Configured() then returns false until the credentials are
// re-supplied. Never returns an error (Reconfigure on IGDBClient never
// fails); the signature matches the Registry's Reconfigure contract.
func IGDBAdapter(c IGDBReconfigurer) Reconfigure {
	return func(creds map[string]string) error {
		c.Reconfigure(identify.IGDBConfig{
			ClientID:     creds["client_id"],
			ClientSecret: creds["client_secret"],
		})
		return nil
	}
}
