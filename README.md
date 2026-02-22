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

Every alert rule requires `count`, `window_minutes`, and `level`. Optionally narrow the scope with `host`, `facility`, `tag`, or `message` (regex, RE2 syntax). Empty fields are ignored. The alerter checks every 60s (configurable via `-alert-interval`) and sends an email when the threshold is reached. Cooldown prevents re-alerting within the same time window.

### Ignore rules

Each rule matches on all specified fields (AND). Multiple rules are OR'd. By default, ignore rules apply to alerting only -- all logs are stored and visible in the UI.

Set `discard: true` to drop matching messages entirely -- they will not be stored in the database or appear in the UI. This is useful for noisy hosts whose logs you never want to see.

The `message` field supports regular expressions using Go's [RE2 syntax](https://github.com/google/re2/wiki/Syntax) (e.g. `CRON|systemd-.*`).

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

Open `http://localhost:8080`. Auto-refreshes every 3 seconds. Filter by host, facility, severity, tag, or free-text search.

Logyard does not provide authentication or TLS. Use a reverse proxy like [Caddy](https://caddyserver.com/) for HTTPS and access control.

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
