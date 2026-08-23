package identify

import (
	"math"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

func TestTitleSimilarity_JackassVolume3Label(t *testing.T) {
	q := "Jackass Volume 3"
	cases := []struct {
		title string
		want  float64
	}{
		// All 3 query tokens match (3 ↔ three) → 3/3 = 1.0.
		{"Jackass Volume Three", 1.0},
		// "3.5" stays as a single token (not split into 3+5), so it
		// does NOT fold to "three". {jackass, 3.5} vs {jackass, volume,
		// three} → intersect {jackass}, union 4 → 0.25.
		{"Jackass 3.5", 0.25},
		// {jackass, 3d} vs {jackass, volume, three} → intersect
		// {jackass}, union 4 → 0.25.
		{"Jackass 3D", 0.25},
		// {the, making, of, jackass, 3d} vs query of 3 →
		// intersect {jackass}, union 7 → 1/7.
		{"The Making of 'Jackass 3D'", 1.0 / 7.0},
	}
	for _, tc := range cases {
		got := TitleSimilarity(q, tc.title)
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("TitleSimilarity(%q, %q) = %v; want %v", q, tc.title, got, tc.want)
		}
	}
}

// TestTitleSimilarity_Jackass3Label is the actual regression: the
// homelab disc had volume label `Jackass_3` (NOT `Jackass_Volume_3`),
// which normalises to "Jackass 3". Under the original tokeniser
// "3.5" split into ["3","5"] and the "3"→"three" fold made
// "Jackass 3.5" share two tokens with the query — tying with the
// correct "Jackass Volume Three" and letting popularity break the
// tie in favour of the wrong title.
func TestTitleSimilarity_Jackass3Label(t *testing.T) {
	q := "Jackass 3"
	if got := TitleSimilarity(q, "Jackass 3.5"); got >= TitleSimilarity(q, "Jackass Volume Three") {
		t.Errorf("Jackass 3.5 should NOT outscore Jackass Volume Three for query %q: 3.5=%v, Volume Three=%v",
			q, got, TitleSimilarity(q, "Jackass Volume Three"))
	}
}

func TestApplyRankConfidence_RanksJackassCandidatesCorrectly(t *testing.T) {
	// End-to-end: candidates in TMDB-popularity order; after the
	// similarity pass Volume Three lands at rank 0.
	cands := []state.Candidate{
		{Title: "Jackass 3.5", Confidence: 100, TMDBID: 65851},
		{Title: "Jackass Volume Three", Confidence: 12, TMDBID: 347115},
		{Title: "The Making of 'Jackass 3D'", Confidence: 8, TMDBID: 936730},
		{Title: "Jackass 3D", Confidence: 75, TMDBID: 16290},
	}
	applyRankConfidence(cands, "Jackass 3", "")
	if cands[0].Title != "Jackass Volume Three" {
		t.Errorf("top match: want Jackass Volume Three, got %q (full order: %v)",
			cands[0].Title, titlesOf(cands))
	}
	// Confidence is round(TitleSimilarity*100), not a fixed rank ladder:
	// query {jackass, three} vs title {jackass, volume, three} shares 2
	// of 3 union tokens = 67%, not a blind 100% — "Volume" is real
	// signal the old ladder threw away.
	if cands[0].Confidence != 67 {
		t.Errorf("rank-0 confidence: want 67, got %d", cands[0].Confidence)
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
	if TitleSimilarity(a, b) != TitleSimilarity(b, a) {
		t.Errorf("similarity is not symmetric: %v vs %v",
			TitleSimilarity(a, b), TitleSimilarity(b, a))
	}
}

func TestTitleSimilarity_DigitWordEquivalence(t *testing.T) {
	if got := TitleSimilarity("Volume 3", "Volume Three"); got != 1.0 {
		t.Errorf("3↔three should match: got %v", got)
	}
	if got := TitleSimilarity("Top 10 Hits", "Top Ten Hits"); got != 1.0 {
		t.Errorf("10↔ten should match: got %v", got)
	}
}

func TestTitleSimilarity_EmptyInputs(t *testing.T) {
	if got := TitleSimilarity("", ""); got != 1.0 {
		t.Errorf("both empty: want 1.0, got %v", got)
	}
	if got := TitleSimilarity("", "anything"); got != 0.0 {
		t.Errorf("one empty: want 0.0, got %v", got)
	}
	if got := TitleSimilarity("anything", ""); got != 0.0 {
		t.Errorf("other empty: want 0.0, got %v", got)
	}
}

func TestTitleSimilarity_CaseAndPunctuationInvariant(t *testing.T) {
	if got := TitleSimilarity("THE MATRIX", "the.matrix"); got != 1.0 {
		t.Errorf("case + punctuation: want 1.0, got %v", got)
	}
}
