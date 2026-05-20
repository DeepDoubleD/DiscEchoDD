#!/usr/bin/env python3
"""Generate docs/screenshots/seed.sql: copyright-safe mock data for README
screenshots. All titles are invented; all cover art is generated here as
data: URIs. Run: python3 gen_seed.py > seed.sql

Idempotent: the emitted SQL deletes any existing `shot-%` rows first, so it
can be re-applied freely.
"""
import base64
import datetime as dt

# Set to False if the daemon sends a CSP that blocks data: images.
# Discs then fall back to DiscEcho's built-in ArtPlaceholder.
USE_POSTERS = True

BASE = dt.datetime(2026, 5, 19, 9, 0, 0)  # fixed base for deterministic output


def ts(offset_minutes: int) -> str:
    """RFC3339Nano literal (millisecond precision, Z suffix)."""
    t = BASE + dt.timedelta(minutes=offset_minutes)
    return t.strftime("%Y-%m-%dT%H:%M:%S.000Z")


def sqlstr(s: str) -> str:
    return "'" + s.replace("'", "''") + "'"


def poster(title: str, subtitle: str, c1: str, c2: str) -> str:
    """Return a data:image/svg+xml;base64 URI for a 250x375 gradient poster."""
    svg = f'''<svg xmlns="http://www.w3.org/2000/svg" width="250" height="375" viewBox="0 0 250 375">
  <defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">
    <stop offset="0" stop-color="{c1}"/><stop offset="1" stop-color="{c2}"/>
  </linearGradient></defs>
  <rect width="250" height="375" fill="url(#g)"/>
  <rect x="0" y="300" width="250" height="75" fill="#000" opacity="0.35"/>
  <text x="20" y="332" fill="#fff" font-family="sans-serif" font-size="20" font-weight="700">{title}</text>
  <text x="20" y="356" fill="#e5e7eb" font-family="sans-serif" font-size="13">{subtitle}</text>
</svg>'''
    b64 = base64.b64encode(svg.encode("utf-8")).decode("ascii")
    return "data:image/svg+xml;base64," + b64


# (id, type, title, year, runtime_s, size_bytes, artist/subtitle, c1, c2)
DISCS = [
    ("shot-disc-uhd", "UHD",      "The Glass Continent",      2021, 8700, 58_000_000_000, "4K UHD",      "#1e3a8a", "#9333ea"),
    ("shot-disc-bd",  "BDMV",     "Echoes of Low Orbit",      2014, 8100, 24_000_000_000, "Blu-ray",     "#0f766e", "#1e3a8a"),
    ("shot-disc-dvd1","DVD",      "The Midnight Cartographer",2019, 6840,  7_200_000_000, "DVD",         "#7c2d12", "#b91c1c"),
    ("shot-disc-dvd2","DVD",      "Harbor of Quiet Engines",  2003, 5520,  6_100_000_000, "DVD",         "#374151", "#111827"),
    ("shot-disc-cd",  "AUDIO_CD", "Parallel Hours",           2022, 3120,    520_000_000, "Neon Tide",   "#be185d", "#7c3aed"),
    ("shot-disc-psx", "PSX",      "Starfall Tactics",         1999, 0,        650_000_000, "PlayStation", "#0e7490", "#155e75"),
]


def metadata_json(d):
    if not USE_POSTERS:
        return "{}"
    title, subtitle = d[2], d[6]
    url = poster(title, subtitle, d[7], d[8])
    import json
    return json.dumps({"poster_url": url})


DRIVES = [
    # id, model, bus, dev_path, state, last_seen_offset
    ("shot-drv-0", "Pioneer BDR-XD08", "sr0", "/dev/sr0", "ripping", 5),
    ("shot-drv-1", "ASUS BW-16D1HT",   "sr1", "/dev/sr1", "idle",    5),
]

# Done jobs: (job_id, disc_id, profile_disc_type, finished_offset_min, transcode_skipped)
DONE_JOBS = [
    ("shot-job-1", "shot-disc-bd",   "BDMV",     -60,   False),
    ("shot-job-2", "shot-disc-dvd1", "DVD",      -240,  False),
    ("shot-job-3", "shot-disc-dvd2", "DVD",      -1500, True),
    ("shot-job-4", "shot-disc-cd",   "AUDIO_CD", -2880, False),
    ("shot-job-5", "shot-disc-psx",  "PSX",      -4320, False),
]

