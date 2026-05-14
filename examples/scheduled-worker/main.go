package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

type UserRecord struct {
	ID    int    `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type SyncResponse struct {
	Users []UserRecord `json:"users"`
}

var (
	db          *sql.DB
	usersAPIURL string
	webhookURL  string
)

func main() {
	usersAPIURL = getenv("USERS_API_URL", "http://users-api:8080")
	webhookURL = getenv("WEBHOOK_URL", "http://webhook-service:8080")

	intervalSecs, err := strconv.Atoi(getenv("SYNC_INTERVAL_SECONDS", "86400"))
	if err != nil || intervalSecs < 1 {
		intervalSecs = 86400
	}

	db, err = sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer db.Close()

	for i := 0; i < 10; i++ {
		if err = db.Ping(); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		log.Fatalf("db ping: %v", err)
	}

	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"healthy"}`)
		})
		log.Fatal(http.ListenAndServe(":8080", mux))
	}()

	log.Printf("Scheduler started, sync interval %ds", intervalSecs)
	ticker := time.NewTicker(time.Duration(intervalSecs) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if err := runNightlySync(); err != nil {
			log.Printf("nightly_sync error: %v", err)
		}
	}
}

func runNightlySync() error {
	resp, err := http.Get(usersAPIURL + "/api/users")
	if err != nil {
		return fmt.Errorf("fetch users: %w", err)
	}
	defer resp.Body.Close()

	var result SyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode users: %w", err)
	}

	synced := 0
	for _, u := range result.Users {
		_, err := db.Exec(`
			INSERT INTO users (id, email, name, synced_at)
			VALUES ($1, $2, $3, NOW())
			ON CONFLICT (id) DO UPDATE SET email = $2, name = $3, synced_at = NOW()`,
			u.ID, u.Email, u.Name,
		)
		if err != nil {
			log.Printf("upsert user %d: %v", u.ID, err)
			continue
		}
		synced++
	}

	body, _ := json.Marshal(map[string]interface{}{
		"event":        "nightly_sync_complete",
		"users_synced": synced,
		"synced_at":    time.Now().UTC().Format(time.RFC3339),
	})
	postResp, err := http.Post(webhookURL+"/api/sync-complete", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	postResp.Body.Close()

	log.Printf("nightly_sync complete: %d users synced", synced)
	return nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
