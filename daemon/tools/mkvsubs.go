package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// MKVTrack is one track reported by `mkvmerge -J` for a Matroska file.
type MKVTrack struct {
	ID       int    // mkvmerge's track ID -- passed to mkvextract as "<ID>:<destPath>"
	Type     string // "video" | "audio" | "subtitles"
	CodecID  string // Matroska codec ID, e.g. "S_TEXT/UTF8", "S_HDMV/PGS"
	Language string // ISO 639-2, e.g. "eng", "jpn"; "und" when unset
}

// IsTextSubtitle reports whether the track is a text-based subtitle
// codec that mkvextract can pull out losslessly as a standalone
// sidecar file. Bitmap codecs (S_HDMV/PGS, S_VOBSUB) need OCR to
// become text and are left muxed in the video instead.
func (t MKVTrack) IsTextSubtitle() bool {
	return t.Type == "subtitles" && strings.HasPrefix(t.CodecID, "S_TEXT/")
}

// SidecarExt returns the file extension a sidecar for this track
// should use, derived from its Matroska codec ID.
func (t MKVTrack) SidecarExt() string {
	switch t.CodecID {
	case "S_TEXT/UTF8":
		return "srt"
	case "S_TEXT/ASS", "S_TEXT/SSA":
		return "ass"
	default:
		return "sub"
	}
}

// MKVToolNix wraps the mkvmerge (identify) and mkvextract (pull a
// track to a standalone file) binaries from the mkvtoolnix package.
type MKVToolNix struct {
	mergeBin   string
	extractBin string
}

// NewMKVToolNix returns an MKVToolNix. Empty bins default to
// "mkvmerge"/"mkvextract", resolved via PATH.
func NewMKVToolNix(mergeBin, extractBin string) *MKVToolNix {
	if mergeBin == "" {
		mergeBin = "mkvmerge"
	}
	if extractBin == "" {
		extractBin = "mkvextract"
	}
	return &MKVToolNix{mergeBin: mergeBin, extractBin: extractBin}
}

// Tracks runs `mkvmerge -J <path>` and returns its tracks.
func (m *MKVToolNix) Tracks(ctx context.Context, path string) ([]MKVTrack, error) {
	cmd := exec.CommandContext(ctx, m.mergeBin, "-J", path)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("mkvmerge -J: %w", err)
	}
	return ParseMKVMergeJSON(out)
}

// ExtractTrack runs `mkvextract tracks <srcPath> <trackID>:<destPath>`,
// pulling one track out to a standalone file.
func (m *MKVToolNix) ExtractTrack(ctx context.Context, srcPath string, trackID int, destPath string) error {
	cmd := exec.CommandContext(ctx, m.extractBin, "tracks", srcPath,
		fmt.Sprintf("%d:%s", trackID, destPath))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mkvextract: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// mkvmergeIdentify mirrors the subset of `mkvmerge -J` JSON this
// package reads. The real output carries many more fields; anything
// not listed here is ignored by json.Unmarshal.
type mkvmergeIdentify struct {
	Tracks []struct {
		ID         int    `json:"id"`
		Type       string `json:"type"`
		Properties struct {
			CodecID  string `json:"codec_id"`
			Language string `json:"language"`
		} `json:"properties"`
	} `json:"tracks"`
}

// ParseMKVMergeJSON extracts MKVTracks from `mkvmerge -J` stdout.
func ParseMKVMergeJSON(data []byte) ([]MKVTrack, error) {
	var doc mkvmergeIdentify
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse mkvmerge JSON: %w", err)
	}
	out := make([]MKVTrack, 0, len(doc.Tracks))
	for _, t := range doc.Tracks {
		lang := t.Properties.Language
		if lang == "" {
			lang = "und"
		}
		out = append(out, MKVTrack{
			ID:       t.ID,
			Type:     t.Type,
			CodecID:  t.Properties.CodecID,
			Language: lang,
		})
	}
	return out, nil
}
