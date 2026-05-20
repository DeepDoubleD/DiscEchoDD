package pipelines

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// NotifyMessage is a fully-rendered notification ready for apprise. Body is
// light Markdown so rich services (Discord/Telegram/Slack/ntfy) render it
// while plain-text targets stay readable. Trigger selects the subscriber set
// ("done" | "failed").
type NotifyMessage struct {
	Title       string
	Body        string
	Attachments []string
	Trigger     string
}

// BuildSuccessNotification renders the media-type-aware "ripped" message from
// the rows already loaded at a job's finalise point. job carries OutputBytes /
// timing / step notes (persisted before the handler returns); disc carries the
// type + metadata_json; prof carries engine/codec/container.
func BuildSuccessNotification(disc *state.Disc, job *state.Job, prof *state.Profile) NotifyMessage {
	meta := parseNotifyMeta(disc)
	var b notifyBody
	paths := moveOutputPaths(job)
	size := ""
	if job.OutputBytes > 0 {
		size = HumanBytes(job.OutputBytes)
	}
	dur := ""
	if d, ok := notifyElapsed(job); ok && d > 0 {
		dur = HumanDuration(d)
	}
	saved := savedToValue(paths)

	switch disc.Type {
	case state.DiscTypeAudioCD:
		artist := firstNonEmpty(metaStr(meta, "artist"), candidateArtist(disc))
		b.headline(joinDash(artist, mdEscape(disc.Title)), "ripped successfully")
		b.line("Type", engineFormat(disc, prof))
		b.line("Tracks", audioTrackCount(meta, job))
		b.line("AccurateRip", accurateRipSummary(job))
		b.line("Label", joinMid(metaStr(meta, "label"), metaStr(meta, "catalog_number")))
		b.line("Duration", dur)
		b.line("Size", size)
		b.line("Saved to", saved)

	case state.DiscTypePSX, state.DiscTypePS2, state.DiscTypeSAT,
		state.DiscTypeDC, state.DiscTypeXBOX:
		b.headline(titleYear(mdEscape(disc.Title), disc.Year), "ripped successfully")
		b.line("System", firstNonEmpty(metaStr(meta, "system"), discTypeLabel(disc.Type)))
		b.line("Region", candidateRegion(disc))
		b.line("Format", prof.Container)
		b.line("Size", size)
		b.line("Duration", dur)
		b.line("Saved to", saved)

	case state.DiscTypeData:
		b.headline(mdEscape(disc.Title), "archived successfully")
		b.line("Type", engineFormat(disc, prof))
		b.line("Files", fileCount(job, paths, "file"))
		b.line("Size", size)
		b.line("Saved to", saved)

	default: // DVD / BDMV / UHD — movie or series
		if isSeries(disc, meta) {
			season := SelectedSeasonFromDisc(disc)
			head := mdEscape(disc.Title)
			if season > 0 {
				head = fmt.Sprintf("%s — Season %d", head, season)
			}
			b.headline(head, "ripped successfully")
			b.line("Type", engineFormat(disc, prof))
			b.line("Episodes", episodeSummary(disc, paths))
			b.line("Duration", dur)
			b.line("Size", size)
			b.line("Saved to", saved)
		} else {
			b.headline(titleYear(mdEscape(disc.Title), disc.Year), "ripped successfully")
			b.line("Type", engineFormat(disc, prof))
			b.line("Duration", dur)
			b.line("Size", size)
			b.line("Files", fileCount(job, paths, "title"))
			b.line("Director", mdEscape(metaStr(meta, "director")))
			b.line("Saved to", saved)
		}
	}

	return NotifyMessage{
		Title:       successTitle(disc),
		Body:        b.String(),
		Attachments: pickAttachment(disc, meta),
		Trigger:     "done",
	}
}

// BuildFailureNotification renders the failure message for a failed job.
func BuildFailureNotification(disc *state.Disc, job *state.Job, prof *state.Profile) NotifyMessage {
	var b notifyBody
	b.headline(titleYear(mdEscape(disc.Title), disc.Year), "failed to rip")
	b.line("Failed at", failedStepLabel(job))
	b.line("Error", mdEscape(truncate(job.ErrorMessage, 500)))
	b.line("Type", discTypeLabel(disc.Type))

	return NotifyMessage{
		Title:   fmt.Sprintf("DiscEcho: %s — rip failed", disc.Title),
		Body:    b.String(),
		Trigger: "failed",
	}
}

// --- body assembly ---------------------------------------------------------

