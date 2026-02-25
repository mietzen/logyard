package main

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

func TestBuildAlertBody(t *testing.T) {
	rule := AlertRule{Name: "test-alert", Level: "err", Count: 5, WindowMinutes: 10}
	logs := []LogEntry{
		{ID: 1, Timestamp: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC), Host: "web1", Facility: "daemon", Severity: "err", Tag: "nginx", Message: "disk full"},
		{ID: 2, Timestamp: time.Date(2025, 1, 15, 10, 31, 0, 0, time.UTC), Host: "web2", Facility: "kern", Severity: "err", Tag: "app", Message: "out of memory"},
	}

	body := buildAlertBody(rule, 7, logs, "http://logyard:8080")

	// Check HTML structure
	if !strings.Contains(body, "<html>") {
		t.Error("expected HTML body")
	}
	if !strings.Contains(body, "<table") {
		t.Error("expected HTML table")
	}

	// Check alert summary
	if !strings.Contains(body, "test-alert") {
		t.Error("expected rule name in body")
	}
	if !strings.Contains(body, "<b>7</b> err messages") {
		t.Error("expected count and level in body")
	}

	// Check table headers
	for _, col := range []string{"Timestamp", "Host", "Facility", "Severity", "Tag", "Message"} {
		if !strings.Contains(body, "<th>"+col+"</th>") {
			t.Errorf("expected column header %q", col)
		}
	}

	// Check log entries in table
	if !strings.Contains(body, "web1") || !strings.Contains(body, "disk full") {
		t.Error("expected first log entry in table")
	}
	if !strings.Contains(body, "web2") || !strings.Contains(body, "out of memory") {
		t.Error("expected second log entry in table")
	}

	// Check URL link
	if !strings.Contains(body, "http://logyard:8080") {
		t.Error("expected URL in body")
	}
	if !strings.Contains(body, "Check out alerts at") {
		t.Error("expected link text")
	}
	if !strings.Contains(body, `<a href="http://logyard:8080">`) {
		t.Error("expected clickable link")
	}

	// Check "showing X of Y" when count > len(logs)
	if !strings.Contains(body, "Showing 2 of 7") {
		t.Error("expected 'Showing 2 of 7' note")
	}
}

func TestBuildAlertBody_NoLogs(t *testing.T) {
	rule := AlertRule{Name: "empty", Level: "err", Count: 1, WindowMinutes: 5}
	body := buildAlertBody(rule, 3, nil, "")

	if !strings.Contains(body, "empty") {
		t.Error("expected rule name")
	}
	if strings.Contains(body, "<table") {
		t.Error("expected no table when no logs")
	}
	if strings.Contains(body, "Check out alerts at") {
		t.Error("expected no URL link when URL is empty")
	}
}

func TestBuildAlertBody_ExactCount(t *testing.T) {
	rule := AlertRule{Name: "exact", Level: "err", Count: 1, WindowMinutes: 5}
	logs := []LogEntry{
		{ID: 1, Timestamp: time.Now(), Host: "h", Facility: "f", Severity: "err", Tag: "t", Message: "m"},
	}
	body := buildAlertBody(rule, 1, logs, "http://example.com")

	// When count == len(logs), no "Showing X of Y" note
	if strings.Contains(body, "Showing") {
		t.Error("should not show 'Showing X of Y' when all logs are shown")
	}
}

func TestBuildAlertBody_HTMLEscape(t *testing.T) {
	rule := AlertRule{Name: "xss<test>", Level: "err", Count: 1, WindowMinutes: 5}
	logs := []LogEntry{
		{ID: 1, Timestamp: time.Now(), Host: "h", Facility: "f", Severity: "err", Tag: "t", Message: "<script>alert('xss')</script>"},
	}
	body := buildAlertBody(rule, 1, logs, "http://example.com?a=1&b=2")

	if strings.Contains(body, "<script>") {
		t.Error("expected HTML-escaped message content")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("expected escaped script tag")
	}
	if strings.Contains(body, "xss<test>") {
		t.Error("expected HTML-escaped rule name")
	}
}

// mockSMTPServer starts a minimal SMTP server that captures the email message.
func mockSMTPServer(t *testing.T) (addr string, msgCh chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	msgCh = make(chan string, 1)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		defer ln.Close()

		writer := bufio.NewWriter(conn)
		reader := bufio.NewReader(conn)

		write := func(s string) {
			writer.WriteString(s + "\r\n")
			writer.Flush()
		}

		readLine := func() string {
			line, _ := reader.ReadString('\n')
			return strings.TrimSpace(line)
		}

		write("220 mock SMTP")
		for {
			line := readLine()
			if strings.HasPrefix(line, "EHLO") || strings.HasPrefix(line, "HELO") {
				write("250 OK")
			} else if strings.HasPrefix(line, "MAIL FROM:") {
				write("250 OK")
			} else if strings.HasPrefix(line, "RCPT TO:") {
				write("250 OK")
			} else if line == "DATA" {
				write("354 Go ahead")
				var msg strings.Builder
				for {
					dataLine := readLine()
					if dataLine == "." {
						break
					}
					msg.WriteString(dataLine + "\n")
				}
				write("250 OK")
				msgCh <- msg.String()
			} else if line == "QUIT" {
				write("221 Bye")
				return
			} else {
				write("500 Unknown")
			}
		}
	}()

	return ln.Addr().String(), msgCh
}

