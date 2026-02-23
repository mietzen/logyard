package main

import "testing"

func TestMatchesIgnoreRule(t *testing.T) {
	tests := []struct {
		name     string
		rule     IgnoreRule
		host     string
		facility string
		severity string
		tag      string
		message  string
		want     bool
	}{
		{"match host", IgnoreRule{Host: "noisy"}, "noisy", "kern", "err", "app", "msg", true},
		{"no match host", IgnoreRule{Host: "noisy"}, "quiet", "kern", "err", "app", "msg", false},
		{"match facility", IgnoreRule{Facility: "kern"}, "h", "kern", "err", "app", "msg", true},
		{"no match facility", IgnoreRule{Facility: "kern"}, "h", "user", "err", "app", "msg", false},
		{"match level", IgnoreRule{Level: "debug"}, "h", "kern", "debug", "app", "msg", true},
		{"no match level", IgnoreRule{Level: "debug"}, "h", "kern", "err", "app", "msg", false},
		{"match tag", IgnoreRule{Tag: "CRON"}, "h", "kern", "err", "CRON", "msg", true},
		{"no match tag", IgnoreRule{Tag: "CRON"}, "h", "kern", "err", "nginx", "msg", false},
		{"match message regex", IgnoreRule{Message: "timeout|refused"}, "h", "kern", "err", "app", "connection refused", true},
		{"no match message regex", IgnoreRule{Message: "timeout|refused"}, "h", "kern", "err", "app", "disk full", false},
		{"match AND all fields", IgnoreRule{Host: "h", Facility: "kern", Level: "err", Tag: "app", Message: "err.*"}, "h", "kern", "err", "app", "error occurred", true},
		{"AND fails on one field", IgnoreRule{Host: "h", Tag: "nginx"}, "h", "kern", "err", "app", "msg", false},
		{"empty rule matches everything", IgnoreRule{}, "h", "kern", "err", "app", "msg", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesIgnoreRule(tt.rule, tt.host, tt.facility, tt.severity, tt.tag, tt.message)
			if got != tt.want {
				t.Errorf("matchesIgnoreRule() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldDiscard(t *testing.T) {
	tests := []struct {
		name    string
		rules   []IgnoreRule
		host    string
		message string
		want    bool
	}{
		{
			"discard rule matches",
			[]IgnoreRule{{Host: "noisy", Discard: true}},
			"noisy", "msg", true,
		},
		{
			"non-discard rule does not discard",
			[]IgnoreRule{{Host: "noisy", Discard: false}},
			"noisy", "msg", false,
		},
		{
			"discard rule does not match",
			[]IgnoreRule{{Host: "noisy", Discard: true}},
			"quiet", "msg", false,
		},
		{
			"mixed rules: only discard rule applies",
			[]IgnoreRule{
				{Host: "noisy", Discard: false},
				{Tag: "CRON", Discard: true},
			},
			"noisy", "msg", false,
		},
		{
			"discard with message regex",
			[]IgnoreRule{{Message: "heartbeat|keepalive", Discard: true}},
			"any", "keepalive check", true,
		},
		{
			"no rules",
			nil,
			"any", "msg", false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Ignore: tt.rules}
			cm := NewConfigManager(cfg, "")
			got := shouldDiscard(cm, tt.host, "kern", "info", "app", tt.message)
			if got != tt.want {
				t.Errorf("shouldDiscard() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchesRewriteRule(t *testing.T) {
	tests := []struct {
		name     string
		rule     SeverityRewriteRule
		host     string
		facility string
		severity string
		tag      string
		message  string
		want     bool
	}{
		{"match host", SeverityRewriteRule{Host: "h"}, "h", "kern", "info", "app", "msg", true},
		{"no match host", SeverityRewriteRule{Host: "h"}, "other", "kern", "info", "app", "msg", false},
		{"match tag", SeverityRewriteRule{Tag: "myapp"}, "h", "kern", "info", "myapp", "msg", true},
		{"no match tag", SeverityRewriteRule{Tag: "myapp"}, "h", "kern", "info", "other", "msg", false},
		{"match level", SeverityRewriteRule{Level: "info"}, "h", "kern", "info", "app", "msg", true},
		{"no match level", SeverityRewriteRule{Level: "info"}, "h", "kern", "err", "app", "msg", false},
		{"match message regex", SeverityRewriteRule{Message: "ERROR|FATAL"}, "h", "kern", "info", "app", "ERROR occurred", true},
		{"no match message regex", SeverityRewriteRule{Message: "ERROR|FATAL"}, "h", "kern", "info", "app", "all good", false},
		{"match AND all fields", SeverityRewriteRule{Host: "h", Tag: "app", Level: "info", Message: "ERR"}, "h", "kern", "info", "app", "ERR happened", true},
		{"AND fails on one field", SeverityRewriteRule{Host: "h", Tag: "nginx"}, "h", "kern", "info", "app", "msg", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesRewriteRule(tt.rule, tt.host, tt.facility, tt.severity, tt.tag, tt.message)
			if got != tt.want {
				t.Errorf("matchesRewriteRule() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplySeverityRewrite(t *testing.T) {
	tests := []struct {
		name     string
		rules    []SeverityRewriteRule
		host     string
		severity string
		tag      string
		message  string
		want     string
	}{
		{
			"rule matches, rewrite severity",
			[]SeverityRewriteRule{{Tag: "myapp", Level: "info", Message: "ERROR", NewSeverity: "err"}},
			"h", "info", "myapp", "ERROR occurred", "err",
		},
		{
			"rule does not match, keep original",
			[]SeverityRewriteRule{{Tag: "myapp", Level: "info", Message: "ERROR", NewSeverity: "err"}},
			"h", "info", "myapp", "all good", "info",
		},
		{
			"first match wins",
			[]SeverityRewriteRule{
				{Tag: "myapp", Message: "FATAL", NewSeverity: "crit"},
				{Tag: "myapp", Message: "FATAL|ERROR", NewSeverity: "err"},
			},
			"h", "info", "myapp", "FATAL error", "crit",
		},
		{
			"second rule matches when first does not",
			[]SeverityRewriteRule{
				{Tag: "myapp", Message: "FATAL", NewSeverity: "crit"},
				{Tag: "myapp", Message: "ERROR", NewSeverity: "err"},
			},
			"h", "info", "myapp", "ERROR happened", "err",
		},
		{
			"no rules, keep original",
			nil,
			"h", "info", "myapp", "msg", "info",
		},
		{
			"host filter limits rewrite",
			[]SeverityRewriteRule{{Host: "specific", Message: "ERROR", NewSeverity: "err"}},
			"other", "info", "myapp", "ERROR occurred", "info",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{SeverityRewrite: tt.rules}
			cm := NewConfigManager(cfg, "")
			got := applySeverityRewrite(cm, tt.host, "kern", tt.severity, tt.tag, tt.message)
			if got != tt.want {
				t.Errorf("applySeverityRewrite() = %q, want %q", got, tt.want)
			}
		})
	}
}