type notifyBody struct{ sb strings.Builder }

func (b *notifyBody) headline(subject, verb string) {
	if subject == "" {
		subject = "Disc"
	}
	fmt.Fprintf(&b.sb, "**%s** %s.\n\n", subject, verb)
}

func (b *notifyBody) line(label, val string) {
	if strings.TrimSpace(val) == "" {
		return
	}
	fmt.Fprintf(&b.sb, "- **%s:** %s\n", label, val)
}

func (b *notifyBody) String() string { return strings.TrimRight(b.sb.String(), "\n") }

// --- field helpers ---------------------------------------------------------

func successTitle(disc *state.Disc) string {
	if disc.Title == "" {
		return "DiscEcho: disc ripped"
	}
	return fmt.Sprintf("DiscEcho: %s ripped", disc.Title)
}

func discTypeLabel(t state.DiscType) string {
	switch t {
	case state.DiscTypeAudioCD:
		return "Audio CD"
	case state.DiscTypeDVD:
		return "DVD"
	case state.DiscTypeBDMV:
		return "Blu-ray"
	case state.DiscTypeUHD:
		return "4K UHD Blu-ray"
	case state.DiscTypePSX:
		return "PlayStation"
	case state.DiscTypePS2:
		return "PlayStation 2"
	case state.DiscTypeSAT:
		return "Sega Saturn"
	case state.DiscTypeDC:
		return "Dreamcast"
	case state.DiscTypeXBOX:
		return "Xbox"
	case state.DiscTypeData:
		return "Data disc"
	default:
		return string(t)
	}
}

// engineFormat renders "<DiscType> · <Engine> (<codec, >container)".
func engineFormat(disc *state.Disc, prof *state.Profile) string {
	if prof == nil {
		return discTypeLabel(disc.Type)
	}
	var fmtParts []string
	if c := prof.VideoCodec; c != "" && c != "copy" {
		fmtParts = append(fmtParts, c)
	}
	if prof.Container != "" {
		fmtParts = append(fmtParts, prof.Container)
	}
	out := discTypeLabel(disc.Type)
	if prof.Engine != "" {
		out += " · " + prof.Engine
	}
	if len(fmtParts) > 0 {
		out += " (" + strings.Join(fmtParts, ", ") + ")"
	}
	return out
}

func isSeries(disc *state.Disc, meta map[string]any) bool {
	if len(SelectedEpisodeMapFromDisc(disc)) > 0 {
		return true
	}
	return metaStr(meta, "media_type") == "tv"
}

func episodeSummary(disc *state.Disc, paths []string) string {
	epMap := SelectedEpisodeMapFromDisc(disc)
	count := len(epMap)
	if count == 0 {
		count = len(paths)
	}
	if count == 0 {
		return ""
	}
	nums := make([]int, 0, len(epMap))
	for _, ea := range epMap {
		if ea.Episode > 0 {
			nums = append(nums, ea.Episode)
		}
	}
	if len(nums) >= 2 {
		sort.Ints(nums)
		return fmt.Sprintf("%d (E%02d–E%02d)", count, nums[0], nums[len(nums)-1])
	}
	return fmt.Sprintf("%d", count)
}

