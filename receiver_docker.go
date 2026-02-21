package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type dockerContainer struct {
	ID    string   `json:"Id"`
	Names []string `json:"Names"`
}

type dockerEvent struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Actor  struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
}

func dockerHost(socketURL string) string {
	u, err := url.Parse(socketURL)
	if err != nil || u.Scheme == "unix" {
		return "localhost"
	}
	host := u.Hostname()
	if host == "" {
		return "localhost"
	}
	return host
}

func dockerHTTPClient(socketURL string) (*http.Client, string, error) {
	u, err := url.Parse(socketURL)
	if err != nil {
		return nil, "", fmt.Errorf("parsing socket URL: %w", err)
	}

	switch u.Scheme {
	case "unix":
		socketPath := u.Path
		return &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			},
		}, "http://localhost", nil
	case "tcp":
		return &http.Client{}, "http://" + u.Host, nil
	default:
		return nil, "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
}

func containerName(c dockerContainer) string {
	if len(c.Names) > 0 {
		return strings.TrimPrefix(c.Names[0], "/")
	}
	if len(c.ID) > 12 {
		return c.ID[:12]
	}
	return c.ID
}

func StartDockerReceiver(cfg DockerConfig, db *sql.DB) error {
	client, baseURL, err := dockerHTTPClient(cfg.Socket)
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}

	host := dockerHost(cfg.Socket)

	var tracked sync.Map

	startFollowing := func(id, name string) {
		if _, loaded := tracked.LoadOrStore(id, true); loaded {
			return
		}
		go func() {
			defer tracked.Delete(id)
			followDockerLogs(client, baseURL, id, name, host, db)
		}()
	}

	containers, err := listContainers(client, baseURL)
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}
	for _, c := range containers {
		name := containerName(c)
		startFollowing(c.ID, name)
		debugf("Docker: following container %s (%s)", name, c.ID[:12])
	}

	go watchDockerEvents(client, baseURL, startFollowing)

	log.Printf("Docker receiver started on %s (%d containers)", cfg.Socket, len(containers))
	return nil
}

func listContainers(client *http.Client, baseURL string) ([]dockerContainer, error) {
	resp, err := client.Get(baseURL + "/containers/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list containers: status %d: %s", resp.StatusCode, body)
	}

	var containers []dockerContainer
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil, fmt.Errorf("decoding container list: %w", err)
	}
	return containers, nil
}

func watchDockerEvents(client *http.Client, baseURL string, onStart func(id, name string)) {
	backoff := time.Second
	for {
		err := streamDockerEvents(client, baseURL, onStart)
		if err != nil {
			log.Printf("Docker events stream error: %v (retrying in %v)", err, backoff)
		}
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func streamDockerEvents(client *http.Client, baseURL string, onStart func(id, name string)) error {
	resp, err := client.Get(baseURL + "/events?filters=" + url.QueryEscape(`{"type":["container"],"event":["start"]}`))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("events: status %d: %s", resp.StatusCode, body)
	}

	decoder := json.NewDecoder(resp.Body)
	for {
		var event dockerEvent
		if err := decoder.Decode(&event); err != nil {
			return fmt.Errorf("decoding event: %w", err)
		}
		name := event.Actor.Attributes["name"]
		if name == "" {
			id := event.Actor.ID
			if len(id) > 12 {
				id = id[:12]
			}
			name = id
		}
		debugf("Docker: container started %s (%s)", name, event.Actor.ID[:12])
		onStart(event.Actor.ID, name)
	}
}

func followDockerLogs(client *http.Client, baseURL, containerID, name, host string, db *sql.DB) {
	since := fmt.Sprintf("%d", time.Now().Unix())
	logURL := fmt.Sprintf("%s/containers/%s/logs?follow=true&stdout=true&stderr=true&timestamps=true&since=%s",
		baseURL, containerID, since)

	resp, err := client.Get(logURL)
	if err != nil {
		debugf("Docker: failed to follow logs for %s: %v", name, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		debugf("Docker: log stream for %s returned status %d: %s", name, resp.StatusCode, body)
		return
	}

	reader := bufio.NewReader(resp.Body)
	header := make([]byte, 8)

	for {
		if _, err := io.ReadFull(reader, header); err != nil {
			if err != io.EOF {
				debugf("Docker: log stream ended for %s: %v", name, err)
			}
			return
		}

		streamType := header[0]
		frameSize := binary.BigEndian.Uint32(header[4:8])

		payload := make([]byte, frameSize)
		if _, err := io.ReadFull(reader, payload); err != nil {
			debugf("Docker: incomplete frame for %s: %v", name, err)
			return
		}

		severity := "info"
		if streamType == 2 {
			severity = "err"
		}

		line := strings.TrimRight(string(payload), "\n")

		ts, message := parseDockerTimestamp(line)

		if err := InsertLog(db, ts, host, "docker", severity, name, message); err != nil {
			log.Printf("Docker: insert error for %s: %v", name, err)
		}
	}
}

func parseDockerTimestamp(line string) (time.Time, string) {
	// Docker timestamp format: 2026-02-21T18:06:48.123456789Z <message>
	if idx := strings.IndexByte(line, ' '); idx > 0 {
		if ts, err := time.Parse(time.RFC3339Nano, line[:idx]); err == nil {
			return ts, line[idx+1:]
		}
	}
	return time.Now(), line
}
