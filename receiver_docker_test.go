package main

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDockerHost(t *testing.T) {
	tests := []struct {
		socket string
		want   string
	}{
		{"unix:///var/run/docker.sock", "localhost"},
		{"tcp://my-docker-host:2375", "my-docker-host"},
		{"tcp://192.168.1.100:2375", "192.168.1.100"},
		{"tcp://proxy:2375", "proxy"},
		{"unix:///custom/path.sock", "localhost"},
	}
	for _, tt := range tests {
		got := dockerHost(tt.socket)
		if got != tt.want {
			t.Errorf("dockerHost(%q) = %q, want %q", tt.socket, got, tt.want)
		}
	}
}

func TestContainerName(t *testing.T) {
	tests := []struct {
		container dockerContainer
		want      string
	}{
		{dockerContainer{ID: "abc123def456", Names: []string{"/my-container"}}, "my-container"},
		{dockerContainer{ID: "abc123def456", Names: []string{"no-slash"}}, "no-slash"},
		{dockerContainer{ID: "abc123def456789", Names: nil}, "abc123def456"},
		{dockerContainer{ID: "short", Names: nil}, "short"},
	}
	for _, tt := range tests {
		got := containerName(tt.container)
		if got != tt.want {
			t.Errorf("containerName(%+v) = %q, want %q", tt.container, got, tt.want)
		}
	}
}

func TestParseDockerTimestamp(t *testing.T) {
	ts, msg := parseDockerTimestamp("2026-02-21T18:06:48.123456789Z hello world")
	if msg != "hello world" {
		t.Errorf("message = %q, want %q", msg, "hello world")
	}
	expected := time.Date(2026, 2, 21, 18, 6, 48, 123456789, time.UTC)
	if !ts.Equal(expected) {
		t.Errorf("timestamp = %v, want %v", ts, expected)
	}

	// No timestamp — should return the full line as message
	ts2, msg2 := parseDockerTimestamp("no timestamp here")
	if msg2 != "no timestamp here" {
		t.Errorf("message = %q, want %q", msg2, "no timestamp here")
	}
	if time.Since(ts2) > time.Second {
		t.Errorf("expected timestamp close to now, got %v", ts2)
	}
}

func testDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	db, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	return db, func() { db.Close() }
}

func TestListContainers(t *testing.T) {
	containers := []dockerContainer{
		{ID: "abc123", Names: []string{"/test-container"}},
		{ID: "def456", Names: []string{"/web-app"}},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/containers/json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(containers)
	}))
	defer server.Close()

	got, err := listContainers(server.Client(), server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(got))
	}
	if got[0].ID != "abc123" {
		t.Errorf("first container ID = %q, want %q", got[0].ID, "abc123")
	}
}

func TestListContainersError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "access denied")
	}))
	defer server.Close()

	_, err := listContainers(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
}

func TestFollowDockerLogs(t *testing.T) {
	db, cleanup := testDB(t)
	defer cleanup()

	now := time.Now().UTC()
	tsStr := now.Format(time.RFC3339Nano)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/containers/test-id/logs" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		// Verify query parameters
		q := r.URL.Query()
		if q.Get("follow") != "true" || q.Get("stdout") != "true" || q.Get("stderr") != "true" || q.Get("timestamps") != "true" {
			t.Errorf("unexpected query params: %v", q)
		}

		// Write stdout frame
		writeDockerFrame(w, 1, fmt.Sprintf("%s stdout message\n", tsStr))
		// Write stderr frame
		writeDockerFrame(w, 2, fmt.Sprintf("%s stderr message\n", tsStr))
	}))
	defer server.Close()

	followDockerLogs(server.Client(), server.URL, "test-id", "test-container", "docker-host", db)

	// Verify logs were inserted
	entries, err := QueryLogs(db, LogFilter{}, 100)
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(entries))
	}

	// Entries are returned newest first
	var stdout, stderr *LogEntry
	for i := range entries {
		if entries[i].Message == "stdout message" {
			stdout = &entries[i]
		}
		if entries[i].Message == "stderr message" {
			stderr = &entries[i]
		}
	}

	if stdout == nil {
		t.Fatal("stdout log entry not found")
	}
	if stdout.Severity != "info" {
		t.Errorf("stdout severity = %q, want %q", stdout.Severity, "info")
	}
	if stdout.Facility != "docker" {
		t.Errorf("stdout facility = %q, want %q", stdout.Facility, "docker")
	}
	if stdout.Host != "docker-host" {
		t.Errorf("stdout host = %q, want %q", stdout.Host, "docker-host")
	}
	if stdout.Tag != "test-container" {
		t.Errorf("stdout tag = %q, want %q", stdout.Tag, "test-container")
	}

	if stderr == nil {
		t.Fatal("stderr log entry not found")
	}
	if stderr.Severity != "err" {
		t.Errorf("stderr severity = %q, want %q", stderr.Severity, "err")
	}
}

func writeDockerFrame(w http.ResponseWriter, streamType byte, payload string) {
	header := make([]byte, 8)
	header[0] = streamType
	binary.BigEndian.PutUint32(header[4:8], uint32(len(payload)))
	w.Write(header)
	w.Write([]byte(payload))
}
