package cdgame_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/cdgame"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// stubIdentifier lets tests drive cdgame.Handler.Identify deterministically.
type stubIdentifier struct {
	disc  *state.Disc
	cands []state.Candidate
	err   error
}

func (s stubIdentifier) Identify(_ context.Context, _ *state.Drive) (*state.Disc, []state.Candidate, error) {
	return s.disc, s.cands, s.err
}

func TestHandler_DiscType(t *testing.T) {
	h := cdgame.New(cdgame.Deps{DiscType: state.DiscTypePSX})
	if got := h.DiscType(); got != state.DiscTypePSX {
		t.Fatalf("DiscType() = %q, want %q", got, state.DiscTypePSX)
	}
}

func TestHandler_Identify_DelegatesToIdentifier(t *testing.T) {
	want := &state.Disc{Type: state.DiscTypeSAT, Title: "Panzer Dragoon"}
	h := cdgame.New(cdgame.Deps{
		DiscType:   state.DiscTypeSAT,
		Identifier: stubIdentifier{disc: want, cands: []state.Candidate{{Title: "Panzer Dragoon"}}},
	})
	disc, cands, err := h.Identify(context.Background(), &state.Drive{})
	if err != nil {
		t.Fatalf("Identify() err = %v", err)
	}
	if disc != want {
		t.Fatalf("Identify() disc = %+v, want %+v", disc, want)
	}
	if len(cands) != 1 || cands[0].Title != "Panzer Dragoon" {
		t.Fatalf("Identify() cands = %+v", cands)
	}
}

func TestHandler_Identify_PropagatesErrNoCandidates(t *testing.T) {
	h := cdgame.New(cdgame.Deps{
		DiscType:   state.DiscTypePS2,
		Identifier: stubIdentifier{disc: &state.Disc{}, err: pipelines.ErrNoCandidates},
	})
	_, _, err := h.Identify(context.Background(), &state.Drive{})
	if !errors.Is(err, pipelines.ErrNoCandidates) {
		t.Fatalf("Identify() err = %v, want ErrNoCandidates", err)
	}
}

func TestHandler_Plan_TranscodeSkipped(t *testing.T) {
	plan := cdgame.New(cdgame.Deps{DiscType: state.DiscTypePSX}).Plan(&state.Disc{}, &state.Profile{})
	if len(plan) != len(state.CanonicalSteps()) {
		t.Fatalf("Plan len = %d, want %d", len(plan), len(state.CanonicalSteps()))
	}
	var sawTranscodeSkipped bool
	for _, p := range plan {
		if p.ID == state.StepTranscode {
			sawTranscodeSkipped = p.Skip
		}
	}
	if !sawTranscodeSkipped {
		t.Fatalf("expected transcode step to be skipped")
	}
}

func TestHandler_PlanRip_TranscodeHalfSkipped(t *testing.T) {
	plan := cdgame.New(cdgame.Deps{DiscType: state.DiscTypePSX}).PlanRip(&state.Disc{}, &state.Profile{})
	skip := map[state.StepID]bool{}
	for _, p := range plan {
		skip[p.ID] = p.Skip
	}
	for _, sid := range []state.StepID{state.StepTranscode, state.StepCompress, state.StepMove, state.StepNotify} {
		if !skip[sid] {
			t.Fatalf("PlanRip: expected %s skipped in rip-half", sid)
		}
	}
	if skip[state.StepRip] {
		t.Fatalf("PlanRip: rip step must not be skipped")
	}
}

func TestHandler_PlanTranscode_TranscodeStepSkipped(t *testing.T) {
	plan := cdgame.New(cdgame.Deps{DiscType: state.DiscTypePSX}).PlanTranscode(&state.Disc{}, &state.Profile{})
	if len(plan) != len(state.CanonicalTranscodeSteps()) {
		t.Fatalf("PlanTranscode len = %d, want %d", len(plan), len(state.CanonicalTranscodeSteps()))
	}
	for _, p := range plan {
		if p.ID == state.StepTranscode && !p.Skip {
			t.Fatalf("PlanTranscode: transcode step must be skipped")
		}
	}
}
