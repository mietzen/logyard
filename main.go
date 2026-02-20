package main

import (
	"flag"
	"log"
)

func main() {
	configPath := flag.String("config", "", "path to config.yaml")
	flag.Parse()

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("Config loaded: syslog=%s/%s, web=%s, db=%s, retention=%dd",
		cfg.Listen.UDP, cfg.Listen.TCP, cfg.WebAddr, cfg.DBPath, cfg.Retention)
}
