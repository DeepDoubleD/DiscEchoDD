package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// retentionPolicyDTO mirrors state.RetentionPolicy on the wire.
type retentionPolicyDTO struct {
	SuccessDays  int `json:"success_days"`
	SuccessCount int `json:"success_count"`
	FailedDays   int `json:"failed_days"`
	FailedCount  int `json:"failed_count"`
}

type retentionWouldDelete struct {
	Success int `json:"success"`
	Failed  int `json:"failed"`
	Total   int `json:"total"`
}

// retentionStatus is the GET /api/retention/status response. It reports the
// current history totals, what the (saved or query-overridden) policy would
// prune, and last/next run info — everything the settings UI needs for a live
// preview without mutating anything.
type retentionStatus struct {
	Forever      bool                 `json:"forever"`
	Policy       retentionPolicyDTO   `json:"policy"`
	SuccessTotal int                  `json:"success_total"`
	FailedTotal  int                  `json:"failed_total"`
	WouldDelete  retentionWouldDelete `json:"would_delete"`
	LastRunAt    string               `json:"last_run_at,omitempty"`
	LastRunCount int                  `json:"last_run_count"`
	NextRunAt    string               `json:"next_run_at"`
}

// savedRetentionPolicy reads the four per-outcome knobs from settings.
func (h *Handlers) savedRetentionPolicy(ctx context.Context) state.RetentionPolicy {
	gi := func(k string) int { n, _ := h.Store.GetInt(ctx, k); return n }
	return state.RetentionPolicy{
		SuccessDays:  gi("retention.success.days"),
		SuccessCount: gi("retention.success.count"),
		FailedDays:   gi("retention.failed.days"),
		FailedCount:  gi("retention.failed.count"),
	}
}

func (h *Handlers) savedRetentionForever(ctx context.Context) bool {
	b, _ := h.Store.GetBool(ctx, "retention.forever")
	return b
}

// queryInt returns the named query param as a non-negative int, or def when
// absent/invalid. Used to overlay an unsaved candidate policy for the preview.
func queryInt(q url.Values, key string, def int) int {
	if v := q.Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

// GetRetentionStatus powers the retention preview. Optional query params
// (forever, success_days, success_count, failed_days, failed_count) override
// the saved policy so the UI can preview unsaved edits. Never mutates state.
func (h *Handlers) GetRetentionStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	forever := h.savedRetentionForever(ctx)
	if v := q.Get("forever"); v != "" {
		forever = v == "true"
	}

	p := h.savedRetentionPolicy(ctx)
	p.SuccessDays = queryInt(q, "success_days", p.SuccessDays)
	p.SuccessCount = queryInt(q, "success_count", p.SuccessCount)
	p.FailedDays = queryInt(q, "failed_days", p.FailedDays)
	p.FailedCount = queryInt(q, "failed_count", p.FailedCount)

	sTot, fTot, err := h.Store.HistoryBucketTotals(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var wd state.RetentionResult
	if !forever {
		if wd, err = h.Store.CountWouldPrune(ctx, p, time.Now()); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	lastAt, _ := h.Store.GetSetting(ctx, "retention.last_run_at")
	lastCount, _ := h.Store.GetInt(ctx, "retention.last_run_count")

	writeJSON(w, http.StatusOK, retentionStatus{
		Forever: forever,
		Policy: retentionPolicyDTO{
			SuccessDays:  p.SuccessDays,
			SuccessCount: p.SuccessCount,
			FailedDays:   p.FailedDays,
			FailedCount:  p.FailedCount,
		},
		SuccessTotal: sTot,
		FailedTotal:  fTot,
		WouldDelete:  retentionWouldDelete{Success: wd.SuccessDeleted, Failed: wd.FailedDeleted, Total: wd.Total()},
		LastRunAt:    lastAt,
		LastRunCount: lastCount,
		NextRunAt:    state.NextThreeAM(time.Now()).UTC().Format(time.RFC3339Nano),
	})
}

// RunRetention runs a prune now against the SAVED policy (not query params —
// callers must save first). A no-op when retention.forever is set. Records the
// run and broadcasts settings.changed so the last-run line refreshes.
func (h *Handlers) RunRetention(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var res state.RetentionResult
	if !h.savedRetentionForever(ctx) {
		now := time.Now()
		var err error
		if res, err = h.Store.PruneHistory(ctx, h.savedRetentionPolicy(ctx), now); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_ = h.Store.SetSetting(ctx, "retention.last_run_at", now.UTC().Format(time.RFC3339Nano))
		_ = h.Store.SetSetting(ctx, "retention.last_run_count", strconv.Itoa(res.Total()))
		if h.Broadcaster != nil {
			h.Broadcaster.Publish(state.Event{
				Name:    "settings.changed",
				Payload: map[string]any{"keys": []string{"retention.last_run_at", "retention.last_run_count"}},
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success_deleted": res.SuccessDeleted,
		"failed_deleted":  res.FailedDeleted,
		"discs_deleted":   res.DiscsDeleted,
		"total":           res.Total(),
	})
}
