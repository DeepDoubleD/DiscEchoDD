package api

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/settings"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// HostInfo is the payload for GET /api/system/host. Disks lists at most
// one entry per resolvable mount; missing paths are skipped silently
// instead of erroring the whole response.
type HostInfo struct {
	Hostname      string     `json:"hostname"`
	Kernel        string     `json:"kernel"`
	CPUCount      int        `json:"cpu_count"`
	UptimeSeconds int64      `json:"uptime_seconds"`
	Disks         []DiskInfo `json:"disks"`
}

type DiskInfo struct {
	// Path is one configured library root that lives on this filesystem.
	// When several configured roots share the same underlying mount
	// (typical Unraid / single-array setups) only the first is returned;
	// the rest are listed in Paths to avoid five identical bars in the
	// UI.
	Path           string   `json:"path"`
	Paths          []string `json:"paths,omitempty"`
	TotalBytes     uint64   `json:"total_bytes"`
	UsedBytes      uint64   `json:"used_bytes"`
	AvailableBytes uint64   `json:"available_bytes"`
}

// IntegrationsInfo is the payload for GET /api/system/integrations.
// The TMDB key itself is never returned — only whether one is set.
//
// The legacy TMDB / MusicBrainz / Apprise objects are kept alongside
// Items for one release so existing clients (mobile read-only view,
// older webui) keep working. New UI code should prefer Items.
type IntegrationsInfo struct {
	TMDB           TMDBIntegration        `json:"tmdb"`
	MusicBrainz    MusicBrainzIntegration `json:"musicbrainz"`
	Apprise        AppriseIntegration     `json:"apprise"`
	LibraryRoots   map[string]string      `json:"library_roots,omitempty"`
	Items          []IntegrationStatus    `json:"items,omitempty"`
	BootCodeCounts map[state.DiscType]int `json:"boot_code_counts,omitempty"`
	GameDiscs      *GameDiscsInfo         `json:"game_discs,omitempty"`
}

// GameDiscsInfo is the per-system identification-coverage breakdown shown in
// the Settings → API keys & connections panel. Game-disc identification uses
// two sources: built-in boot-code maps (pre-rip naming) and operator-supplied
// Redump .dat files (post-rip MD5 verification). Status reflects dat coverage
// so the tile doesn't look fully configured when dats are missing.
type GameDiscsInfo struct {
	RedumperStatus string           `json:"redumper_status"` // connected | not configured | error: …
	RedumperBin    string           `json:"redumper_bin"`
	DatDir         string           `json:"dat_dir"`
	Status         string           `json:"status"` // connected | partial | error: …
	Systems        []GameDiscSystem `json:"systems"`
}

// GameDiscSystem is one row of the game-disc coverage table.
type GameDiscSystem struct {
	System        state.DiscType `json:"system"`
	Label         string         `json:"label"`
	Subdir        string         `json:"subdir"`    // Redump folder name under DatDir (e.g. "psx", "dc")
	BootCode      string         `json:"boot_code"` // ok | missing | na
	BootCodeCount int            `json:"boot_code_count"`
	RedumpDat     string         `json:"redump_dat"` // loaded | missing
}

// IntegrationStatus is a single row in the connections list. Status
// values: "connected", "not configured", or "error: <detail>". Detail
// is a free-form short string ("topic: homelab-disc", "v1.7.0", etc).
// Editable is the env var an operator would change to set this up;
// empty when the row is configured indirectly (e.g. via Apprise URLs).
type IntegrationStatus struct {
	Name     string    `json:"name"`
	Hint     string    `json:"hint,omitempty"`
	Status   string    `json:"status"`
	Detail   string    `json:"detail,omitempty"`
	Editable string    `json:"editable,omitempty"`
	SubItems []SubItem `json:"sub_items,omitempty"`
}

// SubItem renders as an indented status line under a tile. Used for the
// per-system breakdown under the Game discs tile.
type SubItem struct {
	Label  string `json:"label"`
	Status string `json:"status"` // "ok" | "missing" | "error" | "partial"
	Detail string `json:"detail,omitempty"`
}

type TMDBIntegration struct {
	Configured bool   `json:"configured"`
	Language   string `json:"language"`
}

type MusicBrainzIntegration struct {
	BaseURL   string `json:"base_url"`
	UserAgent string `json:"user_agent"`
}

type AppriseIntegration struct {
	Bin     string `json:"bin"`
	Version string `json:"version,omitempty"`
}

