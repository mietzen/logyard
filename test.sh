#!/usr/bin/env bash
set -euo pipefail

cleanup() {
    echo "Cleaning up..."
    docker rm -f logyard-test mailpit-test 2>/dev/null || true
    docker network rm logyard-testnet 2>/dev/null || true
}
trap cleanup EXIT

cleanup

echo "=== Building logyard ==="
docker build -t logyard-test .

echo "=== Setting up test network ==="
docker network create logyard-testnet

echo "=== Starting mailpit ==="
docker run -d --name mailpit-test --network logyard-testnet \
    axllent/mailpit:latest

echo "=== Writing test config ==="
TMPDIR=$(mktemp -d)
cat > "$TMPDIR/config.yaml" << 'YAML'
db_path: /data/test-logyard.db
retention: 1

listen:
  udp: ":514"
  tcp: ":514"

web_addr: ":8080"

smtp:
  host: mailpit-test
  port: 1025
  from: alerts@test.local
  to: admin@test.local

alerts:
  - name: "test-warning-alert"
    count: 1
    window_minutes: 5
    level: warning
YAML

echo "=== Starting logyard ==="
docker run -d --name logyard-test --network logyard-testnet \
    -v "$TMPDIR/config.yaml":/data/config.yaml \
    -p 1514:514/udp \
    -p 1515:514/tcp \
    -p 8080:8080 \
    logyard-test -config /data/config.yaml -alert-interval 3s
sleep 2

echo "=== Sending syslog message (UDP) ==="
SYSLOG_TS=$(date -u '+%b %d %H:%M:%S')
echo "<12>${SYSLOG_TS} testhost myapp: this is a warning test message" | nc -u -w1 127.0.0.1 1514
sleep 2

echo "=== Checking database ==="
docker exec logyard-test sh -c 'apk add --no-cache sqlite >/dev/null 2>&1; sqlite3 /data/test-logyard.db "SELECT * FROM logs;"'
COUNT=$(docker exec logyard-test sh -c 'apk add --no-cache sqlite >/dev/null 2>&1; sqlite3 /data/test-logyard.db "SELECT count(*) FROM logs;"')
echo "Log count: $COUNT"
if [ "$COUNT" -lt 1 ]; then
    echo "FAIL: No logs in database"
    exit 1
fi
echo "PASS: Log stored"

echo "=== Waiting for alert evaluation ==="
sleep 5

echo "=== Logyard logs ==="
docker logs logyard-test

echo "=== Checking mailpit for alert email ==="
MAIL_RESPONSE=$(docker exec mailpit-test wget -qO- http://localhost:8025/api/v1/messages)
MSG_COUNT=$(echo "$MAIL_RESPONSE" | grep -o '"messages_count":[0-9]*' | cut -d: -f2)
echo "Email count: $MSG_COUNT"
if [ "$MSG_COUNT" -lt 1 ]; then
    echo "FAIL: No alert email received"
    echo "$MAIL_RESPONSE"
    exit 1
fi
echo "PASS: Alert email received"

echo "=== Checking web UI ==="
STATUS=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/)
echo "Web UI status: $STATUS"
if [ "$STATUS" != "200" ]; then
    echo "FAIL: Web UI not responding"
    exit 1
fi

BODY=$(curl -s http://127.0.0.1:8080/api/logs)
if ! echo "$BODY" | grep -q "testhost"; then
    echo "FAIL: Log entry not in API response"
    echo "$BODY"
    exit 1
fi
echo "PASS: Web UI and API working"

echo ""
echo "=== All tests passed ==="
