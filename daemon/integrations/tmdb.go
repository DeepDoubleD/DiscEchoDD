package integrations

import "github.com/jumpingmushroom/DiscEcho/daemon/identify"

// TMDBReconfigurer is the slice of identify.TMDBClient the adapter
// actually needs. Lets tests pass a minimal capture struct instead of
// constructing a full client.
type TMDBReconfigurer interface {
	Reconfigure(cfg identify.TMDBConfig)
}

// TMDBAdapter maps {"key", "lang"} to identify.TMDBConfig. Missing
// "lang" defaults to "en-US" — that's the TMDBConfig zero-language
// default and surfacing it explicitly here means the daemon doesn't
// suddenly start serving English titles when the user clears a French
// preference without realizing it.
func TMDBAdapter(c TMDBReconfigurer) Reconfigure {
	return func(creds map[string]string) error {
		lang := creds["lang"]
		if lang == "" {
			lang = "en-US"
		}
		c.Reconfigure(identify.TMDBConfig{
			APIKey:   creds["key"],
			Language: lang,
		})
		return nil
	}
}
