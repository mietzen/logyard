package main

import (
	"flag"
	"log"
	"time"
)

func main() {
	configPath := flag.String("config", "", "path to config.yaml")
	alertInterval := flag.Duration("alert-interval", 60*time.Second, "alert evaluation interval")
	flag.Parse()

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	db, err := InitDB(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to init database: %v", err)
	}
	defer db.Close()

	if err := StartReceiver(cfg.Listen, db); err != nil {
		log.Fatalf("Failed to start syslog receiver: %v", err)
	}

	StartAlerter(cfg, db, *alertInterval)

	log.Fatal(StartWeb(cfg.WebAddr, db))
}
