package main

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mattn/go-sqlite3"
)

// Standard syslog facility names (RFC 5424)
var facilityNames = map[int]string{
	0: "kern", 1: "user", 2: "mail", 3: "daemon",
	4: "auth", 5: "syslog", 6: "lpr", 7: "news",
	8: "uucp", 9: "cron", 10: "authpriv", 11: "ftp",
	16: "local0", 17: "local1", 18: "local2", 19: "local3",
	20: "local4", 21: "local5", 22: "local6", 23: "local7",
}

// Standard syslog severity names (RFC 5424)
var severityNames = map[int]string{
	0: "emerg", 1: "alert", 2: "crit", 3: "err",
	4: "warning", 5: "notice", 6: "info", 7: "debug",
}

var severityOrder = []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}

func severitiesAtOrAbove(level string) []string {
	for i, s := range severityOrder {
		if s == level {
			return severityOrder[:i+1]
		}
	}
	return []string{level}
}

type LogEntry struct {
	ID        int64
	Timestamp time.Time
	Host      string
	Facility  string
	Severity  string
	Tag       string
	Message   string
}

type LogFilter struct {
	Host     string
	Facility string
	Severity string
	Tag      string
	Search   string
	Since    string
	Until    string
}

func init() {
	sql.Register("sqlite3_regexp", &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			return conn.RegisterFunc("regexp", func(pattern, s string) (bool, error) {
				return regexp.MatchString(pattern, s)
			}, true)
		},
	})
}

func InitDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3_regexp", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	schema := `
	CREATE TABLE IF NOT EXISTS logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME NOT NULL,
		host TEXT,
		facility TEXT,
		severity TEXT,
		tag TEXT,
		message TEXT
	);
	CREATE TABLE IF NOT EXISTS alert_state (
		rule_name TEXT PRIMARY KEY,
		last_alerted_at DATETIME
	);
	CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON logs(timestamp);
	CREATE INDEX IF NOT EXISTS idx_logs_severity_timestamp ON logs(severity, timestamp);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}

	return db, nil
}

func InsertLog(db *sql.DB, timestamp time.Time, host, facility, severity, tag, message string) error {
	_, err := db.Exec(
		"INSERT INTO logs (timestamp, host, facility, severity, tag, message) VALUES (?, ?, ?, ?, ?, ?)",
		timestamp, host, facility, severity, tag, message,
	)
	return err
}

func QueryLogs(db *sql.DB, filter LogFilter, limit int) ([]LogEntry, error) {
	var conditions []string
	var args []interface{}

	if filter.Host != "" {
		conditions = append(conditions, "host = ?")
		args = append(args, filter.Host)
	}
	if filter.Facility != "" {
		conditions = append(conditions, "facility = ?")
		args = append(args, filter.Facility)
	}
	if filter.Severity != "" {
		sevs := severitiesAtOrAbove(filter.Severity)
		if len(sevs) > 0 {
			placeholders := make([]string, len(sevs))
			for i, s := range sevs {
				placeholders[i] = "?"
				args = append(args, s)
			}
			conditions = append(conditions, "severity IN ("+strings.Join(placeholders, ",")+")")
		}
	}
	if filter.Tag != "" {
		conditions = append(conditions, "tag = ?")
		args = append(args, filter.Tag)
	}
	if filter.Search != "" {
		conditions = append(conditions, "message LIKE ?")
		args = append(args, "%"+filter.Search+"%")
	}
	if filter.Since != "" {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, filter.Since)
	}
	if filter.Until != "" {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, filter.Until)
	}

	query := "SELECT id, timestamp, host, facility, severity, tag, message FROM logs"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []LogEntry
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.Host, &e.Facility, &e.Severity, &e.Tag, &e.Message); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func CountMatchingLogs(db *sql.DB, rule AlertRule, ignoreRules []IgnoreRule, since time.Time) (int, error) {
	var conditions []string
	var args []interface{}

	if rule.Above {
		sevs := severitiesAtOrAbove(rule.Level)
		placeholders := make([]string, len(sevs))
		for i, s := range sevs {
			placeholders[i] = "?"
			args = append(args, s)
		}
		conditions = append(conditions, "severity IN ("+strings.Join(placeholders, ",")+")")
	} else {
		conditions = append(conditions, "severity = ?")
		args = append(args, rule.Level)
	}
	conditions = append(conditions, "timestamp > ?")
	args = append(args, since)

	if rule.Host != "" {
		conditions = append(conditions, "host = ?")
		args = append(args, rule.Host)
	}
	if rule.Facility != "" {
		conditions = append(conditions, "facility = ?")
		args = append(args, rule.Facility)
	}
	if rule.Tag != "" {
		conditions = append(conditions, "tag = ?")
		args = append(args, rule.Tag)
	}
	if rule.Message != "" {
		conditions = append(conditions, "message REGEXP ?")
		args = append(args, rule.Message)
	}

	// Exclude ignored logs: each rule is AND within, OR across (via multiple NOT clauses)
	for _, rule := range ignoreRules {
		var parts []string
		var ruleArgs []interface{}
		if rule.Host != "" {
			parts = append(parts, "host = ?")
			ruleArgs = append(ruleArgs, rule.Host)
		}
		if rule.Facility != "" {
			parts = append(parts, "facility = ?")
			ruleArgs = append(ruleArgs, rule.Facility)
		}
		if rule.Tag != "" {
			parts = append(parts, "tag = ?")
			ruleArgs = append(ruleArgs, rule.Tag)
		}
		if rule.Level != "" {
			parts = append(parts, "severity = ?")
			ruleArgs = append(ruleArgs, rule.Level)
		}
		if rule.Message != "" {
			parts = append(parts, "message REGEXP ?")
			ruleArgs = append(ruleArgs, rule.Message)
		}
		if len(parts) > 0 {
			conditions = append(conditions, "NOT ("+strings.Join(parts, " AND ")+")")
			args = append(args, ruleArgs...)
		}
	}

	query := "SELECT COUNT(*) FROM logs WHERE " + strings.Join(conditions, " AND ")

	var count int
	err := db.QueryRow(query, args...).Scan(&count)
	return count, err
}

func GetLastAlerted(db *sql.DB, ruleName string) (time.Time, error) {
	var t time.Time
	err := db.QueryRow("SELECT last_alerted_at FROM alert_state WHERE rule_name = ?", ruleName).Scan(&t)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	return t, err
}

func SetLastAlerted(db *sql.DB, ruleName string, t time.Time) error {
	_, err := db.Exec(
		"INSERT INTO alert_state (rule_name, last_alerted_at) VALUES (?, ?) ON CONFLICT(rule_name) DO UPDATE SET last_alerted_at = ?",
		ruleName, t, t,
	)
	return err
}

func DistinctValues(db *sql.DB, column string, filters map[string]string) ([]string, error) {
	conditions := []string{fmt.Sprintf("%s != ''", column)}
	var args []interface{}
	for col, val := range filters {
		if val != "" && col != column {
			conditions = append(conditions, fmt.Sprintf("%s = ?", col))
			args = append(args, val)
		}
	}
	query := fmt.Sprintf("SELECT DISTINCT %s FROM logs WHERE %s ORDER BY %s",
		column, strings.Join(conditions, " AND "), column)
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var values []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

func PurgeLogs(db *sql.DB, retentionDays int) (int64, error) {
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
	result, err := db.Exec("DELETE FROM logs WHERE timestamp < ?", cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
