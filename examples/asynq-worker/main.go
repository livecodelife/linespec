package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

type Job struct {
	Type     string `json:"type"`
	UserID   int    `json:"user_id"`
	Template string `json:"template,omitempty"`
	Month    string `json:"month,omitempty"`
}

var (
	bg         = context.Background()
	rdb        *redis.Client
	db         *sql.DB
	emailURL   string
	webhookURL string
)

const queueKey = "worker:jobs"

func main() {
	emailURL = getenv("EMAIL_SERVICE_URL", "http://email-service")
	webhookURL = getenv("WEBHOOK_URL", "http://webhook-service")

	rdb = redis.NewClient(&redis.Options{Addr: redisAddr(os.Getenv("REDIS_URL"))})

	var err error
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

	log.Printf("worker started, polling %s", queueKey)
	for {
		result, err := rdb.BRPop(bg, 5*time.Second, queueKey).Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			log.Printf("brpop: %v", err)
			continue
		}
		var job Job
		if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
			log.Printf("unmarshal: %v", err)
			continue
		}
		if err := processJob(job); err != nil {
			log.Printf("job %s error: %v", job.Type, err)
		}
	}
}

func processJob(job Job) error {
	switch job.Type {
	case "send_email":
		return handleSendEmail(job)
	case "generate_report":
		return handleGenerateReport(job)
	case "count_users":
		return handleCountUsers(job)
	default:
		return fmt.Errorf("unknown job type: %s", job.Type)
	}
}

func handleSendEmail(job Job) error {
	var email, name string
	err := db.QueryRow("SELECT email, name FROM users WHERE id = $1", job.UserID).Scan(&email, &name)
	if err != nil {
		return fmt.Errorf("lookup user %d: %w", job.UserID, err)
	}

	body, _ := json.Marshal(map[string]string{"to": email, "template": job.Template, "user_name": name})
	resp, err := http.Post(emailURL+"/api/send", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("send email: %w", err)
	}
	resp.Body.Close()

	_, err = db.Exec(
		"INSERT INTO email_log (user_id, template, recipient, sent_at) VALUES ($1, $2, $3, $4)",
		job.UserID, job.Template, email, time.Now(),
	)
	return err
}

func handleGenerateReport(job Job) error {
	rows, err := db.Query(
		"SELECT id, amount FROM orders WHERE user_id = $1 AND to_char(created_at, 'YYYY-MM') = $2",
		job.UserID, job.Month,
	)
	if err != nil {
		return fmt.Errorf("query orders: %w", err)
	}
	defer rows.Close()

	var totalOrders int
	var totalAmount float64
	for rows.Next() {
		var id int
		var amount float64
		rows.Scan(&id, &amount)
		totalOrders++
		totalAmount += amount
	}

	_, err = db.Exec(
		"INSERT INTO reports (user_id, month, total_orders, total_amount) VALUES ($1, $2, $3, $4)",
		job.UserID, job.Month, totalOrders, totalAmount,
	)
	if err != nil {
		return err
	}

	body, _ := json.Marshal(map[string]interface{}{
		"event": "report_generated", "user_id": job.UserID,
		"month": job.Month, "total_amount": totalAmount,
	})
	resp, err := http.Post(webhookURL+"/api/notify", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	resp.Body.Close()
	return nil
}

func handleCountUsers(_ Job) error {
	var count int64
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return fmt.Errorf("count users: %w", err)
	}

	body, _ := json.Marshal(map[string]interface{}{"event": "users_counted", "count": count})
	resp, err := http.Post(webhookURL+"/api/notify", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	resp.Body.Close()
	return nil
}

func redisAddr(url string) string {
	addr := strings.TrimPrefix(url, "redis://")
	if addr == "" {
		return "localhost:6379"
	}
	return addr
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