STEP_ORDER = ["detect", "identify", "rip", "transcode", "compress", "move", "notify", "eject"]


def emit():
    out = []
    out.append("BEGIN;")
    out.append("DELETE FROM job_steps WHERE job_id LIKE 'shot-%';")
    out.append("DELETE FROM jobs   WHERE id LIKE 'shot-%';")
    out.append("DELETE FROM discs  WHERE id LIKE 'shot-%';")
    out.append("DELETE FROM drives WHERE id LIKE 'shot-%';")

    for did, model, bus, dev, state, off in DRIVES:
        out.append(
            "INSERT INTO drives (id,model,bus,dev_path,state,last_seen_at,notes) VALUES "
            f"({sqlstr(did)},{sqlstr(model)},{sqlstr(bus)},{sqlstr(dev)},{sqlstr(state)},{sqlstr(ts(off))},'');"
        )

    for d in DISCS:
        did, typ, title, year, runtime, size = d[0], d[1], d[2], d[3], d[4], d[5]
        # The actively-ripping disc is attached to the ripping drive; rest NULL.
        drive = "'shot-drv-0'" if did == "shot-disc-uhd" else "NULL"
        out.append(
            "INSERT INTO discs (id,drive_id,type,title,year,runtime_seconds,size_bytes_raw,"
            "toc_hash,metadata_provider,metadata_id,candidates_json,created_at,metadata_json) VALUES "
            f"({sqlstr(did)},{drive},{sqlstr(typ)},{sqlstr(title)},{year},{runtime},{size},"
            f"'','','','[]',{sqlstr(ts(-5000))},{sqlstr(metadata_json(d))});"
        )

    # Active running job: UHD on the ripping drive, rip step at 38%.
    pid = "(SELECT id FROM profiles WHERE disc_type='UHD' AND enabled=1 LIMIT 1)"
    out.append(
        "INSERT INTO jobs (id,disc_id,drive_id,profile_id,state,active_step,progress,speed,"
        "eta_seconds,elapsed_seconds,started_at,finished_at,error_message,created_at,active_substep) VALUES "
        f"('shot-job-run','shot-disc-uhd','shot-drv-0',{pid},'running','rip',38,'118.4 MB/s',"
        f"1380,920,{sqlstr(ts(-15))},'','',{sqlstr(ts(-16))},'read_raw_data');"
    )
    run_steps = {"detect": "done", "identify": "done", "rip": "running"}
    for i, st in enumerate(STEP_ORDER):
        state = run_steps.get(st, "pending")
        sa = ts(-15 + i) if state in ("done", "running") else ""
        fa = ts(-15 + i + 1) if state == "done" else ""
        out.append(
            "INSERT INTO job_steps (job_id,step,state,attempt_count,started_at,finished_at,notes_json) VALUES "
            f"('shot-job-run',{sqlstr(st)},{sqlstr(state)},{1 if state!='pending' else 0},{sqlstr(sa)},{sqlstr(fa)},'{{}}');"
        )

    for jid, disc, dtyp, foff, tskip in DONE_JOBS:
        pid = f"(SELECT id FROM profiles WHERE disc_type='{dtyp}' AND enabled=1 LIMIT 1)"
        started = foff - 45
        out.append(
            "INSERT INTO jobs (id,disc_id,drive_id,profile_id,state,active_step,progress,speed,"
            "eta_seconds,elapsed_seconds,started_at,finished_at,error_message,created_at,active_substep) VALUES "
            f"({sqlstr(jid)},{sqlstr(disc)},NULL,{pid},'done','',100,'',0,2700,"
            f"{sqlstr(ts(started))},{sqlstr(ts(foff))},'',{sqlstr(ts(started-1))},'');"
        )
        for i, st in enumerate(STEP_ORDER):
            if st in ("transcode", "compress") and tskip:
                state = "skipped"
            else:
                state = "done"
            sa = ts(started + i)
            fa = ts(started + i + 1) if state == "done" else ""
            out.append(
                "INSERT INTO job_steps (job_id,step,state,attempt_count,started_at,finished_at,notes_json) VALUES "
                f"({sqlstr(jid)},{sqlstr(st)},{sqlstr(state)},{1 if state=='done' else 0},{sqlstr(sa)},{sqlstr(fa)},'{{}}');"
            )

    out.append("COMMIT;")
    return "\n".join(out) + "\n"


if __name__ == "__main__":
    import sys
    sys.stdout.write(emit())