func fileCount(job *state.Job, paths []string, noun string) string {
	n := len(paths)
	if n == 0 {
		if c := ripFileCount(job); c > 0 {
			n = c
		}
	}
	if n <= 0 {
		return ""
	}
	plural := noun + "s"
	if n == 1 {
		plural = noun
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func ripFileCount(job *state.Job) int {
	if v, ok := stepNote(job, state.StepRip, "rip_file_count").(float64); ok {
		return int(v)
	}
	return 0
}

func audioTrackCount(meta map[string]any, job *state.Job) string {
	if tracks, ok := meta["tracks"].([]any); ok && len(tracks) > 0 {
		return fmt.Sprintf("%d", len(tracks))
	}
	if notes := stepNotes(job, state.StepRip); notes != nil {
		if ar, ok := notes["accuraterip"].(map[string]any); ok {
			if t, ok := ar["total_tracks"].(float64); ok && t > 0 {
				return fmt.Sprintf("%d", int(t))
			}
		}
	}
	return ""
}

func accurateRipSummary(job *state.Job) string {
	notes := stepNotes(job, state.StepRip)
	if notes == nil {
		return ""
	}
	ar, ok := notes["accuraterip"].(map[string]any)
	if !ok {
		return ""
	}
	status, _ := ar["status"].(string)
	if status == "" {
		return ""
	}
	verified, _ := ar["verified_tracks"].(float64)
	total, _ := ar["total_tracks"].(float64)
	if total > 0 {
		return fmt.Sprintf("%s (%d/%d)", status, int(verified), int(total))
	}
	return status
}

func candidateRegion(disc *state.Disc) string {
	if len(disc.Candidates) > 0 {
		return disc.Candidates[0].Region
	}
	return ""
}

func candidateArtist(disc *state.Disc) string {
	if len(disc.Candidates) > 0 {
		return disc.Candidates[0].Artist
	}
	return ""
}

// pickAttachment returns the cover/poster image URL for the disc, or nil.
func pickAttachment(disc *state.Disc, meta map[string]any) []string {
	var url string
	switch disc.Type {
	case state.DiscTypeAudioCD:
		url = metaStr(meta, "cover_url")
		if url == "" {
			if mbid := metaStr(meta, "release_group_mbid"); mbid != "" {
				url = "https://coverartarchive.org/release-group/" + mbid + "/front-500"
			}
		}
	case state.DiscTypePSX, state.DiscTypePS2, state.DiscTypeSAT,
		state.DiscTypeDC, state.DiscTypeXBOX:
		url = metaStr(meta, "cover_url")
	default:
		url = metaStr(meta, "poster_url")
	}
	url = strings.TrimSpace(url)
	if url == "" || strings.HasPrefix(url, "-") {
		return nil
	}
	return []string{url}
}

func failedStepLabel(job *state.Job) string {
	for _, s := range job.Steps {
		if s.State == state.JobStepStateFailed {
			return stepLabel(s.Step)
		}
	}
	if job.ActiveStep != "" {
		return stepLabel(job.ActiveStep)
	}
	return ""
}

func stepLabel(s state.StepID) string {
	switch s {
	case state.StepRip:
		return "rip"
	case state.StepTranscode:
		return "transcode"
	case state.StepMove:
		return "move"
	default:
		return string(s)
	}
}

// --- generic helpers --------------------------------------------------------

func parseNotifyMeta(disc *state.Disc) map[string]any {
	if disc == nil || disc.MetadataJSON == "" || disc.MetadataJSON == "{}" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(disc.MetadataJSON), &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func metaStr(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func stepNotes(job *state.Job, step state.StepID) map[string]any {
	for _, s := range job.Steps {
		if s.Step == step {
			return s.Notes
		}
	}
	return nil
}

func stepNote(job *state.Job, step state.StepID, key string) any {
	if n := stepNotes(job, step); n != nil {
		return n[key]
	}
	return nil
}

// moveOutputPaths extracts the library output path(s) from the move step.
func moveOutputPaths(job *state.Job) []string {
	notes := stepNotes(job, state.StepMove)
	if notes == nil {
		return nil
	}
	if p, ok := notes["path"].(string); ok && p != "" {
		return []string{p}
	}
	if arr, ok := notes["paths"].([]any); ok {
		out := make([]string, 0, len(arr))
		for _, v := range arr {
			if s, ok := v.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func savedToValue(paths []string) string {
	switch len(paths) {
	case 0:
		return ""
	case 1:
		return "`" + paths[0] + "`"
	default:
		return fmt.Sprintf("`%s` (%d files)", filepath.Dir(paths[0]), len(paths))
	}
}

func notifyElapsed(job *state.Job) (time.Duration, bool) {
	if job.StartedAt != nil && job.FinishedAt != nil {
		return job.FinishedAt.Sub(*job.StartedAt), true
	}
	if job.ElapsedSeconds > 0 {
		return time.Duration(job.ElapsedSeconds) * time.Second, true
	}
	if job.StartedAt != nil {
		return time.Since(*job.StartedAt), true
	}
	return 0, false
}

func titleYear(title string, year int) string {
	if title == "" {
		return ""
	}
	if year > 0 {
		return fmt.Sprintf("%s (%d)", title, year)
	}
	return title
}

func joinDash(a, b string) string {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + " — " + b
}

func joinMid(a, b string) string {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + " · " + b
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// mdEscape escapes the Markdown control characters most likely to appear in a
// disc title or person name (e.g. "[REC]", "*batteries not included") so they
// don't corrupt the rendered message. Kept narrow to avoid uglifying titles.
func mdEscape(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		"`", "\\`",
		"*", `\*`,
		"_", `\_`,
		"[", `\[`,
		"]", `\]`,
	)
	return r.Replace(s)
}
