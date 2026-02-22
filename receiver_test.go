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