// GetSystemHost returns kernel/CPU/uptime + disk usage for the paths
// the daemon writes to.
func (h *Handlers) GetSystemHost(w http.ResponseWriter, r *http.Request) {
	info := HostInfo{
		Hostname: readTrim("/proc/sys/kernel/hostname"),
		Kernel:   readTrim("/proc/sys/kernel/osrelease"),
		CPUCount: runtime.NumCPU(),
	}
	if up, ok := readUptime("/proc/uptime"); ok {
		info.UptimeSeconds = up
	}
	// Group statfs results by filesystem-identity key so a single
	// underlying mount only produces one bar in the UI even when several
	// library paths point to it.
	type bucket struct {
		idx   int
		paths []string
	}
	buckets := map[uint64]*bucket{}
	for _, p := range hostDiskPaths(h.Settings) {
		if p == "" {
			continue
		}
		d, key, ok := statDiskWithKey(p)
		if !ok {
			continue
		}
		if b, seen := buckets[key]; seen {
			info.Disks[b.idx].Paths = append(info.Disks[b.idx].Paths, p)
			continue
		}
		info.Disks = append(info.Disks, d)
		buckets[key] = &bucket{idx: len(info.Disks) - 1, paths: nil}
	}
	writeJSON(w, http.StatusOK, info)
}

// GetSystemIntegrations returns connection status + non-secret config
// for external integrations (TMDB, MusicBrainz, Apprise).
func (h *Handlers) GetSystemIntegrations(w http.ResponseWriter, r *http.Request) {
	info := IntegrationsInfo{
		TMDB: TMDBIntegration{Configured: false},
		MusicBrainz: MusicBrainzIntegration{
			BaseURL:   "https://musicbrainz.org",
			UserAgent: "DiscEcho",
		},
		Apprise: AppriseIntegration{Bin: "apprise"},
	}
	if h.Settings != nil {
		info.TMDB.Configured = strings.TrimSpace(h.Settings.TMDBKey) != ""
		info.TMDB.Language = h.Settings.TMDBLang
		info.MusicBrainz.BaseURL = h.Settings.MusicBrainzBaseURL
		info.MusicBrainz.UserAgent = h.Settings.MusicBrainzUserAgent
		info.Apprise.Bin = h.Settings.AppriseBin
		info.LibraryRoots = h.Settings.LibraryRootsMap()
	}
	if v, ok := appriseVersion(r.Context(), info.Apprise.Bin); ok {
		info.Apprise.Version = v
	}
	if h.BootCodeIndex != nil {
		info.BootCodeCounts = h.BootCodeIndex.Counts()
	}
	info.GameDiscs = h.buildGameDiscsInfo()
	info.Items = h.buildIntegrationItems(r.Context(), info)
	writeJSON(w, http.StatusOK, info)
}

// buildIntegrationItems composes the connections list shown on the
// Settings → System tab. Order: MusicBrainz, Game discs, Apprise,
// GPU transcoding. TMDB and IGDB are owned by /api/integrations.
func (h *Handlers) buildIntegrationItems(ctx context.Context, info IntegrationsInfo) []IntegrationStatus {
	items := []IntegrationStatus{
		{
			Name:   "MusicBrainz",
			Hint:   "audio CD metadata + AccurateRip",
			Status: "connected",
			Detail: info.MusicBrainz.BaseURL,
		},
		{
			Name:   "Apprise",
			Hint:   "notification dispatch",
			Status: appriseStatus(ctx, h, info),
			Detail: info.Apprise.Version,
		},
		{
			Name:   "GPU transcoding",
			Hint:   "NVIDIA NVENC hardware encoder",
			Status: gpuStatus(h.NVENCAvailable),
			Detail: gpuDetail(h.NVENCAvailable),
		},
	}
	return items
}

func redumpStatus(s *settings.Settings) string {
	if s == nil || strings.TrimSpace(s.RedumperBin) == "" {
		return "not configured"
	}
	if _, err := exec.LookPath(s.RedumperBin); err != nil {
		return "error: redumper binary not found on PATH"
	}
	return "connected"
}

