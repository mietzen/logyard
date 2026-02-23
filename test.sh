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
url: "http://logyard-test:8080"

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
  - name: "test-filtered-alert"
    count: 1
    window_minutes: 5
    level: err
    host: filterhost
    tag: filtapp
    message: "disk.*full"

ignore:
  - tag: ignored-app
  - message: "should-be-ignored"
  - host: discardhost
    discard: true
  - tag: rewrite-test

severity_rewrite:
  - tag: rewrite-test
    level: info
    message: "ERROR|FATAL"
    new_severity: err
  - tag: rewrite-test
    level: info
    message: "WARN"
    new_severity: warning
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
sleep 1

# --- Filtered alert: matching message (should trigger test-filtered-alert) ---
echo "=== Sending err message matching filtered alert ==="
SYSLOG_TS5=$(date -u '+%b %d %H:%M:%S')
echo "<11>${SYSLOG_TS5} filterhost filtapp: disk is full error" | nc -u -w1 127.0.0.1 1514
sleep 1

# --- Filtered alert: non-matching host (should NOT trigger test-filtered-alert) ---
echo "=== Sending err message not matching filtered alert (wrong host) ==="
SYSLOG_TS6=$(date -u '+%b %d %H:%M:%S')
echo "<11>${SYSLOG_TS6} otherhost filtapp: disk is full error" | nc -u -w1 127.0.0.1 1514
sleep 1

# --- Discard rule: message from discardhost should NOT be stored ---
echo "=== Sending message from discardhost (should be discarded) ==="
SYSLOG_TS7=$(date -u '+%b %d %H:%M:%S')
echo "<12>${SYSLOG_TS7} discardhost noisy: this should be discarded entirely" | nc -u -w1 127.0.0.1 1514
sleep 2

# --- Severity rewrite: Docker syslog driver tests ---
# On Docker Desktop (macOS/Windows) use host.docker.internal; on Linux use 127.0.0.1
if [ "$(uname)" = "Linux" ]; then
    SYSLOG_HOST="127.0.0.1"
else
    SYSLOG_HOST="host.docker.internal"
fi

echo "=== Testing severity rewrite with Docker syslog driver (RFC 3164) ==="
docker run --rm \
    --log-driver syslog \
    --log-opt syslog-address=udp://${SYSLOG_HOST}:1514 \
    --log-opt tag=rewrite-test \
    --log-opt syslog-format=rfc3164 \
    alpine echo "ERROR this is a critical failure from rfc3164"
sleep 2

echo "=== Testing severity rewrite with Docker syslog driver (RFC 5424) ==="
docker run --rm \
    --log-driver syslog \
    --log-opt syslog-address=udp://${SYSLOG_HOST}:1514 \
    --log-opt tag=rewrite-test \
    --log-opt syslog-format=rfc5424 \
    alpine echo "ERROR this is a critical failure from rfc5424"
sleep 2

echo "=== Testing severity rewrite non-match (should stay info) ==="
docker run --rm \
    --log-driver syslog \
    --log-opt syslog-address=udp://${SYSLOG_HOST}:1514 \
    --log-opt tag=rewrite-test \
    --log-opt syslog-format=rfc3164 \
    alpine echo "this is a normal info message"
sleep 2

echo "=== Checking database ==="
docker exec logyard-test sh -c 'apt-get update -qq && apt-get install -y -qq sqlite3 >/dev/null 2>&1; sqlite3 /data/test-logyard.db "SELECT * FROM logs;"'
COUNT=$(docker exec logyard-test sh -c 'sqlite3 /data/test-logyard.db "SELECT count(*) FROM logs;"')
echo "Log count: $COUNT"
if [ "$COUNT" -lt 10 ]; then
    echo "FAIL: Expected at least 10 logs, got $COUNT"
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

# Verify discarded message is NOT in DB
DISCARD_COUNT=$(docker exec logyard-test sh -c "sqlite3 /data/test-logyard.db \"SELECT count(*) FROM logs WHERE host='discardhost';\"")
if [ "$DISCARD_COUNT" -ne 0 ]; then
    echo "FAIL: Discarded message should not be in DB, got $DISCARD_COUNT entries"
    exit 1
fi
echo "PASS: Discard rule prevented log from being stored"

