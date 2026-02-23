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
