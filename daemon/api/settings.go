package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// GetSettings returns the full key/value map.
func (h *Handlers) GetSettings(w http.ResponseWriter, r *http.Request) {
	all, err := h.Store.GetAllSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, all)
}

// PutSettings accepts a partial map of editable settings, validates
// each known key, and upserts via Store.SetSetting.
//
// Editable keys (and their value types):
//
//	retention.forever  bool
//	retention.days     int >= 1 (when retention.forever is false)
//	library.movies     absolute filesystem path
//	library.tv         absolute filesystem path
//	library.music      absolute filesystem path
//	library.games      absolute filesystem path
//	library.data       absolute filesystem path
//	library.path       (deprecated) absolute path; setting it writes
//	                   library.{movies,tv,music,games,data} as
//	                   <value>/<media>. Removed in a follow-up release.
//
// Unknown keys → 422. Cross-key rule: if retention.forever is false,
// retention.days must be present in the PATCH (or already stored)
// and >= 1.
func (h *Handlers) PutSettings(w http.ResponseWriter, r *http.Request) {
	var patch map[string]any
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, "bad json: "+err.Error())
		return
	}

	encoded, errs := validateSettingsPatch(patch)

	// Cross-key retention check: only when forever is explicitly false.
	if forever, present := patch["retention.forever"]; present {
		if b, ok := forever.(bool); ok && !b {
			daysVal, daysPresent := patch["retention.days"]
			if !daysPresent {
				existing, _ := h.Store.GetSetting(r.Context(), "retention.days")
				n, _ := strconv.Atoi(existing)
				if n < 1 {
					errs = append(errs, ValidationError{
						Field: "retention.days",
						Msg:   "must be >= 1 when forever is false",
					})
				}
			} else if d, ok := daysVal.(float64); !ok || int(d) < 1 {
				errs = append(errs, ValidationError{
					Field: "retention.days",
					Msg:   "must be >= 1 when forever is false",
				})
			}
		}
	}

	if len(errs) > 0 {
		writeValidationErrors(w, errs)
		return
	}
	// Deprecated library.path → fan out to typed roots for one release.
	if v, ok := encoded["library.path"]; ok {
		for _, m := range []string{"movies", "tv", "music", "games", "data"} {
			key := "library." + m
			if _, alreadySet := encoded[key]; alreadySet {
				continue
			}
			encoded[key] = filepath.Join(v, m)
		}
	}
	for k, v := range encoded {
		if err := h.Store.SetSetting(r.Context(), k, v); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	// Hot-reload side effects: compute pool concurrency picks up the
	// new value without a daemon restart. Spool cap is read on each
	// backpressure check by the orchestrator so it requires no explicit
	// notification here.
	if v, ok := encoded["compute.concurrent_encodes"]; ok && h.Compute != nil {
		if n, err := strconv.Atoi(v); err == nil {
			h.Compute.SetConcurrency(n)
		}
	}
	if h.Broadcaster != nil {
		h.Broadcaster.Publish(state.Event{
			Name:    "settings.changed",
			Payload: map[string]any{"keys": settingsKeys(encoded)},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// allowedSettings maps each editable key to a validator that returns
// the encoded string value or an error.
var allowedSettings = map[string]func(any) (string, error){
	"retention.forever": func(v any) (string, error) {
		b, ok := v.(bool)
		if !ok {
			return "", fmt.Errorf("must be boolean")
		}
		return strconv.FormatBool(b), nil
	},
	"retention.days": func(v any) (string, error) {
		f, ok := v.(float64)
		if !ok {
			return "", fmt.Errorf("must be integer")
		}
		n := int(f)
		if n < 0 {
			return "", fmt.Errorf("must be >= 0")
		}
		return strconv.Itoa(n), nil
	},
	"library.path":   absolutePathValidator,
	"library.movies": absolutePathValidator,
	"library.tv":     absolutePathValidator,
	"library.music":  absolutePathValidator,
	"library.games":  absolutePathValidator,
	"library.data":   absolutePathValidator,
	"operation.mode": func(v any) (string, error) {
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("must be string")
		}
		s = strings.TrimSpace(s)
		if s != "batch" && s != "manual" {
			return "", fmt.Errorf("must be \"batch\" or \"manual\"")
		}
		return s, nil
	},
	"rip.eject_on_finish": func(v any) (string, error) {
		b, ok := v.(bool)
		if !ok {
			return "", fmt.Errorf("must be boolean")
		}
		return strconv.FormatBool(b), nil
	},
	"compute.concurrent_encodes": intRangeValidator(1, 8),
	"spool.cap_bytes":            intRangeValidator(1<<20, 10*1024*1024*1024*1024), // 1 MiB .. 10 TiB
}

// intRangeValidator returns a validator that accepts a JSON number or
// a string representation, parses it as an int64, and asserts the
// value falls within [lo, hi] inclusive. JSON numbers decode as
// float64 so the validator tolerates both shapes — the webui sends
// numbers, but a curl one-liner sending a string still works.
func intRangeValidator(lo, hi int64) func(any) (string, error) {
	return func(v any) (string, error) {
		var n int64
		switch x := v.(type) {
		case float64:
			n = int64(x)
		case string:
			s := strings.TrimSpace(x)
			if s == "" {
				return "", fmt.Errorf("must not be empty")
			}
			parsed, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return "", fmt.Errorf("must be an integer: %w", err)
			}
			n = parsed
		default:
			return "", fmt.Errorf("must be an integer")
		}
		if n < lo || n > hi {
			return "", fmt.Errorf("must be between %d and %d", lo, hi)
		}
		return strconv.FormatInt(n, 10), nil
	}
}

func absolutePathValidator(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("must be string")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("must not be empty")
	}
	if !filepath.IsAbs(s) {
		return "", fmt.Errorf("must be an absolute path")
	}
	return s, nil
}

func validateSettingsPatch(patch map[string]any) (encoded map[string]string, errs []ValidationError) {
	encoded = make(map[string]string, len(patch))
	for k, v := range patch {
		validator, ok := allowedSettings[k]
		if !ok {
			errs = append(errs, ValidationError{Field: k, Msg: "unknown setting key"})
			continue
		}
		s, err := validator(v)
		if err != nil {
			errs = append(errs, ValidationError{Field: k, Msg: err.Error()})
			continue
		}
		encoded[k] = s
	}
	return encoded, errs
}

func settingsKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