# Verify severity rewrite: ERROR messages from rewrite-test tag should be stored as "err"
REWRITE_ERR_COUNT=$(docker exec logyard-test sh -c "sqlite3 /data/test-logyard.db \"SELECT count(*) FROM logs WHERE tag='rewrite-test' AND severity='err' AND message LIKE '%ERROR%';\"")
echo "Rewrite err count: $REWRITE_ERR_COUNT"
if [ "$REWRITE_ERR_COUNT" -lt 2 ]; then
    echo "FAIL: Expected at least 2 severity-rewritten err messages (rfc3164+rfc5424), got $REWRITE_ERR_COUNT"
    docker exec logyard-test sh -c "sqlite3 /data/test-logyard.db \"SELECT severity, tag, message FROM logs WHERE tag='rewrite-test';\""
    exit 1
fi
echo "PASS: Severity rewrite changed info->err for ERROR messages"

# Verify non-matching message kept original severity (info)
REWRITE_INFO_COUNT=$(docker exec logyard-test sh -c "sqlite3 /data/test-logyard.db \"SELECT count(*) FROM logs WHERE tag='rewrite-test' AND severity='info' AND message LIKE '%normal info%';\"")
echo "Rewrite info count: $REWRITE_INFO_COUNT"
if [ "$REWRITE_INFO_COUNT" -lt 1 ]; then
    echo "FAIL: Non-matching message should keep severity info, got $REWRITE_INFO_COUNT"
    docker exec logyard-test sh -c "sqlite3 /data/test-logyard.db \"SELECT severity, tag, message FROM logs WHERE tag='rewrite-test';\""
    exit 1
fi
echo "PASS: Non-matching message kept original severity (info)"

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

# Check that the filtered alert DID fire (matching host+tag+message)
FILTERED_ALERT=$(echo "$MAIL_RESPONSE" | grep -o 'test-filtered-alert' || true)
if [ -z "$FILTERED_ALERT" ]; then
    echo "FAIL: Filtered alert (test-filtered-alert) should have fired for filterhost/filtapp/disk full"
    echo "$MAIL_RESPONSE"
    exit 1
fi
echo "PASS: Filtered alert triggered for matching host+tag+message"

# Check email sender name is "Logyard"
LOGYARD_SENDER=$(echo "$MAIL_RESPONSE" | grep -o '"Name":"Logyard"' || true)
if [ -z "$LOGYARD_SENDER" ]; then
    echo "FAIL: Expected sender name 'Logyard' in email From header"
    echo "$MAIL_RESPONSE"
    exit 1
fi
echo "PASS: Email sender name is Logyard"

# Fetch first email body to check HTML content
FIRST_MSG_ID=$(echo "$MAIL_RESPONSE" | grep -o '"ID":"[^"]*"' | head -1 | cut -d'"' -f4)
if [ -n "$FIRST_MSG_ID" ]; then
    MSG_DETAIL=$(docker exec mailpit-test wget -qO- "http://localhost:8025/api/v1/message/${FIRST_MSG_ID}")
    MSG_HTML=$(echo "$MSG_DETAIL" | grep -o '"HTML":"[^"]*"' | head -1 || true)

    # Check for HTML table (JSON response uses \u003c for <)
    if echo "$MSG_DETAIL" | grep -qE '<table|\\u003ctable'; then
        echo "PASS: Email contains HTML table"
    else
        echo "FAIL: Email does not contain HTML table"
        echo "$MSG_DETAIL"
        exit 1
    fi

    # Check for "Check out alerts at" link
    if echo "$MSG_DETAIL" | grep -q 'Check out alerts at'; then
        echo "PASS: Email contains alerts link"
    else
        echo "FAIL: Email does not contain alerts link"
        echo "$MSG_DETAIL"
        exit 1
    fi
else
    echo "WARN: Could not extract message ID for body verification"
fi

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

# Verify data attributes on log rows for bell-off ignore feature
if ! echo "$BODY" | grep -q 'data-host="rfc3164host"'; then
    echo "FAIL: Log rows missing data-host attribute"
    echo "$BODY"
    exit 1
fi
if ! echo "$BODY" | grep -q 'data-severity='; then
    echo "FAIL: Log rows missing data-severity attribute"
    echo "$BODY"
    exit 1
fi
if ! echo "$BODY" | grep -q 'action-cell'; then
    echo "FAIL: Log rows missing action-cell column"
    echo "$BODY"
    exit 1
fi
echo "PASS: Log rows contain data attributes and action cell"

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
    -d '{"smtp":{"host":"mailpit-test","port":1025,"from":"alerts@test.local","to":"admin@test.local"},"alerts":[{"name":"put-test-alert","count":5,"window_minutes":10,"level":"err","above":false}],"ignore":[],"retention":7,"debug":false,"url":""}')
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
    -d '{"smtp":{},"alerts":[{"name":"bad","count":1,"window_minutes":5,"level":"banana"}],"ignore":[],"retention":7,"debug":false,"url":""}')
