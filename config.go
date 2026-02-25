package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// parseDuration parses a human-readable duration string like "5m", "10s", "2h", "300".
// Supported units (case-insensitive): s/sec/second/seconds, m/min/minute/minutes, h/hour/hours.
// Unitless values default to seconds.
var durationRe = regexp.MustCompile(`^\s*(\d+)\s*(\w*)\s*$`)

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}

	matches := durationRe.FindStringSubmatch(s)
	if matches == nil {
		return 0, fmt.Errorf("invalid duration format: %q", s)
	}

	value, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("invalid duration number: %q", matches[1])
	}

	unit := strings.TrimSpace(strings.ToLower(matches[2]))

	switch unit {
	case "", "s", "sec", "second", "seconds":
		return time.Duration(value) * time.Second, nil
	case "m", "min", "minute", "minutes":
		return time.Duration(value) * time.Minute, nil
	case "h", "hour", "hours":
		return time.Duration(value) * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown duration unit: %q (use s/sec/second/seconds, m/min/minute/minutes, h/hour/hours)", unit)
	}
}

type ConfigManager struct {
	mu   sync.RWMutex
	cfg  Config
	path string
}

func NewConfigManager(cfg Config, path string) *ConfigManager {
	return &ConfigManager{cfg: cfg, path: path}
}

func (cm *ConfigManager) Get() Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.cfg
}

func (cm *ConfigManager) Update(cfg Config) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}
	if err := os.WriteFile(cm.path, data, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	cm.cfg = cfg
	debugMode = cfg.Debug
	return nil
}

type Config struct {
	SMTP            SMTPConfig            `yaml:"smtp"`
	Alerts          []AlertRule           `yaml:"alerts"`
	Ignore          []IgnoreRule          `yaml:"ignore"`
	SeverityRewrite []SeverityRewriteRule `yaml:"severity_rewrite"`
	Retention       int                   `yaml:"retention"`
	Digest    DigestConfig  `yaml:"digest"`
	Debug     bool          `yaml:"debug"`
	DBPath    string        `yaml:"db_path"`
	Listen    ListenConfig  `yaml:"listen"`
	WebAddr   string        `yaml:"web_addr"`
	URL       string        `yaml:"url"`
}

type SMTPConfig struct {
	Host     string `yaml:"host" json:"host"`
	Port     int    `yaml:"port" json:"port"`
	User     string `yaml:"user" json:"user"`
	Password string `yaml:"password" json:"password"`
	From     string `yaml:"from" json:"from"`
	To       string `yaml:"to" json:"to"`
}

type AlertRule struct {
	Name          string `yaml:"name" json:"name"`
	Count         int    `yaml:"count" json:"count"`
	WindowMinutes int    `yaml:"window_minutes" json:"window_minutes"`
	Level         string `yaml:"level" json:"level"`
	Above         bool   `yaml:"above" json:"above"`
	Host          string `yaml:"host,omitempty" json:"host"`
	Facility      string `yaml:"facility,omitempty" json:"facility"`
	Tag           string `yaml:"tag,omitempty" json:"tag"`
	Message       string `yaml:"message,omitempty" json:"message"`
}

type IgnoreRule struct {
	Host     string `yaml:"host" json:"host"`
	Facility string `yaml:"facility" json:"facility"`
	Tag      string `yaml:"tag" json:"tag"`
	Level    string `yaml:"level" json:"level"`
	Message  string `yaml:"message" json:"message"`
	Discard  bool   `yaml:"discard" json:"discard"`
}

type SeverityRewriteRule struct {
	Host        string `yaml:"host" json:"host"`
	Facility    string `yaml:"facility" json:"facility"`
	Tag         string `yaml:"tag" json:"tag"`
	Level       string `yaml:"level" json:"level"`
	Message     string `yaml:"message" json:"message"`
	NewSeverity string `yaml:"new_severity" json:"new_severity"`
}

type DigestConfig struct {
	Enabled    bool    `yaml:"enabled" json:"enabled"`
	Initial    string  `yaml:"initial" json:"initial"`
	Multiplier float64 `yaml:"multiplier" json:"multiplier"`
	Max        string  `yaml:"max" json:"max"`
	Cooldown   string  `yaml:"cooldown" json:"cooldown"`
}

type ListenConfig struct {
	UDP string `yaml:"udp"`
	TCP string `yaml:"tcp"`
}

