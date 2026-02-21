package main

import (
	"testing"
	"time"
)

func TestFacilityName(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{0, "kern"}, {1, "user"}, {2, "mail"}, {3, "daemon"},
		{4, "auth"}, {5, "syslog"}, {16, "local0"}, {23, "local7"},
		{99, "facility99"},
	}
	for _, tt := range tests {
		got := facilityName(tt.code)
		if got != tt.want {
			t.Errorf("facilityName(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestSeverityName(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{0, "emerg"}, {1, "alert"}, {2, "crit"}, {3, "err"},
		{4, "warning"}, {5, "notice"}, {6, "info"}, {7, "debug"},
		{99, "severity99"},
	}
	for _, tt := range tests {
		got := severityName(tt.code)
		if got != tt.want {
			t.Errorf("severityName(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestSeveritiesAtOrAbove(t *testing.T) {
	tests := []struct {
		level string
		want  []string
	}{
		{"emerg", []string{"emerg"}},
		{"err", []string{"emerg", "alert", "crit", "err"}},
		{"debug", []string{"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"}},
		{"unknown", []string{"unknown"}},
	}
	for _, tt := range tests {
		got := severitiesAtOrAbove(tt.level)
		if len(got) != len(tt.want) {
			t.Errorf("severitiesAtOrAbove(%q) = %v, want %v", tt.level, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("severitiesAtOrAbove(%q)[%d] = %q, want %q", tt.level, i, got[i], tt.want[i])
			}
		}
	}
}

func TestGetString(t *testing.T) {
	parts := map[string]interface{}{
		"host":    "myhost",
		"number":  42,
		"nilval":  nil,
	}
	if v := getString(parts, "host"); v != "myhost" {
		t.Errorf("getString(host) = %q, want %q", v, "myhost")
	}
	if v := getString(parts, "missing"); v != "" {
		t.Errorf("getString(missing) = %q, want empty", v)
	}
	if v := getString(parts, "nilval"); v != "" {
		t.Errorf("getString(nilval) = %q, want empty", v)
	}
	if v := getString(parts, "number"); v != "42" {
		t.Errorf("getString(number) = %q, want %q", v, "42")
	}
}

func TestGetInt(t *testing.T) {
	parts := map[string]interface{}{
		"facility": 1,
		"str":      "hello",
		"nilval":   nil,
	}
	if v := getInt(parts, "facility"); v != 1 {
		t.Errorf("getInt(facility) = %d, want 1", v)
	}
	if v := getInt(parts, "missing"); v != 0 {
		t.Errorf("getInt(missing) = %d, want 0", v)
	}
	if v := getInt(parts, "nilval"); v != 0 {
		t.Errorf("getInt(nilval) = %d, want 0", v)
	}
	if v := getInt(parts, "str"); v != 0 {
		t.Errorf("getInt(str) = %d, want 0", v)
	}
}

func TestGetTime(t *testing.T) {
	now := time.Now()
	ts := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	parts := map[string]interface{}{
		"timestamp": ts,
		"str":       "not a time",
		"nilval":    nil,
	}
	if v := getTime(parts, "timestamp", now); !v.Equal(ts) {
		t.Errorf("getTime(timestamp) = %v, want %v", v, ts)
	}
	if v := getTime(parts, "missing", now); !v.Equal(now) {
		t.Errorf("getTime(missing) should return fallback")
	}
	if v := getTime(parts, "nilval", now); !v.Equal(now) {
		t.Errorf("getTime(nilval) should return fallback")
	}
	if v := getTime(parts, "str", now); !v.Equal(now) {
		t.Errorf("getTime(str) should return fallback")
	}
}

func TestGetTag(t *testing.T) {
	// RFC 3164 uses "tag"
	parts := map[string]interface{}{"tag": "myapp"}
	if v := getTag(parts); v != "myapp" {
		t.Errorf("getTag(rfc3164) = %q, want %q", v, "myapp")
	}
	// RFC 5424 uses "app_name"
	parts = map[string]interface{}{"app_name": "myapp5424"}
	if v := getTag(parts); v != "myapp5424" {
		t.Errorf("getTag(rfc5424) = %q, want %q", v, "myapp5424")
	}
	// tag takes precedence
	parts = map[string]interface{}{"tag": "rfc3164", "app_name": "rfc5424"}
	if v := getTag(parts); v != "rfc3164" {
		t.Errorf("getTag(both) = %q, want %q", v, "rfc3164")
	}
}

func TestGetMessage(t *testing.T) {
	// RFC 3164 uses "content"
	parts := map[string]interface{}{"content": "hello"}
	if v := getMessage(parts); v != "hello" {
		t.Errorf("getMessage(content) = %q, want %q", v, "hello")
	}
	// RFC 5424 uses "message"
	parts = map[string]interface{}{"message": "world"}
	if v := getMessage(parts); v != "world" {
		t.Errorf("getMessage(message) = %q, want %q", v, "world")
	}
}

func TestInitDB_AndInsertQuery(t *testing.T) {
	db, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	ts := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	if err := InsertLog(db, ts, "host1", "kern", "err", "myapp", "something broke"); err != nil {
		t.Fatalf("InsertLog: %v", err)
	}

	entries, err := QueryLogs(db, LogFilter{}, 100)
	if err != nil {
		t.Fatalf("QueryLogs: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Host != "host1" || e.Facility != "kern" || e.Severity != "err" || e.Tag != "myapp" || e.Message != "something broke" {
		t.Errorf("unexpected entry: %+v", e)
	}
}

func TestQueryLogs_Filters(t *testing.T) {
	db, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	ts := time.Now()
	InsertLog(db, ts, "host1", "kern", "err", "app1", "error message")
	InsertLog(db, ts, "host2", "user", "info", "app2", "info message")

	// Filter by host
	entries, _ := QueryLogs(db, LogFilter{Host: "host1"}, 100)
	if len(entries) != 1 || entries[0].Host != "host1" {
		t.Errorf("host filter: expected 1 entry for host1, got %d", len(entries))
	}

	// Filter by severity (err = emerg+alert+crit+err)
	entries, _ = QueryLogs(db, LogFilter{Severity: "err"}, 100)
	if len(entries) != 1 || entries[0].Severity != "err" {
		t.Errorf("severity filter: expected 1 entry for err, got %d", len(entries))
	}

	// Filter by search
	entries, _ = QueryLogs(db, LogFilter{Search: "info"}, 100)
	if len(entries) != 1 || entries[0].Message != "info message" {
		t.Errorf("search filter: expected 1 entry, got %d", len(entries))
	}

	// Filter by tag
	entries, _ = QueryLogs(db, LogFilter{Tag: "app2"}, 100)
	if len(entries) != 1 || entries[0].Tag != "app2" {
		t.Errorf("tag filter: expected 1 entry for app2, got %d", len(entries))
	}
}

func TestCountMatchingLogs_WithIgnoreRules(t *testing.T) {
	db, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	ts := time.Now()
	InsertLog(db, ts, "host1", "kern", "err", "app1", "real error")
	InsertLog(db, ts, "host1", "kern", "err", "ignored-app", "ignored error")
	InsertLog(db, ts, "host1", "kern", "err", "app1", "should-be-ignored error")

	since := ts.Add(-1 * time.Minute)

	// No ignore rules: all 3 match
	count, err := CountMatchingLogs(db, "err", false, nil, since)
	if err != nil {
		t.Fatalf("CountMatchingLogs: %v", err)
	}
	if count != 3 {
		t.Errorf("no ignores: expected 3, got %d", count)
	}

	// Ignore by tag
	count, _ = CountMatchingLogs(db, "err", false, []IgnoreRule{{Tag: "ignored-app"}}, since)
	if count != 2 {
		t.Errorf("ignore by tag: expected 2, got %d", count)
	}

	// Ignore by message regex
	count, _ = CountMatchingLogs(db, "err", false, []IgnoreRule{{Message: "should-be-ignored"}}, since)
	if count != 2 {
		t.Errorf("ignore by regex: expected 2, got %d", count)
	}

	// Ignore by both rules
	count, _ = CountMatchingLogs(db, "err", false, []IgnoreRule{
		{Tag: "ignored-app"},
		{Message: "should-be-ignored"},
	}, since)
	if count != 1 {
		t.Errorf("ignore both: expected 1, got %d", count)
	}
}

func TestCountMatchingLogs_Above(t *testing.T) {
	db, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	ts := time.Now()
	InsertLog(db, ts, "host1", "kern", "err", "app1", "error")
	InsertLog(db, ts, "host1", "kern", "warning", "app1", "warning")
	InsertLog(db, ts, "host1", "kern", "info", "app1", "info")

	since := ts.Add(-1 * time.Minute)

	// above=false: only exact match
	count, _ := CountMatchingLogs(db, "warning", false, nil, since)
	if count != 1 {
		t.Errorf("exact warning: expected 1, got %d", count)
	}

	// above=true: warning and above (emerg, alert, crit, err, warning)
	count, _ = CountMatchingLogs(db, "warning", true, nil, since)
	if count != 2 {
		t.Errorf("warning and above: expected 2, got %d", count)
	}
}

func TestAlertState(t *testing.T) {
	db, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	// No state yet
	ts, err := GetLastAlerted(db, "test-rule")
	if err != nil {
		t.Fatalf("GetLastAlerted: %v", err)
	}
	if !ts.IsZero() {
		t.Errorf("expected zero time, got %v", ts)
	}

	// Set state
	now := time.Now().UTC().Truncate(time.Second)
	if err := SetLastAlerted(db, "test-rule", now); err != nil {
		t.Fatalf("SetLastAlerted: %v", err)
	}

	ts, err = GetLastAlerted(db, "test-rule")
	if err != nil {
		t.Fatalf("GetLastAlerted after set: %v", err)
	}
	if !ts.UTC().Truncate(time.Second).Equal(now) {
		t.Errorf("expected %v, got %v", now, ts)
	}

	// Update state
	later := now.Add(5 * time.Minute)
	SetLastAlerted(db, "test-rule", later)
	ts, _ = GetLastAlerted(db, "test-rule")
	if !ts.UTC().Truncate(time.Second).Equal(later) {
		t.Errorf("expected updated time %v, got %v", later, ts)
	}
}

func TestPurgeLogs(t *testing.T) {
	db, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	old := time.Now().Add(-48 * time.Hour)
	recent := time.Now()
	InsertLog(db, old, "host1", "kern", "err", "app", "old message")
	InsertLog(db, recent, "host1", "kern", "err", "app", "recent message")

	deleted, err := PurgeLogs(db, 1)
	if err != nil {
		t.Fatalf("PurgeLogs: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	entries, _ := QueryLogs(db, LogFilter{}, 100)
	if len(entries) != 1 {
		t.Errorf("expected 1 remaining entry, got %d", len(entries))
	}
}

func TestDistinctValues(t *testing.T) {
	db, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	ts := time.Now()
	InsertLog(db, ts, "host1", "kern", "err", "app1", "msg1")
	InsertLog(db, ts, "host2", "user", "info", "app2", "msg2")
	InsertLog(db, ts, "host1", "kern", "err", "app1", "msg3")

	hosts, err := DistinctValues(db, "host", nil)
	if err != nil {
		t.Fatalf("DistinctValues: %v", err)
	}
	if len(hosts) != 2 {
		t.Errorf("expected 2 distinct hosts, got %d: %v", len(hosts), hosts)
	}
}
