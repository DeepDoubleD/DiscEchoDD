package state

import (
	"strings"
	"testing"
)

func TestDriveErrorTip(t *testing.T) {
	tests := []struct {
		name   string
		errMsg string
		want   []string // substrings that must all appear; empty = expect ""
	}{
		{
			name:   "empty",
			errMsg: "",
			want:   nil,
		},
		{
			name:   "cd-info exit surfaces Kreon prominently",
			errMsg: "classify: cd-info: exit status 1",
			want:   []string{"Kreon", "https://kreon.dev", "dirty", "eject and re-insert"},
		},
		{
			name:   "deadline exceeded",
			errMsg: "identify: context deadline exceeded",
			want:   []string{"slowly"},
		},
		{
			name:   "makemkv key error",
			errMsg: "rip: makemkvcon: evaluation period has expired",
			want:   []string{"Integrations", "MakeMKV"},
		},
		{
			name:   "unknown error has no tip",
			errMsg: "some unrelated failure",
			want:   nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DriveErrorTip(tt.errMsg)
			if len(tt.want) == 0 {
				if got != "" {
					t.Fatalf("want empty tip, got %q", got)
				}
				return
			}
			for _, sub := range tt.want {
				if !strings.Contains(got, sub) {
					t.Errorf("tip missing %q\ngot: %q", sub, got)
				}
			}
		})
	}
}
