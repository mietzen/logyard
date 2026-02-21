package main

import (
	"fmt"
	"os"
	"regexp"
	"sync"

	"gopkg.in/yaml.v3"
)

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
	SMTP      SMTPConfig   `yaml:"smtp"`
	Alerts    []AlertRule  `yaml:"alerts"`
	Ignore    []IgnoreRule `yaml:"ignore"`
	Retention int          `yaml:"retention"`
	Debug     bool         `yaml:"debug"`
	DBPath    string       `yaml:"db_path"`
	Listen    ListenConfig `yaml:"listen"`
	WebAddr   string       `yaml:"web_addr"`
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
}

type IgnoreRule struct {
	Host     string `yaml:"host" json:"host"`
	Facility string `yaml:"facility" json:"facility"`
	Tag      string `yaml:"tag" json:"tag"`
	Level    string `yaml:"level" json:"level"`
	Message  string `yaml:"message" json:"message"`
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
	return nil
}