func TestSendAlert_EmailFormat(t *testing.T) {
	addr, msgCh := mockSMTPServer(t)
	parts := strings.SplitN(addr, ":", 2)
	host := parts[0]
	port := 0
	for _, c := range parts[1] {
		port = port*10 + int(c-'0')
	}

	cfg := SMTPConfig{
		Host: host,
		Port: port,
		From: "alerts@test.local",
		To:   "admin@test.local",
	}
	rule := AlertRule{Name: "test-email", Level: "err", Count: 1, WindowMinutes: 5}
	logs := []LogEntry{
		{ID: 1, Timestamp: time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC), Host: "srv1", Facility: "daemon", Severity: "err", Tag: "myapp", Message: "something broke"},
	}

	err := sendAlert(cfg, rule, 3, logs, "http://logyard.example.com")
	if err != nil {
		t.Fatalf("sendAlert: %v", err)
	}

	select {
	case msg := <-msgCh:
		// Verify From header has Logyard sender name
		if !strings.Contains(msg, "From: Logyard <alerts@test.local>") {
			t.Error("expected 'Logyard' sender name in From header")
		}

		// Verify HTML content type
		if !strings.Contains(msg, "Content-Type: text/html; charset=utf-8") {
			t.Error("expected text/html content type")
		}

		// Verify HTML table with log entry
		if !strings.Contains(msg, "<table") {
			t.Error("expected HTML table in body")
		}
		if !strings.Contains(msg, "srv1") {
			t.Error("expected log host in table")
		}
		if !strings.Contains(msg, "something broke") {
			t.Error("expected log message in table")
		}

		// Verify URL link
		if !strings.Contains(msg, "http://logyard.example.com") {
			t.Error("expected URL in body")
		}
		if !strings.Contains(msg, "Check out alerts at") {
			t.Error("expected link text")
		}

	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for email")
	}
}

func TestBuildDigestBody(t *testing.T) {
	items := []DigestItem{
		{
			Rule:  AlertRule{Name: "rule-one", Level: "err", Count: 5, WindowMinutes: 10},
			Count: 7,
			Logs: []LogEntry{
				{ID: 1, Timestamp: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC), Host: "web1", Facility: "daemon", Severity: "err", Tag: "nginx", Message: "disk full"},
			},
		},
		{
			Rule:  AlertRule{Name: "rule-two", Level: "warning", Count: 3, WindowMinutes: 5},
			Count: 3,
			Logs: []LogEntry{
				{ID: 2, Timestamp: time.Date(2025, 1, 15, 10, 31, 0, 0, time.UTC), Host: "web2", Facility: "kern", Severity: "warning", Tag: "app", Message: "high load"},
			},
		},
	}

	body := buildDigestBody(items, "http://logyard:8080", 5*time.Minute, 15*time.Minute)

	if !strings.Contains(body, "<h2>Logyard Alert Digest</h2>") {
		t.Error("expected digest header")
	}
	if !strings.Contains(body, "2 alert rule(s) triggered") {
		t.Error("expected rule count summary")
	}
	if !strings.Contains(body, "5m0s") {
		t.Error("expected current window in body")
	}
	if !strings.Contains(body, "15m0s") {
		t.Error("expected next window in body")
	}
	if !strings.Contains(body, "<h3>rule-one</h3>") {
		t.Error("expected rule-one heading")
	}
	if !strings.Contains(body, "<h3>rule-two</h3>") {
		t.Error("expected rule-two heading")
	}
	if !strings.Contains(body, "disk full") {
		t.Error("expected rule-one log entry")
	}
	if !strings.Contains(body, "high load") {
		t.Error("expected rule-two log entry")
	}
	if !strings.Contains(body, "Showing 1 of 7") {
		t.Error("expected 'Showing 1 of 7' note for rule-one")
	}
	if !strings.Contains(body, "http://logyard:8080") {
		t.Error("expected URL in body")
	}
}

