package main

import (
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"time"

	"gopkg.in/mcuadros/go-syslog.v2"
)

func StartReceiver(cfg ListenConfig, db *sql.DB, cm *ConfigManager) error {
	channel := make(syslog.LogPartsChannel, 1000)
	handler := syslog.NewChannelHandler(channel)

	server := syslog.NewServer()
	server.SetFormat(syslog.Automatic)
	server.SetHandler(handler)

	if err := server.ListenUDP(cfg.UDP); err != nil {
		return fmt.Errorf("listen UDP %s: %w", cfg.UDP, err)
	}
	if err := server.ListenTCP(cfg.TCP); err != nil {
		return fmt.Errorf("listen TCP %s: %w", cfg.TCP, err)
	}

	if err := server.Boot(); err != nil {
		return fmt.Errorf("boot syslog server: %w", err)
	}

	go func() {
		for parts := range channel {
			ts := getTime(parts, "timestamp", time.Now())
			host := getString(parts, "hostname")
			facility := facilityName(getInt(parts, "facility"))
			severity := severityName(getInt(parts, "severity"))
			tag := getTag(parts)
			message := getMessage(parts)

			if shouldDiscard(cm, host, facility, severity, tag, message) {
				debugf("discarding log from host=%s tag=%s", host, tag)
				continue
			}

			severity = applySeverityRewrite(cm, host, facility, severity, tag, message)

			if err := InsertLog(db, ts, host, facility, severity, tag, message); err != nil {
				log.Printf("insert error: %v", err)
			}
		}
	}()

	log.Printf("Syslog receiver started on UDP %s and TCP %s", cfg.UDP, cfg.TCP)
	return nil
}

func shouldDiscard(cm *ConfigManager, host, facility, severity, tag, message string) bool {
	cfg := cm.Get()
	for _, rule := range cfg.Ignore {
		if !rule.Discard {
			continue
		}
		if matchesIgnoreRule(rule, host, facility, severity, tag, message) {
			return true
		}
	}
	return false
}

func applySeverityRewrite(cm *ConfigManager, host, facility, severity, tag, message string) string {
	cfg := cm.Get()
	for _, rule := range cfg.SeverityRewrite {
		if matchesRewriteRule(rule, host, facility, severity, tag, message) {
			debugf("rewriting severity %s -> %s for host=%s tag=%s", severity, rule.NewSeverity, host, tag)
			return rule.NewSeverity
		}
	}
	return severity
}

func matchesRewriteRule(rule SeverityRewriteRule, host, facility, severity, tag, message string) bool {
	if rule.Host != "" && rule.Host != host {
		return false
	}
	if rule.Facility != "" && rule.Facility != facility {
		return false
	}
	if rule.Level != "" && rule.Level != severity {
		return false
	}
	if rule.Tag != "" && rule.Tag != tag {
		return false
	}
	if rule.Message != "" {
		matched, err := regexp.MatchString(rule.Message, message)
		if err != nil || !matched {
			return false
		}
	}
	return true
}

func matchesIgnoreRule(rule IgnoreRule, host, facility, severity, tag, message string) bool {
	if rule.Host != "" && rule.Host != host {
		return false
	}
	if rule.Facility != "" && rule.Facility != facility {
		return false
	}
	if rule.Level != "" && rule.Level != severity {
		return false
	}
	if rule.Tag != "" && rule.Tag != tag {
		return false
	}
	if rule.Message != "" {
		matched, err := regexp.MatchString(rule.Message, message)
		if err != nil || !matched {
			return false
		}
	}
	return true
}

// getTag returns the tag/app_name from LogParts, handling both RFC3164 and RFC5424.
func getTag(parts map[string]interface{}) string {
	if v := getString(parts, "tag"); v != "" {
		return v
	}
	return getString(parts, "app_name")
}

// getMessage returns the message content from LogParts, handling both RFC3164 and RFC5424.
func getMessage(parts map[string]interface{}) string {
	if v := getString(parts, "content"); v != "" {
		return v
	}
	return getString(parts, "message")
}

func getString(parts map[string]interface{}, key string) string {
	v, ok := parts[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	return s
}

func getInt(parts map[string]interface{}, key string) int {
	v, ok := parts[key]
	if !ok || v == nil {
		return 0
	}
	i, ok := v.(int)
	if !ok {
		return 0
	}
	return i
}

func getTime(parts map[string]interface{}, key string, fallback time.Time) time.Time {
	v, ok := parts[key]
	if !ok || v == nil {
		return fallback
	}
	t, ok := v.(time.Time)
	if !ok {
		return fallback
	}
	return t
}

func facilityName(code int) string {
	if name, ok := facilityNames[code]; ok {
		return name
	}
	return fmt.Sprintf("facility%d", code)
}

func severityName(code int) string {
	if name, ok := severityNames[code]; ok {
		return name
	}
	return fmt.Sprintf("severity%d", code)
}
