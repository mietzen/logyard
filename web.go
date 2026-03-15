package main

import (
	"database/sql"
	"embed"
	"encoding/json"
	"html/template"
	"io"
	"log"
	"net/http"
	"strconv"
)

//go:embed web/index.html
var indexHTML string

//go:embed web/htmx.min.js
var htmxJS []byte

var rowsTemplate = template.Must(template.New("rows").Parse(`
{{- range . -}}
<tr class="sev-{{.Severity}}" data-host="{{.Host}}" data-facility="{{.Facility}}" data-severity="{{.Severity}}" data-tag="{{.Tag}}" data-message="{{.Message}}">
  <td>{{.Timestamp.Format "2006-01-02 15:04:05"}}</td>
  <td>{{.Host}}</td>
  <td>{{.Facility}}</td>
  <td>{{.Severity}}</td>
  <td>{{.Tag}}</td>
  <td class="msg-cell">{{.Message}}</td>
  <td class="action-cell"></td>
</tr>
{{- end -}}
`))

// Suppress unused import for embed.
var _ embed.FS

// EditableConfig is the subset of Config exposed via the settings API.
type EditableConfig struct {
	SMTP            SMTPConfig            `json:"smtp"`
	Alerts          []AlertRule           `json:"alerts"`
	Ignore          []IgnoreRule          `json:"ignore"`
	SeverityRewrite []SeverityRewriteRule `json:"severity_rewrite"`
	Retention       int                   `json:"retention"`
	Digest          DigestConfig          `json:"digest"`
	Debug           bool                  `json:"debug"`
	URL             string                `json:"url"`
}

func StartWeb(addr string, db *sql.DB, cm *ConfigManager) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(indexHTML))
	})

	mux.HandleFunc("/static/htmx.min.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(htmxJS)
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(); err != nil {
			http.Error(w, "db unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filter := LogFilter{
			Host:     q.Get("host"),
			Facility: q.Get("facility"),
			Severity: q.Get("severity"),
			Tag:      q.Get("tag"),
			Search:   q.Get("search"),
			Since:    q.Get("since"),
			Until:    q.Get("until"),
		}

		limit := 200
		if v := q.Get("limit"); v != "" {
			if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
				limit = parsed
			}
		}
		if filter.Since != "" && filter.Until != "" {
			limit = 0
		}

		entries, err := QueryLogs(db, filter, limit)
		if err != nil {
			log.Printf("query error: %v", err)
			http.Error(w, "query failed", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := rowsTemplate.Execute(w, entries); err != nil {
			log.Printf("template error: %v", err)
		}
	})

	mux.HandleFunc("/api/filters", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filters := map[string]string{
			"host":     q.Get("host"),
			"facility": q.Get("facility"),
			"severity": q.Get("severity"),
			"tag":      q.Get("tag"),
		}

		hosts, _ := DistinctValues(db, "host", filters)
		facilities, _ := DistinctValues(db, "facility", filters)
		severities, _ := DistinctValues(db, "severity", filters)
		tags, _ := DistinctValues(db, "tag", filters)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string][]string{
			"hosts":      hosts,
			"facilities": facilities,
			"severities": severities,
			"tags":       tags,
		})
	})

	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			cfg := cm.Get()
			ec := EditableConfig{
				SMTP:            cfg.SMTP,
				Alerts:          cfg.Alerts,
				Ignore:          cfg.Ignore,
				SeverityRewrite: cfg.SeverityRewrite,
				Retention:       cfg.Retention,
				Digest:          cfg.Digest,
				Debug:           cfg.Debug,
				URL:             cfg.URL,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(ec)

		case http.MethodPut:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read body failed", http.StatusBadRequest)
				return
			}
			var ec EditableConfig
			if err := json.Unmarshal(body, &ec); err != nil {
				http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
				return
			}

			cfg := cm.Get()
			cfg.SMTP = ec.SMTP
			cfg.Alerts = ec.Alerts
			cfg.Ignore = ec.Ignore
			cfg.SeverityRewrite = ec.SeverityRewrite
			cfg.Retention = ec.Retention
			cfg.Digest = ec.Digest
			cfg.Debug = ec.Debug
			cfg.URL = ec.URL

			if err := ValidateConfig(cfg); err != nil {
				log.Printf("config validation error: %v", err)
				http.Error(w, "invalid config: "+err.Error(), http.StatusBadRequest)
				return
			}

			if err := cm.Update(cfg); err != nil {
				http.Error(w, "save failed: "+err.Error(), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	log.Printf("Web UI listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}
