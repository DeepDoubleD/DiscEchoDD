package pipelines

import "testing"

func TestClassifyExtraByDuration(t *testing.T) {
	cases := []struct {
		name    string
		seconds int
		want    ExtrasBucket
	}{
		{"zero-length", 0, BucketTrailer},
		{"short trailer", 90, BucketTrailer},
		{"upper trailer boundary", 300, BucketTrailer},
		{"just past trailer", 301, BucketFeaturette},
		{"mid featurette", 900, BucketFeaturette},
		{"upper featurette boundary", 1800, BucketFeaturette},
		{"just past featurette", 1801, BucketBehindTheScene},
		{"hour-long making-of", 3600, BucketBehindTheScene},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyExtraByDuration(tc.seconds)
			if got != tc.want {
				t.Fatalf("ClassifyExtraByDuration(%d) = %+v, want %+v",
					tc.seconds, got, tc.want)
			}
		})
	}
}

func TestClassifyExtraBySizeRatio(t *testing.T) {
	const main int64 = 4_000_000_000 // 4 GB main feature
	cases := []struct {
		name string
		size int64
		main int64
		want ExtrasBucket
	}{
		{"tiny trailer", 50_000_000, main, BucketTrailer},
		{"at trailer boundary (5%)", 200_000_000, main, BucketTrailer},
		{"just past trailer", 220_000_000, main, BucketFeaturette},
		{"mid featurette (15%)", 600_000_000, main, BucketFeaturette},
		{"at featurette boundary (25%)", 1_000_000_000, main, BucketFeaturette},
		{"just past featurette", 1_100_000_000, main, BucketBehindTheScene},
		{"long doc (50%)", 2_000_000_000, main, BucketBehindTheScene},
		{"main size zero falls back to featurette", 500_000_000, 0, BucketFeaturette},
		{"negative main also falls back", 500_000_000, -1, BucketFeaturette},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyExtraBySizeRatio(tc.size, tc.main)
			if got != tc.want {
				t.Fatalf("ClassifyExtraBySizeRatio(%d, %d) = %+v, want %+v",
					tc.size, tc.main, got, tc.want)
			}
		})
	}
}
