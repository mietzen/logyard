#!/usr/bin/env bash
# Syslog message generator for local development.
# Sends a mix of RFC 3164 (UDP) and RFC 5424 (TCP) syslog messages to logyard
# so you can test the web UI, alert rules, ignore rules, and digest batching.
#
# Pattern: steady stream of info/notice messages with periodic bursts
# of errors and warnings to trigger alert rules reliably.

set -euo pipefail

LOGYARD_HOST="logyard"
LOGYARD_PORT="514"

# Wait for logyard to be ready
echo "Waiting for logyard to be ready..."
sleep 5

send_rfc3164() {
    # RFC 3164 via UDP: <PRI>TIMESTAMP HOST TAG: MSG
    echo "$1" > /dev/udp/$LOGYARD_HOST/$LOGYARD_PORT 2>/dev/null || true
}

send_rfc5424() {
    # RFC 5424 via TCP: <PRI>1 TIMESTAMP HOST APP PID MSGID - MSG
    echo "$1" > /dev/tcp/$LOGYARD_HOST/$LOGYARD_PORT 2>/dev/null || true
}

HOSTS=("web1" "web2" "db1" "db2" "api1" "worker1")
TAGS=("nginx" "myapp" "postgres" "redis" "sshd" "cron" "healthcheck")

INFO_MESSAGES=(
    "request completed in 42ms"
    "user login successful uid=1234"
    "cache hit ratio: 95%"
    "connection pool size: 10/50"
    "scheduled backup started"
    "routine maintenance completed"
    "health check passed"
    "GET /api/status 200 12ms"
    "POST /api/login 200 89ms"
    "worker job completed id=5678"
)

WARNING_MESSAGES=(
    "high memory usage: 85%"
    "connection pool reaching limit: 45/50"
    "slow query detected: 2.3s"
    "certificate expires in 7 days"
    "retry attempt 2/3 for upstream"
)

ERR_MESSAGES=(
    "connection refused to upstream"
    "disk nearly full: /var/log 92%"
    "out of memory: killed process 1234"
    "ERROR unhandled exception in request handler"
    "FATAL database connection lost"
    "no space left on device"
)

LONG_MESSAGES=(
    "ERROR: unhandled exception in module auth.middleware at line 847: TypeError: Cannot read properties of undefined (reading 'session') | stack: at AuthMiddleware.handle (/app/src/middleware/auth.js:847:23) at Layer.handle (/app/node_modules/express/lib/router/layer.js:95:5) at next (/app/node_modules/express/lib/router/route.js:144:13) at Route.dispatch (/app/node_modules/express/lib/router/route.js:114:3) at Layer.handle (/app/node_modules/express/lib/router/layer.js:95:5) | request_id=a1b2c3d4-e5f6-7890-abcd-ef1234567890 user_agent=Mozilla/5.0 ip=10.0.45.12"
    "WARN slow query detected: SELECT u.id, u.name, u.email, o.id, o.total, o.status, p.name, p.price, oi.quantity FROM users u JOIN orders o ON u.id = o.user_id JOIN order_items oi ON o.id = oi.order_id JOIN products p ON oi.product_id = p.id WHERE o.created_at > '2026-01-01' AND o.status IN ('pending', 'processing', 'shipped') AND u.active = true ORDER BY o.created_at DESC LIMIT 1000 -- execution_time=12847ms rows_examined=2847593 rows_sent=1000 lock_time=234ms tmp_tables=3"
    "INFO [deployment] Rolling update completed for service api-gateway: replicas 5/5 ready | previous_version=v2.14.3 new_version=v2.15.0 strategy=rolling max_unavailable=1 max_surge=2 | health_checks: pod/api-gateway-7d8f9b6c4-xk2nm=healthy pod/api-gateway-7d8f9b6c4-j9m3p=healthy pod/api-gateway-7d8f9b6c4-q4r7t=healthy pod/api-gateway-7d8f9b6c4-w2v5x=healthy pod/api-gateway-7d8f9b6c4-a8b1c=healthy | total_duration=4m32s zero_downtime=true"
    "FATAL OOM killer invoked: process postgres (pid=28471) killed, score=847, total_vm=8294612kB, rss=6182940kB, pgtables=12108kB, oom_score_adj=0 | system memory: total=16384MB used=15987MB free=42MB available=397MB swap_total=0MB swap_used=0MB | cgroup: memory.limit_in_bytes=8589934592 memory.usage_in_bytes=8589934590 memory.max_usage_in_bytes=8589934592"
)

CRIT_MESSAGES=(
    "CRITICAL: primary database unreachable"
    "system panic: kernel stack overflow"
)

# Syslog priority = facility * 8 + severity
# facility 1 = user, facility 3 = daemon
# severity: 0=emerg, 1=alert, 2=crit, 3=err, 4=warning, 5=notice, 6=info
PRI_INFO=14      # user(1)*8 + info(6)
PRI_WARNING=12   # user(1)*8 + warning(4)
PRI_ERR=11       # user(1)*8 + err(3)
PRI_CRIT=10      # user(1)*8 + crit(2)
PRI_NOTICE=13    # user(1)*8 + notice(5)

# Daemon facility variants for RFC 5424 messages
PRI_DAEMON_INFO=30    # daemon(3)*8 + info(6)
PRI_DAEMON_ERR=27     # daemon(3)*8 + err(3)
PRI_DAEMON_WARNING=28 # daemon(3)*8 + warning(4)

COUNTER=0
BURST_COUNTER=0

echo "Starting syslog message generation..."

