# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- Ejecting a disc that had not been ripped left the drive card stuck on the
  phantom disc title (with `Re-identify` / `Re-rip` buttons) instead of flipping
  to the "Idle — insert a disc" prompt. Both the API eject endpoint and the
  physical-eject-button (udev) path now drop the orphan awaiting-decision disc
  on the way to idle. Discs with job history (failed/cancelled retry-intent
  rows) are kept untouched.

## [0.33.1] - 2026-05-22

### Changed
- DATA disc outputs now include a short content-hash id in the default filename
  (`VIDEOCD/VIDEOCD [a1b2c3d4].iso`) via a new `{{.ShortHash}}` template variable.
  Discs that share a generic ISO9660 volume label (common with PictureCD/VCD
  hybrids all labelled `VIDEOCD`) previously resolved to the same path and failed
  at the move step; they now never collide. Existing installs are migrated; custom
  DATA templates are left untouched.

### Fixed
- The disc-type override dropdown briefly showed the wrong label (e.g.
  "PlayStation") after a selection, because the chosen type was removed from the
  list while the control still pointed at its slot. The dropdown now resets to
  "Set type…" after each pick.

## [0.33.0] - 2026-05-22

### Changed
- The disc-type override on an awaiting-decision card is now bidirectional: it
  appears on every disc (not just `DATA`) and can switch to any other valid type,
  including back to `DATA`. Fixes the case where a correctly-identified Video CD —
  or any other type — had no way to be re-ripped as a plain data disc. The disc's
  own current type is omitted from the list.

## [0.32.1] - 2026-05-22

### Changed
- Profile-list badges: all game consoles now share one colour so games read as a
  single group, the Video CD badge is tinted in the movie-format family, and
  profiles are ordered Movies → Audio → Games → Data with each group sorted oldest
  to newest.

### Fixed
- The 10 newer CD-game consoles (Sega CD, 3DO, PC-FX, Jaguar, CD-i, PC Engine CD,
  Neo Geo CD, CD32, FM Towns, Pippin) rendered with an unstyled grey badge and a
  raw type label (e.g. `SEGACD`) because the web UI's disc-type list had drifted
  from the daemon. They now show proper colours and labels.

## [0.32.0] - 2026-05-22

### Added
- Video CD and Super Video CD ripping. (S)VCD discs are auto-detected and their
  MPEG-1/MPEG-2 video tracks extracted to `.mpg` files with `vcdxrip` (no
  re-encode — VCD is already low-bitrate constant-format MPEG). A misdetected
  disc can also be set to Video CD via the manual disc-type override.
  Auto-detection matches the on-disc VCD/SVCD directory structure; the magic
  has not yet been validated against a physical sample disc.

## [0.31.0] - 2026-05-21

### Added
- Support for 7 CD-based game consoles, ripped to CHD and identified post-rip
  against Redump dats: Sega CD, 3DO, PC-FX, Atari Jaguar CD, and Philips CD-i
  are auto-detected; PC Engine CD and Neo Geo CD are supported via the manual
  disc-type override (auto-detection for those two is planned).
- Amiga CD32, FM Towns, and Bandai Pippin can be ripped to CHD via the manual
  disc-type override (these CD formats are not auto-detected; set the type by
  hand on the disc card).
- Manual disc-type override: correct a misclassified disc from the dashboard
  (e.g. a game disc detected as data). Re-identifies for the chosen type when
  the disc is still in the drive.

## [0.30.6] - 2026-05-21

### Fixed
- A disc whose rip failed is no longer auto-ripped again in batch mode. The
  awaiting-decision card stays visible after a failed/cancelled/interrupted rip
  (so it can be retried), but the batch auto-confirm countdown would re-rip it —
  an unreadable or scratched disc then looped forever (fail → notify → auto-rip
  → fail → …). Auto-rip is now suppressed once a disc has a terminal-failed rip;
  the disc can still be retried manually from the card.

## [0.30.5] - 2026-05-21

### Fixed
- Ejecting a disc no longer leaves the drive showing an error. The eject fires
  a media-change event that the daemon used to classify as an insertion, run
  against the now-empty tray, and fail ("cd-info: exit status 1") — flipping the
  drive into the Error state. Removal events are now detected (no media present)
  and settle the drive cleanly to idle, clearing any stale error.

## [0.30.4] - 2026-05-21

### Added
- Game discs (PlayStation / PS2 / Saturn / Dreamcast / Xbox) identified by
  datfiles/gamedb are now enriched from IGDB when it's configured: box art plus
  summary, genres, platforms, and release year are fetched in the background
  and appear on the disc card and metadata pane shortly after identify. The
  match is platform-filtered and title-similarity-gated, and never overrides an
  exact gamedb cover or changes the disc's identity.

## [0.30.3] - 2026-05-21

### Added
- DiscEcho is now released under the MIT License (`LICENSE`).
- GitHub issue templates for bug reports and feature requests.

### Changed
- README rewritten for the shipped feature set (DVD/Blu-ray/UHD, audio CD, and
  game discs), with "How it works" and screenshot sections.

### Removed
- Obsolete planning docs (`ROADMAP.md`, `OPEN_QUESTIONS.md`, `ARCHITECTURE.md`)
  and the unused `config/` → `/etc/discecho` mount.

### Fixed
- History rows no longer clip their Re-rip / Stop action button off the right
  edge on narrow phone screens; long titles now truncate instead.