func TestBuildDigestBody_HTMLEscape(t *testing.T) {
	items := []DigestItem{
		{
			Rule:  AlertRule{Name: "xss<test>", Level: "err", Count: 1, WindowMinutes: 5},
			Count: 1,
			Logs: []LogEntry{
				{ID: 1, Timestamp: time.Now(), Host: "h", Facility: "f", Severity: "err", Tag: "t", Message: "<script>alert('xss')</script>"},
			},
		},
	}

	body := buildDigestBody(items, "", 5*time.Minute, 15*time.Minute)

	if strings.Contains(body, "<script>") {
		t.Error("expected HTML-escaped message content")
	}
	if strings.Contains(body, "xss<test>") {
		t.Error("expected HTML-escaped rule name")
	}
}

func TestDeduplicateItems(t *testing.T) {
	items := []DigestItem{
		{Rule: AlertRule{Name: "rule-a"}, Count: 5},
		{Rule: AlertRule{Name: "rule-b"}, Count: 3},
		{Rule: AlertRule{Name: "rule-a"}, Count: 10},
		{Rule: AlertRule{Name: "rule-b"}, Count: 1},
	}

	result := deduplicateItems(items)

	if len(result) != 2 {
		t.Fatalf("expected 2 items, got %d", len(result))
	}
	for _, item := range result {
		switch item.Rule.Name {
		case "rule-a":
			if item.Count != 10 {
				t.Errorf("rule-a: expected count 10, got %d", item.Count)
			}
		case "rule-b":
			if item.Count != 3 {
				t.Errorf("rule-b: expected count 3, got %d", item.Count)
			}
		default:
			t.Errorf("unexpected rule: %s", item.Rule.Name)
		}
	}
}

func TestDigestState_EscalationAndReset(t *testing.T) {
	ds := &DigestState{currentWindow: 10 * time.Second}
	cfg := DigestConfig{
		Enabled:    true,
		Initial:    "10s",
		Multiplier: 2,
		Max:        "60s",
		Cooldown:   "30s",
	}

	// First escalation: 10s * 2 = 20s
	ds.currentWindow = ds.nextWindow(cfg)
	if ds.currentWindow != 20*time.Second {
		t.Errorf("expected 20s, got %s", ds.currentWindow)
	}

	// Second escalation: 20s * 2 = 40s
	ds.currentWindow = ds.nextWindow(cfg)
	if ds.currentWindow != 40*time.Second {
		t.Errorf("expected 40s, got %s", ds.currentWindow)
	}

	// Third escalation: 40s * 2 = 80s, capped at 60s
	ds.currentWindow = ds.nextWindow(cfg)
	if ds.currentWindow != 60*time.Second {
		t.Errorf("expected 60s (capped), got %s", ds.currentWindow)
	}

	// Further escalation stays at cap
	ds.currentWindow = ds.nextWindow(cfg)
	if ds.currentWindow != 60*time.Second {
		t.Errorf("expected 60s (still capped), got %s", ds.currentWindow)
	}
}

func TestDigestState_Add(t *testing.T) {
	ds := &DigestState{currentWindow: 10 * time.Second}

	ds.Add(DigestItem{Rule: AlertRule{Name: "r1"}, Count: 1})
	ds.Add(DigestItem{Rule: AlertRule{Name: "r2"}, Count: 2})

	ds.mu.Lock()
	defer ds.mu.Unlock()

	if len(ds.pending) != 2 {
		t.Fatalf("expected 2 pending items, got %d", len(ds.pending))
	}
	if ds.lastActivityAt.IsZero() {
		t.Error("expected lastActivityAt to be set")
	}
}

func TestSendDigest_EmailFormat(t *testing.T) {
	addr, msgCh := mockSMTPServer(t)
	parts := strings.SplitN(addr, ":", 2)
	host := parts[0]
	port := 0
	for _, c := range parts[1] {
		port = port*10 + int(c-'0')
	}

	cfg := SMTPConfig{
		Host: host,
		Port: port,
		From: "alerts@test.local",
		To:   "admin@test.local",
	}
	items := []DigestItem{
		{
			Rule:  AlertRule{Name: "test-digest-rule", Level: "err", Count: 1, WindowMinutes: 5},
			Count: 3,
			Logs: []LogEntry{
				{ID: 1, Timestamp: time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC), Host: "srv1", Facility: "daemon", Severity: "err", Tag: "myapp", Message: "something broke"},
			},
		},
	}

	err := sendDigest(cfg, items, "http://logyard.example.com", 5*time.Minute, 15*time.Minute)
	if err != nil {
		t.Fatalf("sendDigest: %v", err)
	}

	select {
	case msg := <-msgCh:
		if !strings.Contains(msg, "Alert Digest: 1 rule(s) triggered") {
			t.Error("expected digest subject")
		}
		if !strings.Contains(msg, "test-digest-rule") {
			t.Error("expected rule name in body")
		}
		if !strings.Contains(msg, "something broke") {
			t.Error("expected log message in body")
		}
		if !strings.Contains(msg, "http://logyard.example.com") {
			t.Error("expected URL in body")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for digest email")
	}
}
