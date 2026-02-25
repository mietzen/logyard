package main

import (
	"testing"
	"time"
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

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"5s", 5 * time.Second},
		{"10m", 10 * time.Minute},
		{"2h", 2 * time.Hour},
		{"300", 300 * time.Second},
		{"5 m", 5 * time.Minute},
		{" 10 s ", 10 * time.Second},
		{"5M", 5 * time.Minute},
		{"10Min", 10 * time.Minute},
		{"2 HOURS", 2 * time.Hour},
		{"1sec", 1 * time.Second},
		{"30second", 30 * time.Second},
		{"5seconds", 5 * time.Second},
		{"1minute", 1 * time.Minute},
		{"3minutes", 3 * time.Minute},
		{"1hour", 1 * time.Hour},
	}
	for _, tt := range tests {
		d, err := parseDuration(tt.input)
		if err != nil {
			t.Errorf("parseDuration(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if d != tt.expected {
			t.Errorf("parseDuration(%q) = %v, want %v", tt.input, d, tt.expected)
		}
	}
}

func TestParseDuration_Errors(t *testing.T) {
	badInputs := []string{
		"",
		"abc",
		"5x",
		"5 days",
		"m5",
		"-5s",
	}
	for _, input := range badInputs {
		_, err := parseDuration(input)
		if err == nil {
			t.Errorf("parseDuration(%q): expected error, got nil", input)
		}
	}
}

func TestValidateConfig_DigestValid(t *testing.T) {
	cfg := Config{
		Retention: 14,
		Digest: DigestConfig{
			Enabled:    true,
			Initial:    "5m",
			Multiplier: 3,
			Max:        "2h",
			Cooldown:   "10m",
		},
	}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateConfig_DigestDisabledSkipsValidation(t *testing.T) {
	cfg := Config{
		Retention: 14,
		Digest: DigestConfig{
			Enabled:    false,
			Initial:    "invalid",
			Multiplier: 0,
		},
	}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("expected no error when digest disabled, got: %v", err)
	}
}

func TestValidateConfig_DigestInvalidInitial(t *testing.T) {
	cfg := Config{
		Retention: 14,
		Digest: DigestConfig{
			Enabled:    true,
			Initial:    "bad",
			Multiplier: 2,
			Max:        "2h",
			Cooldown:   "10m",
		},
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for invalid initial")
	}
}

func TestValidateConfig_DigestMaxLessThanInitial(t *testing.T) {
	cfg := Config{
		Retention: 14,
		Digest: DigestConfig{
			Enabled:    true,
			Initial:    "10m",
			Multiplier: 2,
			Max:        "5m",
			Cooldown:   "10m",
		},
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for max < initial")
	}
}

func TestValidateConfig_DigestMultiplierTooLow(t *testing.T) {
	cfg := Config{
		Retention: 14,
		Digest: DigestConfig{
			Enabled:    true,
			Initial:    "5m",
			Multiplier: 1.2,
			Max:        "2h",
			Cooldown:   "10m",
		},
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for multiplier < 1.5")
	}
}

func TestValidateConfig_DigestInvalidCooldown(t *testing.T) {
	cfg := Config{
		Retention: 14,
		Digest: DigestConfig{
			Enabled:    true,
			Initial:    "5m",
			Multiplier: 2,
			Max:        "2h",
			Cooldown:   "nope",
		},
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("expected error for invalid cooldown")
	}
}
