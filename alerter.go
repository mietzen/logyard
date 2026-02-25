package main

import (
	"crypto/tls"
	"database/sql"
	"fmt"
	"html"
	"log"
	"math"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

type DigestItem struct {
	Rule  AlertRule
	Count int
	Logs  []LogEntry
}

type DigestState struct {
	mu             sync.Mutex
	pending        []DigestItem
	currentWindow  time.Duration
	lastActivityAt time.Time
}

func StartAlerter(cm *ConfigManager, db *sql.DB, alertInterval time.Duration) {
	cfg := cm.Get()
	if len(cfg.Alerts) == 0 {
		log.Println("No alert rules configured, alerter disabled")
	}

	// Always create DigestState so digest can be enabled via config hot-reload.
	var initialWindow time.Duration
	if cfg.Digest.Enabled {
		initialWindow, _ = parseDuration(cfg.Digest.Initial)
	}
	if initialWindow == 0 {
		initialWindow = 5 * time.Second // short poll interval until digest is configured
	}
	ds := &DigestState{currentWindow: initialWindow}
	go ds.runFlushLoop(cm)

	if cfg.Digest.Enabled {
		log.Printf("Digest mode enabled: initial=%s, multiplier=%.1f, max=%s, cooldown=%s",
			cfg.Digest.Initial, cfg.Digest.Multiplier, cfg.Digest.Max, cfg.Digest.Cooldown)
	}

	ticker := time.NewTicker(alertInterval)
	go func() {
		for range ticker.C {
			c := cm.Get()
			if c.Digest.Enabled {
				evaluateAlertsDigest(c, db, ds)
			} else {
				evaluateAlerts(c, db)
			}
			purgeOldLogs(c.Retention, db)
		}
	}()

	log.Printf("Alerter started, checking every %s", alertInterval)
}

func evaluateAlerts(cfg Config, db *sql.DB) {
	for _, rule := range cfg.Alerts {
		since := time.Now().Add(-time.Duration(rule.WindowMinutes) * time.Minute)

		if len(cfg.Ignore) > 0 {
			debugf("alert %q: applying %d ignore rule(s)", rule.Name, len(cfg.Ignore))
		}

		count, err := CountMatchingLogs(db, rule, cfg.Ignore, since)
		if err != nil {
			log.Printf("alert query error for %q: %v", rule.Name, err)
			continue
		}

		debugf("alert %q: %d matching messages (threshold: %d)", rule.Name, count, rule.Count)

		if count < rule.Count {
			continue
		}

		lastAlerted, err := GetLastAlerted(db, rule.Name)
		if err != nil {
			log.Printf("alert state error for %q: %v", rule.Name, err)
			continue
		}

		// Cooldown: don't re-alert if we already alerted within the window
		if !lastAlerted.IsZero() && lastAlerted.After(since) {
			debugf("alert %q: skipping, already alerted at %s", rule.Name, lastAlerted.Format(time.RFC3339))
			continue
		}

		logs, err := FetchMatchingLogs(db, rule, cfg.Ignore, since, 50)
		if err != nil {
			log.Printf("alert fetch logs error for %q: %v", rule.Name, err)
		}

		debugf("alert %q: sending email to %s", rule.Name, cfg.SMTP.To)

		if err := sendAlert(cfg.SMTP, rule, count, logs, cfg.URL); err != nil {
			log.Printf("failed to send alert %q: %v", rule.Name, err)
			continue
		}

		if err := SetLastAlerted(db, rule.Name, time.Now()); err != nil {
			log.Printf("failed to update alert state for %q: %v", rule.Name, err)
		}

		log.Printf("Alert %q triggered: %d %s messages in last %d minutes",
			rule.Name, count, rule.Level, rule.WindowMinutes)
	}
}

func purgeOldLogs(retentionDays int, db *sql.DB) {
	deleted, err := PurgeLogs(db, retentionDays)
	if err != nil {
		log.Printf("purge error: %v", err)
		return
	}
	if deleted > 0 {
		log.Printf("Purged %d old log entries", deleted)
	}
}

func sendAlert(cfg SMTPConfig, rule AlertRule, count int, logs []LogEntry, url string) error {
	subject := fmt.Sprintf("[Logyard] Alert: %s", rule.Name)
	body := buildAlertBody(rule, count, logs, url)
	return smtpSend(cfg, subject, body)
}

func smtpSend(cfg SMTPConfig, subject, body string) error {
	if cfg.Host == "" {
		return fmt.Errorf("SMTP not configured")
	}

	msg := strings.Join([]string{
		fmt.Sprintf("From: Logyard <%s>", cfg.From),
		"To: " + cfg.To,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=utf-8",
		"",
		body,
	}, "\r\n")

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()

	// Try STARTTLS if available, skip certificate verification
	// so self-signed certs (e.g. mailpit) work.
	if ok, _ := client.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: true}
		if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}

	if cfg.User != "" {
		auth := smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}

	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}
	if err := client.Rcpt(cfg.To); err != nil {
		return fmt.Errorf("rcpt to: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close data: %w", err)
	}

	return client.Quit()
}

