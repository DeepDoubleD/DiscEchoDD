# Code Review — DiscEcho — 2026-07-02 — reviewed by Fable 5

Severity mapping used below: SEV-1 = Critical (exploitable security / data loss), SEV-2 = High (user-facing bug), SEV-3 = Medium (latent bug / reliability risk), SEV-4 = Low (hygiene worth doing).

## Summary

The codebase is in good health for its size (~51k LOC Go, ~18k LOC webui). The daemon shows consistently careful engineering: all SQL is parameterized, all process execution uses arg-vectors (no shell interpolation), output paths are sanitized against traversal, HTTP clients have timeouts, multi-statement writes are transactional, and known race classes (udev event storms, drive-claim CAS, duplicate /start) have explicit, well-commented mitigations. No SEV-1 issues were found. The dominant defect classes are: (a) SSE/webui contract drift — an event the daemon publishes that the UI never subscribes to, and hand-mirrored schema files that have gone stale; (b) one inconsistency in the "terminal job states" SQL tuple that CLAUDE.md itself warns about; (c) API-side drive-state writes that bypass the CAS discipline the discflow path established; and (d) timezone mixing in the stats queries. A cluster of hygiene items (dead fields, stale comments, no-op settings) rounds out the list.

## Findings

### [SEV-2] `job.substep` SSE events are published by the daemon but never subscribed by the webui
> **FIXED:** subscribe webui to `job.substep` SSE, patch `active_substep` live, clear it on step-done.
- **File:** webui/src/lib/store.ts:92-109 (SSE_EVENT_NAMES), daemon/jobs/sink.go:150-161 (publisher)
- **Issue:** `PersistentSink.OnSubStep` persists `jobs.active_substep` and broadcasts a `job.substep` event, but `SSE_EVENT_NAMES` doesn't include `job.substep` and `handleSSEEvent` has no case for it. A connected dashboard never sees live sub-phase changes (MakeMKV "Scanning titles…", redumper DUMP/REFINE/SPLIT); the label only updates from the `state.snapshot` at page load/reconnect, so it renders stale or empty for the whole rip.
- **Evidence:** `rg 'job.substep' webui/src` matches nothing outside `ripSubStepLabel` consumers reading `job.active_substep`; the only writer of that field client-side is the snapshot. Desktop `DriveHeroCard.svelte:114` and mobile `DriveCard.svelte:194` both render `ripSubStepLabel(job?.active_substep)`.
- **Suggested fix:** Add `'job.substep'` to `SSE_EVENT_NAMES` and a `handleSSEEvent` case that patches `active_substep` onto the matching job (mirror the `job.progress` overlay pattern). Also consider clearing it on `job.step` transitions, matching the daemon's clear-on-step-done.
- **Verification:** Unit test in `store.test.ts`: dispatch `handleSSEEvent('job.substep', {job_id, substep: 'REFINE'})` and assert `$jobs` updates. Live check against Unraid: start a rip, watch the drive card label change from "Rip — Dumping…" to "Rip — Refining…" without a reload.

