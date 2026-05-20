# README screenshots

Reproducible mock data + capture steps for the screenshots in the top-level
README. All data here is fabricated and copyright-safe (invented titles,
generated SVG cover art). Local-only — these rows live in the gitignored
`.docker-data` volume and never touch production.

## Refresh the screenshots

1. Ensure the local container is up: `docker compose up -d` (from repo root),
   then `curl -s http://localhost:8088/api/version`.
2. Regenerate the seed if you changed the data:
   `python3 docs/screenshots/gen_seed.py > docs/screenshots/seed.sql`
   (set `USE_POSTERS = False` first if the daemon ever sends a `data:`-blocking
   CSP; today it sends none).
3. Install sqlite3 in the container (once) and apply the seed:
   ```
   docker exec discecho sh -c 'command -v sqlite3 >/dev/null || (apt-get update -qq && apt-get install -y -qq sqlite3)'
   docker exec -i discecho sqlite3 /var/lib/discecho/discecho.sqlite < docs/screenshots/seed.sql
   ```
   Do NOT restart the container after seeding — startup recovery flips the
   seeded `running` job to `interrupted`.
4. Capture with the Chrome DevTools MCP at `http://localhost:8088`:
   - Desktop `1280x900x1`: dashboard `/`, `/history`, `/settings`.
   - Mobile `390x844x2,mobile,touch`: same three.
   Save into `docs/img/` as `dashboard.png`, `history.png`, `settings.png`
   and the `-mobile.png` variants.
5. Clean up the local DB when done (optional):
   `docker exec -i discecho sqlite3 /var/lib/discecho/discecho.sqlite < docs/screenshots/cleanup.sql`
