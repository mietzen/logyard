package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/smtp"
	"strings"
	"time"
)

func StartAlerter(cfg Config, db *sql.DB, alertInterval time.Duration) {
	if len(cfg.Alerts) == 0 {
		log.Println("No alert rules configured, alerter disabled")
		return
	}

	ticker := time.NewTicker(alertInterval)
	go func() {
		for range ticker.C {
			evaluateAlerts(cfg, db)
			purgeOldLogs(cfg.Retention, db)
		}
	}()

	log.Printf("Alerter started with %d rules, checking every %s", len(cfg.Alerts), alertInterval)
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
	var auth smtp.Auth
	if cfg.User != "" {
		auth = smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)
	}

	return smtp.SendMail(addr, auth, cfg.From, []string{cfg.To}, []byte(msg))
}
