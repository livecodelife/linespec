package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Product struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"id,omitempty"`
	Name      string             `bson:"name" json:"name"`
	SKU       string             `bson:"sku" json:"sku"`
	Price     float64            `bson:"price" json:"price"`
	Stock     int                `bson:"stock" json:"stock"`
	Category  string             `bson:"category" json:"category"`
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
}

type CreateProductRequest struct {
	Name     string  `json:"name"`
	SKU      string  `json:"sku"`
	Price    float64 `json:"price"`
	Stock    int     `json:"stock"`
	Category string  `json:"category"`
}

type handler struct {
	products *mongo.Collection
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

func (h *handler) listProducts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	cursor, err := h.products.Find(ctx, bson.D{})
	if err != nil {
		http.Error(w, "failed to query products", http.StatusInternalServerError)
		return
	}
	defer cursor.Close(ctx)

	var products []Product
	if err := cursor.All(ctx, &products); err != nil {
		http.Error(w, "failed to decode products", http.StatusInternalServerError)
		return
	}

	if products == nil {
		products = []Product{}
	}

	writeJSON(w, http.StatusOK, products)
}

func (h *handler) getProduct(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "invalid product id", http.StatusBadRequest)
		return
	}

	var product Product
	err = h.products.FindOne(ctx, bson.M{"_id": oid}).Decode(&product)
	if err == mongo.ErrNoDocuments {
		http.Error(w, "product not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to query product", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, product)
}

func (h *handler) createProduct(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var req CreateProductRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	product := Product{
		ID:        primitive.NewObjectID(),
		Name:      req.Name,
		SKU:       req.SKU,
		Price:     req.Price,
		Stock:     req.Stock,
		Category:  req.Category,
		CreatedAt: time.Now().UTC(),
	}

	_, err := h.products.InsertOne(ctx, product)
	if err != nil {
		http.Error(w, "failed to create product", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, product)
}

func (h *handler) deleteProduct(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	idStr := r.PathValue("id")
	oid, err := primitive.ObjectIDFromHex(idStr)
	if err != nil {
		http.Error(w, "invalid product id", http.StatusBadRequest)
		return
	}

	result, err := h.products.DeleteOne(ctx, bson.M{"_id": oid})
	if err != nil {
		http.Error(w, "failed to delete product", http.StatusInternalServerError)
		return
	}
	if result.DeletedCount == 0 {
		http.Error(w, "product not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func main() {
	mongoURI := os.Getenv("MONGODB_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Fatalf("failed to connect to MongoDB: %v", err)
	}
	defer client.Disconnect(context.Background())

	if err := client.Ping(ctx, nil); err != nil {
		log.Fatalf("failed to ping MongoDB: %v", err)
	}
	log.Println("connected to MongoDB")

	db := client.Database("catalog_service")
	h := &handler{products: db.Collection("products")}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /products", h.listProducts)
	mux.HandleFunc("GET /products/{id}", h.getProduct)
	mux.HandleFunc("POST /products", h.createProduct)
	mux.HandleFunc("DELETE /products/{id}", h.deleteProduct)

	log.Printf("listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
