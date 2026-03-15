# Logyard

Lightweight syslog aggregator with web UI and email alerting. Single Go binary, no external dependencies except SQLite.

## Build & Test

```bash
go build -o logyard .     # Build binary
go test ./...             # Run all tests
bash test.sh              # Integration tests (requires Docker)
```

## Local Dev Environment

```bash
docker compose -f docker-compose.dev.yaml up --build
```

Starts three containers:

- **logyard** — the app on `localhost:8080` (syslog on UDP `:1514` / TCP `:1515`)
- **mailpit** — email testing UI on `localhost:8025` (catches alert emails)
- **syslog-generator** — sends a continuous stream of test syslog messages via `dev/generate.sh`

Config is at `dev/config.yaml`, data persisted in a Docker volume.

## Project Structure

- `main.go` — Entry point, CLI flags, wires up config/DB/receiver/alerter/web
- `config.go` — YAML config parsing, validation, hot-reload via ConfigManager (RWMutex)
- `db.go` — SQLite database (WAL mode), log insert/query, retention cleanup
- `receiver.go` — Syslog UDP/TCP receiver, ignore rules, severity rewriting
- `alerter.go` — Periodic alert evaluation, email sending, digest mode
- `web.go` — HTTP server, API endpoints, Go HTML templates for log rows
- `web/index.html` — Single-page UI (vanilla JS + HTMX, dark theme, ~38KB)
- `web/htmx.min.js` — HTMX library (embedded)
- `dev/` — Local dev setup (syslog generator, config)

## Architecture

- **Message flow**: Syslog UDP/TCP → Receiver (ignore/rewrite) → SQLite → Web API → HTMX polling (3s)
- **Config**: YAML file, hot-reloadable via `PUT /api/config`. ConfigManager wraps with RWMutex.
- **Frontend**: No framework. HTMX swaps server-rendered HTML rows into `#log-body` tbody. All JS/CSS inline in `index.html`.
- **API endpoints**: `GET /api/logs` (HTML rows), `GET /api/filters` (dropdown values), `GET|PUT /api/config`, `GET /healthz`

## Key Patterns

- Tag and Message fields in rules (alert, ignore, severity rewrite) use **regex matching** (Go RE2 syntax)
- Host, Facility, Level fields use exact string matching
- Rule evaluation: all specified fields must match (AND logic), first matching rule wins
- Row template in `web.go` uses Go `html/template`; frontend JS uses event delegation on `#log-body` for click handlers (survives HTMX swaps)
