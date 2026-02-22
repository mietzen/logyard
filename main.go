package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"
)

var version = "dev"

var debugMode bool

func debugf(format string, args ...interface{}) {
	if debugMode {
		log.Printf("[DEBUG] "+format, args...)
	}
}

func main() {
	configPath := flag.String("config", "", "path to config.yaml")
	alertInterval := flag.Duration("alert-interval", 60*time.Second, "alert evaluation interval")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	cfg, cfgPath, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	debugMode = cfg.Debug
	cm := NewConfigManager(cfg, cfgPath)

	db, err := InitDB(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to init database: %v", err)
	}
	defer db.Close()

	if err := StartReceiver(cfg.Listen, db, cm); err != nil {
		log.Fatalf("Failed to start syslog receiver: %v", err)
	}

	StartAlerter(cm, db, *alertInterval)

	log.Fatal(StartWeb(cfg.WebAddr, db, cm))
}
