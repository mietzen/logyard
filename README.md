# Logyard

A lightweight syslog aggregator with web UI and email alerting. Single Go binary, SQLite storage, no external dependencies. Accepts both RFC 3164 (BSD) and RFC 5424 syslog messages over UDP and TCP.

![Gif of Logyards log console](assets/censored-console.gif)

---

![Gif of Logyards settings](assets/settings.gif)

---

## Build

Requires Go 1.25+ and CGO (for SQLite).

```shell
CGO_ENABLED=1 go build -o logyard .
```

## Usage

```shell
./logyard -config ./config.yaml
```

Logyard looks for `config.yaml` in the current directory or `/etc/logyard/config.yaml`.

### Flags

```text
-config string    Path to config.yaml
-alert-interval   Alert evaluation interval (default 60s)
```

## Config

See [config.yaml.example](config.yaml.example) for a full example.

```yaml
# db_path: ./logyard.db
# retention: 14  # days
# debug: false
# web_addr: ":8080"
# url: "https://logyard.example.com"  # used in alert emails

listen:
  udp: ":514"
  tcp: ":514"

smtp:
  host: smtp.example.com
  port: 587
  user: alerts@example.com
  password: secret
  from: alerts@example.com
  to: admin@example.com

alerts:
  - name: "Many warnings"
    count: 10
    window_minutes: 5
    level: warning
  - name: "Any critical"
    count: 1
    window_minutes: 5
    level: crit
  - name: "Nginx errors"
    count: 5
    window_minutes: 10
    level: err
    tag: nginx
    message: "timeout|connection refused"

ignore:
  - host: noisy-box.lan
  - facility: kern
  - tag: CRON
  - host: proxmox
    level: warning
  - message: "CRON|systemd-.*"
  - host: noisy-box.lan
    discard: true

digest:
  enabled: true
  initial: "5m"
  multiplier: 3
  max: "2h"
  cooldown: "10m"

severity_rewrite:
  - tag: my-docker-app
    level: info
    message: "ERROR|FATAL"
    new_severity: err
  - tag: my-docker-app
    level: info
    message: "WARN"
    new_severity: warning
```

### Alert rules

Every alert rule requires `count`, `window_minutes`, and `level`. Optionally narrow the scope with `host`, `facility`, `tag` (regex), or `message` (regex, RE2 syntax). Empty fields are ignored. The alerter checks every 60s (configurable via `-alert-interval`) and sends an email when the threshold is reached. Cooldown prevents re-alerting within the same time window.

Alert emails are sent from "Logyard" and include an HTML table of the triggering log entries (up to 50). Set `url` in your config to include a link to your Logyard instance in the email footer. If not set, it defaults to `http://<hostname>:<web_port>`.

### Digest mode

When multiple alert rules fire continuously (e.g. overnight), individual emails can pile up quickly. Digest mode batches all triggered alerts into a single email and escalates the collection window using a multiplier, reducing noise while preserving all alert information.

```yaml
digest:
  enabled: true
  initial: "5m"       # first digest window
  multiplier: 3       # multiply window each escalation (min 1.5)
  max: "2h"           # maximum window cap
  cooldown: "10m"     # quiet period before resetting to initial
```

**Escalation example** (multiplier=3): 5m &rarr; 15m &rarr; 45m &rarr; 2h (capped). If no alerts fire for the `cooldown` duration after the last digest, the window resets to `initial`.

Duration values support human-readable units: `s`/`sec`/`seconds`, `m`/`min`/`minutes`, `h`/`hour`/`hours`. Unitless values default to seconds.

Per-rule cooldowns still apply during digest mode -- each rule only fires once per its `window_minutes`.

When digest is not configured (or `enabled: false`), behavior is identical to the default per-rule alerting.

### Ignore rules

Each rule matches on all specified fields (AND). Multiple rules are OR'd. By default, ignore rules apply to alerting only -- all logs are stored and visible in the UI.

Set `discard: true` to drop matching messages entirely -- they will not be stored in the database or appear in the UI. This is useful for noisy hosts whose logs you never want to see.

The `tag` and `message` fields support regular expressions using Go's [RE2 syntax](https://github.com/google/re2/wiki/Syntax) (e.g. `CRON|systemd-.*`). Use `^exactmatch$` for exact tag matching.

### Severity rewrite rules

