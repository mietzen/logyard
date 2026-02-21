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
TEST_TMPDIR=$(mktemp -d)
cat > "$TEST_TMPDIR/config.yaml" << 'YAML'
db_path: /data/test-logyard.db
retention: 1
debug: true

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
  - name: "test-above-alert"
    count: 1
    window_minutes: 5
    level: warning
    above: true
  - name: "test-ignored-tag-alert"
    count: 1
    window_minutes: 5
    level: notice
  - name: "test-ignored-regex-alert"
    count: 1
    window_minutes: 5
    level: info

ignore:
  - tag: ignored-app
  - message: "should-be-ignored"
YAML

echo "=== Starting logyard ==="
docker run -d --name logyard-test --network logyard-testnet \
    -v "$TEST_TMPDIR/config.yaml":/data/config.yaml \
    -p 1514:514/udp \
    -p 1515:514/tcp \
    -p 8080:8080 \
    logyard-test -config /data/config.yaml -alert-interval 3s
sleep 2

# --- RFC 3164 (BSD syslog) via UDP ---
echo "=== Sending RFC 3164 message (UDP) ==="
SYSLOG_TS=$(date -u '+%b %d %H:%M:%S')
echo "<12>${SYSLOG_TS} rfc3164host myapp: RFC3164 warning test" | nc -u -w1 127.0.0.1 1514
sleep 1

# --- RFC 5424 via TCP ---
echo "=== Sending RFC 5424 message (TCP) ==="
RFC5424_TS=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
echo "<12>1 ${RFC5424_TS} rfc5424host myapp 1234 - - RFC5424 warning test" | nc -w1 127.0.0.1 1515
sleep 1

# --- RFC 3164 err message via UDP (for above test) ---
echo "=== Sending RFC 3164 err message (UDP, for above test) ==="
SYSLOG_TS2=$(date -u '+%b %d %H:%M:%S')
echo "<11>${SYSLOG_TS2} abovehost myapp: RFC3164 err test for above" | nc -u -w1 127.0.0.1 1514
sleep 1

# --- Ignored by tag rule ---
echo "=== Sending notice message with ignored tag ==="
SYSLOG_TS3=$(date -u '+%b %d %H:%M:%S')
echo "<13>${SYSLOG_TS3} taghost ignored-app: this notice should be ignored by tag" | nc -u -w1 127.0.0.1 1514
sleep 1

# --- Ignored by message regex rule ---
echo "=== Sending info message with ignored message pattern ==="
SYSLOG_TS4=$(date -u '+%b %d %H:%M:%S')
echo "<14>${SYSLOG_TS4} regexhost someapp: this should-be-ignored by regex" | nc -u -w1 127.0.0.1 1514
sleep 2

echo "=== Checking database ==="
docker exec logyard-test sh -c 'apt-get update -qq && apt-get install -y -qq sqlite3 >/dev/null 2>&1; sqlite3 /data/test-logyard.db "SELECT * FROM logs;"'
COUNT=$(docker exec logyard-test sh -c 'sqlite3 /data/test-logyard.db "SELECT count(*) FROM logs;"')
echo "Log count: $COUNT"
if [ "$COUNT" -lt 5 ]; then
    echo "FAIL: Expected at least 5 logs, got $COUNT"
    exit 1
fi

# Verify RFC 3164 entry
RFC3164_COUNT=$(docker exec logyard-test sh -c "sqlite3 /data/test-logyard.db \"SELECT count(*) FROM logs WHERE host='rfc3164host';\"")
if [ "$RFC3164_COUNT" -lt 1 ]; then
    echo "FAIL: RFC 3164 message not found"
    exit 1
fi
echo "PASS: RFC 3164 message stored"

# Verify RFC 5424 entry
RFC5424_COUNT=$(docker exec logyard-test sh -c "sqlite3 /data/test-logyard.db \"SELECT count(*) FROM logs WHERE host='rfc5424host';\"")
if [ "$RFC5424_COUNT" -lt 1 ]; then
    echo "FAIL: RFC 5424 message not found"
    exit 1
