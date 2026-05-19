package identify

import (
	"regexp"
	"strings"
)

// titleSimilarity returns a 0..1 Jaccard similarity between two title
// strings, computed over their lowercased word tokens. Used to rank
// TMDB search results by how well each candidate's title actually
// matches the disc-label-derived query, instead of letting TMDB's
// popularity field decide. Returns 1.0 when both inputs are empty
// (degenerate but harmless), 0.0 when either is empty but the other
// isn't.
//
// Tokenisation: split on non-alphanumeric runes, lowercase, strip
// empties. Digit tokens 1..10 are folded to their English word form
// ("3" ↔ "three") so a "Volume 3" / "Volume Three" pair matches.
func titleSimilarity(query, title string) float64 {
	a := tokenSet(tokenise(query))
	b := tokenSet(tokenise(title))
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}
	intersect := 0
	for t := range a {
		if _, ok := b[t]; ok {
			intersect++
		}
	}
	union := len(a) + len(b) - intersect
	return float64(intersect) / float64(union)
}

func tokenSet(toks []string) map[string]struct{} {
	out := make(map[string]struct{}, len(toks))
	for _, t := range toks {
		out[t] = struct{}{}
	}
	return out
}

var digitWord = map[string]string{
	"0": "zero", "1": "one", "2": "two", "3": "three", "4": "four",
	"5": "five", "6": "six", "7": "seven", "8": "eight", "9": "nine",
	"10": "ten",
}

// tokenRE matches an alnum run, optionally followed by `.digits` runs
// so decimal numbers like "3.5" stay as a single token. Otherwise the
// "3" half folds to "three" via digitWord and matches a query "3" —
// which makes the wrong title win when the disc label is "Jackass 3"
// and TMDB returns both "Jackass 3.5" and "Jackass Volume Three".
var tokenRE = regexp.MustCompile(`[a-z0-9]+(?:\.[0-9]+)*`)

func tokenise(s string) []string {
	s = strings.ToLower(s)
	fields := tokenRE.FindAllString(s, -1)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if w, ok := digitWord[f]; ok {
			out = append(out, w)
			continue
		}
		out = append(out, f)
	}
	return out
}