// gameDiscSystems is the single source of truth for the coverage table:
// display order, human label, and the on-disk Redump subdirectory name. The
// subdir is both globbed by redumpDatInventory and shown in the UI so the
// folder a user must create can never diverge from the folder we look in.
var gameDiscSystems = []struct {
	sys    state.DiscType
	label  string
	subdir string
}{
	{state.DiscTypePSX, "PlayStation", "psx"},
	{state.DiscTypePS2, "PlayStation 2", "ps2"},
	{state.DiscTypeSAT, "Saturn", "saturn"},
	{state.DiscTypeDC, "Dreamcast", "dc"},
	{state.DiscTypeXBOX, "Xbox", "xbox"},
	{state.DiscTypeSegaCD, "Sega CD", "sega-cd"},
	{state.DiscType3DO, "3DO", "3do"},
	{state.DiscTypePCFX, "PC-FX", "pc-fx"},
	{state.DiscTypeJaguarCD, "Atari Jaguar CD", "jaguar-cd"},
	{state.DiscTypeCDi, "Philips CD-i", "cdi"},
	{state.DiscTypePCECD, "PC Engine CD", "pc-engine-cd"},
	{state.DiscTypeNeoCD, "Neo Geo CD", "neo-geo-cd"},
}

// redumpDatInventory returns the per-system count of *.dat files under
// the Redump root directory. Used by the Settings → System tile to
// show which Redump dats are loaded vs missing.
func redumpDatInventory(rootDir string) map[state.DiscType]int {
	out := make(map[state.DiscType]int, len(gameDiscSystems))
	for _, gs := range gameDiscSystems {
		matches, _ := filepath.Glob(filepath.Join(rootDir, gs.subdir, "*.dat"))
		out[gs.sys] = len(matches)
	}
	return out
}

// buildGameDiscsInfo assembles the per-system identification-coverage view:
// built-in boot-code maps (pre-rip) + on-disk Redump dat presence (post-rip
// verify). On-disk presence is what's reported — dats load at boot, so a file
// added afterward needs a restart (the UI says so).
func (h *Handlers) buildGameDiscsInfo() *GameDiscsInfo {
	info := &GameDiscsInfo{}
	var inv map[state.DiscType]int
	if h.Settings != nil {
		info.RedumperStatus = redumpStatus(h.Settings)
		info.RedumperBin = h.Settings.RedumperBin
		info.DatDir = h.Settings.RedumpDataDir
		inv = redumpDatInventory(h.Settings.RedumpDataDir)
	}
	var counts map[state.DiscType]int
	if h.BootCodeIndex != nil {
		counts = h.BootCodeIndex.Counts()
	}

	for _, gs := range gameDiscSystems {
		row := GameDiscSystem{System: gs.sys, Label: gs.label, Subdir: gs.subdir}
		// Boot-code: Xbox and the Tier-1 CD consoles have no boot-code index
		// (Xbox uses publisher codes, not XBE IDs; the CD consoles are MD5-only).
		switch {
		case gs.sys == state.DiscTypeXBOX ||
			gs.sys == state.DiscTypeSegaCD ||
			gs.sys == state.DiscType3DO ||
			gs.sys == state.DiscTypePCFX ||
			gs.sys == state.DiscTypeJaguarCD ||
			gs.sys == state.DiscTypeCDi ||
			gs.sys == state.DiscTypePCECD ||
			gs.sys == state.DiscTypeNeoCD:
			row.BootCode = "na"
		case counts[gs.sys] > 0:
			row.BootCode = "ok"
			row.BootCodeCount = counts[gs.sys]
		default:
			row.BootCode = "missing"
		}
		if inv[gs.sys] > 0 {
			row.RedumpDat = "loaded"
		} else {
			row.RedumpDat = "missing"
		}
		info.Systems = append(info.Systems, row)
	}

	// Top-level status reflects Redump dat coverage so the tile stops looking
	// fully configured when dats are missing (pre-rip boot-code ID still works).
	switch {
	case info.RedumperStatus != "" && strings.HasPrefix(info.RedumperStatus, "error:"):
		info.Status = info.RedumperStatus
	default:
		info.Status = combinedStatus(inv) // ok → connected, else partial/missing
		if info.Status == "ok" {
			info.Status = "connected"
		} else {
			info.Status = "partial"
		}
	}
	return info
}

func combinedStatus(counts map[state.DiscType]int) string {
	hasAny := false
	all := true
	for _, n := range counts {
		if n > 0 {
			hasAny = true
		} else {
			all = false
		}
	}
	switch {
	case all:
		return "ok"
	case hasAny:
		return "partial"
	default:
		return "missing"
	}
}