fi
echo "PASS: RFC 5424 message stored"

# Verify above test entry
ABOVE_COUNT=$(docker exec logyard-test sh -c "sqlite3 /data/test-logyard.db \"SELECT count(*) FROM logs WHERE host='abovehost';\"")
if [ "$ABOVE_COUNT" -lt 1 ]; then
    echo "FAIL: Above test message not found"
    exit 1
fi
echo "PASS: Above test message stored"

# Verify ignored-by-tag entry is still stored (ignore only affects alerting)
TAGHOST_COUNT=$(docker exec logyard-test sh -c "sqlite3 /data/test-logyard.db \"SELECT count(*) FROM logs WHERE host='taghost';\"")
if [ "$TAGHOST_COUNT" -lt 1 ]; then
    echo "FAIL: Tag-ignored message not stored in DB"
    exit 1
fi
echo "PASS: Tag-ignored message stored (ignore only affects alerting)"

# Verify ignored-by-regex entry is still stored
REGEXHOST_COUNT=$(docker exec logyard-test sh -c "sqlite3 /data/test-logyard.db \"SELECT count(*) FROM logs WHERE host='regexhost';\"")
if [ "$REGEXHOST_COUNT" -lt 1 ]; then
    echo "FAIL: Regex-ignored message not stored in DB"
    exit 1
fi
echo "PASS: Regex-ignored message stored (ignore only affects alerting)"

echo "=== Waiting for alert evaluation ==="
sleep 5

echo "=== Logyard logs ==="
docker logs logyard-test

echo "=== Checking mailpit for alert emails ==="
MAIL_RESPONSE=$(docker exec mailpit-test wget -qO- http://localhost:8025/api/v1/messages)
MSG_COUNT=$(echo "$MAIL_RESPONSE" | grep -o '"messages_count":[0-9]*' | cut -d: -f2)
echo "Email count: $MSG_COUNT"
if [ "$MSG_COUNT" -lt 1 ]; then
    echo "FAIL: No alert email received"
    echo "$MAIL_RESPONSE"
    exit 1
fi
echo "PASS: Alert email(s) received"

# Check that the "above" alert fired (err triggers warning-and-above rule)
# The above rule should fire because err is above warning
ABOVE_ALERT=$(echo "$MAIL_RESPONSE" | grep -o 'test-above-alert' || true)
if [ -z "$ABOVE_ALERT" ]; then
    echo "FAIL: Above alert (test-above-alert) did not fire for err-level message"
    echo "$MAIL_RESPONSE"
    exit 1
fi
echo "PASS: Above alert triggered by err-level message"

# Check that the tag-ignored alert did NOT fire
TAG_ALERT=$(echo "$MAIL_RESPONSE" | grep -o 'test-ignored-tag-alert' || true)
if [ -n "$TAG_ALERT" ]; then
    echo "FAIL: Tag-ignored alert (test-ignored-tag-alert) should not have fired"
    echo "$MAIL_RESPONSE"
    exit 1
fi
echo "PASS: Tag ignore rule prevented alert"

# Check that the regex-ignored alert did NOT fire
REGEX_ALERT=$(echo "$MAIL_RESPONSE" | grep -o 'test-ignored-regex-alert' || true)
if [ -n "$REGEX_ALERT" ]; then
    echo "FAIL: Regex-ignored alert (test-ignored-regex-alert) should not have fired"
    echo "$MAIL_RESPONSE"
    exit 1
fi
echo "PASS: Message regex ignore rule prevented alert"

echo "=== Checking web UI ==="
STATUS=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/)
echo "Web UI status: $STATUS"
if [ "$STATUS" != "200" ]; then
    echo "FAIL: Web UI not responding"
    exit 1
fi