if [ "$BAD_LEVEL" != "400" ]; then
    echo "FAIL: Bad level should return 400, got $BAD_LEVEL"
    exit 1
fi
echo "PASS: Bad alert level rejected"

# Bad retention
BAD_RETENTION=$(curl -s -o /dev/null -w '%{http_code}' -X PUT http://127.0.0.1:8080/api/config \
    -H 'Content-Type: application/json' \
    -d '{"smtp":{},"alerts":[],"ignore":[],"retention":0,"debug":false,"url":""}')
if [ "$BAD_RETENTION" != "400" ]; then
    echo "FAIL: Bad retention should return 400, got $BAD_RETENTION"
    exit 1
fi
echo "PASS: Bad retention rejected"

# Bad ignore level
BAD_IGNORE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT http://127.0.0.1:8080/api/config \
    -H 'Content-Type: application/json' \
    -d '{"smtp":{},"alerts":[],"ignore":[{"level":"banana"}],"retention":7,"debug":false,"url":""}')
if [ "$BAD_IGNORE" != "400" ]; then
    echo "FAIL: Bad ignore level should return 400, got $BAD_IGNORE"
    exit 1
fi
echo "PASS: Bad ignore level rejected"

# Bad regex
BAD_REGEX=$(curl -s -o /dev/null -w '%{http_code}' -X PUT http://127.0.0.1:8080/api/config \
    -H 'Content-Type: application/json' \
    -d '{"smtp":{},"alerts":[],"ignore":[{"message":"[invalid"}],"retention":7,"debug":false,"url":""}')
if [ "$BAD_REGEX" != "400" ]; then
    echo "FAIL: Bad regex should return 400, got $BAD_REGEX"
    exit 1
fi
echo "PASS: Bad ignore regex rejected"

# Bad severity rewrite: invalid new_severity
BAD_REWRITE=$(curl -s -o /dev/null -w '%{http_code}' -X PUT http://127.0.0.1:8080/api/config \
    -H 'Content-Type: application/json' \
    -d '{"smtp":{},"alerts":[],"ignore":[],"severity_rewrite":[{"tag":"test","new_severity":"banana"}],"retention":7,"debug":false,"url":""}')
if [ "$BAD_REWRITE" != "400" ]; then
    echo "FAIL: Bad rewrite new_severity should return 400, got $BAD_REWRITE"
    exit 1
fi
echo "PASS: Bad severity rewrite new_severity rejected"

# Bad severity rewrite: invalid message regex
BAD_REWRITE_REGEX=$(curl -s -o /dev/null -w '%{http_code}' -X PUT http://127.0.0.1:8080/api/config \
    -H 'Content-Type: application/json' \
    -d '{"smtp":{},"alerts":[],"ignore":[],"severity_rewrite":[{"tag":"test","message":"[invalid","new_severity":"err"}],"retention":7,"debug":false,"url":""}')
if [ "$BAD_REWRITE_REGEX" != "400" ]; then
    echo "FAIL: Bad rewrite regex should return 400, got $BAD_REWRITE_REGEX"
    exit 1
fi
echo "PASS: Bad severity rewrite regex rejected"

# Bad severity rewrite: no match fields
BAD_REWRITE_EMPTY=$(curl -s -o /dev/null -w '%{http_code}' -X PUT http://127.0.0.1:8080/api/config \
    -H 'Content-Type: application/json' \
    -d '{"smtp":{},"alerts":[],"ignore":[],"severity_rewrite":[{"new_severity":"err"}],"retention":7,"debug":false,"url":""}')
if [ "$BAD_REWRITE_EMPTY" != "400" ]; then
    echo "FAIL: Empty rewrite rule should return 400, got $BAD_REWRITE_EMPTY"
    exit 1
fi
echo "PASS: Empty severity rewrite rule rejected"

# Restore original config for clean state
curl -s -X PUT http://127.0.0.1:8080/api/config \
    -H 'Content-Type: application/json' \
    -d '{"smtp":{"host":"mailpit-test","port":1025,"from":"alerts@test.local","to":"admin@test.local"},"alerts":[{"name":"test-warning-alert","count":1,"window_minutes":5,"level":"warning"}],"ignore":[],"retention":1,"debug":true,"url":""}' > /dev/null

echo ""
echo "=== All tests passed ==="
