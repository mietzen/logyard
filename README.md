# Logyard

A lightweight syslog aggregator with web UI and email alerting. Single Go binary, SQLite storage, no external dependencies.

## Build

Requires Go 1.22+ and CGO (for SQLite).

```shell
CGO_ENABLED=1 go build -o logyard .
```

## Usage

```shell
./logyard -config ./config.yaml
```

Logyard looks for `config.yaml` in the current directory or `/etc/logyard/config.yaml`.

### Flags

```
-config string    Path to config.yaml
-alert-interval   Alert evaluation interval (default 60s)
```

## Config

See [config.yaml.example](config.yaml.example) for a full example.

```yaml
# db_path: ./logyard.db
# retention: 14  # days
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

ignore:
  - host: noisy-box.lan
  - facility: kern
  - host: proxmox
    level: warning
```

### Alert rules

Every alert rule requires `count`, `window_minutes`, and `level`. The alerter checks every 60s (configurable via `-alert-interval`) and sends an email when the threshold is reached. Cooldown prevents re-alerting within the same time window.

### Ignore rules

Each rule matches on all specified fields (AND). Multiple rules are OR'd. Ignore rules apply to alerting only -- all logs are stored and visible in the UI.

## Web UI

Open `http://localhost:8080`. Auto-refreshes every 3 seconds. Filter by host, facility, severity, tag, or free-text search.

## Systemd

```shell
sudo cp logyard /usr/local/bin/
sudo mkdir -p /etc/logyard /var/lib/logyard
sudo cp config.yaml /etc/logyard/
sudo cp logyard.service /etc/systemd/system/
sudo systemctl enable --now logyard
```

Port 514 requires `CAP_NET_BIND_SERVICE` (included in the service file). During development, use ports above 1024.