BODY=$(curl -s http://127.0.0.1:8080/api/logs)
if ! echo "$BODY" | grep -q "rfc3164host"; then
    echo "FAIL: RFC 3164 entry not in API response"
    echo "$BODY"
    exit 1
fi
if ! echo "$BODY" | grep -q "rfc5424host"; then
    echo "FAIL: RFC 5424 entry not in API response"
    echo "$BODY"
    exit 1
fi
echo "PASS: Web UI and API working"

echo "=== Checking healthz ==="
HEALTH=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/healthz)
if [ "$HEALTH" != "200" ]; then
    echo "FAIL: Healthcheck failed"
    exit 1
fi
echo "PASS: Healthcheck OK"

echo "=== Checking filters API ==="
FILTERS=$(curl -s http://127.0.0.1:8080/api/filters)
if ! echo "$FILTERS" | grep -q "rfc3164host"; then
    echo "FAIL: Filters API missing host"
    echo "$FILTERS"
    exit 1
fi
echo "PASS: Filters API working"

echo "=== Checking config API ==="
CONFIG=$(curl -s http://127.0.0.1:8080/api/config)
if ! echo "$CONFIG" | grep -q "test-above-alert"; then
    echo "FAIL: Config API missing above alert rule"
    echo "$CONFIG"
    exit 1
fi
echo "PASS: Config API working"

echo "=== Checking --version flag ==="
VERSION_OUTPUT=$(docker run --rm logyard-test -version)
if [ -z "$VERSION_OUTPUT" ]; then
    echo "FAIL: --version produced no output"
    exit 1
fi
echo "PASS: --version output: $VERSION_OUTPUT"

echo "=== Checking log field correctness ==="
# Verify RFC 3164 fields: facility=user (code 1), severity=warning (code 4), tag=myapp
RFC3164_FIELDS=$(docker exec logyard-test sh -c "sqlite3 /data/test-logyard.db \"SELECT facility, severity, tag FROM logs WHERE host='rfc3164host' LIMIT 1;\"")
echo "RFC3164 fields: $RFC3164_FIELDS"
if ! echo "$RFC3164_FIELDS" | grep -q "user"; then
    echo "FAIL: RFC 3164 facility should be 'user', got: $RFC3164_FIELDS"
    exit 1
fi
if ! echo "$RFC3164_FIELDS" | grep -q "warning"; then
    echo "FAIL: RFC 3164 severity should be 'warning', got: $RFC3164_FIELDS"
    exit 1
fi
if ! echo "$RFC3164_FIELDS" | grep -q "myapp"; then
    echo "FAIL: RFC 3164 tag should be 'myapp', got: $RFC3164_FIELDS"
    exit 1
fi
echo "PASS: RFC 3164 field correctness (facility=user, severity=warning, tag=myapp)"

# Verify RFC 5424 fields
RFC5424_FIELDS=$(docker exec logyard-test sh -c "sqlite3 /data/test-logyard.db \"SELECT facility, severity, tag FROM logs WHERE host='rfc5424host' LIMIT 1;\"")
echo "RFC5424 fields: $RFC5424_FIELDS"
if ! echo "$RFC5424_FIELDS" | grep -q "user"; then
    echo "FAIL: RFC 5424 facility should be 'user', got: $RFC5424_FIELDS"
    exit 1
fi
if ! echo "$RFC5424_FIELDS" | grep -q "warning"; then
    echo "FAIL: RFC 5424 severity should be 'warning', got: $RFC5424_FIELDS"
    exit 1
fi
if ! echo "$RFC5424_FIELDS" | grep -q "myapp"; then
    echo "FAIL: RFC 5424 tag should be 'myapp', got: $RFC5424_FIELDS"
    exit 1
fi
echo "PASS: RFC 5424 field correctness (facility=user, severity=warning, tag=myapp)"

echo "=== Checking config PUT API (save and reload) ==="
# Save new config via PUT
PUT_RESPONSE=$(curl -s -w '\n%{http_code}' -X PUT http://127.0.0.1:8080/api/config \
    -H 'Content-Type: application/json' \
    -d '{"smtp":{"host":"mailpit-test","port":1025,"from":"alerts@test.local","to":"admin@test.local"},"alerts":[{"name":"put-test-alert","count":5,"window_minutes":10,"level":"err","above":false}],"ignore":[],"retention":7,"debug":false}')
PUT_STATUS=$(echo "$PUT_RESPONSE" | tail -1)
if [ "$PUT_STATUS" != "200" ]; then
    echo "FAIL: Config PUT returned $PUT_STATUS"
    echo "$PUT_RESPONSE"
    exit 1
fi

# Read back and verify
GET_CONFIG=$(curl -s http://127.0.0.1:8080/api/config)
if ! echo "$GET_CONFIG" | grep -q "put-test-alert"; then
    echo "FAIL: Config PUT did not persist alert rule"
    echo "$GET_CONFIG"
    exit 1
fi
if ! echo "$GET_CONFIG" | grep -q '"retention":7'; then
    echo "FAIL: Config PUT did not persist retention"
    echo "$GET_CONFIG"
    exit 1
fi
echo "PASS: Config PUT API (save and reload)"

echo "=== Checking config validation rejects bad input ==="
# Bad level
BAD_LEVEL=$(curl -s -o /dev/null -w '%{http_code}' -X PUT http://127.0.0.1:8080/api/config \
    -H 'Content-Type: application/json' \
    -d '{"smtp":{},"alerts":[{"name":"bad","count":1,"window_minutes":5,"level":"banana"}],"ignore":[],"retention":7,"debug":false}')
if [ "$BAD_LEVEL" != "400" ]; then
    echo "FAIL: Bad level should return 400, got $BAD_LEVEL"
    exit 1
fi
echo "PASS: Bad alert level rejected"

# Bad retention
BAD_RETENTION=$(curl -s -o /dev/null -w '%{http_code}' -X PUT http://127.0.0.1:8080/api/config \
    -H 'Content-Type: application/json' \
    -d '{"smtp":{},"alerts":[],"ignore":[],"retention":0,"debug":false}')
if [ "$BAD_RETENTION" != "400" ]; then
    echo "FAIL: Bad retention should return 400, got $BAD_RETENTION"
    exit 1
fi
echo "PASS: Bad retention rejected"

# Bad ignore level
BAD_IGNORE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT http://127.0.0.1:8080/api/config \
    -H 'Content-Type: application/json' \
    -d '{"smtp":{},"alerts":[],"ignore":[{"level":"banana"}],"retention":7,"debug":false}')
if [ "$BAD_IGNORE" != "400" ]; then
    echo "FAIL: Bad ignore level should return 400, got $BAD_IGNORE"
    exit 1
fi
echo "PASS: Bad ignore level rejected"

# Bad regex
BAD_REGEX=$(curl -s -o /dev/null -w '%{http_code}' -X PUT http://127.0.0.1:8080/api/config \
    -H 'Content-Type: application/json' \
    -d '{"smtp":{},"alerts":[],"ignore":[{"message":"[invalid"}],"retention":7,"debug":false}')
if [ "$BAD_REGEX" != "400" ]; then
    echo "FAIL: Bad regex should return 400, got $BAD_REGEX"
    exit 1
fi
echo "PASS: Bad ignore regex rejected"

# Restore original config for clean state
curl -s -X PUT http://127.0.0.1:8080/api/config \
    -H 'Content-Type: application/json' \
    -d '{"smtp":{"host":"mailpit-test","port":1025,"from":"alerts@test.local","to":"admin@test.local"},"alerts":[{"name":"test-warning-alert","count":1,"window_minutes":5,"level":"warning"}],"ignore":[],"retention":1,"debug":true}' > /dev/null

echo ""
echo "=== All tests passed ==="
