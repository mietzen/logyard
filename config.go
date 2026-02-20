package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	SMTP      SMTPConfig   `yaml:"smtp"`
	Alerts    []AlertRule  `yaml:"alerts"`
	Ignore    []IgnoreRule `yaml:"ignore"`
	Retention int          `yaml:"retention"`
	DBPath    string       `yaml:"db_path"`
	Listen    ListenConfig `yaml:"listen"`
	WebAddr   string       `yaml:"web_addr"`
}

type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	From     string `yaml:"from"`
	To       string `yaml:"to"`
}

type AlertRule struct {
	Name          string `yaml:"name"`
	Count         int    `yaml:"count"`
	WindowMinutes int    `yaml:"window_minutes"`
	Level         string `yaml:"level"`
}

type IgnoreRule struct {
	Host     string `yaml:"host"`
	Facility string `yaml:"facility"`
	Level    string `yaml:"level"`
}

type ListenConfig struct {
	UDP string `yaml:"udp"`
	TCP string `yaml:"tcp"`
}

func LoadConfig(path string) (Config, error) {
	candidates := []string{path, "./config.yaml", "/etc/logyard/config.yaml"}

	var data []byte
	var err error
	for _, p := range candidates {
		if p == "" {
			continue
		}
		data, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	if data == nil {
		return Config{}, fmt.Errorf("no config file found")
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config: %w", err)
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

	// Validate alert rules
	for i, rule := range cfg.Alerts {
		if rule.Name == "" {
			return Config{}, fmt.Errorf("alert rule %d: name is required", i)
		}
		if rule.Count == 0 {
			return Config{}, fmt.Errorf("alert rule %q: count is required", rule.Name)
		}
		if rule.WindowMinutes == 0 {
			return Config{}, fmt.Errorf("alert rule %q: window_minutes is required", rule.Name)
		}
		if rule.Level == "" {
			return Config{}, fmt.Errorf("alert rule %q: level is required", rule.Name)
		}
	}

	return cfg, nil
}