Rewrite rules change the severity of matching messages before they are stored in the database. This is useful when log sources (like Docker's syslog driver) send all messages with the same severity regardless of actual log level.

Rules are evaluated in order -- **first match wins**. Each rule matches on all specified fields (AND logic, same as ignore rules). At least one match field is required. The `new_severity` field is required and must be a valid severity level.

```yaml
severity_rewrite:
  - tag: my-docker-app
    level: info
    message: "ERROR|FATAL"
    new_severity: err
  - tag: my-docker-app
    level: info
    message: "WARN"
    new_severity: warning
```

### Docker syslog logging

Docker's syslog logging driver forwards container output to a syslog server. All messages are sent as severity `info` regardless of content, which is where severity rewrite rules come in handy.

```shell
docker run -d \
  --log-driver syslog \
  --log-opt syslog-address=udp://logyard-host:514 \
  --log-opt tag=my-app \
  my-image
```

With Docker Compose:

```yaml
services:
  my-app:
    image: my-image
    logging:
      driver: syslog
      options:
        syslog-address: "udp://logyard-host:514"
        tag: my-app
        syslog-format: rfc3164
```

You can use `syslog-format: rfc5424` for RFC 5424 messages. Logyard auto-detects both formats.

#### Global daemon configuration

To apply syslog logging to all containers by default, configure Docker's `daemon.json` (typically `/etc/docker/daemon.json`):

```json
{
  "log-driver": "syslog",
  "log-opts": {
    "syslog-address": "udp://logyard-host:514",
    "syslog-facility": "docker",
    "syslog-format": "rfc3164"
  }
}
```

Restart the Docker daemon after changing `daemon.json` (`sudo systemctl restart docker`). Individual containers can still override these defaults with per-container `logging:` options.

When using the global log driver, you can still set a custom tag per container in Docker Compose without overriding the driver:

```yaml
services:
  my-app:
    image: my-image
    logging:
      options:
        tag: my-app
```

Using a dedicated facility like `local0` for all Docker containers lets you write a single set of severity rewrite rules that cover every container:

```yaml
severity_rewrite:
  - facility: docker
    level: info
    message: "ERROR|FATAL|PANIC"
    new_severity: err
  - facility: docker
    level: info
    message: "WARN"
    new_severity: warning
```

Alternatively, use per-container `tag` matching if you only need rewrite rules for specific containers:

```yaml
severity_rewrite:
  - tag: my-app
    level: info
    message: "ERROR|FATAL|PANIC"
    new_severity: err
  - tag: my-app
    level: info
    message: "WARN"
    new_severity: warning
```

## Web UI

Open `http://localhost:8080`. Auto-refreshes every 3 seconds. Filter by host, facility, severity, tag, or free-text search. The row limit dropdown controls how many rows are displayed (default 200); selecting a custom date range automatically shows unlimited rows.

Each row has a copy button that copies all fields (timestamp, host, facility, severity, tag, message) to the clipboard. Use the pause button in the filter bar to stop auto-refresh and select text across multiple rows.

Log rows that match any configured alert rule show a bell-off icon. Click it to open the settings modal with a new ignore rule prefilled from that log line's host, facility, tag, severity, and message. Review the fields, adjust the regex if needed, and click Save.

Logyard does not provide authentication or TLS. Use a reverse proxy like [Caddy](https://caddyserver.com/) for HTTPS and access control.

## Timestamps

Logyard auto-detects both RFC 3164 and RFC 5424 syslog formats. Timestamps are displayed as received -- Logyard does not convert between timezones.

- **RFC 3164** (`<PRI>Jun  1 14:30:00 ...`): No year, no timezone. The parser fills in the current year and stores the time value as-is. The displayed time matches whatever clock the sender used. Docker's syslog driver defaults to this format.
- **RFC 5424** (`<PRI>1 2025-06-01T14:30:00+02:00 ...`): Full ISO 8601 date with timezone offset. The timestamp is displayed in the sender's timezone (e.g. a message sent with `+02:00` is displayed at that offset, a message sent with `Z` is displayed as UTC).

## Docker

```shell
docker run -d \
  -v ./config.yaml:/data/config.yaml \
  -v logyard-data:/data \
  -p 514:514/udp \
  -p 514:514/tcp \
  -p 8080:8080 \
  --name logyard \
  --restart unless-stopped \
  mietzen/logyard:latest
```

Or with docker compose:

```yaml
services:
  logyard:
    image: mietzen/logyard:latest
    container_name: logyard
    volumes:
      - ./config.yaml:/data/config.yaml
      - logyard-data:/data
    ports:
      - "514:514/udp"
      - "514:514/tcp"
      - "8080:8080"
    restart: unless-stopped

volumes:
  logyard-data:
```

## Systemd

```shell
sudo cp logyard /usr/local/bin/
sudo mkdir -p /etc/logyard /var/lib/logyard
sudo cp config.yaml /etc/logyard/
sudo cp logyard.service /etc/systemd/system/
sudo systemctl enable --now logyard
```

Port 514 requires `CAP_NET_BIND_SERVICE` (included in the service file). During development, use ports above 1024.

## Local Development

A docker-compose setup is included for testing the web UI, alert rules, ignore rules, and digest batching locally. It starts logyard, a [mailpit](https://mailpit.axllent.org/) SMTP mock with web UI, and a syslog message generator that sends a continuous stream of realistic messages.

```shell
docker compose -f docker-compose.dev.yaml up --build
```

| Service   | URL                    | Description                           |
|-----------|------------------------|---------------------------------------|
| Logyard   | <http://localhost:8080> | Web UI and log console                |
| Mailpit   | <http://localhost:8025> | Email inbox UI (captures alert mails) |

The generator sends a weighted mix of severities (mostly info, some warnings, occasional errors, rare crits) from multiple hosts and tags. Edit `dev/config.yaml` to test different alert rules, ignore rules, severity rewrites, or digest settings. Changes to the config can be applied live via the settings modal in the Logyard web UI.

Stop the stack with:

```shell
docker compose -f docker-compose.dev.yaml down -v
```
