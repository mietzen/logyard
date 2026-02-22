package main

import (
	"testing"
)

func validBaseConfig() Config {
	return Config{
		Retention: 14,
		Alerts: []AlertRule{
			{Name: "test", Count: 1, WindowMinutes: 5, Level: "err"},
		},
	}
}

func TestValidateConfig_ValidConfig(t *testing.T) {
	cfg := validBaseConfig()
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateConfig_RetentionTooLow(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Retention = 0
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for retention=0")
	}
	cfg.Retention = -1
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for negative retention")
	}
}

func TestValidateConfig_AlertMissingName(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Alerts[0].Name = ""
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for missing alert name")
	}
}

func TestValidateConfig_AlertMissingCount(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Alerts[0].Count = 0
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for missing alert count")
	}
}

func TestValidateConfig_AlertMissingWindow(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Alerts[0].WindowMinutes = 0
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for missing window_minutes")
	}
}

func TestValidateConfig_AlertMissingLevel(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Alerts[0].Level = ""
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for missing alert level")
	}
}

func TestValidateConfig_AlertInvalidLevel(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Alerts[0].Level = "banana"
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for invalid alert level")
	}
}

func TestValidateConfig_AllValidLevels(t *testing.T) {
	levels := []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}
	for _, level := range levels {
		cfg := validBaseConfig()
		cfg.Alerts[0].Level = level
		if err := ValidateConfig(cfg); err != nil {
			t.Fatalf("level %q should be valid, got: %v", level, err)
		}
	}
}

func TestValidateConfig_IgnoreInvalidLevel(t *testing.T) {
	cfg := Config{
		Retention: 14,
		Ignore:    []IgnoreRule{{Level: "banana"}},
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for invalid ignore level")
	}
}

func TestValidateConfig_IgnoreValidLevel(t *testing.T) {
	cfg := Config{
		Retention: 14,
		Ignore:    []IgnoreRule{{Level: "err"}},
	}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateConfig_IgnoreEmptyLevelOK(t *testing.T) {
	cfg := Config{
		Retention: 14,
		Ignore:    []IgnoreRule{{Host: "myhost"}},
	}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected no error for empty ignore level, got: %v", err)
	}
}

func TestValidateConfig_IgnoreInvalidRegex(t *testing.T) {
	cfg := Config{
		Retention: 14,
		Ignore:    []IgnoreRule{{Message: "[invalid"}},
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestValidateConfig_IgnoreValidRegex(t *testing.T) {
	cfg := Config{
		Retention: 14,
		Ignore:    []IgnoreRule{{Message: "CRON|systemd-.*"}},
	}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateConfig_NoAlerts(t *testing.T) {
	cfg := Config{Retention: 14}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected no error for empty alerts, got: %v", err)
	}
}

func TestValidateConfig_SeverityRewriteValid(t *testing.T) {
	cfg := Config{
		Retention: 14,
		SeverityRewrite: []SeverityRewriteRule{
			{Tag: "myapp", Level: "info", Message: "ERROR", NewSeverity: "err"},
		},
	}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateConfig_SeverityRewriteMissingNewSeverity(t *testing.T) {
	cfg := Config{
		Retention:       14,
		SeverityRewrite: []SeverityRewriteRule{{Tag: "myapp"}},
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for missing new_severity")
	}
}

func TestValidateConfig_SeverityRewriteInvalidNewSeverity(t *testing.T) {
	cfg := Config{
		Retention:       14,
		SeverityRewrite: []SeverityRewriteRule{{Tag: "myapp", NewSeverity: "banana"}},
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for invalid new_severity")
	}
}

func TestValidateConfig_SeverityRewriteInvalidLevel(t *testing.T) {
	cfg := Config{
		Retention:       14,
		SeverityRewrite: []SeverityRewriteRule{{Tag: "myapp", Level: "banana", NewSeverity: "err"}},
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for invalid level")
	}
}

func TestValidateConfig_SeverityRewriteInvalidRegex(t *testing.T) {
	cfg := Config{
		Retention:       14,
		SeverityRewrite: []SeverityRewriteRule{{Tag: "myapp", Message: "[invalid", NewSeverity: "err"}},
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestValidateConfig_SeverityRewriteNoMatchFields(t *testing.T) {
	cfg := Config{
		Retention:       14,
		SeverityRewrite: []SeverityRewriteRule{{NewSeverity: "err"}},
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for empty match fields")
	}
}