while true; do
    COUNTER=$((COUNTER + 1))
    BURST_COUNTER=$((BURST_COUNTER + 1))
    HOST=${HOSTS[$((RANDOM % ${#HOSTS[@]}))]}
    TAG=${TAGS[$((RANDOM % ${#TAGS[@]}))]}

    # Every 30 messages (~30s), send a burst of errors/warnings to trigger alerts
    if [ $BURST_COUNTER -ge 30 ]; then
        BURST_COUNTER=0
        echo "[generator] Sending error/warning burst at message $COUNTER..."

        # Burst of 4-6 errors (triggers high-error-rate rule which needs 3)
        # Mix of RFC 3164 and RFC 5424
        BURST_SIZE=$((4 + RANDOM % 3))
        for i in $(seq 1 $BURST_SIZE); do
            BHOST=${HOSTS[$((RANDOM % ${#HOSTS[@]}))]}
            BMSG=${ERR_MESSAGES[$((RANDOM % ${#ERR_MESSAGES[@]}))]}
            if [ $((i % 2)) -eq 0 ]; then
                # RFC 5424 via TCP (with local timezone)
                BTS=$(date '+%Y-%m-%dT%H:%M:%S%:z')
                send_rfc5424 "<${PRI_ERR}>1 ${BTS} ${BHOST} myapp - - - ${BMSG}"
            else
                # RFC 3164 via UDP
                BTS=$(date '+%b %d %H:%M:%S')
                send_rfc3164 "<${PRI_ERR}>${BTS} ${BHOST} myapp: ${BMSG}"
            fi
            sleep 0.2
        done

        # Burst of sshd warnings (triggers auth-failures rule which needs 5)
        for i in $(seq 1 6); do
            BHOST=${HOSTS[$((RANDOM % ${#HOSTS[@]}))]}
            if [ $((i % 2)) -eq 0 ]; then
                BTS=$(date '+%Y-%m-%dT%H:%M:%S%:z')
                send_rfc5424 "<${PRI_WARNING}>1 ${BTS} ${BHOST} sshd - - - failed password for user admin from 10.0.0.$((RANDOM % 255))"
            else
                BTS=$(date '+%b %d %H:%M:%S')
                send_rfc3164 "<${PRI_WARNING}>${BTS} ${BHOST} sshd: failed password for user admin from 10.0.0.$((RANDOM % 255))"
            fi
            sleep 0.2
        done

        # Occasional critical (triggers critical-failures rule which needs 1)
        if [ $((RANDOM % 3)) -eq 0 ]; then
            BTS=$(date '+%Y-%m-%dT%H:%M:%S%:z')
            BHOST=${HOSTS[$((RANDOM % ${#HOSTS[@]}))]}
            BMSG=${CRIT_MESSAGES[$((RANDOM % ${#CRIT_MESSAGES[@]}))]}
            send_rfc5424 "<${PRI_CRIT}>1 ${BTS} ${BHOST} postgres - - - ${BMSG}"
        fi

        continue
    fi

    # Every 10 messages, send a long message
    if [ $((COUNTER % 10)) -eq 0 ]; then
        LMSG=${LONG_MESSAGES[$((RANDOM % ${#LONG_MESSAGES[@]}))]}
        LTS=$(date '+%b %d %H:%M:%S')
        LHOST=${HOSTS[$((RANDOM % ${#HOSTS[@]}))]}
        LTAG=${TAGS[$((RANDOM % ${#TAGS[@]}))]}
        send_rfc3164 "<${PRI_ERR}>${LTS} ${LHOST} ${LTAG}: ${LMSG}"
        sleep 1
        continue
    fi

    # Normal traffic: mostly info and notice, mix of RFC 3164 and RFC 5424
    ROLL=$((RANDOM % 100))

    if [ $ROLL -lt 60 ]; then
        MSG=${INFO_MESSAGES[$((RANDOM % ${#INFO_MESSAGES[@]}))]}
        PRI=$PRI_INFO
    elif [ $ROLL -lt 85 ]; then
        MSG="process $((RANDOM % 9999)) status normal"
        PRI=$PRI_NOTICE
    elif [ $ROLL -lt 95 ]; then
        MSG=${WARNING_MESSAGES[$((RANDOM % ${#WARNING_MESSAGES[@]}))]}
        PRI=$PRI_WARNING
    elif [ $ROLL -lt 99 ]; then
        MSG=${ERR_MESSAGES[$((RANDOM % ${#ERR_MESSAGES[@]}))]}
        PRI=$PRI_ERR
    else
        MSG=${CRIT_MESSAGES[$((RANDOM % ${#CRIT_MESSAGES[@]}))]}
        PRI=$PRI_CRIT
    fi

    # Alternate between RFC 3164 (UDP) and RFC 5424 (TCP)
    if [ $((COUNTER % 3)) -eq 0 ]; then
        # RFC 5424 via TCP (~33% of messages) — has proper timezone
        TS=$(date '+%Y-%m-%dT%H:%M:%S%:z')
        send_rfc5424 "<${PRI}>1 ${TS} ${HOST} ${TAG} - - - ${MSG}"
    else
        # RFC 3164 via UDP (~67% of messages)
        TS=$(date '+%b %d %H:%M:%S')
        send_rfc3164 "<${PRI}>${TS} ${HOST} ${TAG}: ${MSG}"
    fi

    # Every 50 messages, print a status line
    if [ $((COUNTER % 50)) -eq 0 ]; then
        echo "[generator] Sent $COUNTER messages so far..."
    fi

    # Send 1 message per second
    sleep 1
done