### [SEV-2] API re-identify paths bypass the drive-state CAS and can stomp a running rip's state; UI left showing "identifying"
> **FIXED:** release re-identify drive via ReleaseDriveFromIdentify CAS + publish idle only on release.
- **File:** daemon/api/discs.go:667-671 and 830-834 (deferred releases), 672-677 and 835-840 (identifying publish with no idle publish)
- **Issue:** `forceReidentify` and `SetDiscType` claim the drive via `ClaimDriveForIdentify`, but release it with a deferred unconditional `UpdateDriveState(…, idle)` instead of the CAS `ReleaseDriveFromIdentify` that discflow uses for exactly this hazard (discflow.go:329-348 documents it). If the user starts a rip while the re-identify is in flight (`Orchestrator.Submit` doesn't check drive state), the orchestrator flips the drive to `ripping` and the deferred write then stomps it back to `idle` — the "drive shows IDLE with Eject/Re-identify offered mid-rip" desync, plus `last_error` is cleared. Separately, both handlers publish `drive.changed {state: identifying}` but never publish the transition back to idle, so even in the happy path every connected client's drive pill sticks on "Identifying…" until an unrelated event or reload.
- **Evidence:** `defer func() { …UpdateDriveState(context.Background(), drv.ID, state.DriveStateIdle)… }()` vs. store.go:266-275 (`ReleaseDriveFromIdentify` — "Scoping the release to state='identifying' makes it a no-op once another flow owns the drive").
- **Suggested fix:** Use `ReleaseDriveFromIdentify` in both deferred blocks, and publish `drive.changed {state: idle}` only when the CAS reports the row moved (same pattern as discflow.go:211-216).
- **Verification:** apitest: claim drive via the endpoint with a stub handler that blocks, flip the drive to `ripping` mid-flight, assert the drive row is still `ripping` after the handler returns. UI check: hit Re-identify, confirm the pill returns to idle without a reload.

### [SEV-3] `ListActiveAndRecentJobs` returns interrupted jobs twice per snapshot (terminal tuple missing `interrupted`)
> **FIXED:** add 'interrupted' to the active-arm NOT IN tuple so it stops double-listing with the recent arm.
- **File:** daemon/state/store.go:1069 (active query), 1094 (recent query)
- **Issue:** The "active" arm filters `state NOT IN ('done','failed','cancelled')` — missing `interrupted` — so interrupted jobs qualify as active; the "recent" arm's tuple `('done','failed','cancelled','interrupted')` also matches them. Every `/api/state` and SSE bootstrap after a daemon crash contains each interrupted job twice. This is exactly the CLAUDE.md "terminal job states is a literal SQL filter in 7+ sites" hazard. Impact is currently masked because the desktop dashboard filters interrupted out of its keyed lists (`DesktopDashboard.svelte:20`), but any consumer that keys an `{#each}` on the raw snapshot array will throw Svelte's duplicate-key error, and counts derived from the snapshot are off by the dup.
- **Evidence:** grep confirms this is the only tuple site missing `interrupted`: `rg "done','failed','cancelled'\)" daemon` → store.go:1057 (comment) + 1069 only.
- **Suggested fix:** Add `'interrupted'` to the active arm's NOT IN tuple (and fix the doc comment at store.go:1057).
- **Verification:** Store test: seed one `interrupted` job, call `ListActiveAndRecentJobs`, assert it appears exactly once. Existing tests should stay green.

### [SEV-3] Stats queries mix local-offset timestamps with stored-UTC strings — dashboard numbers wrong when TZ ≠ UTC
> **FIXED:** normalize `now` to UTC in Stats() and format all cutoffs via the UTC `timestamp()` helper.
- **File:** daemon/state/store.go:2877 (statsTodayRipped), 2937-2961 (statsFailures), 2984 (dailyByteSeries)
- **Issue:** Timestamps are stored as RFC3339Nano **UTC** strings (`timestamp()`), but these queries build cutoffs with `now.Format(time.RFC3339)` in the daemon's **local** zone (e.g. `2026-07-02T00:00:00+02:00`) and compare lexically in SQL. String comparison between `…+02:00` and `…Z` suffixed values is not chronological: with TZ=Europe/Oslo, a job finished 23:00 UTC (01:00 local "today") is excluded from "Today ripped". The 30-day spark buckets also mix `date(finished_at)` (UTC date) with `dayOffsetIndex` (local-midnight buckets). Hidden today because the container likely runs TZ=UTC.
- **Evidence:** `startOfToday := time.Date(…, now.Location()).Format(time.RFC3339)` vs `timestamp(t) = t.UTC().Format(time.RFC3339Nano)`.
- **Suggested fix:** Convert all cutoffs to UTC before formatting (`startOfToday.UTC().Format(time.RFC3339Nano)`), and decide one zone (UTC is simplest) for the day-bucket mapping in `dayOffsetIndex`/`date(finished_at)`.
- **Verification:** Table-driven store test with a fake `now` in a +02:00 location and a job finished between local and UTC midnight; assert it lands in the right day bucket.

### [SEV-3] Integration secrets returned in full to an unauthenticated-by-default API
> **FIXED (integration secrets):** mask secret credential fields in GET/PUT responses; Test merges stored creds so it still works; editor no longer prefills secrets. **DEFERRED (notification Apprise URLs):** the URL is a single all-or-nothing credential the editor must display to edit — masking it correctly needs a write-only-URL editor redesign, out of scope for a minimal fix. Tracked in the Fix Session Summary.
- **File:** daemon/api/integrations.go:69-94 (GetIntegration), 222-238 (respondWithDetail); daemon/api/notifications.go:24-31 (ListNotifications)
- **Issue:** `GET /api/integrations/{name}` returns `Values: creds` — the raw TMDB key, IGDB client_secret, and MakeMKV license key — and `GET /api/notifications` returns full Apprise URLs, which embed provider tokens. The documented default deployment runs with `Token == ""` (auth disabled, LAN mode), so anything on the LAN (including other containers, guests on the Wi-Fi, or an XSS foothold in any LAN app) can read the purchased MakeMKV `M-` key and all API credentials. `system.go`'s `IntegrationsInfo` explicitly avoids this ("The TMDB key itself is never returned"), so the codebase has both conventions.
- **Evidence:** `writeJSON(w, …, integrationDetail{…, Values: creds})` with no masking; contrast system.go:44-45.
- **Suggested fix:** Mask values in GET responses (e.g. `sk-…last4`) and keep `fields_present` for the UI's "configured" display; the PUT path already supports partial updates so the UI never needs to echo secrets back. Given the homelab threat model this is hardening, not an emergency — but it's cheap.
- **Verification:** apitest asserting `values` are masked; manual: `curl http://host:8088/api/integrations/tmdb` shows no usable key.

### [SEV-3] TS engine-schema mirror stale: `HandBrake` options missing 4 keys → editor hides and can drop seeded extras options
> **FIXED:** add show_title_picker/include_extras/min_extra_seconds/extras_max_ratio to the TS HandBrake schema.
- **File:** webui/src/lib/profile_schema.ts:76-88 vs daemon/api/profile_schema.go:105-121; webui/src/lib/components/desktop/ProfileEditor.svelte:146-151, 172-175
- **Issue:** Go's `engineSchemas["HandBrake"].Options` includes `show_title_picker`, `include_extras`, `min_extra_seconds`, `extras_max_ratio`; the TS mirror's `HandBrake.options` omits all four. Consequences: (1) the seeded "DVD Movie + extras" profile (engine `HandBrake`, settings.go:379-407) shows none of its extras knobs in the editor — the user can't see or change why extras are being ripped; (2) the editor's "drop options not in the new schema" logic on engine/disc-type change filters against the **TS** spec, so switching a profile to `HandBrake` silently deletes options the server considers valid.
- **Evidence:** `if (s.options[k]) filtered[k] = working.options[k]` (ProfileEditor.svelte:148-149) with `s = ENGINES['HandBrake']` lacking those keys.
- **Suggested fix:** Add the four options to `ENGINES.HandBrake.options` in profile_schema.ts. Consider a small generated-file or test that diffs the two schemas to stop this class of drift (a vitest that imports a JSON dump generated from the Go side would do).
- **Verification:** Open the seeded "DVD Movie + extras" profile in the editor — the extras checkboxes render; switch engine MakeMKV+HandBrake → HandBrake and confirm `include_extras` survives in the PUT body.

### [SEV-3] `drive_policy` is validated, stored, edited — and consumed by nothing
> **FIXED:** remove the no-op Drive-policy control from the editor; document the field as reserved/unenforced. Full drive-pinning deferred as a feature (needs real drive-ID enumeration + absent-drive semantics).
- **File:** daemon/api/profile_schema.go:239-242 (DrivePolicies), daemon/jobs/orchestrator.go (no reader), webui profile_schema.ts:170-180
- **Issue:** The profile editor offers "Pin to drv-1/2/3", validation enforces the value, seeds set `any` — but no scheduler code reads `Profile.DrivePolicy`; jobs run on whatever drive the disc is in. Worse, real drive IDs are UUIDs (`state.NewID`), so the allow-listed literals `drv-1..3` can never match an actual drive, and the comment "UI offers the IDs of currently-attached drives" would be rejected by `ValidateProfile` if it did. A user pinning a profile gets a silent no-op.
- **Evidence:** `rg DrivePolicy daemon --glob '!*_test.go'` matches only schema, store scan/write, and seeders.
- **Suggested fix:** Either implement the pin in `Orchestrator.Submit` (reject/queue when `disc.DriveID` doesn't satisfy the policy) with validation accepting real drive IDs, or remove the field from the editor until it does something (leaving the column is fine).
- **Verification:** If implemented: unit test that Submit refuses a disc in the wrong drive. If removed: editor no longer renders the control.

### [SEV-3] Orchestrator/Compute early-return error paths strand jobs in `queued`/`running` until next restart
> **FIXED:** route the pre-flight lookup / running-transition failures through a terminal `failed` write (orchestrator `failEarly`; compute `finalise`). Defensive: `jobs.{disc,drive,profile}_id` are FK-constrained so these lookups only error on infrastructure failure, not missing rows — no deterministic unit test is possible without converting `OrchestratorConfig.Store` to an interface (out of scope); verified no regression across the jobs suite.
- **File:** daemon/jobs/orchestrator.go:327-364, daemon/jobs/compute.go:264-311
- **Issue:** After a worker pops a job, failures of `GetJob`/`GetDisc`/`GetDrive`/`GetProfile` or of the `UpdateJobState(running)` write log an error and `return` without writing a terminal state. The queue item is consumed, so the job sits `queued` (or `running`) forever — invisible to retry, blocking `DiscHasActiveJob`/eject guards on that disc/drive — until a daemon restart flips it to `interrupted`. Compute's later failures correctly route through `finalise(…, failed, …)`; the early ones don't.
- **Evidence:** e.g. orchestrator.go:338-341: `disc, err := …GetDisc(…); if err != nil { slog.Error(…); return }`.
- **Suggested fix:** On these paths, best-effort `UpdateJobState(context.Background(), jobID, failed, err.Error())` (and drive back to idle where it was claimed) before returning — matching what `runJob` already does for "no handler".
- **Verification:** Unit test with a store fake whose `GetProfile` errors: assert the job row ends `failed`, not `queued`.

### [SEV-3] `RetryTranscode` / `ResetTranscodeJob` errors surface as 500 instead of 409, and API-level state check is racy-only-benign by SQLite accident
> **FIXED:** map `ErrInvalidJobStateForRetry`→409 and `ErrInvalidJobKindForRetry`→422 in the handler. The store re-check accepts exactly {failed,interrupted} like the pre-check, so the sentinel only fires on a genuine TOCTOU race — not deterministically reachable single-threaded, and a concurrency test would flake on -race runners (per CLAUDE.md); existing failure-path suite still green.
- **File:** daemon/api/jobs.go:101-142, daemon/state/store.go:1491-1541
- **Issue:** The handler pre-checks kind/state, then `ResetTranscodeJob` re-checks inside a transaction and returns `ErrInvalidJobKindForRetry`/`ErrInvalidJobStateForRetry` — but the handler maps any store error to 500, so a legitimately-lost race (double-click retry) produces a 500 rather than the 409 the pre-check produces. Uncertain how often this fires in practice (WAL snapshot-upgrade conflicts also surface as `database is locked` 500s here).
- **Evidence:** `if err := h.Store.ResetTranscodeJob(…); err != nil { writeError(w, http.StatusInternalServerError, …) }` with no `errors.Is` mapping for the two sentinel errors it exports.
- **Suggested fix:** Map `ErrInvalidJobStateForRetry` → 409 and `ErrInvalidJobKindForRetry` → 422 in the handler (the sentinels exist precisely for this).
- **Verification:** apitest: call retry twice back-to-back; second returns 409.

### [SEV-4] Graceful shutdown can't complete while a rip is running — `Orchestrator.Close` never cancels in-flight job contexts
> **DEFERRED (SEV-4):** a shutdown-behavior change (mirror Compute.Close's cancel loop) wanting its own test; the deploy runbook already gates on no-running-jobs. Skipped per "SEV-4 unless trivial".
- **File:** daemon/jobs/orchestrator.go:86-94, daemon/cmd/discecho/main.go:575-589
- **Issue:** `Compute.Close` cancels every in-flight per-job context; `Orchestrator.Close` only closes `stopped` and then `wg.Wait()`s — a worker mid-`handler.Run` blocks it for the duration of the rip (hours). On SIGTERM, main returns and the deferred `orch.Close()` hangs until docker's kill timeout force-kills the process. The deploy runbook already gates on "no running jobs", so this is consistent-but-inelegant; the asymmetry with Compute looks unintentional.
- **Evidence:** compute.go:174-184 (`for _, cancel := range c.cancels { cancel() }`) vs orchestrator.go:86-94 (no cancel loop).
- **Suggested fix:** Mirror Compute: in `Orchestrator.Close`, cancel all entries in `o.cancels` before `wg.Wait()`. Jobs then end `cancelled` (or with a distinct shutdown state) instead of `interrupted`-after-SIGKILL.
- **Verification:** Test: start a job whose handler blocks on ctx, call Close, assert it returns promptly and the job row is terminal.

### [SEV-4] Cancelled-before-pickup jobs broadcast `job.failed` without `state:'cancelled'` — UI shows FAILED for a cancelled job
> **FIXED (trivial):** add `"state":"cancelled"` to both cancelled-before-pickup publishes.
- **File:** daemon/jobs/orchestrator.go:332-336, daemon/jobs/compute.go:273-275
- **Issue:** When a queued job was cancelled before the worker popped it, both pools publish `job.failed` with only `{job_id}`. The webui's `job.failed` handler (store.ts:296-313) sets `state: 'failed'` unless `p.state === 'cancelled'`, so the card renders FAILED until the next snapshot, while the DB says cancelled.
- **Evidence:** Contrast with the normal terminal path, which publishes `{job_id, state: "cancelled"}` (orchestrator.go:427).
- **Suggested fix:** Include `"state": "cancelled"` in both early-exit publishes.
- **Verification:** store.test.ts already covers the cancelled branch; add a daemon test asserting the payload shape, or just grep-verify all `job.failed` publishers carry `state` when not failed.

### [SEV-4] `StartDisc` mutates disc metadata before the authoritative duplicate guard
> **DEFERRED (SEV-4):** needs a careful reorder of the promotion writes under `startMu`; low real-world impact (racers are usually the same auto-confirm). Skipped per "SEV-4 unless trivial".
- **File:** daemon/api/discs.go:82-128 vs 130-145
- **Issue:** The candidate-promotion writes (`UpdateDiscMetadata`, runtime fetch, extended-metadata blob) happen before the `startMu`-protected re-check. A request that ultimately loses the race and 409s has already overwritten the disc's title/metadata_id/metadata_json — potentially between the winner's `Submit` and the pipeline's re-read of the disc row. In practice both racers are usually the same auto-confirm with the same candidate, so impact is low; it's still a write-then-reject ordering smell.
- **Evidence:** Comment at line 64-67 acknowledges the fast-path check is non-atomic, but the mutations sit between the two checks.
- **Suggested fix:** Move the duplicate re-check (or the whole promotion block) under `startMu` before any disc writes, or tolerate documented last-write-wins and note it.
- **Verification:** apitest: two concurrent /start with different candidate_index; loser gets 409 and the winner's candidate is what the job used.

### [SEV-4] IGDB token request puts `client_secret` in the URL query string
> **DEFERRED (SEV-4):** small but touches the token flow + httptest fakes; the secret is in the daemon's *outbound* request to Twitch, not exposed to LAN clients. Recommended as the next follow-up (security hygiene).
- **File:** daemon/identify/igdb.go:318-327
- **Issue:** `getToken` builds `cfg.TokenURL + "?" + form.Encode()` with client_id/client_secret as query params. Twitch accepts it, but secrets in URLs land in proxy/server access logs. The sibling implementation in api/integrations.go:312-322 (`testIGDB`) correctly POSTs the form in the body.
- **Suggested fix:** Send the form as the POST body with `Content-Type: application/x-www-form-urlencoded`, matching `testIGDB`.
- **Verification:** Existing httptest-based IGDB tests — update the fake token endpoint to read the body; live: IGDB search still works.

### [SEV-4] `Spool.gen` is written on every Create/Cleanup/GC but never read — the documented cache invalidation doesn't exist
> **DEFERRED (SEV-4):** dead-field removal (or implementing the invalidation) touching the spool internals; harmless at current scale. Skipped per "SEV-4 unless trivial".
- **File:** daemon/spool/spool.go:48-51, 92, 105, 157, 165-172
- **Issue:** The comment says "gen ticks every Cleanup/Create so concurrent UsageBytes callers can invalidate stale caches", but `UsageBytes` only checks the 5s TTL and never consults `gen`. Post-cleanup usage (and thus the backpressure check) can read up to 5s stale — harmless at this scale, but the field is dead weight and the comment misleads.
- **Suggested fix:** Either compare a snapshot of `gen` in `UsageBytes` and bypass the TTL when it changed, or delete the field and fix the comment.
- **Verification:** `go vet`/tests; a unit test that Cleanup makes the next UsageBytes reflect the removal (if implementing invalidation).

### [SEV-4] main.go runs spool GC before `MarkInterruptedJobs`, contradicting its own comment
> **DEFERRED (SEV-4):** reordering startup calls (or correcting the comment) carries startup-sequencing risk for a currently-conservative behavior. Skipped per "SEV-4 unless trivial".
- **File:** daemon/cmd/discecho/main.go:453-461 (GC) vs 475 (NewOrchestrator, which calls MarkInterruptedJobs)
- **Issue:** The GC comment says "MarkInterruptedJobs (already called by NewOrchestrator) keeps the dirs…", but NewOrchestrator is constructed ~20 lines later. Behavior happens to be conservative (crashed rip jobs are still `running` at GC time, so their dirs are kept; the next boot's GC removes them), but the stated invariant is false and a future reorder could silently change GC semantics.
- **Suggested fix:** Move the GC call after `jobs.NewOrchestrator(...)` (or call `MarkInterruptedJobs` explicitly before GC) and fix the comment.
- **Verification:** Startup test/manual: crash mid-rip, restart, confirm the interrupted rip's spool dir is (still) removed only when no transcode row references it.

### [SEV-4] webui `DISC_TYPE_DEFAULTS.DATA` still uses the pre-021 collision-prone template
> **FIXED (trivial):** update the TS DATA default to the `[{{.ShortHash}}]` variant matching migration 021 + the seeder.
- **File:** webui/src/lib/profile_schema.ts:417-423 vs daemon/state/migrations/021_data_iso_shorthash.sql and daemon/settings/settings.go:894
- **Issue:** Migration 021 and the seeder moved the DATA template to `{{.Title}}/{{.Title}} [{{.ShortHash}}].iso` specifically because same-label discs collide at the move step. A *new* DATA profile created through the editor pre-fills the old `{{.Title}}/{{.Title}}.iso` and reintroduces the collision the migration fixed.
- **Suggested fix:** Update `DISC_TYPE_DEFAULTS.DATA.outputPathTemplate` to the ShortHash variant.
- **Verification:** Create a new DATA profile in the editor; the template field pre-fills with `[{{.ShortHash}}]`.

### [SEV-4] `jobs.elapsed_seconds` is never written non-zero — dead wire field
> **DEFERRED (SEV-4):** removing the parameter/field (or computing elapsed) touches `UpdateJobProgress`'s signature + all call sites; UI already derives elapsed from `started_at`. Skipped per "SEV-4 unless trivial".
- **File:** daemon/jobs/sink.go:133 (`UpdateJobProgress(…, p.eta, 0)`), daemon/state/store.go:1368-1383
- **Issue:** Every progress write passes `elapsedSeconds = 0`; nothing else writes the column. `notify_message.go:450` guards on `job.ElapsedSeconds > 0` (so it falls back correctly), and the UI derives elapsed from `started_at`. The column, struct field, and wire field are effectively dead — a trap for someone who trusts them.
- **Suggested fix:** Either compute and pass elapsed in `flushProgress` (the sink knows the step start time) or drop the parameter/field.
- **Verification:** grep for remaining readers after the change; `pnpm check` + `go test ./...`.

### [SEV-4] `paused` missing from two active-state tuples (latent — pause is never used)
> **DEFERRED (SEV-4):** purely latent (`JobStatePaused` is never set today); left to avoid touching crash-recovery/eject-guard SQL without a driving need. Revisit if pause ships.
- **File:** daemon/state/store.go:1266-1279 (`HasActiveJobOnDrive`: `('queued','running','identifying')`), 1552-1590 (`MarkInterruptedJobs`: `('queued','identifying','running')`)
- **Issue:** `DiscHasActiveJob`, `ActiveSpoolReferences`, and `ClearHistory` all treat `paused` as active; these two don't. Today `JobStatePaused` is never set ("M1: never; pause is 501"), so this is purely latent — but the day pause ships, a paused job wouldn't block eject/reclassify and wouldn't be crash-recovered to `interrupted`.
- **Suggested fix:** Add `'paused'` to both tuples now (cheap), or centralize the active/terminal tuples as shared SQL fragments so the CLAUDE.md "grep 7+ sites" chore disappears.
- **Verification:** Store tests seeding a `paused` job and asserting both methods treat it as active.

### [SEV-4] Retention SQL's `finished_at IS NOT NULL` guards are ineffective — the column stores `''`, never NULL
> **DEFERRED (SEV-4):** harmless today (all terminal writers stamp `finished_at`); left to avoid touching retention/prune SQL without a driving need. Skipped per "SEV-4 unless trivial".
- **File:** daemon/state/store.go:2590-2610 (retentionWhere), 2718-2721 (HistoryBucketTotals)
- **Issue:** Jobs are inserted with `finished_at = ''` (via `timestampPtr(nil)`), so `IS NOT NULL` is always true and the real guard is the terminal-state tuple. Currently harmless (all terminal writers stamp finished_at), but the day-cutoff arm `finished_at < ?` would classify a hypothetical terminal-with-empty-finished_at row as "older than everything" and prune it. Brittle invariant worth making explicit.
- **Suggested fix:** Change guards to `finished_at != ''` (matching `ListActiveAndRecentJobs`'s `NULLIF(finished_at,'')` awareness), or normalize the column to real NULLs.
- **Verification:** Store test: terminal job with empty finished_at is not pruned by a days-only policy.

### [SEV-4] `statsLibrary` loads every done job on each snapshot and ignores timestamp parse errors
> **DEFERRED (SEV-4):** a query/perf refactor (GROUP BY daily-sum); small under retention, grows only with `retention.forever`. Skipped per "SEV-4 unless trivial".
- **File:** daemon/state/store.go:2892-2934
- **Issue:** The 30-day cumulative spark loads all `done` rows and does an O(30·N) in-memory scan on every `/api/state` and SSE connect. With retention on this stays small; with `retention.forever` (the default) it grows unboundedly with library size. Also `t, _ := time.Parse(…)` — a malformed row lands at zero time and inflates every bucket.
- **Suggested fix:** Replace with a `GROUP BY date(finished_at)` daily-sum query + Go-side running total (the `dailyByteSeries` shape); log/skip parse failures.
- **Verification:** Benchmark or just assert one query replaces the full-table scan; existing stats tests cover bucket values.

### [SEV-4] `DeleteJob` spool-cleanup failure is silently discarded despite "Log only" comment
> **FIXED (trivial):** replace `_ = err` with a `slog.Warn` carrying job id + spool path.
- **File:** daemon/api/jobs.go:209-218
- **Issue:** The comment says "Non-fatal … Log only." but the code is `_ = err` — nothing is logged. If cleanup fails repeatedly (permissions), spool space leaks with no trace until the next startup GC (which also only logs at Warn).
- **Suggested fix:** `slog.Warn("delete job: spool cleanup", "err", err, "job", id)`.
- **Verification:** Trivial; grep the diff.

## Category coverage (skill checklist)

- **Security — auth/session:** 1 finding (secrets returned to auth-less-by-default API, SEV-3). Bearer-token middleware itself is correct (constant-time compare, opt-in documented).
- **Security — SQL injection:** no findings. Every query is parameterized; the only string-built SQL fragments are compile-time constant state tuples and `?`-placeholder lists. LIKE-prefix inputs are wildcard-validated (`store_integrations.go`).
- **Security — command injection / path traversal / XSS / secrets in code:** no findings. All `exec.Command` calls are arg-vectors; output paths sanitized (`output.go`); static serving via `http.FS`; the only `{@html}` renders a static icon table; no hardcoded secrets; CI workflows have minimal `permissions:` and no injection-prone `run:` interpolation. Two hygiene-level items: IGDB secret in query string (SEV-4), TMDB `api_key` query param (that's TMDB v3's own convention — not flagged).
- **Correctness bugs:** 6 findings (SEV-2 ×2, SEV-3 ×3, SEV-4 ×1 for the cancelled-shows-failed event).
- **Error handling:** 3 findings (stranded jobs SEV-3, RetryTranscode mapping SEV-3, DeleteJob `_ = err` SEV-4). Otherwise consistently good — best-effort paths are deliberate and commented.
- **Data integrity:** 2 findings (retention `IS NOT NULL` SEV-4, StartDisc write-before-guard SEV-4). Transactions are used everywhere they matter (CreateJob, ResetTranscodeJob, MarkInterruptedJobs, PruneHistory, ClearHistory, DeleteJobAndOrphans); migrations are versioned, transactional, and count-asserted in tests.
- **Dead code:** 3 findings (`drive_policy` no-op SEV-3, `Spool.gen` SEV-4, `elapsed_seconds` SEV-4). Also noted: `GetSystemHost`'s `bucket.paths` field is written-never-used (not separately filed).
- **Performance:** 1 finding (`statsLibrary` SEV-4). N+1 patterns are already batched (`hydrateJobSteps`, `LifecycleStates`, `discsByIDs`).
- **Consistency / mirror drift:** 3 findings (HandBrake options SEV-3, DATA default template SEV-4, terminal/paused tuples SEV-3/SEV-4). Note: CLAUDE.md's claim that `Orchestrator.Cancel` "unconditionally writes cancelled" is stale — the code now guards via `CancelJobIfActive` (good; update CLAUDE.md, which is untracked so not a repo finding).
- **Test coverage gaps:** no dedicated findings filed, but per-finding Verification lines identify the missing tests: stats timezone behavior, `ListActiveAndRecentJobs` dedup, orchestrator early-error terminal states, SSE `job.substep` handling.

## Summary table

| Sev | Title | File |
|---|---|---|
| SEV-2 | `job.substep` SSE never subscribed by webui | webui/src/lib/store.ts:92 |
| SEV-2 | API re-identify bypasses drive-state CAS; stale "identifying" / rip-state stomp | daemon/api/discs.go:667,830 |
| SEV-3 | Interrupted jobs duplicated in every snapshot | daemon/state/store.go:1069 |
| SEV-3 | Stats timezone mixing (local cutoffs vs stored UTC) | daemon/state/store.go:2877 |
| SEV-3 | Raw integration secrets + Apprise URLs served over auth-less default API | daemon/api/integrations.go:87 |
| SEV-3 | HandBrake TS schema mirror missing 4 options; editor hides/drops them | webui/src/lib/profile_schema.ts:76 |
| SEV-3 | `drive_policy` validated/stored but consumed by nothing | daemon/api/profile_schema.go:242 |
| SEV-3 | Worker early-error paths strand jobs until restart | daemon/jobs/orchestrator.go:327 |
| SEV-3 | RetryTranscode race errors map to 500 not 409 | daemon/api/jobs.go:133 |
| SEV-4 | Orchestrator.Close never cancels in-flight jobs (shutdown hang) | daemon/jobs/orchestrator.go:86 |
| SEV-4 | Cancelled-before-pickup broadcast lacks `state:'cancelled'` | daemon/jobs/orchestrator.go:334 |
| SEV-4 | StartDisc mutates metadata before authoritative dup guard | daemon/api/discs.go:82 |
| SEV-4 | IGDB client_secret in token URL query | daemon/identify/igdb.go:322 |
| SEV-4 | `Spool.gen` written, never read | daemon/spool/spool.go:50 |
| SEV-4 | Spool GC ordering contradicts its comment | daemon/cmd/discecho/main.go:453 |
| SEV-4 | webui DATA default template missing ShortHash | webui/src/lib/profile_schema.ts:417 |
| SEV-4 | `elapsed_seconds` never written non-zero | daemon/jobs/sink.go:133 |
| SEV-4 | `paused` missing from 2 active-state tuples (latent) | daemon/state/store.go:1266 |
| SEV-4 | Retention `finished_at IS NOT NULL` guards ineffective | daemon/state/store.go:2602 |
| SEV-4 | `statsLibrary` full-scan per snapshot; parse errors ignored | daemon/state/store.go:2900 |
| SEV-4 | DeleteJob spool-cleanup error silently discarded | daemon/api/jobs.go:216 |

## Top 3 to fix first

1. **`job.substep` subscription (SEV-2)** — one-line list addition + a small handler case fixes a visible, always-on dashboard defect (frozen rip-phase labels) with zero risk.
2. **Re-identify drive-state CAS + idle publish (SEV-2)** — swaps an unconditional write for the CAS helper that already exists and adds one publish; closes both a real state-stomp race and a persistent UI desync in the same small diff.
3. **Terminal-tuple fix in `ListActiveAndRecentJobs` (SEV-3)** — one token in one query; it's the exact hazard CLAUDE.md warns about, it double-reports jobs on the hottest payload in the system (every snapshot), and it's a landmine for any future keyed render of the raw jobs array.

## Not Reviewed

- **Per-disc-type pipeline handler subpackages** (`daemon/pipelines/{audiocd,bdmv,dvdvideo,uhd,cdgame,psx,ps2,saturn,dreamcast,xbox,data,vcd}/`, ~50 files) — only the shared core (handler.go, output.go, sink wiring, orchestrator/compute integration) was read in full; handlers were spot-checked via grep for exec-arg construction. Reason: session scope; the shared helpers they compose were verified.
- **`daemon/tools/*` wrappers and progress parsers** (makemkv, handbrake, redumper, whipper, chdman, etc.) — exec call sites grepped (all arg-vector, all `CommandContext`), but parser state machines not line-audited.
- **`daemon/identify` probers and classify.go internals** (binary disc parsers, retry decorators, tmdb.go/redump.go bodies) — HTTP-client hygiene (timeouts, body limits on error paths) grep-verified; igdb.go read in full as the representative client; the fixture-tested binary parsers were not re-derived.
- **`daemon/drive` udev watcher**, `daemon/embed`, most of `daemon/settings/settings.go` (seeder bodies beyond the DVD/DATA sections), and migrations 001–018 (list + 019/021 read; earlier ones trusted against the count-asserting replay tests).
- **~120 webui Svelte components and routes** — store.ts, sse.ts, wire.ts, profile_schema.ts read fully; ProfileEditor, QueueTable, EncodeQueueCard, DesktopDashboard partially; the three PipelineStepper variants' lockstep and the AwaitingDecision components were not diffed.
- **Test files** — consulted for behavior confirmation only, not reviewed for quality.
- Six parallel reviewer subagents were dispatched to cover these areas but were killed by a session usage limit before returning results; the review above is a single-pass manual review prioritized by risk.

## Fix Session Summary

Fixed findings in severity order (SEV-1 → SEV-2 → SEV-3), plus the trivial SEV-4s. Each finding was re-verified against current code before changing; none were stale/invalid, so nothing was marked DISPUTED. One commit per finding (or small related group).

**Verification (whole tree, after all fixes):**
- Daemon: `gofmt -l` clean · `go vet ./...` clean · `go test -race ./...` all packages pass · `golangci-lint run ./...` → 0 issues.
- Webui: `pnpm format:check` clean · `pnpm check` 0 errors/0 warnings · `pnpm test` 389 pass · `pnpm build` OK.
- Container rebuild / Unraid deploy **not** performed — no deploy was requested and this was a code+test pass; the local container has no optical drive for a rip smoke.

**Fixed — SEV-2 (2/2):**
1. `job.substep` SSE now subscribed by the webui (+ clears on step-done); live rip sub-phase labels track again. New store tests.
2. API re-identify (`forceReidentify` / `SetDiscType`) releases the drive via the `ReleaseDriveFromIdentify` CAS and publishes `idle` only on release — closes the rip-state stomp and the stuck-"Identifying…" desync. New apitests (idle release + no-stomp-while-ripping).

**Fixed — SEV-3 (7/7, two partially):**
3. `ListActiveAndRecentJobs` active arm gained `'interrupted'` — no more double-listing. New store test.
4. Stats computed in UTC (`now.UTC()` + `timestamp()` cutoffs) to match stored timestamps. New zone-stability store test (proven to fail on the pre-fix code).
5. Integration secrets **masked** in API responses; Test merges stored creds; editor no longer prefills secrets. **Notification Apprise-URL masking deferred** (needs a write-only-URL editor redesign — the URL is one opaque credential the editor must display to edit). Tests updated/added both sides.
6. HandBrake TS schema mirror gained the 4 missing extras options.
7. `drive_policy`: misleading no-op control **removed** from the editor; field documented as reserved. **Full drive-pinning deferred** as a feature (needs real drive-ID enumeration + absent-drive queue/reject semantics).
8. Orchestrator/Compute pre-flight-lookup failures now write a terminal `failed` state instead of stranding the job. Defensive — the lookups are FK-protected, so they only fire on infrastructure errors; no deterministic test without converting `OrchestratorConfig.Store` to an interface.
9. `RetryTranscode` maps `ResetTranscodeJob`'s sentinels to 409/422 instead of 500. Defensive — only reachable via a TOCTOU race the pre-checks otherwise shadow; a concurrency test would flake on `-race` runners.

**Fixed — SEV-4 (trivial only):**
- Cancelled-before-pickup jobs broadcast `state:'cancelled'` (both pools).
- webui DATA default template updated to the `[{{.ShortHash}}]` variant.
- `DeleteJob` spool-cleanup failure now logged (`slog.Warn`) instead of `_ = err`.

**Deferred — SEV-4 (non-trivial; not in scope for this pass):**
- Orchestrator.Close doesn't cancel in-flight jobs (SIGTERM hang) — a shutdown-behavior change wanting its own test.
- `StartDisc` mutates metadata before the dup guard — needs a careful reorder under `startMu`.
- IGDB `client_secret` in the token URL query — small but touches the token flow + httptest fakes (security hygiene; recommended next).
- `Spool.gen` dead field, `elapsed_seconds` dead wire field — removal touches signatures/call sites.
- Spool-GC ordering comment, retention `IS NOT NULL` guards, `statsLibrary` full-scan, `paused` missing from 2 tuples — latent/harmless-today; left to avoid touching startup/retention/crash-recovery SQL without a driving need.

**Disputed:** none — every finding acted on was confirmed against current code. (Separately, CLAUDE.md's stale claim that `Orchestrator.Cancel` "unconditionally writes cancelled" remains stale but CLAUDE.md is untracked, so not a repo change.)
