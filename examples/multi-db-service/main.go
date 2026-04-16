package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type CreateOrderRequest struct {
	ProductID  string `json:"product_id"`
	Quantity   int    `json:"quantity"`
	CustomerID string `json:"customer_id"`
}

type OrderResponse struct {
	ID          string `json:"id"`
	ProductID   string `json:"product_id"`
	Quantity    int    `json:"quantity"`
	CustomerID  string `json:"customer_id"`
	OrderStatus string `json:"order_status"`
	CreatedAt   string `json:"created_at"`
}

type OrderEvent struct {
	EventType  string `bson:"event_type" json:"event_type"`
	ProductID  string `bson:"product_id" json:"product_id"`
	Quantity   int    `bson:"quantity" json:"quantity"`
	CustomerID string `bson:"customer_id" json:"customer_id"`
}

type handler struct {
	db     *sql.DB
	events *mongo.Collection
	dbName string
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

func (h *handler) createOrder(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var req CreateOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Insert into MySQL orders table
	result, err := h.db.ExecContext(ctx,
		"INSERT INTO orders (product_id, quantity, customer_id, status) VALUES (?, ?, ?, ?)",
		req.ProductID, req.Quantity, req.CustomerID, "pending",
	)
	if err != nil {
		http.Error(w, "failed to create order", http.StatusInternalServerError)
		log.Printf("MySQL insert error: %v", err)
		return
	}

	orderID, _ := result.LastInsertId()

	// Update the order status to "processing" now that downstream systems are notified.
	// This UPDATE uses the lastInsertId from the INSERT — if the proxy returns 0 instead
	// of the real ID, this UPDATE will target the wrong row and the test will catch it.
	_, err = h.db.ExecContext(ctx,
		"UPDATE orders SET status = ? WHERE id = ?",
		"processing", orderID,
	)
	if err != nil {
		http.Error(w, "failed to update order status", http.StatusInternalServerError)
		log.Printf("MySQL update error: %v", err)
		return
	}

	// Insert event into MongoDB order_events collection
	event := bson.D{
		bson.E{Key: "event_type", Value: "order_created"},
		bson.E{Key: "product_id", Value: req.ProductID},
		bson.E{Key: "quantity", Value: req.Quantity},
		bson.E{Key: "customer_id", Value: req.CustomerID},
	}
	_, err = h.events.InsertOne(ctx, event)
	if err != nil {
		http.Error(w, "failed to record order event", http.StatusInternalServerError)
		log.Printf("MongoDB insert error: %v", err)
		return
	}

	resp := OrderResponse{
		ID:          fmt.Sprintf("order_%08d", orderID),
		ProductID:   req.ProductID,
		Quantity:    req.Quantity,
		CustomerID:  req.CustomerID,
		OrderStatus: "processing",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	writeJSON(w, http.StatusCreated, resp)
}

func main() {
	// MySQL connection — env vars injected by linespec runner:
	//   DB_HOST, DB_PORT, DB_USERNAME, DB_PASSWORD  (legacy unprefixed, first database)
	dbHost := os.Getenv("DB_HOST")
	if dbHost == "" {
		dbHost = "localhost"
	}
	dbPort := os.Getenv("DB_PORT")
	if dbPort == "" {
		dbPort = "3306"
	}
	dbUser := os.Getenv("DB_USERNAME")
	if dbUser == "" {
		dbUser = "root"
	}
	dbPass := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")
	if dbName == "" {
		dbName = "todo_api_development"
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true", dbUser, dbPass, dbHost, dbPort, dbName)
	mysqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("failed to open MySQL: %v", err)
	}
	defer mysqlDB.Close()

	// Retry until MySQL is ready
	for i := 0; i < 30; i++ {
		if err := mysqlDB.Ping(); err == nil {
			break
		}
		log.Printf("waiting for MySQL at %s:%s...", dbHost, dbPort)
		time.Sleep(time.Second)
	}
	if err := mysqlDB.Ping(); err != nil {
		log.Fatalf("MySQL not ready after 30s: %v", err)
	}
	log.Println("connected to MySQL")

	// MongoDB connection — env var injected by linespec runner:
	//   MONGO_MONGODB_URI  (prefixed by name "mongo", second database)
	mongoURI := os.Getenv("MONGO_MONGODB_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}
	mongoDB := os.Getenv("MONGO_DB_NAME")
	if mongoDB == "" {
		mongoDB = "order_events"
	}

	mongoClient, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Fatalf("failed to connect to MongoDB: %v", err)
	}
	defer mongoClient.Disconnect(context.Background())

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pingCancel()
	if err := mongoClient.Ping(pingCtx, nil); err != nil {
		log.Fatalf("failed to ping MongoDB: %v", err)
	}
	log.Println("connected to MongoDB")

	h := &handler{
		db:     mysqlDB,
		events: mongoClient.Database(mongoDB).Collection("order_events"),
		dbName: dbName,
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("POST /orders", h.createOrder)

	log.Printf("listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
