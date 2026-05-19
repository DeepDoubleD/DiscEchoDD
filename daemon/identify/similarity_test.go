package identify

import (
	"math"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

func TestTitleSimilarity_JackassVolumeThreeBeatsJackass35(t *testing.T) {
	q := "Jackass Volume 3"
	cases := []struct {
		title string
		want  float64
	}{
		// All 3 query tokens match (3 ↔ three) → 3/3 = 1.0.
		{"Jackass Volume Three", 1.0},
		// {jackass, three, five} vs {jackass, volume, three} →
		// intersect {jackass, three}, union 4 → 0.5.
		{"Jackass 3.5", 0.5},
		// {jackass, 3d} vs {jackass, volume, three} →
		// intersect {jackass}, union 4 → 0.25. ("3d" is one alnum token,
		// not split into digits.)
		{"Jackass 3D", 0.25},
		// {the, making, of, jackass, 3d} vs query of 3 →
		// intersect {jackass}, union 7 → 1/7.
		{"The Making of 'Jackass 3D'", 1.0 / 7.0},
	}
	for _, tc := range cases {
		got := titleSimilarity(q, tc.title)
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("titleSimilarity(%q, %q) = %v; want %v", q, tc.title, got, tc.want)
		}
	}
}

func TestTitleSimilarity_RanksJackassCandidatesCorrectly(t *testing.T) {
	// End-to-end: feed the rank-confidence pipeline the same candidates
	// in the order TMDB returned them (popularity-ordered), confirm
	// Volume Three lands at rank 0 after the similarity pass.
	cands := []state.Candidate{
		{Title: "Jackass 3.5", Confidence: 100, TMDBID: 65851},
		{Title: "Jackass Volume Three", Confidence: 12, TMDBID: 347115},
		{Title: "The Making of 'Jackass 3D'", Confidence: 8, TMDBID: 936730},
		{Title: "Jackass 3D", Confidence: 75, TMDBID: 16290},
	}
	applyRankConfidence(cands, "Jackass Volume 3")
	if cands[0].Title != "Jackass Volume Three" {
		t.Errorf("top match: want Jackass Volume Three, got %q (full order: %v)",
			cands[0].Title, titlesOf(cands))
	}
	if cands[0].Confidence != 100 {
		t.Errorf("rank-0 confidence: want 100, got %d", cands[0].Confidence)
	}
}

func titlesOf(cs []state.Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Title
	}
	return out
}

func TestTitleSimilarity_Symmetric(t *testing.T) {
	a, b := "Arrival", "Arrival 2016"
	if titleSimilarity(a, b) != titleSimilarity(b, a) {
		t.Errorf("similarity is not symmetric: %v vs %v",
			titleSimilarity(a, b), titleSimilarity(b, a))
	}
}

func TestTitleSimilarity_DigitWordEquivalence(t *testing.T) {
	if got := titleSimilarity("Volume 3", "Volume Three"); got != 1.0 {
		t.Errorf("3↔three should match: got %v", got)
	}
	if got := titleSimilarity("Top 10 Hits", "Top Ten Hits"); got != 1.0 {
		t.Errorf("10↔ten should match: got %v", got)
	}
}

func TestTitleSimilarity_EmptyInputs(t *testing.T) {
	if got := titleSimilarity("", ""); got != 1.0 {
		t.Errorf("both empty: want 1.0, got %v", got)
	}
	if got := titleSimilarity("", "anything"); got != 0.0 {
		t.Errorf("one empty: want 0.0, got %v", got)
	}
	if got := titleSimilarity("anything", ""); got != 0.0 {
		t.Errorf("other empty: want 0.0, got %v", got)
	}
}

func TestTitleSimilarity_CaseAndPunctuationInvariant(t *testing.T) {
	if got := titleSimilarity("THE MATRIX", "the.matrix"); got != 1.0 {
		t.Errorf("case + punctuation: want 1.0, got %v", got)
	}
}