func buildAlertBody(rule AlertRule, count int, logs []LogEntry, url string) string {
	var b strings.Builder
	b.WriteString("<html><body style=\"font-family:sans-serif;font-size:14px;color:#222\">")
	b.WriteString(fmt.Sprintf("<p>Alert rule <b>%s</b> triggered.</p>", html.EscapeString(rule.Name)))
	b.WriteString(fmt.Sprintf("<p><b>%d</b> %s messages in the last %d minutes (threshold: %d).</p>",
		count, html.EscapeString(rule.Level), rule.WindowMinutes, rule.Count))

	if len(logs) > 0 {
		b.WriteString("<table border=\"1\" cellpadding=\"4\" cellspacing=\"0\" style=\"border-collapse:collapse;font-size:13px\">")
		b.WriteString("<tr style=\"background:#f0f0f0\">")
		b.WriteString("<th>Timestamp</th><th>Host</th><th>Facility</th><th>Severity</th><th>Tag</th><th>Message</th>")
		b.WriteString("</tr>")
		for _, e := range logs {
			b.WriteString("<tr>")
			b.WriteString(fmt.Sprintf("<td style=\"white-space:nowrap\">%s</td>", html.EscapeString(e.Timestamp.Format("2006-01-02 15:04:05"))))
			b.WriteString(fmt.Sprintf("<td>%s</td>", html.EscapeString(e.Host)))
			b.WriteString(fmt.Sprintf("<td>%s</td>", html.EscapeString(e.Facility)))
			b.WriteString(fmt.Sprintf("<td>%s</td>", html.EscapeString(e.Severity)))
			b.WriteString(fmt.Sprintf("<td>%s</td>", html.EscapeString(e.Tag)))
			b.WriteString(fmt.Sprintf("<td>%s</td>", html.EscapeString(e.Message)))
			b.WriteString("</tr>")
		}
		b.WriteString("</table>")
		if count > len(logs) {
			b.WriteString(fmt.Sprintf("<p><i>Showing %d of %d matching messages.</i></p>", len(logs), count))
		}
	}

	if url != "" {
		b.WriteString(fmt.Sprintf("<p>Check out alerts at: <a href=\"%s\">%s</a></p>",
			html.EscapeString(url), html.EscapeString(url)))
	}

	b.WriteString("</body></html>")
	return b.String()
}

func (ds *DigestState) Add(item DigestItem) {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.pending = append(ds.pending, item)
	ds.lastActivityAt = time.Now()
}

func (ds *DigestState) nextWindow(cfg DigestConfig) time.Duration {
	max, _ := parseDuration(cfg.Max)
	next := time.Duration(math.Round(float64(ds.currentWindow) * cfg.Multiplier))
	if next > max {
		next = max
	}
	return next
}

func (ds *DigestState) runFlushLoop(cm *ConfigManager) {
	ds.mu.Lock()
	timer := time.NewTimer(ds.currentWindow)
	ds.mu.Unlock()

	for {
		<-timer.C

		cfg := cm.Get()

		// If digest is not enabled, just poll periodically in case it gets enabled.
		if !cfg.Digest.Enabled {
			// Drain any pending items (shouldn't happen, but be safe).
			ds.mu.Lock()
			ds.pending = nil
			// Reset window so when digest gets enabled it starts fresh.
			if initial, err := parseDuration(cfg.Digest.Initial); err == nil && initial > 0 {
				ds.currentWindow = initial
			}
			ds.mu.Unlock()
			timer.Reset(5 * time.Second)
			continue
		}

		ds.mu.Lock()
		items := ds.pending
		ds.pending = nil

		if len(items) > 0 {
			ds.mu.Unlock()

			items = deduplicateItems(items)
			window := ds.currentWindow
			nextWindow := ds.nextWindow(cfg.Digest)
			if err := sendDigest(cfg.SMTP, items, cfg.URL, window, nextWindow); err != nil {
				log.Printf("failed to send digest: %v", err)
				ds.mu.Lock()
				ds.pending = append(items, ds.pending...)
				ds.mu.Unlock()
			} else {
				ds.mu.Lock()
				ds.currentWindow = ds.nextWindow(cfg.Digest)
				ds.mu.Unlock()
				log.Printf("Digest sent with %d alert(s), next window: %s", len(items), ds.currentWindow)
			}
		} else {
			cooldown, _ := parseDuration(cfg.Digest.Cooldown)
			if !ds.lastActivityAt.IsZero() && time.Since(ds.lastActivityAt) > cooldown {
				initial, _ := parseDuration(cfg.Digest.Initial)
				ds.currentWindow = initial
				debugf("Digest window quiet, reset to initial %s", initial)
			}
			ds.mu.Unlock()
		}

		ds.mu.Lock()
		timer.Reset(ds.currentWindow)
		ds.mu.Unlock()
	}
}