func LoadConfig(path string) (Config, string, error) {
	candidates := []string{path, "./config.yaml", "/etc/logyard/config.yaml"}

	var data []byte
	var err error
	var resolvedPath string
	for _, p := range candidates {
		if p == "" {
			continue
		}
		data, err = os.ReadFile(p)
		if err == nil {
			resolvedPath = p
			break
		}
	}
	if data == nil {
		return Config{}, "", fmt.Errorf("no config file found")
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, "", fmt.Errorf("parsing config: %w", err)
	}

	// Defaults
	if cfg.Retention == 0 {
		cfg.Retention = 14
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "./logyard.db"
	}
	if cfg.Listen.UDP == "" {
		cfg.Listen.UDP = ":514"
	}
	if cfg.Listen.TCP == "" {
		cfg.Listen.TCP = ":514"
	}
	if cfg.WebAddr == "" {
		cfg.WebAddr = ":8080"
	}
	if cfg.SMTP.Port == 0 {
		cfg.SMTP.Port = 587
	}
	if cfg.Digest.Enabled {
		if cfg.Digest.Initial == "" {
			cfg.Digest.Initial = "5m"
		}
		if cfg.Digest.Multiplier == 0 {
			cfg.Digest.Multiplier = 3
		}
		if cfg.Digest.Max == "" {
			cfg.Digest.Max = "2h"
		}
		if cfg.Digest.Cooldown == "" {
			cfg.Digest.Cooldown = "10m"
		}
	}
	if cfg.URL == "" {
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "localhost"
		}
		port := cfg.WebAddr
		if strings.HasPrefix(port, ":") {
			port = port[1:]
		}
		cfg.URL = fmt.Sprintf("http://%s:%s", hostname, port)
	}

	if err := ValidateConfig(cfg); err != nil {
		return Config{}, "", err
	}

	return cfg, resolvedPath, nil
}

var validLevels = map[string]bool{
	"emerg": true, "alert": true, "crit": true, "err": true,
	"warning": true, "notice": true, "info": true, "debug": true,
}

func ValidateConfig(cfg Config) error {
	if cfg.Retention < 1 {
		return fmt.Errorf("retention must be at least 1 day")
	}
	for i, rule := range cfg.Alerts {
		if rule.Name == "" {
			return fmt.Errorf("alert rule %d: name is required", i)
		}
		if rule.Count == 0 {
			return fmt.Errorf("alert rule %q: count is required", rule.Name)
		}
		if rule.WindowMinutes == 0 {
			return fmt.Errorf("alert rule %q: window_minutes is required", rule.Name)
		}
		if rule.Level == "" {
			return fmt.Errorf("alert rule %q: level is required", rule.Name)
		}
		if !validLevels[rule.Level] {
			return fmt.Errorf("alert rule %q: invalid level %q", rule.Name, rule.Level)
		}
		if rule.Message != "" {
			if _, err := regexp.Compile(rule.Message); err != nil {
				return fmt.Errorf("alert rule %q: invalid message regex: %w", rule.Name, err)
			}
		}
	}
	for i, rule := range cfg.Ignore {
		if rule.Level != "" && !validLevels[rule.Level] {
			return fmt.Errorf("ignore rule %d: invalid level %q", i, rule.Level)
		}
		if rule.Message != "" {
			if _, err := regexp.Compile(rule.Message); err != nil {
				return fmt.Errorf("ignore rule %d: invalid message regex: %w", i, err)
			}
		}
	}
	for i, rule := range cfg.SeverityRewrite {
		if rule.NewSeverity == "" {
			return fmt.Errorf("severity rewrite rule %d: new_severity is required", i)
		}
		if !validLevels[rule.NewSeverity] {
			return fmt.Errorf("severity rewrite rule %d: invalid new_severity %q", i, rule.NewSeverity)
		}
		if rule.Level != "" && !validLevels[rule.Level] {
			return fmt.Errorf("severity rewrite rule %d: invalid level %q", i, rule.Level)
		}
		if rule.Message != "" {
			if _, err := regexp.Compile(rule.Message); err != nil {
				return fmt.Errorf("severity rewrite rule %d: invalid message regex: %w", i, err)
			}
		}
		if rule.Host == "" && rule.Facility == "" && rule.Tag == "" && rule.Level == "" && rule.Message == "" {
			return fmt.Errorf("severity rewrite rule %d: at least one match field is required", i)
		}
	}
	if cfg.Digest.Enabled {
		initial, err := parseDuration(cfg.Digest.Initial)
		if err != nil {
			return fmt.Errorf("digest: invalid initial: %w", err)
		}
		if initial < time.Second {
			return fmt.Errorf("digest: initial must be at least 1s")
		}
		max, err := parseDuration(cfg.Digest.Max)
		if err != nil {
			return fmt.Errorf("digest: invalid max: %w", err)
		}
		if max < initial {
			return fmt.Errorf("digest: max must be >= initial")
		}
		cooldown, err := parseDuration(cfg.Digest.Cooldown)
		if err != nil {
			return fmt.Errorf("digest: invalid cooldown: %w", err)
		}
		if cooldown < time.Second {
			return fmt.Errorf("digest: cooldown must be at least 1s")
		}
		_ = cooldown
		if cfg.Digest.Multiplier < 1.5 {
			return fmt.Errorf("digest: multiplier must be at least 1.5")
		}
	}
	return nil
}