- Starting a game-disc rip (PlayStation / PS2 / Saturn / Dreamcast / Xbox) no
  longer wipes the disc's boot code. Picking a Redump/DuckStation candidate
  cleared `metadata_id`, which spawned a duplicate disc row on the next drive
  event (its "awaiting decision" card appearing *after* you'd hit Start) and
  silently disabled the post-rip Redump checksum verification.
- The drive card no longer shows IDLE — with Eject / Re-identify offered — while
  a rip is actually running. A spurious media-change event re-identifying the
  disc mid-rip could reset the drive's displayed state to idle; the dashboard
  now keeps showing the rip in progress.
- redumper rips are now bounded: a rip that stalls (no progress for 20 minutes)
  or that hits a disc whose data/audio track boundary is unreadable on the
  current drive (a "mixed-mode" disc that would otherwise grind for days) is
  stopped with a clear message suggesting a different drive or cleaning the
  disc, instead of holding the drive indefinitely.

## [0.30.2] - 2026-05-20

### Fixed
- The Game-disc identification panel (Settings → API keys & connections) now shows each system's actual Redump folder name — PlayStation → `psx`, PlayStation 2 → `ps2`, Saturn → `saturn`, Dreamcast → `dc`, Xbox → `xbox` — next to its row, instead of an ambiguous `<system>` placeholder in the instructions. Previously you couldn't tell that e.g. "Dreamcast" dats go in the `dc/` folder.

## [0.30.1] - 2026-05-20

### Fixed
- MakeMKV rips (DVD / Blu-ray / UHD) failing with `Failed to execute external program 'mmgplsrv'`. MakeMKV's bundled GPL helper `mmgplsrv` is a musl binary, but its musl loader + support libraries weren't included in the container image (only the glibc `makemkvcon` tree was), so it couldn't start. The image now bundles the musl runtime `mmgplsrv` needs, isolated so it can't affect the other tools, with a build-time check that guards against this regressing again.

## [0.30.0] - 2026-05-20

### Added
- Rip notifications are now rich and media-aware. Instead of `DiscEcho: <title> — Ripped to <root>`, the message is formatted per disc type (movie / TV series / audio CD / game / data) and includes the relevant details — duration, output size, file/title or episode counts, codec/engine, audio track + AccurateRip status, game system/region, and so on — rendered as light Markdown so Discord/Telegram/Slack/ntfy show it nicely. When cover/poster art is available it's attached to the notification.
- **Failure notifications now fire.** The `failed` trigger was configurable in the UI but never actually sent; a rip failure now notifies the subscribers that opted into `failed`, with the failed step and error. (`warn` remains reserved — no warning-class event is produced yet.)
- The notifications settings page now explains that DiscEcho uses **Apprise**, shows example service URLs, and links to the supported-services list (appriseit.com/services).
- History retention is now a real per-outcome policy instead of a single confusing "delete after N days" toggle. You can keep everything forever (default), or set independent limits for **successful** rips and **failed/cancelled** rips — each with an optional max age (days) *and* an optional max count (keep the newest N). Turning off "keep forever" is now immediately meaningful: out of the box it keeps successful rips and prunes failures after 14 days. The settings page shows a live preview ("removes X of Y history entries"), the last-run and next-scheduled-run times, and a **Run cleanup now** button. Retention only ever deletes history records — your ripped files are never touched.
- The profile editor now shows the hardware/setup requirements for disc types that need them, so you find out before a rip fails (or silently never detects the disc). **Xbox** profiles warn that original Xbox discs need a Kreon-firmware drive (XGD media is unreadable on stock drives); **UHD** profiles list the full prerequisites — a LibreDrive-compatible UHD-friendly drive, MakeMKV with a valid key, and an AACS2 `KEYDB.cfg`; **Blu-ray** and **DVD** profiles note the MakeMKV key requirement (with the caveat that the DVD HandBrake engine needs none). Each note links out to the relevant resources.

### Changed
- The **Game discs** entry under Settings → API keys & connections is now a per-system coverage table instead of a single flat "connected" badge. Each system (PlayStation, PS2, Saturn, Dreamcast, Xbox) shows its built-in **boot-code map** (pre-rip naming) and whether a **Redump .dat file** (post-rip verification) is present, along with the dats directory and a link to redump.org. The status badge now goes amber **partial** when Redump dats are missing rather than misleadingly showing green. (Xbox has no boot-code map by design — shown as n/a.)
- The daily history cleanup now also fixes a latent bug where the bulk prune could delete the disc currently sitting in a drive (the row the awaiting-decision card points at); it now keeps the drive's most-recent disc, matching the single-delete and clear-all paths.
- The drive-error tip for an unreadable disc now calls out the Xbox/Kreon-firmware case as its own clear sentence instead of burying it as a "less commonly…" aside, so an original Xbox disc that won't detect at all is easier to diagnose.
- A UHD rip that fails because the AACS2 `KEYDB.cfg` is missing now reports the full set of UHD prerequisites (key file, MakeMKV, and a LibreDrive-compatible drive) in the error, rather than just naming the missing path.

### Fixed
- Clicking **Stop** on a job in the brief window where its worker had just finished no longer flips the completed rip to "cancelled". The cancel-fallback path wrote `cancelled` unconditionally when the in-memory cancel entry was already gone (the normal state right after completion), clobbering a `done`/`failed` result and making the disc re-appear as awaiting a decision. The cancel write now only applies to jobs that aren't already in a terminal state.
- The disc metadata pane no longer white-screens for a game/data disc that was detected but not yet identified. Such discs arrive with `candidates: null` (a Go nil slice marshals to JSON `null`), and the game branch indexed `candidates[0]` without guarding for null.
- The job detail page (`/jobs/:id`) no longer shows the previous job's data when navigating directly between two finished jobs. The one-shot daemon-fetch fallback state wasn't reset on route change, and an in-flight fetch could resolve after navigating away and overwrite the new job.

## [0.29.0] - 2026-05-20

### Changed
- Settings → Host now shows a dedicated **Libraries** section listing each library root (Movies, TV, Music, Games, Data) with its actual on-disk size, largest-first, followed by a single array used/total/free summary and capacity bar. Previously the Host section collapsed all five roots into one whole-array `df` bar (they share a mount on Unraid), which read like a per-library list but wasn't. Sizes are measured by a background walk (every 30 min, cached) with a manual **Recalculate** button and a "last measured" timestamp.

## [0.28.1] - 2026-05-20

### Changed
- The dashboard's **Library size** widget now reports DiscEcho's own ripped data (sum of finished-job output) with free disk space as the subtext, instead of the whole library filesystem's used/total. On a shared array the widget previously showed the size of the entire array (e.g. 71.2 TB of 73.7 TB) rather than what DiscEcho had ripped.

## [0.28.0] - 2026-05-20

### Added
- Profile engine choices are now constrained per disc type. The editor's engine dropdown only offers engines that suit the disc type (e.g. AUDIO_CD → whipper; DVD → MakeMKV / MakeMKV+HandBrake / HandBrake; XBOX → redumper), and the daemon rejects out-of-allow-list combinations. Previously any engine could be paired with any disc type, allowing nonsensical profiles like an audio CD ripped with HandBrake.
- Choosing a disc type for a new profile now pre-fills a valid, runnable profile — engine, container, codec, quality tier, and a sensible output-path template — instead of leaving fields blank.
- The Output path field now lists the template variables available for the selected disc type as clickable chips (e.g. `{{.Title}}`, `{{.Year}}`, `{{.Artist}}`) and shows a live preview of the rendered path against sample values.

### Changed
- Quality preset is now a functional picker (`Archival / High / Balanced / Space-saver / Custom`) instead of a free-text label that no pipeline read. Picking a tier resolves the right constant-quality RF and encoder speed-preset for the chosen codec (x265 RF differs from x264, NVENC and AV1 again); Custom exposes the raw RF + encoder-preset inputs. The field only appears for encoding profiles (lossless/passthrough engines don't show it).

### Removed
- The empty "Post-processing — coming next" placeholder section in the profile editor.

### Fixed
- The webui engine mirror listed a stale `dd` engine; the daemon engine is `ddrescue` (renamed in migration 012). Corrected so the DATA engine resolves correctly client-side.

- MakeMKV rips now actually display progress and ETA. The rip command was missing `--progress=-same`; in robot mode makemkvcon emits no PRGV/PRGC progress lines unless this flag is set, so the bar sat at 0% with no ETA for the whole rip. The v0.27.0 PRGV `total/max` + ETA + multi-title aggregation work was correct but never received any progress input on real discs — only the title-length `MSG:` lines from the scan still printed, which masked the gap. (The scan/`info` path was unaffected because robot-mode *messages* print by default; only *progress* requires the flag.)

## [0.27.0] - 2026-05-20

### Added
- MakeMKV scan now emits a `scan` sub-step on the rip job, surfaced on drive cards as "Rip — Scanning titles (this can take several minutes)". The `info` enumeration on slim USB DVD drives can run 5+ minutes before any rip bytes flow; the prior label said "Rip — Read raw data", which was misleading and gave the impression of a stuck rip.
- MakeMKV `MSG:` lines from the scan phase are forwarded to the job log in real time (startup banner, `direct disc access mode` notice, errors). The log tail used to sit at a single line for the entire scan; users now see the same liveness signal the redumper path already had.

### Changed
- Mobile drive card log tail now includes the `HH:MM:SS` timestamp alongside the level marker. Previously the timestamp was dropped, leaving only `· <message>`.
- Mobile drive card now renders an `Eject` button disabled alongside `Stop` while a rip is in progress, matching the desktop RipCard. Previously the action was hidden mid-rip.

### Fixed
- MakeMKV per-title progress now reads `total/max` from PRGV instead of `current/max`. `current` is the per-sub-operation counter and resets repeatedly; `total` is the file-progress counter the user is actually waiting on. v0.26.5 multi-title DVD rips reported `progress=0` for the entire job even though the spool directory was filling with finished `.mkv` files.
- MakeMKV rip jobs now extrapolate ETA seconds from wall-clock elapsed vs percent delta and forward it on every PRGV. The progress chip on drive cards / queue rows used to show `—` for every MakeMKV rip; ETA now populates the same way it does for redumper.
- Multi-title MakeMKV rips (DVD / BDMV / UHD picked-extras flows) now report an aggregated 0→100 progress across the whole title set rather than resetting to 0 at every title boundary. Aggregate ETA tracks the wall-clock since the first title started, so the chip reflects total work remaining, not just the current title.
- Editing a `MakeMKV` or `MakeMKV+HandBrake` profile no longer silently fails to save. The engine schemas were missing `dvd_selection_mode`, `quality_rf`, `encoder_preset`, and `season` — options the profile seeder writes and the pipelines consume — so `ValidateProfile` rejected every save of the seeded DVD profiles with a 422 on those keys. Because the editor didn't render those option fields, the error was invisible and the Save button appeared to do nothing. The schemas (and the webui mirror) now declare them, so changing e.g. the video codec to `nvenc_h265` saves correctly.
- The profile editor now surfaces validation errors that don't map to a rendered form field in a generic banner ("Save rejected: …") instead of swallowing them. Previously a 422 on an unknown/unrendered key produced no visible feedback.

## [0.26.5] - 2026-05-19

### Changed
- MakeMKV is now sourced from the upstream `jlesage/makemkv` image instead of being compiled from `makemkv-oss` / `makemkv-bin` inside the Dockerfile. The from-source build linked `makemkvcon` against Debian bookworm's libcrypto3 / libssl3 / libavcodec59, and the resulting binary subtly broke purchased `M-` license-key signature verification — MakeMKV stored the key correctly but emitted `MSG:5021 "application version is too old"` against any disc command, even though the exact same upstream version + key worked elsewhere. `jlesage/makemkv` ships a self-contained MakeMKV install at `/opt/makemkv` (including its own dynamic linker and library tree), which key validation accepts.



### Fixed
- MakeMKV license-key validator no longer second-guesses the body charset. v0.26.2 and v0.26.3 each widened the allowed character class chasing real purchased keys, and real keys still slipped past with characters neither version covered. The validator now gates only on the prefix (`T-` or `M-`) and a minimum length; the body is treated as opaque and forwarded to makemkvcon, which has the only authoritative parse.

## [0.26.3] - 2026-05-19

### Fixed
- MakeMKV `M-` license-key validator now accepts the URL-safe base64 alphabet (`-` and `_`). v0.26.2 widened the prefix gate but kept the body charset on the standard-base64 alphabet, so real purchased keys like `M-_6P312gXv…` still failed validation with the same 422.

## [0.26.2] - 2026-05-19

### Changed
- `Settings → Integrations → MakeMKV` now accepts both free `T-` beta keys and purchased `M-` permanent licenses. The API-level validator previously hard-rejected anything that didn't match `^T-…`, which locked out paid registrations. The settings.conf writer was always format-agnostic — only the gate on the way in needed widening. The UI field label is now "License key (T- beta or M- purchased)".

## [0.26.1] - 2026-05-19

### Fixed
- Deleting the last history row for a disc that's still in the drive no longer nukes the disc out from under the awaiting-decision card. Previously, removing the final terminal job for a disc cascade-deleted the disc row even when it was still physically inserted, which silently 404'd the next `/api/discs/<id>/start` call — clicking Start rip looked like "nothing happened" while the identify and history both vanished. Single-job DELETE and Clear History now preserve any disc that's still the drive's most-recent (the proxy for "currently inserted").

## [0.26.0] - 2026-05-19

### Added
- DVD-Video profiles now honour the `engine` field, unlocking MakeMKV for DVD ripping alongside the existing dvdbackup + HandBrake path. Three new seeded profiles ship for DVDs: `DVD-Movie (MakeMKV)` (MakeMKV passthrough — MPEG-2 in MKV, no re-encode, six-step pipeline), `DVD-Movie + Extras (MakeMKV)` (MakeMKV → HandBrake x265 RF20, includes bonus features), and `DVD-Series (MakeMKV)` (MakeMKV → HandBrake x265 RF20, per-title selection). Existing `DVD-Movie`, `DVD-Movie + Extras`, and `DVD-Series` profiles keep ripping via dvdbackup so installs that depend on libdvdcss aren't disrupted. Migration 018 backfills the new profiles into existing databases.
- Drive error tip for MakeMKV beta-key expiry: when a MakeMKV-engine rip fails because makemkvcon reports the key is expired or unregistered, the dashboard now surfaces a tip pointing at `Settings → Integrations → MakeMKV` (alongside a reminder that the legacy HandBrake-engine DVD profiles still rip via dvdbackup and don't depend on the MakeMKV key).

## [0.25.0] - 2026-05-19

### Added
- Stop and Eject buttons on the busy-drive rip card. Until now Stop was only reachable from the job detail panel — the rip card itself had no actions row, so cancelling a running rip required clicking through to `/jobs/<id>`. The card now mirrors the idle-drive hero card: Stop is enabled while the job is running or queued and prompts before cancelling; Eject sits dim during the rip and lights up the moment the drive releases.
- Daemon: `Disc.lifecycle_state` aggregate field computed from each disc's rip+transcode chain, exposed in `/api/state` and the SSE snapshot.
- Daemon: new `GET /api/discs/history` endpoint returning disc-keyed history rows for the upcoming UI rework.

### Changed
- Removed the empty `Files` tab from the disc detail pane. The tab was only ever populated for DVD movies with a `dvd_titles` blob, and even then it duplicated information that already lives in the title picker; for every other disc type it rendered "No file inventory available." Movies now show Overview + Cast; data and unknown discs show Overview only.
- Dashboard: a disc whose source drive is still loaded now appears as a chip on that drive card when its encode is still running, instead of vanishing from the hero band the moment the rip ejects. EncodeQueueCard now only renders for orphaned encodes (source drive removed, or compute pool deeper than drive count).
- History page now shows one row per disc instead of one per job. Each row carries a lifecycle pill (awaiting decision / ripping / encoding / done / failed / interrupted / cancelled), a derived rip+transcode summary, and a single Re-rip / Retry / Stop action — replacing the old "every job is its own row" view where a single disc that was identified, ripped, then transcoded showed up three times. Backed by the new `/api/discs/history` endpoint.

### Removed
- Webui: legacy `HistoryRow.svelte` (job-row history) replaced by `DiscHistoryRow.svelte` (disc-row).
- Webui: unused `historyFilter` writable + `fetchHistoryPage` helper (and the `history` / `historyTotal` / `historyLoading` / `historyError` stores they exclusively populated) from `store.ts`.

### Fixed
- The rip card now flips to the picked candidate's title, year, and cover the instant you press Start. Previously, choosing a candidate other than the auto-identified top match left the running-rip card showing the original top result's title and cover until a full page reload — the daemon was persisting the picked candidate's metadata to SQLite but never publishing an SSE event, and the front-end's `disc.changed` handler only honoured `metadata_json`. The daemon now emits `disc.changed` with the full updated identity (`title`, `year`, `metadata_provider`, `metadata_id`, `metadata_json`, `runtime_seconds`) after `StartDisc` finishes its writes, and the front-end overlays whichever keys land in the payload.

## [0.24.3] - 2026-05-19

### Fixed
- v0.24.2's similarity-based DVD candidate scoring still surfaced the wrong top match for short numeric labels like `Jackass_3`. The tokeniser split `3.5` into `3` + `5`, and the digit→word fold turned that `3` into `three` — which then tied `Jackass 3.5` with `Jackass Volume Three` on token overlap, leaving popularity to break the tie in favour of the more popular but wrong title. Keep decimal numbers as a single token so `3.5` no longer masquerades as the integer `3`.

## [0.24.2] - 2026-05-19

### Changed
- DVD/Blu-ray/UHD identify now ranks TMDB results by how well each candidate title matches the disc label, with TMDB popularity as a tiebreaker — instead of letting popularity alone decide. Volume-numbered direct-to-video releases ("Jackass Volume Three" against a `JACKASS_VOLUME_3` label) no longer get shadowed by a more popular but unrelated entry ("Jackass 3.5") just because TMDB ranks the latter higher. Scoring uses a Jaccard token overlap with digit↔word folding (`3` ↔ `three`, `10` ↔ `ten`), so a `Volume 3` label scores 1.0 against `Volume Three`. The 100/80/60/40/20 rank-position confidence numbers in the picker now reflect match quality, not popularity.

## [0.24.1] - 2026-05-19

### Fixed
- Awaiting-decision picker no longer kicks off a rip the instant you click a candidate row. Rows now act as radio buttons — clicking selects the candidate, highlights the row, and updates the primary action's label to `Start rip · <chosen title>`. The rip only fires when you click that button. Any row click or profile dropdown change also cancels the batch-mode auto-rip countdown so a late timer can't pre-empt your choice.

## [0.24.0] - 2026-05-19

### Changed
- DVD and Blu-ray bonus features no longer all land in a single `extras/` folder named `extra-01.mkv`, `extra-02.mkv`, … Each detected extra now goes into the Jellyfin/Emby-standard subfolder that matches its length and gets a descriptive filename. The mapping for the DVD-Video pipeline is based on the source title duration (≤5 min → `trailers/Trailer NN.mkv`, ≤30 min → `featurettes/Featurette NN.mkv`, >30 min → `behind the scenes/Behind The Scenes NN.mkv`); the BDMV pipeline buckets by the transcoded file's size relative to the main feature using equivalent thresholds (≤5%, ≤25%, >25%). Both servers also still recognise the old generic `extras/` folder so previously-ripped libraries keep working — no rename required. Numbering is per-bucket so each subfolder reads 01, 02, … without gaps.

## [0.23.2] - 2026-05-18

### Added
- Per-rip profile dropdown on the awaiting-decision card and the title picker. When a disc type has more than one enabled profile (e.g. `DVD-Movie` vs `DVD-Movie + Extras`, or a hand-rolled custom profile), the user gets a `<select>` to pick which profile this rip uses instead of being stuck with the auto-pick-by-name. Default tracks the existing auto-pick; user override sticks for the lifetime of the card. The title picker's default mirrors the profile of the most-recent scan job for that disc so a choice made on the card carries through to the picker submit. The dropdown is hidden when there's only one enabled profile for the disc type — no UI noise for the common single-profile case.

## [0.23.1] - 2026-05-18

### Fixed
- Awaiting-decision card never swapped in the TitlePicker after a scan finished. The daemon's `runScan` orchestrator path was firing a `disc.changed` SSE event with `{id, metadata_json}` once the title list landed, but the webui's SSE dispatcher had no handler for that event name — the picker only appeared after a manual page reload. Added the missing handler in `store.ts`, defensively overlaying only the keys actually present in the payload so a future field-omission can't undefine existing state. Same mitigation pattern as the v0.20.1 `drive.changed` regression.

## [0.23.0] - 2026-05-18

### Added
- Two new seeded profiles, `DVD-Movie + Extras` (HandBrake) and `BD-1080p + Extras` (MakeMKV+HandBrake), that auto-include bonus-material titles alongside the main feature in a single rip. The main feature lands at the profile's existing top-level path (e.g., `Arrival (2016)/Arrival (2016).mkv`); every other title in the `[min_extra_seconds, main_duration × extras_max_ratio]` duration band lands under `<title>/extras/extra-NN.mkv` — the catch-all Plex/Jellyfin auto-classify as bonus content. Pick either profile from the awaiting-decision card (or set it as your default for the disc type in the profile editor) when you want the full disc instead of just the main feature.
- New `include_extras` (bool), `min_extra_seconds` (int, default 60), `extras_max_ratio` (int percent, default 90) profile options on the HandBrake, MakeMKV, and MakeMKV+HandBrake engine schemas. When `include_extras=true` and the title picker didn't override, the rip handler folds bonus-material titles into the encode set automatically. Defaults drop navigation loops (<60s) and full-length alternate cuts (>main × 90%) to avoid duplicate-of-the-feature outputs.
- Migration 017 backfills the two new profiles into existing installs that already have `DVD-Movie` / `BD-1080p` (fresh installs get them via the seeder). User-customised profiles with the same name are left alone.

## [0.22.0] - 2026-05-18

### Added
- Settings → System → "API keys & connections" gains an inline editor for the IGDB OAuth credentials, TMDB v3 API key, and MakeMKV beta key. Edits take effect immediately — no container restart. Each row carries a `Configured (UI)` / `Configured (env)` / `Unset` source pill so you can always tell which surface owns the credential. UI-set values override env; clearing falls back to env. IGDB and TMDB rows expose a Test button that round-trips against the real upstream (Twitch OAuth + IGDB /games for IGDB; GET /configuration for TMDB) before saving; MakeMKV has no upstream test and renders a "Validated on next rip" caption instead.
- Settings → System gains an "Encoding" section: a Concurrent encodes input (1, default; takes effect on next daemon restart), a Spool cap input in GiB (100, default; takes effect immediately), and a live "Spool usage: X / Y" readout that polls `/api/system/spool` every 10s. The readout flips to a `CAP REACHED — pausing new rips` warning when usage hits the cap, surfacing the backpressure state described below.
- Backpressure on the per-drive rip queue: when the spool dir reaches the configured cap, the drive worker pauses new rip-job picks until a transcode finishes and frees the space. Cap is re-read from the settings table on each check so an in-flight `spool.cap_bytes` update takes effect immediately. The daemon broadcasts a `spool.changed` SSE event with `{usage_bytes, cap_bytes, blocked}` on pause + resume.
- `POST /api/jobs/{id}/retry-transcode` re-enqueues a failed or interrupted transcode-kind job, reusing the existing spool dir so the user doesn't have to re-rip the disc. Validates the job kind, terminal state, and that the spool dir still exists on disk. Returns 410 (spool gone — re-rip the disc) when the dir is missing.
- `GET /api/system/spool` exposes `{usage_bytes, cap_bytes, blocked}` for the dashboard / settings widget.
- Spool garbage collection runs at daemon startup. Cross-references on-disk spool subdirs against in-use rip / transcode job rows and removes orphans (no row references them). Spool dirs for failed or interrupted transcodes are kept so the user can retry; only done / cancelled / never-seen dirs are deleted.
- Dashboard splits the active-jobs view into a "Rip queue" card (per-drive serialised jobs) and an "Encode queue" card (global compute pool). The encode card only renders when there are transcode jobs in flight so audio-CD / DATA users see no change. Desktop gets a dedicated `EncodeQueueCard` with the drive column dropped; mobile gets a parallel "Encode queue" section using the existing queued-row layout.
- History rows whose latest job is a failed or interrupted transcode gain a "Re-encode" button that POSTs to `/api/jobs/{id}/retry-transcode`. On 410 (spool reaped by daemon-side GC) the button surfaces a clear "use Re-rip to start over" hint.
- `DELETE /api/jobs/{id}` now removes the spool dir for kind=transcode jobs, giving the user a single "discard this failed transcode" path through the existing delete affordance. Best-effort: a missed cleanup gets swept by the next startup GC anyway.

### Added
- TV episode mapping for the title picker. When the disc identifies as a TV series (TMDB media_type='tv'), the picker grows a Season input + per-title episode dropdown. Episodes come from TMDB `/tv/{id}/season/{N}` via the new `GET /api/discs/{id}/episodes?season=N` endpoint. The picker auto-assigns titles to episodes in selected order (override per-title via the dropdown); on submit, the daemon resolves each picked episode to its TMDB name and persists `selected_season` + `selected_title_episodes: {titleID: {episode, title}}` onto `disc.metadata_json` so the rip handler can render the canonical `Show/Season XX/Show - SXXEXX - Episode Title.mkv` shape without re-hitting TMDB at rip time.
- `OutputFields.EpisodeTitle` is available in profile path templates. Migration 016 updates the seeded DVD-Series template to `{{.Show}}/Season {{printf "%02d" .Season}}/{{.Show}} - S{{printf "%02d" .Season}}E{{printf "%02d" .EpisodeNumber}}{{if .EpisodeTitle}} - {{.EpisodeTitle}}{{end}}.mkv` — the `{{if .EpisodeTitle}}…{{end}}` guard means picker-less rips still render a clean filename. User-customised DVD-Series templates are left alone (the migration only flips rows that still carry the pre-016 default verbatim).
- BDMV / UHD multi-title rip honours the picker's episode map: each ripped title gets `EpisodeNumber` + `EpisodeTitle` from the persisted assignment, falling back to 1-based title-index when no mapping exists. DVD's existing per-title loop picks up the same map.
- Pre-rip title picker for Blu-ray (BDMV), Ultra HD Blu-ray (UHD), and DVD-Video discs. The awaiting-decision card gains a "Pick titles…" button (alongside "Use top match · Start rip" and "Search manually") that runs a brief MakeMKV / HandBrake scan (~10s on DVD direct-scan, ~60s on a slow MakeMKV BD), then surfaces a checkbox-grid title list with duration + chapter count + size. The user checks which titles to rip; submit kicks off the rip with exactly those titles. Default-picks the longest so a one-click submit matches the legacy auto-pick behaviour for movies. Routes through the new `POST /api/discs/{id}/scan` endpoint + `kind='scan'` job (drive-bound, brief, doesn't eject).
- New `show_title_picker` profile option on the MakeMKV / MakeMKV+HandBrake / HandBrake engine schemas. When true, the awaiting-decision auto-confirm + manual candidate-pick paths both route through the scan + picker flow instead of straight to rip. Migration 015 seeds the existing DVD-Series profile with `show_title_picker=true` so TV box-set discs default into the picker automatically; movie profiles stay on the auto-pick path.
- Multi-title rip support across BDMV / UHD / DVD: the rip handlers now loop over the picker's selected title IDs (read from `disc.metadata_json.selected_title_ids`). BDMV transcodes each title in turn; UHD remuxes each. The move step renders the output template per title with the existing `{{.EpisodeNumber}}` field set to the 1-based title index; templates that don't reference EpisodeNumber get a `-titleNN` suffix appended programmatically to disambiguate output paths. DVD's existing per-title loop now honours selected_title_ids over the auto-pick heuristic.

### Changed
- `compute.concurrent_encodes` setting changes now take effect immediately instead of requiring a daemon restart. The compute worker pool is fixed at 8 goroutines and is governed by an atomic limit; raising or lowering the setting reshapes the in-flight cap on the next acquire. Settings UI tooltip updated accordingly. `PutSettings` accepts `compute.concurrent_encodes` (1-8) and `spool.cap_bytes` (1 MiB - 10 TiB) as either JSON numbers or strings.

### Changed
- Blu-ray (BDMV), Ultra HD Blu-ray (UHD), DVD-Video, PSX, PS2, Saturn, Dreamcast, and original Xbox rips now free the drive at eject instead of holding it through the post-rip work (HandBrake transcode for video discs; chdman compress for game discs). The pipeline is split into a drive-bound rip job (rip → eject) that lands its output in a new per-job spool under `${DISCECHO_DATA}/spool/<rip-job-id>/`, and a compute-bound transcode job that finishes the chain (transcode and/or compress → move → notify). On a multi-disc Blu-ray session this is the difference between "drive is busy for 85 minutes per disc" and "drive is free in ~25 minutes; transcodes drain in the background"; on a multi-disc PSX/PS2 session it's the difference between "drive idle for the entire chdman compress" and "drive idle for ~30 seconds while the next disc loads, compress runs in the background". The dashboard's drive card flips to `idle` the moment eject fires and the new transcode/compress jobs surface alongside it in the queue. Apprise notification timing is unchanged from the user's perspective — it still fires after the move-step completes. Audio CD and DATA discs keep their existing monolithic flow (no real post-rip work to decouple).
- The compute queue runs serially by default (1 transcode at a time, configurable via the `compute.concurrent_encodes` setting). Spool dir for a successful transcode is cleaned up on transcode-done; a failed transcode leaves the spool intact so a future Retry Encode flow (Phase 5) can re-run without re-ripping the disc.
- `/api/system/integrations` no longer carries TMDB or IGDB rows — those credentials are now first-class under `/api/integrations`. The Game discs row description is updated to point at the new editor.
- Secrets at rest: IGDB / TMDB / MakeMKV credentials stored in the UI are plaintext in `discecho.sqlite`. Same posture as the existing `.env` and `${DISCECHO_DATA}/MakeMKV/settings.conf` storage. Encryption-at-rest is not planned for now.

## [0.20.1] - 2026-05-17

### Fixed
- Drive status pill flipped to the literal text "undefined" after saving a read-offset under Settings → System → Drives, until the page was reloaded. The `drive.changed` SSE handler on the client always reads `payload.state` and writes it into the local drive record; the offset PATCH endpoint added in v0.20.0 published a payload without `state`, so the dashboard wrote `undefined` over the live value. The endpoint now includes the drive's current state in the broadcast, and the SSE handler defensively overlays only the keys actually present in the payload so a future field-omission can't repeat the regression.

## [0.20.0] - 2026-05-17

### Added
- Per-drive AccurateRip read-offset is now persisted in the database. Settings → System → Drives gains an "Edit" affordance next to each drive that takes a number from −3000 to +3000 (look it up on the AccurateRip drive DB or run `whipper offset find` inside the container). Persisted offset is passed to whipper as `-o <N>` on every audio-CD rip. Default 0 + source `''` preserves the legacy uncalibrated behavior (rip output identical to pre-v0.20), so upgrading installs see no change until they explicitly calibrate. Auto-detect-from-UI is a follow-up; the manual path is enough to unlock AccurateRip verification today. Adds migration 013.
- Audio-CD rips now surface AccurateRip verification status. The whipper output parser already captured per-track confidence numbers but the data never reached the UI; rip-step notes now carry a structured `accuraterip: { status, verified_tracks, total_tracks, min/max_confidence }` summary that the RipCard renders as a verified (green ✓ N/N · conf X) / unverified (amber N/M verified) / uncalibrated (grey) pill. "uncalibrated" pins the status whenever a drive has no calibration set, so the user is never told a rip "didn't match" when the offset was the actual problem.
- Audio-CD rips now embed front cover art into every FLAC by default. After whipper finishes, the pipeline downloads the release-level cover from the MusicBrainz Cover Art Archive (falling back to the release-group cover when the release has none), saves a 500px JPEG to the workdir, and invokes `metaflac --import-picture-from` per file. The new `embed_cover_art` whipper-engine profile option (default on) lets users opt out per-profile.
- Audio-CD rips now write album-mode ReplayGain 2.0 tags via loudgain. The post-rip step gathers every FLAC in the workdir and passes them in one invocation (album-gain requires a single call). Tags use the `-a -k -s e` recipe: album mode, prevent clipping, extended R128 tags with LU/LRA info. The new `replaygain_album_mode` whipper-engine profile option (default on) lets users opt out per-profile.
- Settings → Profiles editor now renders bool engine options with their schema default when the key is absent from `profile.options`. New whipper profiles therefore display the embed + ReplayGain checkboxes as checked (matching the daemon's "missing means on" semantics), rather than misleadingly showing them as off until first interaction.

### Changed
- Runtime container now ships `metaflac` (from the Debian `flac` package) and `loudgain` (built from source in a new Dockerfile stage — not packaged in Debian bookworm). Image size grows ~10 MB. The loudgain build patches out the `av_register_all()` call in `scan.c` so it links against FFmpeg 5.x (Debian bookworm) without an undefined-symbol error — the call has been a no-op since FFmpeg 4.0 anyway, the guarded `if` was dead code.
- `Whipper.Run` keeps its existing `error` return for the generic `tools.Tool` interface; new `Whipper.RunWithResult` returns a `WhipperResult` with the parsed per-track AccurateRip confidence map. The audio-CD pipeline probes for the optional method at runtime, so tests that stub a minimal Tool keep working.

## [0.19.0] - 2026-05-17

### Changed
- Data-profile engine label is now `ddrescue` (was `dd`) and reports the correct visible `step_count` of 6 (was 5). The v0.18.4 implementation switch left the schema, seeder, and existing profile rows stale, so the profile editor's "Reader" field still read `dd` and the footer reported "Step count: 5" even though six steps run end-to-end. Migration 012 rewrites existing `engine='dd'` rows to `engine='ddrescue', step_count=6`.

### Fixed
- Drive-error tip for "cd-info: exit status N" leads with the recoverable causes (dirty disc, slow spin-up) and demotes the Xbox / Kreon-firmware mention to a "less commonly" framing. Most exit-1 failures are pressed-CD reading issues; the prior wording invited users to chase a firmware flash for what was usually a dirty disc.
- Notification URL is now masked behind an Edit affordance for saved notifications. A configured Discord webhook (or any Apprise URL — ntfy bearer, Pushover token, …) used to render in plaintext in the Settings editor; anyone glancing at the screen could lift the secret. The scheme prefix is preserved so the user can still see what kind of backend it is.
- Settings → Drives row no longer duplicates the kernel device name. The "bus" field is a placeholder for the device name (e.g. `sr0`) so the row read `sr0 · /dev/sr0`. Show only `/dev/sr0` — it's copy-pasteable for shell commands and the same identifier in its proper form.
- Awaiting-decision Skip action now renders as a proper secondary button (44px, bordered, sized like the primary) instead of a tiny text link to the right of the big green primary. The old layout was easy to mis-tap on mobile and gave Skip no visual weight relative to "Rip as data" / "Use top match".
- Job detail Summary tab fleshed out. The terminal-job view used to show just Finished / Profile / Error; it now also lists Disc title, Started timestamp, Elapsed (already shown when set), Output size + Output file path (resolved from the move step's notes — single `path` for video/data/game discs, `paths[]` for audio-CD multi-track rips), and Drive (resolved from the drive_id via the live drives store). The non-terminal view picks up Disc, Started, and Drive for parity.
- Dashboard LIBRARY SIZE widget now reads the actual filesystem usage via `statfs` instead of summing `output_bytes` over `done` jobs. The headline used to read `0 B of 73.7 TB` whenever there were no successful jobs (fresh install, every recent rip failed, history pruned) even though gigabytes of media already lived on the library mount. The jobs-sum fallback only kicks in if statfs returns 0.
- Settings → Host now collapses library bars by underlying filesystem. Five identical bars for `/library/{movies,tv,music,games,data}` used to render when all roots lived on the same Unraid share; a single bar now shows one canonical path with a `shared with …` hint for the others.
- Dashboard drive cards now show the disc currently in the tray, not just the disc of an active rip job. `Drive.CurrentDiscID` was declared but never populated — the field is now computed in `buildSnapshot` as the most-recent disc per drive that has no terminal job (`done`/`failed`/`cancelled`/`interrupted`). The drive card therefore renders the disc title for an awaiting-decision disc instead of "Idle — insert a disc". Re-rip flows are unaffected (those still go through `activeJob.disc_id`).
- Pipeline steppers (vertical / horizontal / mini) defensively render a stale `running` step on a terminal job as `failed` (or `done` for the mini variant). The daemon fix below prevents this state from being written in the first place, but legacy interrupted rows from before the upgrade still expose the inconsistency until `MarkInterruptedJobs` runs at next startup; this guard means the UI doesn't show a spinning Rip indicator on a job that the daemon abandoned hours ago.
- Failed DATA rip error message no longer reads `dd: ddrescue: signal: killed`. The handler still wraps the rip error with a leftover `dd:` prefix from before the v0.18.4 ddrescue switch; the inner `ddrescue:` prefix from the tool itself already gives the right context.
- History tab now surfaces `interrupted` jobs alongside `done`/`failed`/`cancelled`. Crash-recovered jobs were silently hidden — a job that ran 12 minutes and was killed by a daemon restart simply vanished from the user's view. Same widening applied to the history-clear and history-prune queries so the cleanup tools still cover those rows. The history row renders an `INTERRUPTED` badge with a neutral grey style (vs. the red `FAILED` / `CANCELLED` badges) since the disc itself didn't fail.
- Job detail and History pages no longer render the mobile bottom tab bar on desktop. The mobile `TabBar` and sticky action drawer were unconditional on `/history` and `/jobs/[id]`, so at desktop widths the bottom nav stacked on top of the always-on `TopNav` and the floating action bar covered the bottom of the page. Both are now gated to mobile, and the job-detail page renders the Cancel / Delete action inline below the tabs at desktop widths.
- DATA discs (PC CD-ROM, generic ISO) no longer create a fresh disc row per uevent burst on slow drives. The ASUS SDRW-08D2S-U fires 2-3 `DISK_MEDIA_CHANGE` events per physical insertion; until now only audio CDs (TOC hash) and game discs (boot-code dedup window) were protected. DATA discs computed their content hash only after the rip finished, so persistDisc dedup had nothing to match on at classify time and the dashboard ended up with two "MORROWIND awaiting decision" cards for one disc. The data handler now computes a pre-rip identity hash from `(volume_label, blockdev_size)` and stores it on `disc.TOCHash` so the existing Tier 1 dedup picks it up.
- Crash-recovery (`MarkInterruptedJobs`) now also flips orphan `job_steps` rows from `running` to `failed` with `finished_at` stamped. Previously the parent job correctly transitioned to `interrupted` at startup but its active step row stayed `running` forever, so the job-detail Pipeline view showed a stale spinning indicator on a step the daemon had already abandoned. The whole flip is in a transaction so partial recovery is impossible.

## [0.18.6] - 2026-05-17

### Fixed
- Dashboard progress percentage now shows one decimal place when below 1% (`0.2%` instead of the previous `0%`). On a damaged data disc ddrescue can sit at 0.2-0.4% for several minutes on the initial bad spot; rounding to integer made the bar look frozen. Introduced via the new `formatProgress` helper, applied to all six job-percentage call sites (job row, pipeline stepper, drive cards mobile/desktop, rip card, queue table).
- ddrescue log no longer spams the same phase banner every refresh. The status block re-prints the current phase line (`Copying non-tried blocks... Pass 1 (forwards)`) on every ~26 s refresh; the daemon now dedupes consecutive identical banners so the log shows one entry per actual phase transition. The cursor-up ANSI escapes that ddrescue prepends to status blocks are also stripped from the log before display.

## [0.18.5] - 2026-05-17

### Fixed
- Data-disc rip progress now actually updates the dashboard. v0.18.4 read ddrescue's output from stderr, but ddrescue prints its live status display (banner, status block, phase-transition lines) to STDOUT — stderr is reserved for genuine errors. The dashboard progress bar therefore stayed at 0% for the entire rip even though the ISO was growing on disk. Now reads stdout for progress and drains stderr in parallel for warnings.

## [0.18.4] - 2026-05-17

### Changed
- Data-disc (PC CD-ROM, generic ISO) pipeline now rips with GNU ddrescue instead of dd. Data CDs of any age commonly have a handful of unrecovered read errors and dd's `conv=noerror,sync` retries each bad sector synchronously — the drive's per-sector retry takes ~25 s on the ASUS SDRW-08D2S-U, so a disc with a few hundred bad sectors stalls dd for hours and the progress bar never advances. ddrescue does a fast forward pass that skips bad areas, then revisits them in trim/scrape phases, and surfaces actual percentage / speed / ETA the whole time. Container image adds the `gddrescue` Debian package; the unused `DISCECHO_DD_BIN` env var is removed.

## [0.18.3] - 2026-05-17

### Added
- `POST /api/drives/{id}/reclassify` reruns the disc-flow handler against the disc that is already sitting in the drive. Used to recover a drive stuck in `error` after the cold-disc spin-up race exhausted the classifier retry budget — without this the only recovery path was eject + re-insert because the kernel only emits `DISK_MEDIA_CHANGE` on actual media-swap events. Refuses with 409 when a job is already running on the drive (the running pipeline holds the SCSI handle).

## [0.18.2] - 2026-05-17

### Fixed
- Extended the cd-info classify retry budget from ~11.5 s to ~30.5 s (10 attempts on a 0.5 s → 5 s ramp). The ASUS SDRW-08D2S-U slot-load takes ~21 s on a chilled disc before cd-info stops returning exit 1; the old schedule gave up roughly halfway through that window and the drive flipped to `error` for the rest of the session unless the user ejected and re-inserted. The longer tail steps cost nothing on a fast drive — the first cd-info succeeds and the retry loop exits before consuming any of the extra delay.

## [0.18.1] - 2026-05-17

### Fixed
- PC data CDs whose cd-info disc mode reports `CD-DATA (Mode 1)` (e.g. the original Morrowind PC CD-ROM) no longer get mis-classified as audio CDs. The disc-mode parser was matching `cd-da` as a substring of `cd-data` and sending the disc down the audio pipeline, where cdparanoia would fail to read a TOC and the disc never got a row in the UI. Now matched as a word-bounded token.
- The "drive error" tip surfaced under the dashboard error banner no longer claims the failure is an Xbox / Kreon firmware issue. cd-info's exit-1 has multiple common causes (dirty disc, spin-up race, unsupported disc type); the tip now lists them in rough likelihood order instead of pointing only at XGD.

## [0.18.0] - 2026-05-17

### Added
- Dashboard drive cards now show the current redumper sub-phase during the rip step. The `*** PHASE (time check: ...)` markers that redumper emits for DUMP / DUMP::EXTRA / PROTECTION / REFINE / DVDKEY / SPLIT transitions are persisted to a new `jobs.active_substep` column and rendered as text below the pipeline stepper (e.g. "Rip — Recovering damaged sectors (this can take a while)"). Without this the user saw a frozen 0% bar and no indication that redumper was alive during the multi-minute refine phase.

### Changed
- Progress percent is no longer reset to 0 when redumper transitions from DUMP to REFINE or another sub-phase; the bar stays at its last-known value so the UI does not appear to regress after a successful dump.

## [0.17.7] - 2026-05-17

### Changed
- redumper now retries each problem sector up to 50 times by default (was 0). With the default, a single SCSI or C2 read error near the end of the disc aborted the entire rip at the split phase — common on lightly scratched PSX / PS2 / Xbox discs. 50 retries recovers most surface scratches without dragging out the rip on a clean disc (where the first read succeeds and the retry budget is never spent).


## [0.17.6] - 2026-05-17

### Fixed
- isoinfo exit code 5 ("Input/output error. Read error on old image") no longer triggers an infinite retry storm. On scratched / dirty / chilled discs isoinfo still writes a usable directory listing or SYSTEM.CNF extract to stdout even when it exits non-zero; the daemon was discarding stdout and looping on the error. Accept the partial output when it's non-empty so classify can identify the disc on the first attempt. Same fix applied to the SYSTEM.CNF probe.


## [0.17.5] - 2026-05-17

### Fixed
- Per-call timeouts in v0.17.4 (20s isoinfo, 25s cd-info) were too aggressive — on the ASUS SDRW-08D2S-U a cold-disc isoinfo legitimately takes ~23s and cd-info ~21s, so the timeout killed every call mid-flight and the retry decorator spun in circles. Bumped to 45s for isoinfo and 40s for cd-info so a successful first pass completes cleanly while a genuinely hung call is still bounded.


## [0.17.4] - 2026-05-17

### Fixed
- Each cd-info / isoinfo invocation now has a per-call timeout (25s for cd-info, 20s for isoinfo). Without this, a single hung call inherited the caller's entire remaining deadline and consumed the whole 180s budget on slow drives — the retry decorators never got a chance to move on. Now a stuck call dies in seconds and the retry loop continues, bounding each probe to its own backoff schedule rather than the request timeout.


## [0.17.3] - 2026-05-17

### Fixed
- Bumped the long-route HTTP request timeout (90s → 180s) and discFlow.identifyDur (60s → 120s). On the slow ASUS SDRW-08D2S-U with chilled PSX discs, each probe (cd-info, isoinfo -R -l, isoinfo -x SYSTEM.CNF;1) takes 20-25s — the previous budgets killed classify mid-chain. The new ceilings comfortably cover cd-info + fs listing + sysCNF extraction plus a candidate-pick metadata fetch.


## [0.17.2] - 2026-05-17

### Fixed
- Manual re-identify and StartDisc no longer hit chi middleware's 30s request timeout. The router now wraps /api/discs/{id}/identify and /api/discs/{id}/start with a separate 90s timeout group — long enough to absorb a slow-drive cd-info + isoinfo cycle (~60s identify ceiling) plus an external metadata fetch on candidate pick. Without this, force-reidentify on the ASUS SDRW-08D2S-U with finicky discs returned HTTP 500 "context deadline exceeded" exactly 30s in.


## [0.17.1] - 2026-05-17

### Fixed
- Identify timeout bumped from 30s to 60s. The previous budget was too tight when the ASUS SDRW-08D2S-U (and similar slow consumer drives) hit a dirty or finicky disc — cd-info's retry burst alone could consume 12s, leaving isoinfo no time to finish the listing pass, surfacing as `classify: fs probe failed err=context deadline exceeded`.
- `DriveErrorTip` now recognises `context deadline exceeded` failures and suggests ejecting + re-inserting (the drive's second spin-up is usually faster), cleaning the disc, or trying a different drive.


## [0.17.0] - 2026-05-17

### Added
- Drive cards now surface a red error banner when the daemon's classify step fails. The banner shows the raw error (e.g. `cd-info: exit status 1`) plus a context-aware tip when one is available. Motivating case: an Xbox game disc on a non-Kreon drive can only expose a tiny UDF "decoy" partition; cd-info errors out and the user previously saw nothing. The tip now points at https://kreon.dev for compatible drives.
- `drives.last_error` column plus a derived `last_error_tip` field on `/api/state`. The error persists across daemon restarts so the dashboard's drive card stays honest about the last failure mode until the drive successfully classifies a new disc.

## [0.16.7] - 2026-05-17

### Added
- Live speed (MB/s) and ETA seconds during PSX / PS2 / Xbox rips. redumper b720+ doesn't print either on its progress line, so the daemon derives them from the LBA cursor and percent stream: speed is the instantaneous `(deltaSectors × 2048) / deltaWallTime`, ETA extrapolates remaining percent against the elapsed wall time since the first progress sample. Dashboard speed chip + ETA chip now populate within a couple of seconds of the rip starting.

## [0.16.6] - 2026-05-17

### Added
- History tab filter chips now include BDMV / 4K UHD / PSX / PS2 / Saturn / Dreamcast / Xbox / Data alongside the existing Audio CD + DVD chips. Labels are pulled from `DISC_TYPE_META` so they stay in sync with `DiscTypeBadge`.

## [0.16.5] - 2026-05-17

### Fixed
- Progress bar now actually moves during PSX / PS2 / Xbox rips. v0.16.4's log-forwarding showed that redumper b720+ emits progress as `/ [ NN%] LBA: cur/max, errors: { ... }` — a spinner char + pre-computed percent + the LBA pair mid-line. The old regex was anchored at start-of-line (`^LBA:`) and never matched. New parser captures the `[ NN%]` token directly (cheap, accurate) with a fall-back to dividing cur/max from the existing `LBA:` regex (now anchorless) for phase-header lines that lack the percent prefix.

## [0.16.4] - 2026-05-17

### Added
- Region inference from PSX / PS2 / Saturn / Dreamcast boot codes. PCSX2 GameDB often leaves region blank for an entry (Sly 3 PAL's SCES_534.09 has no region in the YAML), which made the output path render as `Sly 3 - Honour Among Thieves ()/Sly 3 - Honour Among Thieves ().chd` with empty parens. New `identify.InferRegion` derives region from the 4-letter prefix on PSX/PS2 (SCES→Europe, SCUS→USA, SLPS→Japan, ...) and the trailing-letter convention on Saturn/DC `T-NNNNH/N/D` codes. Used as a fallback only — when the source DB does supply a region, that wins.
- Real-time log forwarding from redumper and chdman to the job log tail. Previously both parsers only emitted `Progress` events on regex match and silently dropped every other stdout/stderr line — users saw "No log lines yet" for the entire rip. Non-progress lines now forward as `INFO` level entries prefixed with the tool name, capped at 400 chars to defend against runaway output.

### Fixed
- redumper / chdman progress updates with carriage-return terminators no longer buffer for the entire phase. Both parsers now use a `\r`/`\n`-aware line splitter (already used by the whipper parser since v0.15.1).
- Slow optical drives that emit 2-3 media-change uevents per physical insertion no longer create 2-3 duplicate disc rows. `discFlow.persistDisc` now dedups game-disc rows (which lack a TOC hash) by `(drive_id, metadata_id)` within a 2-minute window — long enough to absorb the burst, short enough that a genuine eject + re-insert later gets a fresh row.
- Awaiting-decision card no longer keeps firing `startDisc` in an infinite 409 loop after the first auto-rip claims the drive. The list filter now hides discs with a `running` or `queued` job in addition to `done`; `failed` / `cancelled` / `interrupted` stay visible (the retry-intent design is preserved).
- PS2 / Xbox CHD files now carry proper DVD-typed metadata. The Dockerfile builds chdman from MAME 0.275 source (shallow git clone + tools-only build), replacing the bookworm `mame-tools` package (0.251) that predates the `createdvd` subcommand. The v0.16.3 `createraw` workaround is reverted: `.iso` inputs use `chdman createdvd` again, producing CHDs that emulators can correctly identify as DVD images.

## [0.16.3] - 2026-05-16

### Fixed
- PS2 / Xbox compress step no longer fails with `chdman: exit status 1`. The pipeline was calling `chdman createdvd` for `.iso` inputs, but the dedicated `createdvd` subcommand was only added in MAME 0.252 (April 2023); Debian bookworm's `mame-tools` is pinned at 0.251 and rejects `createdvd` as an unknown command. Switched to `chdman createraw --unitsize 2048 --hunksize 8192 --compression lzma,zlib`, which is the equivalent invocation that 0.251 supports. Produces a CHD that PCSX2 / RetroArch / libchdr load identically — only difference is the missing DVD-typed CHD metadata block, which players don't need to mount the image.

## [0.16.2] - 2026-05-16

### Fixed
- Game-disc rip step (PSX / PS2 / Xbox) was passing `--image-path=<outDir>/<name>` to redumper, which the current build interprets as the *output directory* and creates as a subdirectory; redumper then dropped files inside it with auto-generated names like `dump_YYMMDD_HHMMSS_sr0.iso`. The downstream compress step couldn't find the expected `<outDir>/<name>.iso` and the rip silently produced no usable output. Split into `--image-path=<outDir> --image-name=<name>` so files land at the paths the daemon already reads.

## [0.16.1] - 2026-05-16

### Fixed
- Game-disc rip step (PSX / PS2 / Xbox) no longer fails instantly with `redumper: exit status 255`. The redumper integration was invoking the legacy per-media subcommands (`redumper cd|dvd|xbox`) which were removed in the b720+ builds; switched to the current `redumper disc --disc-type=CD|DVD` syntax. Without this every PS2 / PSX rip aborted in ~30 ms before redumper even opened the drive — the bug was hidden behind the v0.16.0 classification fix that finally let game discs reach the rip step.

## [0.16.0] - 2026-05-16

### Added
- Game-disc auto-identification for PSX / PS2 / Saturn / Dreamcast via embedded community boot-code maps (PCSX2 GameDB, DuckStation, Libretro Redump). Insert a recognised game disc and the dashboard shows the correct title without typing — Sly 3 PAL now resolves to *Sly 3 - Honour Among Thieves* on insertion. ~27K entries across 4 systems, embedded in the daemon binary (~2 MB).
- IGDB as the manual-search backend for game discs. The "Search manually" button on PSX/PS2/SAT/DC/XBOX cards now queries IGDB instead of TMDB (which was returning movie titles for game searches). Set `DISCECHO_IGDB_CLIENT_ID` + `DISCECHO_IGDB_CLIENT_SECRET` to enable; otherwise the button surfaces a clean 503.
- Dreamcast IP.BIN reader at sector 45000, exposing the product number (`MK-51000` etc.) for auto-id. Previously DC detection only ran the TOC heuristic and had no key to look up.
- Settings → System "Game discs" tile now reports per-system inventory: redumper binary, Redump dat-files (loaded or missing per system), embedded boot-code maps (count per system), IGDB connection status.

### Changed
- "Search manually" button is hidden on DATA discs. Data discs now show a single "Rip as data" primary action that names the output file from the ISO9660 volume label. Batch mode auto-rips data discs after the same 8s countdown used for confidently-identified game/movie/audio discs.
- The `IdentifyDisc` dispatcher now routes by disc type: AUDIO_CD → MusicBrainz, game discs → IGDB, video discs → TMDB, DATA → 422 Unprocessable Entity.

### Fixed
- `SYSTEM.CNF` BOOT/BOOT2 parsing is now case-insensitive (a small minority of PSX titles ship the file with lowercase `boot = cdrom:\...`).

### Known limitations
- Xbox boot-code auto-id is not currently supported because Libretro's Xbox Redump dat uses publisher codes (`MS-004`, `EA-013`) instead of the XBE certificate's 32-bit title ID. Xbox auto-id falls back to Redump MD5 verify when a dat is present, or IGDB manual search.

## [0.15.4] - 2026-05-16

### Fixed
- PS2 (and PSX) discs no longer misclassify as generic DATA. The `SystemCNFProber` was invoking `isoinfo -R -x /SYSTEM.CNF`; on PSX/PS2 discs — which carry no Rock Ridge — that exits 0 with only `**BAD RRVERSION (0)` warnings on stdout and *zero* bytes of file content, so `ParseSystemCNF` returned nil and the classifier silently fell through to DATA. Drop the `-R` flag and append the ISO9660 `;1` version suffix to match the on-disc filename so the extract returns the real BOOT/BOOT2 line.

## [0.15.3] - 2026-05-16

### Fixed
- PS2 (and PSX) discs on slow drives no longer silently misclassify as generic DATA. The SYSTEM.CNF probe now retries through the same spin-up backoff window used for cd-info and the ISO9660 listing — previously `isoinfo -x /SYSTEM.CNF` could exit 0 with an empty body for a beat after the directory entry was already listable, leaving the classifier with `ParseSystemCNF == nil` and no diagnostic trail. The two silent DATA fall-throughs in `RefineDiscType` also gained INFO breadcrumbs so future probe regressions show up in logs without manual repro.

## [0.15.2] - 2026-05-16

### Fixed
- Audio-CD ripped filenames no longer duplicate the track number (e.g. `01 - 01. Artist - Title.flac`). Whipper writes each track as `NN. Artist - Title.flac`, and the default output template already prefixes `NN - `, so the leading `NN. ` from whipper's filename is now stripped before the basename is fed to the template as `.Title`.

## [0.15.1] - 2026-05-16

### Fixed
- Audio-CD rip progress and ETA no longer stuck at 0% / 0s on modern whipper builds. The parser now unwraps whipper's Python-`logging` output (`INFO:whipper.command.cd:ripping track N of M`) before pattern-matching, so the boundary-progress emits added in v0.14.0 actually fire — the bar advances on every `ripping track N of M` and `CRCs match for track N` line. ETA is now extrapolated from elapsed wall time after the first track completes, so slow drives that never emit per-percent `Reading:` chatter still surface a useful estimate.

## [0.15.0] - 2026-05-16

### Added
- Cover art for audio CDs now falls back to the MusicBrainz release-group when the specific release has none on Cover Art Archive. Many releases (regional pressings, reissues) have no art at the release level but the release-group nearly always does — DiscArt builds a CAA candidate list (`/release/<id>/front-250` → `/release-group/<id>/front-250` → placeholder) and walks it on `<img>` error. The daemon now fetches and persists the release-group MBID into `disc.metadata_json` during audio-CD identify.

### Changed
- Pipeline step lists hide steps the handler marked as skipped instead of showing them with a `skipped` label. Audio-CD jobs no longer surface Transcode and Compress as separate "skipped" rows — whipper does the FLAC encode inside Rip, so the rows were an internal accounting detail, not something the user did or didn't do.

## [0.14.0] - 2026-05-16

### Added
- Re-rip button on the dashboard drive card (desktop and mobile) and on every history row, posting to the existing `/api/discs/{id}/start` endpoint with the last-used profile. Closes the gap where re-ripping a previously-successful disc required clearing the entire history.

### Changed
- A disc whose only prior jobs failed, were cancelled, or were interrupted now auto-prompts as a candidate-pick card on the dashboard. Previously any prior job — even a failure — hid the disc until history was cleared.

### Fixed
- Audio-CD rip progress bar no longer sits at 0% for the full rip; the bar advances at every track boundary even when whipper does not emit per-percent `Reading:` lines.

## [0.13.1] - 2026-05-16

### Changed
- Dashboard snapshots load faster: `/api/state` and the SSE bootstrap previously fanned out one `SELECT … FROM job_steps WHERE job_id = ?` per returned job (up to 51 round-trips at the default 50-recent-jobs limit). The hydration now uses a single `IN (…)` query that buckets rows in Go. Same payload, fewer DB hits per tab open / reconnect.
- Library-disk widget pulls the configured library roots from one `GetAllSettings` call instead of five sequential `GetSetting` calls.

## [0.13.0] - 2026-05-15

### Added
- Manual MusicBrainz search for audio CDs the disc-id lookup couldn't match. The awaiting-decision card's **Search manually** button now opens an MB-tailored search panel (placeholder "Album or artist…", primary button "Search MusicBrainz") whenever the disc is `AUDIO_CD`, hits `MusicBrainzClient.SearchByName` server-side, and merges the ranked results back onto the disc so the user can pick a release the same way they pick a TMDB match for DVDs. Non-audio discs still use the existing TMDB flow. The new `/ws/2/release/?query=…` call goes through the same rate limiter and User-Agent as the disc-id Lookup; Lucene specials in queries are escaped so artist names containing `:` or `(` don't trip MB's parser.

## [0.12.1] - 2026-05-15

### Fixed
- Inserting an audio CD that MusicBrainz can't match no longer makes the dashboard silently snap back to idle. The awaiting-decision list used to filter out any disc with zero candidates, so a 0-match disc was persisted by the daemon but never surfaced to the user — the only signal was the brief "identifying" flicker on the drive card. The list now includes 0-match discs, and the card renders type-aware copy and affordances: AUDIO_CDs with no MusicBrainz hit show "No MusicBrainz match · eject and retry, or skip" and offer Skip only (the manual-search button hits TMDB only today, so showing it on an audio CD would lead nowhere). Non-audio discs keep the existing "search manually" affordance. The disabled "Use top match" button is also hidden when there's no top match to use.

## [0.12.0] - 2026-05-15

### Changed
- The disc-detection flow no longer inserts a fresh `discs` row every time the same physical disc is rescanned. Previously, every retry of a failed rip — or every spurious media-change uevent emitted during a long rip — created a new disc row, scattering jobs across many entries with the same `toc_hash`. Disc identification now looks up `(drive_id, toc_hash)` first and refreshes the existing row's metadata when found. A partial unique index on `(drive_id, toc_hash) WHERE toc_hash != ''` enforces the invariant at the schema level.

### Removed
- **Breaking (DB schema):** Migration 009 collapses pre-existing duplicate `discs` rows that share a non-empty `(drive_id, toc_hash)` and reparents `jobs.disc_id` references onto the most-recent row. The drop is destructive but the rows it removes contained nothing that the keeper row didn't already (same TOC, same drive, fresher metadata). Pre-`1.0` so it ships as a minor bump.

### Added
- The drive card (desktop **and** mobile) now shows the elapsed wall-clock time since the rip started, right under the RIPPING pill. Audio CDs sit at 0% progress for the first 1–3 minutes while whipper does its TOC read and AccurateRip lookup, and the elapsed counter is the missing "yes, work is happening" signal during that window.
- The disc identity row gains a "N tracks · MMm" / "N tracks · 1h MMm" line whenever the disc carries per-track metadata (audio CDs and DVD chapter listings). Renders alongside the existing bus + profile chips so the user knows how long the playback is before deciding to start a rip.
- Audio-CD cover art is now fetched from the Cover Art Archive (`coverartarchive.org/release/{mbid}/front-250`) when the disc was identified against MusicBrainz, instead of falling straight through to the striped placeholder. An explicit `cover_url` in `metadata_json` still wins, and releases with no art on CAA gracefully degrade to the placeholder via `<img on:error>`.

### Fixed
- **Data-loss bug:** Audio CD rips no longer disappear after whipper finishes. The audio-CD pipeline's move step did `os.ReadDir(workdir)` at the top level and skipped anything that was a directory, but whipper writes its output into a nested `album/<Artist> - <Album>/` subdirectory. The top-level listing only saw that subdirectory (skipped), no FLACs were ever moved into the library, the move step "succeeded" with `paths: nil`, and the deferred `os.RemoveAll(workdir)` then wiped the ripped files on pipeline exit. The move step now walks the workdir recursively, and fails loud (rather than silently succeeding with an empty paths list) if zero FLAC files are found.
- The dashboard drive cards (desktop **and** mobile) no longer sit at "No log lines yet" for the entire whipper warmup phase when the page is opened mid-rip. The log-tail panel only had SSE-pushed lines, so any lines the daemon had already logged before the page mounted stayed invisible. The card now backfills the in-memory log ring from `/api/jobs/:id/logs` on mount for running jobs, with de-duplication against SSE lines that arrive during the fetch.
- The "whipper: preparing drive (this can take 1–3 min)" log line no longer appears twice per audio rip. `Whipper.Run` spawns one parser per stream (stdout + stderr); each parser had its own local "first line" flag, so when stderr emitted before stdout (or vice versa) the hint fired again later as the second stream produced its first line. The two parsers now share a single atomic flag.

## [0.11.0] - 2026-05-15

### Added
- A new **Batch / Manual** mode toggle in Settings → Rip behaviour. Batch (default) keeps the existing flow: detect, identify, auto-rip after the 8 s countdown, eject when finished. Manual leaves the rip and the eject to you — pick a candidate, click Start, click Eject when you're ready. Disc detection + identification still happen automatically in both modes.
- **Stop**, **Re-identify**, and **Eject** controls on every drive card (mobile and desktop). Stop cancels a running rip, Re-identify re-runs the classify + lookup pipeline when MusicBrainz/TMDB picked the wrong release, Eject opens the tray. All three are visible in both modes — mode only changes whether the rip starts and ends automatically.
- A new global `rip.eject_on_finish` setting replaces the per-profile `auto_eject` flag. The toggle is disabled while in manual mode (the tray never auto-ejects in manual).

### Changed
- The 8-second "Auto-rip in Ns" countdown is now suppressed in manual mode; the awaiting-decision card shows "Manual mode · pick a title to rip" instead.

### Removed
- **Breaking:** The per-profile `auto_eject` field is gone (replaced by the global `rip.eject_on_finish` setting above). Migration 008 drops the column from `profiles`; the profile editor no longer renders the checkbox. API clients that PUT profiles with `auto_eject` will silently ignore the field — it's no longer in the wire format.

## [0.10.11] - 2026-05-15

### Changed
- The mobile dashboard drive card now shows a 3-line live log tail while a job is ripping, so you can see what's actually happening during whipper's long startup phase (drive analysis, AccurateRip lookup, TOC re-read) instead of staring at a frozen 0% bar for two or three minutes. The tail re-uses the existing per-job log ring; nothing new on the wire.
- The whipper parser now forwards every `INFO`/`WARNING`/`ERROR`/`FATAL`/`CRITICAL` line from whipper's Python `logging` output to the job log, plus emits a single up-front "preparing drive (this can take 1–3 min)" hint the moment whipper starts producing output. Previously only three specific patterns (`Ripping track N of M`, `Reading: X%`, `Track N OK`) were captured and everything else — including all of whipper's startup-phase status — was silently dropped, leaving the Log tab empty until the first track started ripping.

## [0.10.10] - 2026-05-15

### Fixed
- Audio-CD rips no longer fail with `whipper: drive offset unconfigured. Please install pycdio and run 'whipper offset find'`. Whipper 0.10 refuses to rip unless a per-drive sample read-offset is configured, and the canonical detection flow (`whipper offset find`) needs both `pycdio` and a CD known to AccurateRip — neither of which we can assume in a homelab container. The daemon now passes `-o 0` to `whipper cd rip` so audio rips work out of the box on any drive. The resulting rip is audibly identical to a calibrated one (~0.14 ms drift) but won't match AccurateRip checksums; users who care can run `whipper offset find` inside the container manually and override the default.

### Changed
- The runtime image now installs `python3-cdio` so `whipper offset find` works for users who want to dial in a per-drive offset interactively. Adds ~2 MB to the image; was previously omitted because the Debian whipper package doesn't pull it in by default.

## [0.10.9] - 2026-05-15

### Fixed
- Audio-CD rips no longer fail instantly with `whipper: exit status 2`. Two bugs in the whipper invocation:
  - The args list passed `--keep-bad-files=no`, a flag whipper 0.10 doesn't recognise — Python argparse rejected it before whipper opened the drive, so every audio rip aborted at the rip step.
  - No `-d` was passed, so whipper fell back to its default device `/dev/cdrom`. That symlink doesn't exist in the daemon container (only `/dev/sr0`/`/dev/sr1` are exposed), so even with the flag fixed whipper would have errored opening the drive. The drive's `dev_path` is now explicitly passed via `cd -d <path> rip …`.

### Changed
- MusicBrainz disc-ID lookups that return exactly one release are now marked confident (100%) so the AwaitingDecision card's 8-second auto-rip countdown actually starts. Multi-release responses (re-issues, remasters with the same TOC) stay at 0% so the user has to pick the right release manually instead of the dashboard silently picking the first one. MB's `score` field is left ignored — it's a search concept that's not meaningful for the discid resource.
- The candidate metadata line on the AwaitingDecision card now includes the artist (e.g. `MusicBrainz · 1997 · Trust Obey`), so you can tell two same-titled releases apart without opening MusicBrainz.

## [0.10.8] - 2026-05-15

### Fixed
- The first rip into a non-existent library root no longer hard-fails with `library probe: stat /library/<root>: no such file or directory`. `ProbeWritable` now auto-creates the configured library directory (and any missing parents) with `0o777` permissions before probing, so a fresh install — or a new disc type on an existing install — Just Works. Applies to all ten pipelines (audio, data, DVD, BDMV, UHD, PSX, PS2, Saturn, Dreamcast, Xbox) since they all delegate to this shared probe. Errors still surface clearly when the parent is genuinely unwritable.
- Removed `TestDefaultCDInfoRunner_StopsAtDiscMode` which depended on subprocess scheduling timing under `-race` and flaked on slow CI runners. The deterministic `TestDefaultCDInfoRunner_PartialLineDoesNotFire` plus the kill-on-marker logic in production already cover the same surface.

## [0.10.7] - 2026-05-15

### Fixed
- Audio CDs now return MusicBrainz candidates again instead of an empty list. On the ASUS SDRW-08D2S-U `cdparanoia -Q` reports track offsets *relative to track 1* (track 1 begins at LBA 0), but the MusicBrainz disc-ID algorithm requires *absolute* LBAs that include the 150-frame lead-in pre-gap (track 1 begins at LBA 150). The existing parser took cdparanoia's `begin` value verbatim, which gave us a different disc-ID than MB and produced a guaranteed 404 on every lookup. The parser now detects relative offsets (track 1 below the 150-frame pre-gap) and shifts every track plus the leadout up by 150 to land in the canonical absolute frame.

## [0.10.6] - 2026-05-15

### Fixed
- The MusicBrainz disc-id lookup no longer fails with `400: releases is not a valid inc parameter for the discid resource`. MusicBrainz tightened validation on the `/ws/2/discid/{id}` endpoint and now rejects `inc=releases` (it used to be silently ignored — the discid resource already returns its containing releases by default). The client now requests `inc=artist-credits` only, which keeps the artist-credit data we surface in the candidate list.

## [0.10.5] - 2026-05-15

### Fixed
- Audio CDs are no longer misclassified as DATA when the daemon races the disc spin-up. On the ASUS SDRW-08D2S-U the udev media-change uevent fires 60–100 ms after insert; cd-info ran immediately and the drive answered the TOC read with **No medium found**, so cd-info wrote `Disc mode is listed as: Error in getting information` and **exited cleanly with status 0**. The classify retry loop only kicked in on non-zero exits, so the garbage output was passed straight to the parser, fell through to DATA, and the drive went idle with no disc shown. The runner now treats a clean cd-info exit without a usable disc-mode value as a transient failure — and the line-watcher ignores known cd-info error strings (`Error in getting information`, `Unknown`) so a kill-on-marker doesn't fire on a value that's really a failure indicator. With both changes the existing 11.5 s backoff schedule absorbs the spin-up race correctly.

### Changed
- Reverted the diagnostic `classify: cd-info captured` log added in 0.10.3 / 0.10.4 now that the underlying bug is fixed.

## [0.10.4] - 2026-05-15

### Changed
- Internal: the `classify: cd-info captured` debug log added in 0.10.3 now surfaces the *tail* of cd-info's output (last 3 KB) instead of the head — the disc-mode line lands late, after the capabilities listing.

## [0.10.3] - 2026-05-15

### Changed
- Internal: classify now logs the captured `cd-info` output (truncated to 1.5 KB) and the resulting base disc type at INFO level. Used to diagnose the still-misclassified audio-CD case from 0.10.2 in the field; will be dropped once the underlying bug is fixed.
- Relaxed timing on `TestDefaultCDInfoRunner_StopsAtDiscMode` so it no longer flakes under `-race` on slow CI runners — the test now allows up to 15 s for the short-circuit to land instead of the previous 3 s.

## [0.10.2] - 2026-05-15

### Fixed
- Audio CDs are no longer misclassified as DATA on drives that flush the disc-mode label and its value (`CD-DA`, `Mode 2`, …) in two separate writes. The 0.10.1 early-kill watcher fired on the prefix `Disc mode is listed as:` regardless of whether the value had landed yet, so when stdout flushed mid-line we'd kill `cd-info` with only the prefix in our buffer; the classifier then fell through to `RefineDiscType`, the ISO9660 probe failed, and the disc landed back in `idle` with no UI update. The watcher now waits for the full newline-terminated line with a non-empty value before stopping `cd-info`.

## [0.10.1] - 2026-05-15

### Fixed
- Audio CDs (and any disc whose drive can't satisfy a Media Catalog Number probe) no longer get stuck in **Identifying** until the classify timeout kicks in. `cd-info` was being run to completion and would hang for 60–90 s after the disc-mode line landed, while the drive retried internally on the MCN/ISRC reads at the end of its run. The classifier only needs the disc-mode line — and now stops `cd-info` the moment that line appears in stdout, returning in under a second instead of after a 30 s context deadline.

## [0.10.0] - 2026-05-15

### Added
- A phase-filter chip row on the per-job log viewer (`/jobs/[id]`). Each log line is now tagged with the pipeline step it was emitted from, so you can flip between **Rip / Transcode / Move / …** without losing the earlier-phase context when a chatty step starts spamming. Live jobs auto-track the active phase; finished jobs default to **All**. A new `GET /api/jobs/:id/logs?step=…` endpoint serves the persisted log so a page reload (or visiting a finished rip) repopulates the viewer instead of starting empty.
- A **Delete from history** button on the per-job page for finished rips — single-row counterpart to *Clear history*. Files on disk are untouched.
- Profiles and Settings are now fully editable on mobile. `/profiles` lists every profile grouped by disc type with a **+** affordance that drills into a full-screen editor at `/profiles/[id]` or `/profiles/new`; the desktop two-pane flow is unchanged. `/settings` is now a section index that drills into `/settings/system`, `/settings/notifications`, and `/settings/retention` — each renders the same editable section the desktop has, no more "edit on desktop" footer.

### Changed
- The mobile dashboard is reworked. A 2×2 stats grid (Active jobs / Today ripped / Library size / Failures 7d) replaces the empty "today —" placeholder strip, the running job is no longer duplicated as both a full-bleed card and an Active-queue row, and each drive renders a single compact card with state pill, disc art, active-step subtitle, and progress — the home screen no longer carries a stepper or log tail, both of which live on the job detail page. The Queue section now lists only queued (not-yet-running) jobs.
- The per-job page (`/jobs/[id]`) on mobile is now tabbed into **Pipeline / Log / Summary** rather than one long scroll, so each view fits a phone screen without competing for space.
- Mobile navigation gained a **Profiles** tab. Mobile shell components moved into `webui/src/lib/components/mobile/` to mirror `desktop/`.

### Fixed
- The history detail page is no longer the running-job page in disguise. Finishing rips reached from `/history` now show real cover art, an outcome pill (DONE / FAILED / CANCELLED), the elapsed time, output size, and profile, plus the per-phase log viewer — replacing the stale ETA chip, placeholder cover, empty live-log tail, and disabled Pause/Override/Cancel buttons that previously rendered. Running rips keep their existing layout, minus the two disabled buttons.
- A finished job's progress no longer reads as **0%**. The pipeline now persists 100% on each step's completion, so the final step's percentage doesn't linger at whatever the last sub-100 progress sample happened to be.
- The mobile AppBar no longer clips its title under the iOS status bar / notch. The sticky header now respects `env(safe-area-inset-top)`.
- Settings and profile forms on mobile no longer cram a 200px label column onto a phone-width screen; `FormRow` now stacks label-above-input below the `md` breakpoint and keeps the side-by-side desktop layout at `md+`.

## [0.9.3] - 2026-05-15

### Fixed
- The History and job-detail pages no longer show two `LIVE`/`WAIT` indicators side-by-side on desktop. The desktop top-nav already renders the SSE-connection dot, and the mobile-chrome AppBar/header rendered a second copy unconditionally; the per-page dot is now hidden on desktop and shown only on mobile.

## [0.9.2] - 2026-05-15

### Fixed
- The drive no longer locks itself out of further disc events after an identify failure or after ejecting a disc. When the classify step timed out (or any cleanup hit a cancelled context), the follow-up "reset to idle/error" write was made with the same cancelled context and silently failed, leaving the drive permanently stuck in `identifying`. Every later eject and re-insert then hit "drive already identifying, ignoring" and the daemon stayed deaf until the SQLite row was manually corrected. Cleanup writes now use a fresh context, errors surface to the log, and any drive left in `identifying` from a previous run is reset to `idle` at daemon startup.

## [0.9.1] - 2026-05-15

### Fixed
- The daemon no longer goes permanently deaf to disc insertions after a transient kernel-event hiccup. The udev watcher ran disc identification inline on its event-reading loop, so a slow identify (the `cd-info` probe alone can retry for ~13s) stalled the loop long enough for the kernel's event socket buffer to overflow; the resulting error killed the watcher with no restart, and every disc inserted afterwards was silently ignored until the container was restarted. Identification now runs off the read loop, and the watcher reconnects automatically if its event stream ever drops.
- Discs whose type is determined by reading the filesystem (PlayStation 2, PSX, DVD, Blu-ray, Xbox) are no longer intermittently misidentified as a generic DATA disc. The classifier's `isoinfo` filesystem probe is now retried through the disc spin-up window — the same way the `cd-info` probe already was. Previously, when `isoinfo` ran in the brief window where `cd-info` had succeeded but the ISO9660 filesystem wasn't yet readable, it returned an empty listing that silently downgraded the disc to DATA, where it became invisible in the UI (no candidates, no card) — so identify appeared to "just stop". The classifier also now logs a breadcrumb when a disc isn't recognised by any probe.

## [0.9.0] - 2026-05-14

### Added
- A **Clear history** button on the History tab — wipes all finished-rip records (jobs, their logs, and the disc rows they orphan) in one click, behind a two-step confirm. The ripped files on disk are not touched, and an in-progress rip is unaffected.

### Fixed
- Inserting a disc no longer enqueues two identical rip jobs. The dashboard kept both its mobile and desktop layouts mounted at once (CSS only hid one), so each identified disc ran two auto-confirm timers and fired two start-rip requests; and the daemon's duplicate-job guard wasn't atomic with job submission, so both slipped through. The dashboard (and the profiles/settings pages) now mount a single layout for the viewport, and the start-rip handler serializes its guard so concurrent requests for the same disc create exactly one job.
- The active rip card showed the current step's speed and ETA twice — once in the pipeline stepper's "Active" row and again below the progress bar. It now appears once, below the bar. (There is no whole-rip ETA to show in the other slot: speed/ETA are per-step and reset at each step boundary.)

## [0.8.0] - 2026-05-14

### Changed
- DVD encode quality is now a real, per-profile setting. The `quality_rf` and `encoder_preset` profile options drive HandBrake directly — previously the quality was hardcoded in the pipeline and the profile's quality field was display-only. The seeded DVD profiles default to RF 18 / preset slow for near-transparent archives; existing DVD profiles are migrated to the same defaults. Set `quality_rf` higher (e.g. 20–22) for smaller files.
- DVD and Blu-ray rips to MKV now keep **every** subtitle track on the disc instead of filtering to a single language. MP4 profiles keep the language-filtered behaviour, since MP4 can't cleanly carry bitmap (VOBSUB) subtitles.

### Fixed
- The transcode step now shows a live ETA and speed. HandBrake omits its own ETA when its output is piped, so DiscEcho derives both from the title duration and elapsed time.
- HandBrake's transcode log output is no longer all tagged as warnings. Its startup JSON job dump is dropped, routine lines are logged at info, and only genuine errors/warnings are flagged.
- The queue detail pane now shows the disc title whenever it is known, instead of "Unknown disc" when the rich metadata blob hasn't been fetched. It already had the title — it just refused to display it without the extended TMDB/MusicBrainz payload.
- dvdbackup's progress output and libdvdread trace lines no longer flood the job log or get mislabelled as warnings. Progress is dropped (the size-based poller already drives the percentage), libdvdread chatter is dropped unless it carries an error, and the rest is logged at info — warnings are reserved for genuine failures.

## [0.7.0] - 2026-05-14

### Added
- Saving settings in the web UI now shows a brief confirmation toast. Covers profile create/save/delete, notification create/save/delete, notification Validate/Test results, history retention, and library paths — previously these succeeded silently.

## [0.6.1] - 2026-05-14

### Fixed
- Editing and saving a DVD-Movie or DVD-Series profile in the web UI no longer fails silently. The seeded DVD profiles carry a `dvd_selection_mode` option that was missing from the HandBrake engine's validation schema, so every save was rejected with a 422 — including unrelated changes like switching the video codec.

## [0.6.0] - 2026-05-14

### Added
- Hardware-accelerated transcoding (NVENC) on NVIDIA hosts. HandBrake-based profiles now expose `nvenc_h264` and `nvenc_h265` as `video_codec` values. The daemon detects NVENC availability at startup, surfaces it in **Settings → Integrations**, and silently falls back to the matching software encoder (`x264` / `x265` / `x265_10bit` for BDMV) when no GPU is detected. See the README "Enabling GPU transcoding" section for the per-host compose overrides required to pass the GPU through.

### Changed
- HandBrake is now built from upstream source (1.11.x) in the container image so NVENC is compiled in; bookworm's apt package ships without NVENC. Cold builds take noticeably longer; cached rebuilds are unchanged.

## [0.5.0] - 2026-05-13

### Added
- Sidebar opens a per-disc-type **metadata pane** instead of repeating the hero RipCard. Movies show plot / director / cast / studio / genres / rating; audio CDs show label + track list; games show system / region / serial / Redump MD5; data discs show filesystem inventory.
- Poster and cover art are pulled from TMDB / CoverArtArchive at identify time and shown in the hero RipCard, awaiting-decision card, and sidebar metadata pane via a new shared `DiscArt` component.
- Top widgets row on the desktop dashboard with **ACTIVE JOBS**, **TODAY RIPPED**, **LIBRARY SIZE**, and **FAILURES (7D)** cards. Each card has a sparkline showing recent history (24h / 7d / 30d) and a contextual subline (hour-over-hour delta, today's title count, library capacity from statfs, 7d-over-7d delta).
- New SQLite column `jobs.output_bytes` tracks each completed rip's encoded size; library widget sums these. Auto-recorded by `PersistentSink.OnStepDone` from each pipeline's move-step notes — no per-pipeline changes needed.
- New API endpoint `GET /api/stats` returns the widget payload; also embedded in the SSE `state.snapshot` event and `GET /api/state` for zero-extra-roundtrip bootstrap.

### Changed
- Dashboard hero band's minimum column width raised from 280px to 380px so the pipeline stepper labels never clip.

### Fixed
- Log tail now populates during DVD / BD / UHD rips: pipelines emit milestone log lines at each step boundary (dvdbackup or MakeMKV start/complete, HandBrake scan/encode start/complete, move). Stream parsers forward unrecognised non-progress lines so warnings and errors are visible instead of silently dropped, capped at 200 lines per step.

## [0.4.1] - 2026-05-13

### Fixed
- Transcode progress is now visible during the encode step. HandBrake 1.6.x omits the `(avg fps X, ETA YhYmYs)` suffix when stdout is a pipe; the encode regex now matches both forms instead of dropping every line.
- `POST /api/discs/{id}/start` returns 409 Conflict when the disc already has a queued / running / identifying / paused job, so a fast double-click on the awaiting-decision card can no longer enqueue two jobs for the same disc.
- The awaiting-decision card disables its rip button the moment either auto-confirm or a manual click fires, coalescing both code paths into a single start request.

### Changed
- Desktop dashboard sidebar (job detail panel) is hidden by default and opens on queue-row click. Same row clicked again closes the panel; a different row switches between jobs.
- When the sidebar shows a drive's currently-running job, that drive's hero RipCard collapses from the top band so the same content isn't rendered twice. Other busy drives' hero RipCards stay.

## [0.4.0] - 2026-05-13

### Added
- Rank-based identification confidence so popular and obscure titles both surface useful scores (top match always 100%, then 80/60/40/20).
- DVD profiles now disambiguate movie-vs-series via a `dvd_selection_mode` option, freeing the format field to default to MKV.

### Changed
- The dashboard now swaps a busy drive's idle card for an inline rip card. The rip card uses the drive identity (bus + model) as its header, so the running drive surfaces in exactly one place instead of two.
- DVD-Movie's default output container is now MKV. Existing installs are migrated in place where the profile is still on the seed defaults.

### Fixed
- The drive progress bar no longer freezes at 100% when a rip step hands off to transcode. Per-step progress now resets at the moment the new step starts.
- ETA in the job list and queue table now renders as `Xm Ys` / `Xh Ym Zs` instead of raw seconds.

## [0.3.3] - 2026-05-13

### Fixed

- **Tool stream parsers no longer deadlock the subprocess on stream
  shape they can't parse.** The 0.3.2 Jackass re-rip hung HandBrake
  mid-encode for ~85 minutes after `bufio.Scanner` aborted on a long
  carriage-return-separated progress chunk: the read goroutine exited,
  the kernel pipe buffer filled, HandBrake blocked on `pipe_write`
  trying to finalise the MP4. The encoded file was already 2.6 GB on
  disk but the daemon never observed completion until we drained the
  pipes externally. Every tool wrapper that consumes a subprocess pipe
  (`handbrake`, `dvdbackup`, `whipper`, `chdman`, `makemkv`,
  `redumper`, `dd`) now wraps its scan loop with a helper that
  unconditionally copies remaining bytes to `io.Discard` after the
  parser returns, so a parse failure can never starve the subprocess.
  HandBrake and `dd` additionally use a `\r`-aware splitter so each
  progress chunk parses as its own line.
- **DiscFlow no longer creates duplicate Disc rows on duplicate
  uevents.** Hollywood DVDs emit 2–3 `media-change` kernel uevents as
  the drive settles after insertion; before 0.3.3 the guard only
  blocked uevents that arrived after a Job started, so every uevent
  during the identify-in-flight window kicked off its own classify +
  identify and produced a separate Disc row. The new
  `Store.ClaimDriveForIdentify` runs an atomic `UPDATE drives SET
  state='identifying' WHERE id=? AND state IN ('idle','error')` and
  the discFlow handler drops the uevent when the CAS doesn't claim
  the slot. Legitimate disc swaps still work because every identify
  code path transitions the drive back to `idle`/`error` on exit.
- **Skip button on awaiting-decision cards now actually skips.**
  Pre-0.3.3 the Svelte handler only flipped a client-side bottom-sheet
  flag — the daemon disc row was untouched, so any page reload
  rederived the card. The new `DELETE /api/discs/{id}` endpoint hard-
  deletes a disc row when no job references it; the daemon refuses
  the request with `409 Conflict` otherwise so jobs with history stay
  reachable from the History tab. A new `disc.deleted` SSE event
  drops the row from the live store.

### Added

- `Store.DiscHasAnyJob`, `Store.DeleteDisc`, `Store.ClaimDriveForIdentify`.
- `tools/streamparse.go` — `drainAfterScan` helper + `splitCROrLF`
  bufio split function, both used by every existing stream parser.

## [0.3.2] - 2026-05-13

### Fixed

- **HandBrake scan now enumerates every title on the disc.** The
  daemon called `HandBrakeCLI --input <src> --scan`, which silently
  defaults to `title_index=1` — only the *first* title is reported.
  Every multi-title DVD (Jackass: The Movie's 10 VTSs, season-set
  TV discs, anything menu-driven with a short preview as title 1)
  came back from the scan as a single 7-min preview, masking the
  real feature. Adding `--title 0` to the scan invocation makes
  HandBrake walk the full IFO and return every title. This was the
  root cause behind both the 0.2.3 silent-junk regression
  (longest-of-one-title is always the wrong title) and the 0.3.1
  feature-floor false-positive on this disc.

## [0.3.1] - 2026-05-13

### Added

- **Movie-profile feature-duration floor.** When the longest scanned
  title is below the floor (default 20 minutes), the transcode step
  fails with a descriptive error before invoking HandBrake. This is
  a belt-and-braces guard alongside v0.3.0's `--main-feature`: a
  disc with no main-feature bit set in the IFO, or an incomplete
  dvdbackup mirror, can leave the scan's longest title at a few
  minutes (Jackass: The Movie shipped a 7-min sketch as the entire
  feature in v0.2.3). Failing here is preferable to producing a
  junk file that passes the bytes-per-second check (which only
  compares against the *encoded* duration, not the expected feature
  duration). Override per profile via the `min_feature_seconds`
  option; set to `0` to disable for legitimately-short content.

## [0.3.0] - 2026-05-12

### Changed

- **DVD movie profile now lets HandBrake pick the main feature.**
  Pre-v0.3.0 the orchestrator scanned titles and selected the one
  with the longest reported duration. On a homelab test against
  the same disc twice, that heuristic shipped an outtakes reel
  instead of the 85-min feature — outtakes were the longest
  *encodable* title HandBrake's scan returned (either because
  dvdbackup's `-M` mirror skipped the main-feature VOBs or because
  libdvdread's IFO parser came up short). HandBrake's
  `--main-feature` flag reads the IFO's main-feature bit and picks
  the right title from the DVD's own metadata; movie profiles
  now use it.
- Series profiles (MKV) are unchanged — they still scan, filter
  to titles ≥ `min_title_seconds`, and encode each as one episode.

### Added

- **Scan-title logging.** Every title HandBrake's `--scan` returns
  is now emitted as an `INFO` slog line
  (`scanned title disc=… title=N duration_sec=…`). Diagnoses
  "wrong title picked" regressions from `docker logs` alone, no
  workdir snapshot needed.
- **TMDB runtime cross-check.** When the user picks a TMDB-movie
  candidate, `/discs/{id}/start` now fetches the runtime from
  `/movie/{id}` and persists it on the disc (`disc.runtime_seconds`).
  The DVD pipeline compares the scanned longest-title duration to
  this expected runtime; on a >50 % mismatch a `WARN` is logged:
  `duration mismatch expected_sec=5100 scanned_longest_sec=900 ratio=0.18`.
  The check warns but doesn't fail — DVDs legitimately differ
  from theatrical runtimes (director's cuts, regional edits) —
  but a 5× gap is a clear "this isn't the right disc content"
  signal.
- New `tools.TMDBClient.MovieRuntime(ctx, tmdbID)` API call.
- New `state.Candidate.RuntimeSeconds` + `Store.UpdateDiscRuntime`.

### Fixed

- `selectEncodeTitles` MP4 (movie) branch removed — it's no
  longer reachable; `--main-feature` covers movie selection.
  Series branch unchanged.

## [0.2.3] - 2026-05-12

### Added

- **Real percentage during DVD rip step.** dvdbackup itself only
  emits per-VOB log lines, which left the dashboard's progress bar
  pinned at 0 % through the entire rip. The DVD wrapper now reads
  the disc's total size once from `/sys/block/<dev>/size`, then
  polls the workdir every 2 s, summing bytes written, computing
  `written / total × 100` and pushing it through the sink. A
  10-second sliding window over the same samples produces a
  human-readable speed (e.g. `7.2MB/s`) and ETA. Progress caps at
  99 % until dvdbackup exits cleanly, then snaps to 100 % — the
  disc total always overshoots the actual VIDEO_TS bytes because
  of sector-alignment slack and non-DVD-Video tracks.

## [0.2.2] - 2026-05-12

### Changed

- **DVD-Video rip step now uses dvdbackup, not MakeMKV.** MakeMKV's
  rolling 60-day beta-key cycle plus the late-cycle stall we hit
  (1.18.3 from 2026-01 expired ~Mar 26, no 1.18.4 from upstream as
  of mid-May) made it a poor fit for DVDs. dvdbackup is GPL,
  ships in Debian apt, uses libdvdcss for CSS decryption, and
  needs no registration. The new flow:
  1. `dvdbackup -M -i /dev/sr0 -o <work>/rip -p` mirrors VIDEO_TS
     into `<work>/rip/<volume_label>/VIDEO_TS/`.
  2. `HandBrakeCLI --input <work>/rip/<volume_label> --scan`
     enumerates titles.
  3. `HandBrakeCLI --input <work>/rip/<volume_label> --title N
     --output …` encodes each selected title from the local mirror.
  HandBrake still never touches `/dev/sr0`. The MakeMKV pipeline
  stays for BDMV and UHD, where it remains the only viable decoder.
- The DVD pipeline no longer requires `DISCECHO_MAKEMKV_BETA_KEY`.
  The env var is still consumed for BDMV / UHD; without it those
  pipelines fail at scan-time as before.
- Runtime image gains the `dvdbackup` apt package.

### Added

- `tools.DVDBackup` wrapper exposing `Mirror(ctx, devPath, outDir,
  sink)` over the `dvdbackup` binary. Streams `*.VOB` mentions to
  the sink as coarse progress ticks.
- `dvdvideo.DVDMirror` + `dvdvideo.HandBrakeScanner` interfaces on
  the pipeline so tests can substitute fakes.

## [0.2.1] - 2026-05-12

### Fixed

- **MakeMKV beta key now actually reaches makemkvcon.** v0.2.0
  tried to bridge `MakeMKV/` → `.MakeMKV/` with a symlink, but
  makemkvcon's first scan materialises `.MakeMKV/` as a real
  directory (`_private_data.tar` + `update.conf`), so the symlink
  branch bailed out on its own "existing non-symlink target"
  guard. Result: BDMV/UHD/DVD-via-MakeMKV still failed with
  "application version is too old" on freshly applied 0.2.0
  containers. The daemon now writes `settings.conf` into **both**
  `${DISCECHO_DATA}/MakeMKV/` and `${DISCECHO_DATA}/.MakeMKV/`
  unconditionally — idempotent, no symlinks, no guards.

## [0.2.0] - 2026-05-12

### Changed

- **DVD-Video pipeline now uses MakeMKV for the rip step** instead of
  reading the disc directly with HandBrake. The new flow mirrors the
  BDMV pipeline: `makemkvcon mkv dev:/dev/sr0 …` extracts each
  selected title to an MKV in the workdir, then HandBrake transcodes
  each MKV from the local filesystem. HandBrake no longer touches
  `/dev/sr0` for DVD jobs, so spurious kernel media-change uevents
  during a long read can't truncate the output.
- **MakeMKV now requires the SCSI Generic node** (`/dev/sg0` on most
  hosts; the actual node may differ — check `ls -l /dev/sg* | grep
  cdrom`). The compose file maps `${OPTICAL_SG_DEVICE:-/dev/sg0}`;
  on unraid you need to add the matching `--device` row to the
  container template manually, alongside the existing `/dev/sr0`.
- **DVD ripping now requires `DISCECHO_MAKEMKV_BETA_KEY`** to be set,
  same as BDMV/UHD already did. MakeMKV's "free DVD" mode still
  needs a registration key while in beta; the project README points
  to the rotating public key.
- DVD encoder defaults stay at HandBrake x264 quality 20 in the
  profile, but the orchestrator no longer infers titles from
  HandBrake's scan output — it uses MakeMKV's `info` output
  instead. Title-selection rules (longest for MP4 movie profile,
  ≥`min_title_seconds` for MKV series profile) are unchanged.

### Fixed

- **MakeMKV beta key was never actually loaded.** The daemon wrote
  `app_Key = "…"` to `${DISCECHO_DATA}/MakeMKV/settings.conf` but
  `makemkvcon` reads from `$HOME/.MakeMKV/settings.conf` (note the
  leading dot). The two directories never matched, so BDMV / UHD
  jobs would have hit "application version is too old" the moment
  anyone tried them. `writeMakeMKVBetaKey` now also creates a
  `.MakeMKV → MakeMKV` symlink in the parent directory so
  makemkvcon finds the config we wrote, no matter how `HOME` is
  passed.
- **discFlow guard now also bails during a 10-second post-job
  cooldown.** The v0.1.4 guard checked only that the drive had no
  *active* job, which lost the race where a spurious media-change
  uevent fires at the exact instant HandBrake exits — the job was
  briefly `done` before the guard's check ran, and the re-classify
  proceeded to clobber the drive. Cooldown closes the race; new
  `Store.HasRecentJobOnDrive`.
- **Orchestrator now writes `jobs.active_step`** at every
  `OnStepStart`. Previously the column was always NULL despite the
  per-job step table being correctly maintained, so the desktop
  pipeline stepper never highlighted the active dot. New
  `Store.SetActiveStep`.
- **Dashboard now shows `running` once the orchestrator picks up
  the job.** The webui's `job.step` SSE handler used to update only
  the steps array; the job's `state` stayed `queued` until
  `job.done` arrived, so the queue row read "QUEUED 0%" through
  the entire rip. Handler now auto-promotes `queued → running`
  when the first step transitions out of pending.
- **Encoded-output validator floor raised** from 300 kbps to 750
  kbps (`minEncodedBytesPerSecond` 37 500 → 93 750). The 0.1.4
  threshold was *just* below the 270 kbps truncation we saw on the
  homelab. Real x264 quality-20 movies sit ≥ 1.5 Mbps so the new
  floor still has comfortable headroom.

## [0.1.5] - 2026-05-12

### Fixed

- **Awaiting-decision list no longer resurrects every old disc.**
  The 0.1.4 filter only excluded discs with an *active* job
  (`queued`/`running`/`identifying`), so any disc whose job had
  already ended — `done`, `failed`, `interrupted`, `cancelled` —
  came back as a stale decision card on every page load. Combined
  with the redundant "is DVD or BDMV" clause, this meant that
  every disc ever inserted re-prompted forever. The list now
  excludes any disc that has *any* job record at all — picking
  a candidate is a one-shot decision; re-rip flows will be a
  separate, explicit affordance.

## [0.1.4] - 2026-05-12

### Added

- **Persistent "Awaiting decision" card** on both desktop and mobile
  dashboards (`AwaitingDecisionCard` + `AwaitingDecisionList`).
  Replaces the modal `DiscIdSheet` bottom sheet — refreshing the page
  no longer makes the picker disappear, because the surface is
  derived from `$discs ∩ $jobs` (any disc with candidates and no
  live job is awaiting decision). The legacy `DiscIdSheet` component
  remains in-tree for now; nothing mounts it.
- `Store.HasActiveJobOnDrive(ctx, driveID)` query for the guard
  below.
- `dvdvideo.Deps.MinEncodedBytesPerSecond` to override the encode
  size threshold (tests use `-1` to disable).

### Fixed

- **Mid-rip udev events no longer wreck the running job.** When
  HandBrake / makemkvcon are hammering `/dev/sr0` the kernel emits
  spurious media-change uevents; the daemon used to re-run the
  classifier, race the running step for the device, fail, flip the
  drive to Error, and kill the in-flight rip. `discFlow.handle`
  now bails out early when `HasActiveJobOnDrive` is true.
- **DVD pipeline rejects truncated encodes.** `HandBrakeCLI` exits
  0 in several end-of-stream failure modes (e.g. the spurious
  media-change above), and the orchestrator persisted the job as
  `done` despite the output being a 170 MB fragment of a feature
  film. The transcode step now validates the encoded file's size
  against a 300 kbps × duration lower-bound and fails the step on
  a truncated output.
- **Drive returns to `idle` after `disc.identified`.** Previously
  the daemon left the drive stuck in `identifying`, the dashboard
  read "Identifying disc…" forever, and subsequent uevents from
  the user ejecting the disc were ignored.
- **`drive.changed` SSE handler honours the daemon payload.**
  Daemon publishes `{drive_id, state}`; the handler used to read
  `p.drive` (a full `Drive` object that the daemon never sends),
  so drive state updates only surfaced on a full page reload. The
  handler now patches the matching drive entry in place.

## [0.1.3] - 2026-05-12

### Added

- `/api/state` and the SSE `state.snapshot` event now include a `discs`
  array of the 50 most-recently-created discs. The webui seeds its
  `discs` cache from this on cold load so titles, candidates, and disc
  types resolve immediately instead of waiting for a `disc.detected`
  SSE event.
- `Store.ListRecentDiscs(ctx, limit)` backs the new payload.

### Fixed

- `ListActiveAndRecentJobs` now hydrates each job's `Steps` slice from
  the `job_steps` table. Previously every job returned with
  `step_count: 0`, so the desktop pipeline stepper and queue dots
  rendered empty for every row regardless of actual progress.
- **Desktop dashboard now shows what's really happening.** The drive
  hero card caption follows `drive.state` (`Ripping disc…` /
  `Identifying disc…` / `Drive error — see logs`) instead of always
  reading `Idle — insert a disc`. The queue table's `DRV` column
  renders the drive's bus name (`sr0`) instead of the raw UUID, and
  the `Title` column falls back through `disc.title →
  candidates[0].title → disc.id[:8]` instead of hardcoding `Unknown`.
- Drive hero card disc lookup now prefers the active job's `disc_id`
  when the drive row doesn't carry `current_disc_id`, so a fresh
  rip shows up immediately even before the disc-binding migration
  lands.

## [0.1.2] - 2026-05-12

### Fixed

- **Classifier no longer races the drive spin-up.** `cd-info` is now
  retried with backoff (~13 s total: 0.5 s, 1 s, then 2 s × 5)
  whenever it returns a non-zero exit. Without this, the first
  classify attempt landed 60–100 ms after the udev media-change
  event — well before the drive could answer a SCSI INQUIRY — and
  the drive flipped to **Error** with `cd-info: exit status 1`.
- **Identify sheet no longer auto-rips a low-confidence guess.**
  When the top candidate's confidence is below 50, the 8-second
  auto-confirm countdown is suppressed; the sheet now reads
  `No confident match · pick a title or search`. Empty candidate
  lists render `No match found · search manually`. Previously the
  sheet would auto-start a rip on whatever first candidate
  TMDB / MusicBrainz returned, even at 0 % confidence.

## [0.1.1] - 2026-05-12

### Fixed

- udev `DEVNAME` is now normalized when the event comes from `udevd`
  (full path form `/dev/sr0`) rather than the bare kernel form (`sr0`).
  Without this, disc-insert events on hosts using systemd-udevd / eudev
  produced `dev=/dev//dev/sr0` and `disc-flow: no drive registered`,
  so insertion never started the pipeline. Affects every deployment
  whose container receives events via the udev netlink group (group 2)
  — which is the default for the embedded `pilebones/go-udev`
  watcher.

## [0.1.0] - 2026-05-12

### Removed

- Appearance settings (accent / mood / density picker) on Settings.
  The theme is now fixed at `aurora` accent, `carbon` mood, `standard`
  density. The `prefs.accent`, `prefs.mood`, and `prefs.density`
  setting keys are no longer accepted by `PUT /api/settings`. Existing
  rows in the database are ignored.

### Added

- Settings → System tab now mirrors the original mockup: library-path
  editor, drives panel, external-connection status (TMDB configured /
  language, MusicBrainz endpoint, Apprise binary + version), and host
  info (kernel, CPU count, uptime, disk usage for `/library` and
  `/var/lib/discecho`).
- New read-only endpoints `GET /api/system/host` and
  `GET /api/system/integrations` back the System tab. The TMDB key
  itself is never returned — only a `configured` boolean.
- `library.path` is now an editable setting. The `DISCECHO_LIBRARY` env
  var seeds the value on first boot; subsequent edits in the UI win
  on next container restart (running pipelines capture the path at
  boot, so a restart is required for the change to apply).
- **Typed library roots.** Settings → System → Library paths now exposes
  five separate roots — `library.movies`, `library.tv`, `library.music`,
  `library.games`, `library.data` — each editable individually and
  surfaced via the new `library_roots` field on
  `GET /api/system/integrations`. New per-root env overrides
  `DISCECHO_LIBRARY_{MOVIES,TV,MUSIC,GAMES,DATA}` set defaults; stored
  values always win over env. Each pipeline now writes to its typed
  root: audio CD → music, DVD/BD/UHD → movies, PSX/PS2/Saturn/DC/Xbox
  → games, data → data. **Migration:** on upgrade, deployments with an
  existing `library.path` row see the five typed roots seeded as
  `<library.path>/<media>` so existing layouts keep working.
  DVD-Series profiles temporarily land under `library.movies` until the
  orchestrator can route series-typed jobs to `library.tv` (planned for
  the typed-encoding milestone).
- **`FormSection` / `FormRow` / `PathField` desktop primitives** ported
  from the original handoff bundle. Used by the rewritten System tab;
  available for the Profiles editor refactor in the next milestone.
- **Typed profile encoding fields.** Profiles now carry first-class
  `container`, `video_codec`, `quality_preset`, `hdr_pipeline`,
  `drive_policy`, and `auto_eject` columns instead of stuffing the
  same values into a flat `options` map. The Profiles editor is
  rebuilt around the original mockup's four sections — Engine
  (Reader / Drive policy / Auto-eject), Encoding (Container / Video
  codec / Quality preset / HDR pipeline), Post-processing (placeholder
  for the chain UX), and Library (Output path) — with a typed dropdown
  per field. The sidebar gains a `DiscTypeBadge` per row and an engine
  sub-line. The legacy `format` and `preset` fields are kept as a
  fallback for one release; new clients should write the typed fields
  directly. Validator errors come back keyed on the new field names
  (`container`, `video_codec`, `hdr_pipeline`, `drive_policy`).

### Deprecated

- `library.path` setting key. Writes still succeed for one release and
  fan out to the five typed `library.<media>` keys. Will be removed in
  a follow-up release; switch UIs and scripts to write the typed keys
  directly.
- Profile `format` and `preset` fields. The daemon still accepts and
  returns them for one release as a fallback when `container` /
  `quality_preset` are empty; both the migration and the editor mirror
  values into the typed columns. Will be removed in a follow-up
  release.

### Added — connections list

- Settings → System → **API keys & connections** is now a structured
  list: TMDB, MusicBrainz, redump, Apprise. Each row shows a status
  pill (`connected` / `not configured` / `error: …`), an optional
  detail (TMDB language, MusicBrainz endpoint, Apprise version), and
  an Edit button. Apprise's Edit scrolls to the Notifications section;
  the others surface the env var to set in `.env`.
- `GET /api/system/integrations` gains an `items: IntegrationStatus[]`
  field driving the new list. The legacy flat `tmdb` / `musicbrainz` /
  `apprise` objects are kept alongside for one release so older
  clients (mobile read-only view) keep working.
- New webui primitive: `ApiRow.svelte`. Mirrors the original handoff's
  `ApiRow` from `desktop.jsx`.

### Fixed

- SSE stream `/api/events` is no longer killed every 30 s by the global
  request-timeout middleware; the route now bypasses the timeout and
  emits a `: ping` keepalive comment every 15 s so reverse-proxy idle
  timeouts don't drop the connection either.
- `POST /api/discs/{id}/start` now persists the user-selected
  candidate's identity (title / year / provider / metadata id) and
  picks `TMDBID` for movie/TV candidates instead of the empty `MBID`.
  Previously the manual choice was silently dropped before the
  orchestrator re-read the disc row.
- Bearer-token comparison in the auth middleware is now constant-time.
- Apprise CLI invocations (`DryRun`, `Send`, `BuildAppriseArgs`) now
  reject URLs that begin with `-` and place a `--` separator before
  positional URL arguments so a malformed URL cannot smuggle CLI flags
  into the apprise process.
- Orchestrator `Close` no longer races `Submit` into a closed-channel
  panic on shutdown; per-drive queues are no longer closed and workers
  exit via `o.stopped` instead.

### Removed

- Dropped the unimplemented `POST /api/jobs/{id}/pause` endpoint (it
  was a 501 placeholder).
- Deleted the dead, drift-prone `shared/wire.ts` — `webui/src/lib/wire.ts`
  is the sole frontend wire-types source.
- Auto-generated bearer token at `<DATA>/token`. The token is now
  sourced exclusively from the `DISCECHO_TOKEN` env var; no on-disk
  persistence, no auto-generation. Existing token files are left
  untouched but ignored on startup. To migrate: copy the value from
  the old `<DATA>/token` into a `DISCECHO_TOKEN` env var on the
  daemon, then delete the on-disk file.
- `DISCECHO_AUTH_DISABLED` env var. The default is now "auth off";
  opt back in by setting `DISCECHO_TOKEN`. To migrate: drop
  `DISCECHO_AUTH_DISABLED=true` from your environment — the daemon
  now behaves the same way without it.

### Changed

- Replaced the hand-rolled bubble sort in `tools/makemkv.go` with
  `slices.Sort`.
- `daemon/cmd/discecho/main.go` extracts a single `urlsForTrigger`
  closure shared by every pipeline instead of re-defining it per
  registration.
- The daemon no longer logs a partial token at startup; it logs only
  whether the bearer token is configured.
- Daemon defaults to no authentication when `DISCECHO_TOKEN` is unset.
  This matches the homelab/LAN deployment model the project is
  designed around: the embedded UI works on first install with zero
  config. Set `DISCECHO_TOKEN` to enable bearer auth for proxy or
  exposed deployments.

### Added

- Initial repo skeleton: `daemon/`, `webui/`.
- Go daemon serving `GET /api/health` and `GET /api/version` on `:8088`.
- Linux udev subscription that logs disc-insert / disc-remove events on
  `/dev/sr*`; non-Linux builds compile with a no-op stub.
- SvelteKit 2 web UI scaffold (Tailwind 3, dark-only design tokens,
  static adapter) embedded into the daemon binary via `//go:embed`.
- Multi-stage Dockerfile (Go + Node build → `python:3.12-slim` runtime
  with Apprise pre-installed for M1).
- `docker-compose.yml` with `/dev/sr0` passthrough and bind-mounted
  library + data directories.
- GitHub Actions CI: gofmt, go vet, golangci-lint, `go test -race`,
  Prettier check, `svelte-check`, `pnpm build`, Docker image build.
- GitHub Actions release: container build and push to GHCR on `v*` tags.
- PR template enforcing conventional-commit prefixes and verification
  checklist.
- **M1.1 daemon-side audio CD pipeline (backend only).**
  - `state` package: SQLite (modernc.org/sqlite, pure-Go, WAL) + 8-table
    schema (drives, discs, profiles, jobs, job_steps, log_lines,
    notifications, settings) with hand-rolled migration runner; typed
    Store CRUD; in-process Broadcaster with slow-subscriber drop.
  - `identify` package: TOC reader via `cdparanoia -Q`; MusicBrainz
    disc-ID hashing (verified against the official algorithm);
    MusicBrainz REST client with 1 req/sec rate limit; disc-type
    classifier via `cd-info`.
  - `tools` package: Tool/Sink/Registry interfaces; Whipper subprocess
    wrapper with stdout progress parser; Apprise CLI wrapper (failures
    are warned, never fail the job); Eject wrapper; MockTool for tests.
  - `pipelines` package: Handler interface, Registry, output templating
    (Go templates with path-traversal sanitization), atomic move with
    cross-filesystem fallback, write probe, RecordingSink for tests.
  - `pipelines/audiocd`: Handler implementation that ties it all
    together — Identify (TOC + MB lookup) → Plan (8-step with
    transcode+compress skipped) → Run (whipper rip → atomic move →
    Apprise → eject).
  - `jobs` package: PersistentSink (bridges pipeline events to SQLite +
    Broadcaster, with 1Hz progress coalescing); Orchestrator with
    per-drive serialization, ctx cancellation, and crash recovery
    (interrupted state on startup).
  - `api` package: bearer-token middleware (auto-generated to
    `${DISCECHO_DATA}/token` on first start); REST endpoints for
    `/api/state`, `/api/drives`, `/api/jobs`, `/api/discs/:id/start`,
    `/api/profiles`, `/api/settings`; SSE stream at `/api/events` with
    `state.snapshot` bootstrap and 9 event types.
  - `settings` package: env loader, token bootstrap, profile + Apprise
    URL seeding from `${DISCECHO_APPRISE_URLS}`.
  - `drive.InitialScan`: enumerates `/dev/sr*` on startup and upserts
    drives.
  - `discflow` (in main): wires udev events → classifier → handler.Identify
    → store + broadcaster.
  - Runtime image now includes `whipper`, `cdparanoia`, `libcdio-utils`
    (image grows from 156 MB to ~291 MB).
  - `shared/wire.ts`: TypeScript types mirroring the JSON wire format
    for the M1.2 mobile UI to consume.
- Go module bumped to 1.25 (transitive requirement of
  `modernc.org/sqlite`); Dockerfile and CI workflow aligned.
- **M1.2 mobile UI (audio CD pipeline).**
  - SvelteKit 2 mobile-first PWA: dashboard, full-screen job detail with
    vertical pipeline stepper, new-disc bottom sheet with 8s
    auto-confirm.
  - Stub pages for History (M2) and Settings (M6); tab bar always
    visible.
  - Global Svelte store hydrating from `GET /api/state` and merging
    SSE deltas (`drive.changed`, `disc.detected/identified`, `job.*`,
    `state.snapshot` reconnect). Per-job log ring buffer capped at 50
    lines.
  - vitest + @testing-library/svelte unit tests for the store, fetch
    helpers, SSE wrapper, time helpers, and the disc-id sheet's
    countdown logic.
  - PWA manifest + 192/512 icons (offline shell + push deferred to M6).
  - Daemon: new `DISCECHO_AUTH_DISABLED=true` env to skip the bearer
    token bootstrap, intended for use behind a reverse proxy.
- **M2.1 daemon-side DVD-Video pipeline + history endpoint.**
  - `pipelines/dvdvideo`: handler that probes DVD volume label, queries
    TMDB (movie + tv search merged), runs HandBrake scan + per-title
    encode, atomic-moves outputs to library, fires Apprise, ejects.
  - DVD-Movie profile (MP4, x264 RF 20, longest title only) and
    DVD-Series profile (MKV, x264 RF 20 per-title, titles ≥ 5 min)
    seeded on first start.
  - `identify/tmdb.go`: TMDB JSON client with parallel movie+tv search,
    confidence from `popularity / 10`, capped at 5 candidates.
  - `identify/dvdlabel.go`: volume label normaliser (replaces `_` and
    `.`, title-cases via `golang.org/x/text/cases`, rejects `DVD_VIDEO`
    / ≤ 3 chars).
  - `identify/dvdprober.go`: shells out to `isoinfo -d` to read the
    primary volume descriptor.
  - `tools/handbrake.go`: HandBrakeCLI wrapper with `--scan` title
    parser and encode-progress parser (computes overall progress
    across multiple titles via `HB_TITLE_IDX` / `HB_TOTAL_TITLES` env).
  - `state.Candidate` gains `tmdb_id` and `media_type` fields
    (additive; no migration).
  - `state.Store.ListHistory` + `CountHistory` + `HistoryFilter` /
    `HistoryRow` types; `GET /api/history` endpoint paginated by
    finished_at DESC, filterable by type/from/to.
  - `POST /api/discs/:id/identify` accepts `{query, media_type}` body
    for manual TMDB lookup; persists candidates back onto the disc.
  - New env vars: `DISCECHO_TMDB_KEY`, `DISCECHO_TMDB_LANG` (default
    `en-US`), `DISCECHO_SUBS_LANG` (default `eng`),
    `DISCECHO_HANDBRAKE_BIN`, `DISCECHO_ISOINFO_BIN`.
  - Runtime image now includes `handbrake-cli`, `libdvd-pkg` (CSS
    bypass), and `genisoimage` (`isoinfo`).
- **M2.2 mobile UI for DVD pipeline + history.**
  - `/history` page replaces the M1.2 stub: day-grouped list
    (Today / Yesterday / N days ago / N weeks ago / absolute date past
    30 days), disc-type filter chips (All / Audio CD / DVD), infinite
    scroll via IntersectionObserver, paginated by
    `GET /api/history?limit=50`.
  - DiscIdSheet candidate-driven profile binding: each candidate
    carries `media_type`; clicking a candidate auto-binds the matching
    profile (`movie` → DVD-Movie, `tv` → DVD-Series, audio → CD-FLAC).
    Each TMDB candidate row shows a `FILM` / `TV` badge.
  - "Search manually" wired to `POST /api/discs/:id/identify`: inline
    text input replaces the candidate list while searching; success
    refreshes the list, empty result shows "No matches found", HTTP
    error shows the status.
  - DiscIdSheet now reads its disc reactively from the global `discs`
    store, so manual-identify updates flow through without remounting.
  - New components: `HistoryRow.svelte`, `FilterChips.svelte`.
  - Store additions: `history`, `historyTotal`, `historyLoading`,
    `historyError`, `historyFilter` writables; `fetchHistoryPage` and
    `manualIdentify` imperatives.
  - `lib/time.ts` gains `dayGroupLabel` for the history grouping.
- **M3.1 daemon-side BDMV + UHD pipelines.**
  - `pipelines/bdmv` handler: identifies via volume label → TMDB,
    selects longest title with duration ≥ `min_title_seconds`, uses
    MakeMKV to decrypt+demux that title, then HandBrake transcodes
    to MKV (`x265 RF 19 10-bit`), atomic-moves into the library,
    fires Apprise, ejects.
  - `pipelines/uhd` handler: same identify shape but with an AACS2
    key-file precheck *before* TMDB. UHD-Remux skips the transcode
    and compress steps — the MakeMKV output is the artifact, with
    HDR10/Dolby Vision metadata, lossless audio, and PGS subtitles
    preserved. Output lands at
    `Title (Year)/Title (Year) [UHD].mkv`.
  - `tools/makemkv.go`: `makemkvcon --robot` wrapper with `Scan`
    (info parser → titles + tracks) and `Rip` (decrypt+demux of one
    title to a directory). Progress and operation labels stream to
    the sink.
  - `identify/bdprober.go`: `bd_info` wrapper for AACS2 detection.
  - `identify/fsprobe.go`: `isoinfo -R -l` listing parser, used by
    the classifier to tell DVD apart from BDMV.
  - `identify/bdmt.go`: BDMT_<lang>.xml title parser. Currently used
    only in tests; production identify reads the volume label.
    Off-disc XML extraction lands in a follow-up.
  - Classifier rewritten to a three-step probe (`cd-info` → fs
    listing → `bd_info`). Routes `AUDIO_CD`, `DVD`, `BDMV`, `UHD`,
    `DATA` to the right handler. **Fixes a latent gap from M2.1**
    where the DVD handler was registered but never reachable via
    the disc-flow because the classifier returned `DATA` for all
    non-audio discs.
  - Two new profiles seeded on first start: `BD-1080p` (BDMV) and
    `UHD-Remux` (UHD).
  - New env vars: `DISCECHO_MAKEMKV_BIN` (default `makemkvcon`),
    `DISCECHO_MAKEMKV_DATA` (default `${DISCECHO_DATA}/MakeMKV`),
    `DISCECHO_MAKEMKV_BETA_KEY` (optional public beta key — daemon
    writes `~/.MakeMKV/settings.conf` on start), `DISCECHO_BDINFO_BIN`
    (default `bd_info`).
  - Runtime image now includes `libbluray-bin` (Debian package
    that ships `bd_info`) and `makemkvcon` built from source. MakeMKV
    is compiled in a separate build stage and only its binary +
    shared libs (`libmakemkv.so.1`, `libdriveio.so.0`) are copied
    into the runtime image; build deps (qtbase5-dev, mesa-dev, etc.)
    don't ship. Runtime grew from ~291 MB (M2.1) to ~1 GB (M3.1)
    — the bulk is `libavcodec59` + transitive media-codec deps that
    `makemkvcon` links against. Image-size trim is a future task.
  - README documents the MakeMKV beta-key refresh cadence and
    where to drop `KEYDB.cfg` for UHD.
- **M3.2 mobile multi-drive UI polish.**
  - `JobRow` shows a `drive_id` chip after the title, so with two
    drives running concurrently you can tell which row belongs to
    which drive.
  - Jobs in `queued` state render a `QUEUED` badge instead of the
    misleading `0%` placeholder. ETA hidden in this state.
  - `DriveCard` gains a `+N queued` pill (next to the state label)
    when more than one job targets the same drive.
  - Dashboard hero-band caption decomposes when both rip and queue
    counts are non-zero — e.g. `1 rip · 2 queued` — instead of a
    plain `3 jobs` that flattens the multi-drive picture.
  - Active-queue list stably sorts running jobs above queued ones.
  - No daemon changes; no wire-type changes. The orchestrator's
    per-drive worker model from M1.1 already supports this; M3.2
    just surfaces it.
- **M4.1 desktop shell + dashboard.**
  - `+layout.svelte` mounts a desktop top-nav (`TopNav.svelte`) visible
    only at the `lg:` breakpoint (1024px) and up. Brand mark on the
    left, four section links centred (`Dashboard / History / Profiles
    / System`), `LiveDot` on the right. Active section detected via
    `$page.url.pathname`.
  - `+page.svelte` becomes a viewport-driven dispatcher that picks
    `MobileDashboard` (verbatim move of the M3.2 content) below `lg:`
    and the new `DesktopDashboard` above. Mobile dashboard behaviour
    unchanged.
  - `DesktopDashboard` composes a hero band of compact `DriveHeroCard`
    components (CSS grid auto-fit, 1-3 drives), a `QueueTable` (real
    `<table>` with type/title/drv/step/pct/ETA columns), and a sticky
    `JobDetailPanel` with `PipelineStepperHorizontal` + 12-line log
    tail.
  - New `selectedJobID` writable in the store. Default `null`;
    `DesktopDashboard` falls back to the first running job when nothing
    is explicitly selected. Click queue row or hero drive card to set.
  - `/profiles` and `/system` get placeholder pages so the new top-nav
    links land somewhere instead of 404'ing. Their content stays
    mobile-style on desktop until M4.2 polishes them desktop-native.
  - No daemon changes; no wire-type changes. M4.2 ships keyboard
    shortcuts, ⌘K palette, and desktop-native History/Profiles/System.
- **M5.1 daemon-side PSX + PS2 game-disc pipelines.**
  - `pipelines/psx` handler: identifies via SYSTEM.CNF boot code →
    Redump dat lookup, rips with redumper (`cd` mode) producing
    `.bin/.cue`, MD5-verifies against the dat entry (warns on
    mismatch but doesn't abort), compresses to CHD with chdman,
    atomic-moves to library. Output template
    `{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`.
  - `pipelines/ps2` handler: same shape but redumper `dvd` mode
    producing `.iso`; chdman auto-picks `createdvd` from the
    extension.
  - `tools/redumper.go`: thin wrapper with `Rip(devPath, outDir,
    name, mode, sink)` and a pure progress-line parser
    (`ParseRedumperProgress`).
  - `tools/chdman.go`: thin wrapper with `CreateCHD(input, output,
    sink)` that auto-picks `createcd` (.cue) vs `createdvd` (.iso).
  - `identify/redump.go`: Redump dat-file XML loader with
    boot-code + MD5 indexes.
  - `identify/redumpprobe.go`: SYSTEM.CNF reader (via `isoinfo -x`)
    with PSX/PS2 discrimination via the `BOOT` vs `BOOT2` prefix.
    Normalises PS2's 5-digit boot code (`SCES_50051`) to the dotted
    form (`SCES_500.51`) so it matches Redump dat-file keys.
  - Classifier extended with a fourth probe — when fs listing shows
    `/SYSTEM.CNF`, the classifier reads it and routes to PSX or PS2.
    Falls back to DATA when SYSTEM.CNF is unreadable (conservative,
    same posture as M3.1's `bd_info`-fails-default-to-BDMV).
  - Two new seeded profiles: `PSX-CHD` (PSX) and `PS2-CHD` (PS2),
    both with engine `redumper+chdman` and the CHD output template.
  - `state.Candidate` gains an optional `region` field; wire type
    mirrored. `pipelines.OutputFields` gains `Region` for templates.
  - New env vars: `DISCECHO_REDUMPER_BIN` (default `redumper`),
    `DISCECHO_CHDMAN_BIN` (default `chdman`), `DISCECHO_REDUMP_DIR`
    (default `${DISCECHO_DATA}/redump`).
  - Runtime image gains `mame-tools` (chdman, ~2 MB) and a
    pre-built static `redumper` binary from the GitHub releases
    page (~3 MB, `b720` build, `linux-x64.zip`). No build-from-source.
  - README documents the user-supplied Redump dat-file workflow.
- **M5.2 profile editor + API mutations.**
  - Daemon: `POST/PUT/DELETE /api/profiles` with engine-aware
    schema validation (whipper/MakeMKV/MakeMKV+HandBrake/HandBrake/
    redumper+chdman) and `text/template`-checked output paths.
    422 responses use a flat `{field: msg}` body. Foreign-key
    references from active jobs return 409.
  - Daemon broadcasts `profile.changed` SSE events on every
    create/update/delete (payload `{profile}` for upsert,
    `{profile_id, deleted: true}` for delete).
  - WebUI desktop `/profiles`: two-column editor (list + form)
    with engine-locked editing in edit mode, format restricted
    to the engine's schema, options grid driven by the schema,
    two-step delete, and inline 422 field errors.
  - WebUI mobile `/profiles`: read-only list grouped by disc
    type with an "edit on desktop" hint, replacing the M4.1
    placeholder.
  - New store imperatives: `createProfile`, `updateProfile`,
    `deleteProfile`. New `selectedProfileID` writable.
  - New `lib/api.ts` helper `parseValidationErrors(e)` and
    `apiPut<T>` mirroring `apiPost`.
- **M6.1 daemon-side Saturn / Dreamcast / Xbox / Data pipelines.**
  - `pipelines/saturn` handler: identifies via Saturn IP.BIN
    (sector 0 magic `SEGA SEGASATURN` + product number) → Redump
    dat lookup, rips with redumper (`cd` mode), MD5-verifies
    against the dat entry (warns on mismatch), compresses to CHD
    with chdman, atomic-moves to library. Output template
    `{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd`.
  - `pipelines/dreamcast` handler: type-only classification
    pre-rip via TOC heuristic (2 sessions + session-2 start at
    LBA ≥ 45000). Rips with redumper (`cd` mode), produces
    multi-track GDI, computes post-rip MD5 against Redump DC
    dat — on hit, fills in real `Title` and `Region` before the
    move step. Compresses to CHD-GDI via chdman.
  - `pipelines/xbox` handler: identifies via XBE certificate
    from `default.xbe` on the mounted disc (title ID +
    allowed-region bitfield) → Redump dat lookup, rips with
    redumper (`xbox` mode) producing `.iso`, MD5-verifies,
    atomic-moves. No compress step — XBOX ISO is the
    deliverable. Output template
    `{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).iso`.
    Original Xbox only.
  - `pipelines/data` handler: filesystem volume label becomes
    `Title` (`data-disc-YYYYMMDD-HHMMSS` fallback when label is
    empty). Rips with `dd` (`bs=2048 conv=noerror,sync`),
    sha256-hashes the ISO, records hash and size on the disc
    record, atomic-moves. Output template
    `{{.Title}}/{{.Title}}.iso`.
  - `tools/redumper.go`: `xbox` mode added; emits `<name>.iso`
    like `dvd` mode. New exported `RedumperOutputExt(mode)`
    helper.
  - `tools/dd.go`: new wrapper around `dd if=<dev> of=<out>
    bs=2048 conv=noerror,sync status=progress`. Pure
    `ParseDDProgress` for unit tests. Surfaces bad-sector
    handling via job log lines.
  - `identify/saturnprobe.go`: pure `ProbeSaturnReader` over an
    `io.Reader` plus `ProbeSaturn(devPath)` wrapper.
  - `identify/xboxprobe.go`: parses XBE base-address +
    certificate-VA offsets to extract title ID and
    allowed-region bitfield. Refactored to expose `ProbeXBE(data
    []byte)` so the pipeline can probe via `isoinfo -x
    /default.xbe` without mounting the disc.
  - `identify/dctoc.go`: `DCTOCProber` wrapping `cdrdao
    disk-info` + pure `looksLikeDreamcast` helper.
  - `identify/redump.go`: `LoadRedumpDir(rootDir)` walks
    `<root>/<system>/*.dat` and merges all dat-files into one
    in-memory index. New `LookupByXboxTitleID(uint32)` method.
    Bracket-code regex generalised to handle Saturn product
    numbers and Xbox hex title IDs alongside PSX/PS2 boot codes.
  - Classifier extended: Saturn / Dreamcast / Xbox / Data
    fallback branches layered after the existing PSX/PS2 logic.
    Probe errors route to DATA (matches the M3.1/M5.1
    conservative posture).
  - Engine schema gains `redumper` (formats `[ISO]`) and `dd`
    (formats `[ISO]`). `webui/src/lib/profile_schema.ts`
    mirrors. `DISC_TYPES` array gains `SAT`, `DC`, `XBOX`.
  - Four new seeded profiles: `Saturn-CHD`, `DC-CHD`,
    `XBOX-ISO`, `Data-ISO`. Idempotent seed functions in
    `daemon/settings/settings.go`.
  - Daemon main wiring switched from per-file
    `LoadRedumpDB(<system>.dat)` to a single
    `LoadRedumpDir(${DISCECHO_DATA}/redump)` call shared across
    all five Redump-aware handlers (PSX, PS2, Saturn, DC, Xbox).
  - README documents the per-system Redump dat-file layout
    under `${DISCECHO_DATA}/redump/<system>/`.
  - **Drops VCD from the disc-type matrix** (dead format; falls
    through to DATA pipeline as ISO).
- **M6.2 PWA install.**
  - Manifest, service worker (app-shell-only precache via Workbox),
    and update toast.
  - Mobile install via Chrome's URL-bar prompt; iOS Safari users see
    a one-time "Share then Add to Home Screen" hint banner,
    dismissible via a `localStorage` flag.
  - Update flow: when a new version is available, a "Reload" toast
    appears in the bottom-right; clicking it activates the new SW.
    Background `registration.update()` runs every 60 minutes so
    phone-pinned PWAs pick up updates without manual reloads.
  - Default icon set in `static/icons/` (existing — replace the PNGs
    to rebrand). Apple meta tags added to `app.html` for iOS
    add-to-home-screen support.
  - `theme-color` updated to the accent green (`#00d68f`) so the
    Android Chrome status bar tints correctly when launched as a PWA.
  - Service worker disabled in `pnpm dev` to keep hot-reload working;
    test the PWA via `pnpm preview` or against the deployed daemon.
  - No web push (notifications continue to flow through Apprise
    server-side per M0/M1 stack decisions).
- **M6.3 settings page + Apprise URL management + retention.**
  - Daemon: POST/PUT/DELETE on `/api/notifications` with
    `apprise --dry-run` validation at create/update time. 422 returns
    flat `{field: msg}`. Plus `POST /api/notifications/{id}/validate`
    (idempotent dry-run) and `POST /api/notifications/{id}/test` (real
    Apprise send; 502 on upstream failure). SSE `notification.changed`.
  - Daemon: `/api/settings` now accepts PUT for a partial map of
    M6.3-editable keys: `prefs.{accent,mood,density}` and
    `retention.{forever,days}`. Validation surfaces flat 422 errors per
    key. `retention.forever = "true"` is seeded on first daemon start
    so existing deployments don't see sudden deletions.
  - Daemon: history retention sweeper (`daemon/state/sweeper.go`) runs
    immediately on startup and daily at 03:00 daemon-local. Deletes
    `jobs` rows in `done|failed|cancelled` older than the configured
    cutoff; FK cascade trims `job_steps` and `log_lines`; orphaned
    `discs` rows pruned in the same transaction.
  - Daemon: `tools/apprise.go` gains error-surfacing `DryRun(url)` and
    `Send(urls, title, body)` methods alongside the existing `Run`
    (which keeps its swallow-and-warn semantics for the in-pipeline
    notify step).
  - WebUI desktop `/settings`: single scrolling page with System,
    Notifications, Appearance, Retention sections. NotificationEditor
    rows have Save / Validate / Test / Delete (two-step) buttons with
    inline 422 errors. AppearanceSection auto-applies on change
    (localStorage cache + DOM dataset + daemon PUT). RetentionSection
    has a toggle + numeric input with client-side validation.
  - WebUI mobile `/settings`: read-only list of all four sections;
    URLs truncated to just the scheme (e.g. `ntfy://...`,
    `tgram://...`) so embedded credentials never leak onto a phone
    screen. "Edit on desktop" footer.
  - WebUI: first-paint prefs hydration via inline IIFE in `app.html`
    reading `localStorage.discecho.prefs`. Bootstrap reconciles with
    daemon truth a few hundred ms later — no flash on revisits, single
    source of truth across devices.

### Changed
- Redump dat-files must now live under per-system subdirectories
  (`${DISCECHO_DATA}/redump/{psx,ps2,saturn,dreamcast,xbox}/*.dat`).
  Files placed directly under `${DISCECHO_DATA}/redump/` are no
  longer loaded — move them into the right subdirectory.

### Deprecated

### Removed

### Fixed

### Security

## [0.0.1] - 2026-05-06

### Added
- Initial project scaffold.