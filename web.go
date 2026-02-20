package main

import (
	"database/sql"
	"embed"
	"html/template"
	"log"
	"net/http"
)

//go:embed web/index.html
var indexHTML string

//go:embed web/htmx.min.js
var htmxJS []byte

var rowsTemplate = template.Must(template.New("rows").Parse(`
{{- range . -}}
<tr class="sev-{{.Severity}}">
  <td>{{.Timestamp.Format "2006-01-02 15:04:05"}}</td>
  <td>{{.Host}}</td>
  <td>{{.Facility}}</td>
  <td>{{.Severity}}</td>
  <td>{{.Tag}}</td>
  <td>{{.Message}}</td>
</tr>
{{- end -}}
`))

// Suppress unused import for embed.
var _ embed.FS

func StartWeb(addr string, db *sql.DB) error {
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

	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		filter := LogFilter{
			Host:     q.Get("host"),
			Facility: q.Get("facility"),
			Severity: q.Get("severity"),
			Tag:      q.Get("tag"),
			Search:   q.Get("search"),
		}

		entries, err := QueryLogs(db, filter, 200)
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

	log.Printf("Web UI listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}