func evaluateAlertsDigest(cfg Config, db *sql.DB, ds *DigestState) {
	for _, rule := range cfg.Alerts {
		since := time.Now().Add(-time.Duration(rule.WindowMinutes) * time.Minute)

		if len(cfg.Ignore) > 0 {
			debugf("alert %q: applying %d ignore rule(s)", rule.Name, len(cfg.Ignore))
		}

		count, err := CountMatchingLogs(db, rule, cfg.Ignore, since)
		if err != nil {
			log.Printf("alert query error for %q: %v", rule.Name, err)
			continue
		}

		debugf("alert %q: %d matching messages (threshold: %d)", rule.Name, count, rule.Count)

		if count < rule.Count {
			continue
		}

		lastAlerted, err := GetLastAlerted(db, rule.Name)
		if err != nil {
			log.Printf("alert state error for %q: %v", rule.Name, err)
			continue
		}

		if !lastAlerted.IsZero() && lastAlerted.After(since) {
			debugf("alert %q: skipping, already alerted at %s", rule.Name, lastAlerted.Format(time.RFC3339))
			continue
		}

		logs, err := FetchMatchingLogs(db, rule, cfg.Ignore, since, 50)
		if err != nil {
			log.Printf("alert fetch logs error for %q: %v", rule.Name, err)
		}

		ds.Add(DigestItem{Rule: rule, Count: count, Logs: logs})

		if err := SetLastAlerted(db, rule.Name, time.Now()); err != nil {
			log.Printf("failed to update alert state for %q: %v", rule.Name, err)
		}

		log.Printf("Alert %q triggered (queued for digest): %d %s messages in last %d minutes",
			rule.Name, count, rule.Level, rule.WindowMinutes)
	}
}

func deduplicateItems(items []DigestItem) []DigestItem {
	seen := make(map[string]int)
	var result []DigestItem
	for _, item := range items {
		if idx, ok := seen[item.Rule.Name]; ok {
			if item.Count > result[idx].Count {
				result[idx] = item
			}
		} else {
			seen[item.Rule.Name] = len(result)
			result = append(result, item)
		}
	}
	return result
}

func sendDigest(cfg SMTPConfig, items []DigestItem, url string, window, nextWindow time.Duration) error {
	subject := fmt.Sprintf("[Logyard] Alert Digest: %d rule(s) triggered", len(items))
	body := buildDigestBody(items, url, window, nextWindow)
	return smtpSend(cfg, subject, body)
}

func buildDigestBody(items []DigestItem, url string, window, nextWindow time.Duration) string {
	var b strings.Builder
	b.WriteString("<html><body style=\"font-family:sans-serif;font-size:14px;color:#222\">")
	b.WriteString(fmt.Sprintf("<h2>Logyard Alert Digest</h2>"))
	b.WriteString(fmt.Sprintf("<p>%d alert rule(s) triggered in this digest window (%s). Next digest window: %s.</p>", len(items), window, nextWindow))

	for _, item := range items {
		b.WriteString(fmt.Sprintf("<h3>%s</h3>", html.EscapeString(item.Rule.Name)))
		b.WriteString(fmt.Sprintf("<p><b>%d</b> %s messages in the last %d minutes (threshold: %d).</p>",
			item.Count, html.EscapeString(item.Rule.Level), item.Rule.WindowMinutes, item.Rule.Count))

		if len(item.Logs) > 0 {
			b.WriteString("<table border=\"1\" cellpadding=\"4\" cellspacing=\"0\" style=\"border-collapse:collapse;font-size:13px\">")
			b.WriteString("<tr style=\"background:#f0f0f0\">")
			b.WriteString("<th>Timestamp</th><th>Host</th><th>Facility</th><th>Severity</th><th>Tag</th><th>Message</th>")
			b.WriteString("</tr>")
			for _, e := range item.Logs {
				b.WriteString("<tr>")
				b.WriteString(fmt.Sprintf("<td style=\"white-space:nowrap\">%s</td>", html.EscapeString(e.Timestamp.Format("2006-01-02 15:04:05"))))
				b.WriteString(fmt.Sprintf("<td>%s</td>", html.EscapeString(e.Host)))
				b.WriteString(fmt.Sprintf("<td>%s</td>", html.EscapeString(e.Facility)))
				b.WriteString(fmt.Sprintf("<td>%s</td>", html.EscapeString(e.Severity)))
				b.WriteString(fmt.Sprintf("<td>%s</td>", html.EscapeString(e.Tag)))
				b.WriteString(fmt.Sprintf("<td>%s</td>", html.EscapeString(e.Message)))
				b.WriteString("</tr>")
			}
			b.WriteString("</table>")
			if item.Count > len(item.Logs) {
				b.WriteString(fmt.Sprintf("<p><i>Showing %d of %d matching messages.</i></p>", len(item.Logs), item.Count))
			}
		}
		b.WriteString("<hr>")
	}

	if url != "" {
		b.WriteString(fmt.Sprintf("<p>Check out alerts at: <a href=\"%s\">%s</a></p>",
			html.EscapeString(url), html.EscapeString(url)))
	}
	b.WriteString("</body></html>")
	return b.String()
}
