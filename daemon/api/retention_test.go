package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/api"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// finishedJob creates a job in the given terminal state on its own disc,
// with finished_at backdated by ageDays.
func finishedJob(t *testing.T, h *api.Handlers, drv *state.Drive, prof *state.Profile, st state.JobState, ageDays int) *state.Job {
	t.Helper()
	ctx := context.Background()
	d := &state.Disc{DriveID: drv.ID, Type: state.DiscTypeAudioCD, Title: "x"}
	if err := h.Store.CreateDisc(ctx, d); err != nil {
		t.Fatal(err)
	}
	j := &state.Job{DiscID: d.ID, DriveID: drv.ID, ProfileID: prof.ID}
	if err := h.Store.CreateJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	if err := h.Store.UpdateJobState(ctx, j.ID, st, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Store.DB().Conn().ExecContext(ctx,
		`UPDATE jobs SET finished_at = ? WHERE id = ?`,
		time.Now().AddDate(0, 0, -ageDays).UTC().Format(time.RFC3339Nano), j.ID); err != nil {
		t.Fatal(err)
	}
	return j
}

func getStatus(t *testing.T, h *api.Handlers, query string) (status struct {
	Forever      bool `json:"forever"`
	SuccessTotal int  `json:"success_total"`
	FailedTotal  int  `json:"failed_total"`
	WouldDelete  struct {
		Success int `json:"success"`
		Failed  int `json:"failed"`
		Total   int `json:"total"`
	} `json:"would_delete"`
	LastRunCount int    `json:"last_run_count"`
	NextRunAt    string `json:"next_run_at"`
}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/retention/status"+query, nil)
	rec := httptest.NewRecorder()
	h.GetRetentionStatus(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestRetentionStatus_PreviewAndTotals(t *testing.T) {
	h := apitestServer(t)
	drv := seedDrive(t, h)
	prof := seedProfile(t, h)
	finishedJob(t, h, drv, prof, state.JobStateDone, 100)
	finishedJob(t, h, drv, prof, state.JobStateFailed, 100)

	// Saved policy: forever absent (=false), failed.days=14 seeded by
	// migration 020, success.days=0. So only the old failure is in scope.
	st := getStatus(t, h, "")
	if st.SuccessTotal != 1 || st.FailedTotal != 1 {
		t.Fatalf("totals: success=%d failed=%d", st.SuccessTotal, st.FailedTotal)
	}
	if st.WouldDelete.Failed != 1 || st.WouldDelete.Success != 0 || st.WouldDelete.Total != 1 {
		t.Fatalf("would_delete (saved): %+v", st.WouldDelete)
	}
	if st.NextRunAt == "" {
		t.Error("next_run_at should be set")
	}

	// Preview an unsaved policy that also prunes successes.
	st = getStatus(t, h, "?success_days=30")
	if st.WouldDelete.Success != 1 || st.WouldDelete.Total != 2 {
		t.Fatalf("would_delete (preview): %+v", st.WouldDelete)
	}

	// forever override zeros everything and never deletes.
	st = getStatus(t, h, "?forever=true")
	if st.WouldDelete.Total != 0 {
		t.Fatalf("forever preview should be 0, got %+v", st.WouldDelete)
	}
	// Nothing was deleted by any preview call.
	if _, err := h.Store.GetJob(context.Background(), ""); err == nil {
		t.Fatal("sanity")
	}
}

func TestRetentionRun_DeletesPerSavedPolicy(t *testing.T) {
	h := apitestServer(t)
	drv := seedDrive(t, h)
	prof := seedProfile(t, h)
	failOld := finishedJob(t, h, drv, prof, state.JobStateFailed, 100)
	doneOld := finishedJob(t, h, drv, prof, state.JobStateDone, 100)

	req := httptest.NewRequest(http.MethodPost, "/api/retention/run", nil)
	rec := httptest.NewRecorder()
	h.RunRetention(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	var res struct {
		SuccessDeleted int `json:"success_deleted"`
		FailedDeleted  int `json:"failed_deleted"`
		Total          int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.FailedDeleted != 1 || res.SuccessDeleted != 0 {
		t.Fatalf("result: %+v", res)
	}
	if _, err := h.Store.GetJob(context.Background(), failOld.ID); err == nil {
		t.Error("old failed job should be deleted (failed.days=14)")
	}
	if _, err := h.Store.GetJob(context.Background(), doneOld.ID); err != nil {
		t.Errorf("old done job should survive (success.days=0): %v", err)
	}
	if n, _ := h.Store.GetInt(context.Background(), "retention.last_run_count"); n != 1 {
		t.Errorf("last_run_count: want 1, got %d", n)
	}
}

func TestRetentionRun_ForeverIsNoOp(t *testing.T) {
	h := apitestServer(t)
	if err := h.Store.SetSetting(context.Background(), "retention.forever", "true"); err != nil {
		t.Fatal(err)
	}
	drv := seedDrive(t, h)
	prof := seedProfile(t, h)
	failOld := finishedJob(t, h, drv, prof, state.JobStateFailed, 100)

	req := httptest.NewRequest(http.MethodPost, "/api/retention/run", nil)
	rec := httptest.NewRecorder()
	h.RunRetention(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: %d", rec.Code)
	}
	if _, err := h.Store.GetJob(context.Background(), failOld.ID); err != nil {
		t.Error("forever=true must not prune anything")
	}
}