func gpuStatus(available bool) string {
	if available {
		return "connected"
	}
	return "not configured"
}

func gpuDetail(available bool) string {
	if available {
		return "NVENC (h264, h265)"
	}
	return "no NVIDIA GPU detected"
}

func appriseStatus(ctx context.Context, h *Handlers, info IntegrationsInfo) string {
	if h == nil || h.Store == nil {
		return "not configured"
	}
	notifs, err := h.Store.ListNotifications(ctx)
	if err != nil {
		return "error: " + err.Error()
	}
	enabled := 0
	for _, n := range notifs {
		if n.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return "no URLs configured"
	}
	if info.Apprise.Version == "" {
		return "error: apprise binary missing or unresponsive"
	}
	return "connected"
}

func hostDiskPaths(s *settings.Settings) []string {
	data := "/var/lib/discecho"
	roots := []string{"/library"}
	if s != nil {
		if s.DataPath != "" {
			data = s.DataPath
		}
		// Stat each unique typed root, falling back to LibraryRoot when
		// none are populated (e.g. handler called pre-Load).
		seen := map[string]bool{}
		var unique []string
		for _, m := range settings.AllMediaRoots {
			p := s.LibraryFor(m)
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			unique = append(unique, p)
		}
		if len(unique) > 0 {
			roots = unique
		} else if s.LibraryRoot != "" {
			roots = []string{s.LibraryRoot}
		}
	}
	return append(roots, data)
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readUptime(path string) (int64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0, false
	}
	f, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	return int64(f), true
}

// statDiskWithKey returns the DiskInfo and a filesystem-identity key
// usable to dedupe several configured paths that resolve to the same
// underlying mount. Same Type+Bsize+Blocks composite as
// libraryFSBytes uses — Fsid layout isn't portable.
func statDiskWithKey(path string) (DiskInfo, uint64, bool) {
	if _, err := os.Stat(path); err != nil {
		return DiskInfo{}, 0, false
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return DiskInfo{}, 0, false
	}
	bs := uint64(st.Bsize)
	total := st.Blocks * bs
	avail := st.Bavail * bs
	used := uint64(0)
	if total > avail {
		used = total - avail
	}
	key := uint64(st.Type)<<48 | uint64(st.Bsize)<<16 | uint64(st.Blocks)
	return DiskInfo{
		Path:           path,
		TotalBytes:     total,
		UsedBytes:      used,
		AvailableBytes: avail,
	}, key, true
}

// appriseVersion shells out with a tight timeout and returns the
// trimmed first line. Best-effort — failures omit the version field.
func appriseVersion(ctx context.Context, bin string) (string, bool) {
	if bin == "" {
		return "", false
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin, "--version").CombinedOutput()
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if line == "" {
		return "", false
	}
	return line, true
}

// SpoolInfo is the GET /api/system/spool payload. UsageBytes is the
// current size of the spool root (cached 5s in the spool package);
// CapBytes is the configured soft cap; Blocked is true when usage
// has reached the cap and the per-drive worker is pausing new rips.
type SpoolInfo struct {
	UsageBytes int64 `json:"usage_bytes"`
	CapBytes   int64 `json:"cap_bytes"`
	Blocked    bool  `json:"blocked"`
}

// GetSystemSpool returns spool usage + cap for the dashboard / settings
// widget. Cheap — spool.UsageBytes uses a 5s cache, so this is safe
// to poll at SSE-tick frequency.
func (h *Handlers) GetSystemSpool(w http.ResponseWriter, r *http.Request) {
	if h.Spool == nil {
		writeJSON(w, http.StatusOK, SpoolInfo{})
		return
	}
	cap := spoolCapBytes(h.Store)
	usage, err := h.Spool.UsageBytes(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, SpoolInfo{
		UsageBytes: usage,
		CapBytes:   cap,
		Blocked:    cap > 0 && usage >= cap,
	})
}

// spoolCapBytes reads spool.cap_bytes from the settings table. Missing
// row or unparseable value falls back to the migration default
// (100 GiB). Shared with the orchestrator's CapBytesFunc closure in
// main.go so the same default lives in one logical place.
func spoolCapBytes(store *state.Store) int64 {
	const def int64 = 100 * 1024 * 1024 * 1024
	if store == nil {
		return def
	}
	v, err := store.GetSetting(context.Background(), "spool.cap_bytes")
	if err != nil || v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
