// Starter API — Go (net/http) on the platform's MySQL.
//
// The platform builds this with `go build` and runs the binary. It injects
// PORT (the port it routes to) and, because deployment.yaml declares
// `database: type: mysql`, the connection details as DATABASE_HOST /
// DATABASE_PORT / DATABASE_NAME / DATABASE_USER / DATABASE_PASSWORD.
// Nothing here is configured by hand.
package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-sql-driver/mysql"
)

var ready atomic.Bool

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func openDB() (*sql.DB, error) {
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = getenv("DATABASE_HOST", "127.0.0.1") + ":" + getenv("DATABASE_PORT", "3306")
	cfg.User = getenv("DATABASE_USER", "appuser")
	cfg.Passwd = os.Getenv("DATABASE_PASSWORD")
	cfg.DBName = getenv("DATABASE_NAME", "appdb")
	cfg.ParseTime = true
	return sql.Open("mysql", cfg.FormatDSN())
}

// initSchema keeps trying rather than exiting: the database can still be
// starting when the API does, and the API should be healthy either way.
func initSchema(db *sql.DB) {
	for {
		_, err := db.Exec(`CREATE TABLE IF NOT EXISTS items (
			id INT AUTO_INCREMENT PRIMARY KEY,
			title VARCHAR(200) NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
		if err == nil {
			ready.Store(true)
			return
		}
		log.Printf("database not ready yet: %v", err)
		time.Sleep(2 * time.Second)
	}
}

type item struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	db, err := openDB()
	if err != nil {
		log.Fatal(err)
	}
	go initSchema(db)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		state := "unavailable"
		if ready.Load() {
			state = "connected"
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "runtime": "go", "database": state})
	})
	mux.HandleFunc("GET /items", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.QueryContext(r.Context(), "SELECT id, title, created_at FROM items ORDER BY id")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		items := []item{}
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.ID, &it.Title, &it.CreatedAt); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			items = append(items, it)
		}
		writeJSON(w, http.StatusOK, items)
	})
	mux.HandleFunc("POST /items", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Title string `json:"title"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		title := strings.TrimSpace(body.Title)
		if len(title) > 200 {
			title = title[:200]
		}
		if title == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title is required"})
			return
		}
		res, err := db.ExecContext(r.Context(), "INSERT INTO items (title) VALUES (?)", title)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		id, _ := res.LastInsertId()
		writeJSON(w, http.StatusCreated, item{ID: id, Title: title, CreatedAt: time.Now().UTC()})
	})

	port := getenv("PORT", "8080")
	log.Printf("listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
