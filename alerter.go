package main

import (
	"crypto/tls"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/smtp"
	"strings"
	"time"
)

func StartAlerter(cm *ConfigManager, db *sql.DB, alertInterval time.Duration) {
	cfg := cm.Get()
	if len(cfg.Alerts) == 0 {
		log.Println("No alert rules configured, alerter disabled")
	}

	ticker := time.NewTicker(alertInterval)
	go func() {
		for range ticker.C {
			c := cm.Get()
			evaluateAlerts(c, db)
			purgeOldLogs(c.Retention, db)
		}
	}()

	log.Printf("Alerter started, checking every %s", alertInterval)
}

func evaluateAlerts(cfg Config, db *sql.DB) {
	for _, rule := range cfg.Alerts {
		since := time.Now().Add(-time.Duration(rule.WindowMinutes) * time.Minute)

		count, err := CountMatchingLogs(db, rule.Level, cfg.Ignore, since)
		if err != nil {
			log.Printf("alert query error for %q: %v", rule.Name, err)
			continue
		}

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
			continue
		}

		if err := sendAlert(cfg.SMTP, rule, count); err != nil {
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

func sendAlert(cfg SMTPConfig, rule AlertRule, count int) error {
	if cfg.Host == "" {
		return fmt.Errorf("SMTP not configured")
	}

	subject := fmt.Sprintf("[Logyard] Alert: %s", rule.Name)
	body := fmt.Sprintf(
		"Alert rule %q triggered.\n\n%d %s messages in the last %d minutes (threshold: %d).",
		rule.Name, count, rule.Level, rule.WindowMinutes, rule.Count,
	)

	msg := strings.Join([]string{
		"From: " + cfg.From,
		"To: " + cfg.To,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
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
