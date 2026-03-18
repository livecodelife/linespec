use axum::{
    extract::{State, Path},
    http::StatusCode,
    response::Json,
    routing::{get, post},
    Router,
};
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use std::net::SocketAddr;
use std::sync::Arc;
use tokio_postgres::{Client, NoTls};
use tracing::{info, error};
use uuid::Uuid;

#[derive(Clone)]
struct AppState {
    db: Arc<Client>,
}

#[derive(Debug, Serialize, Deserialize)]
struct Order {
    id: Uuid,
    customer_name: String,
    total_amount: f64,
    status: String,
    created_at: DateTime<Utc>,
}

#[derive(Debug, Deserialize)]
struct CreateOrderRequest {
    customer_name: String,
    total_amount: f64,
}

#[derive(Debug, Serialize)]
struct CreateOrderResponse {
    id: Uuid,
    customer_name: String,
    total_amount: f64,
    status: String,
    created_at: DateTime<Utc>,
}

#[derive(Debug, Serialize)]
struct HealthResponse {
    status: String,
}

async fn health_check() -> Json<HealthResponse> {
    Json(HealthResponse {
        status: "healthy".to_string(),
    })
}

async fn create_order(
    State(state): State<AppState>,
    Json(payload): Json<CreateOrderRequest>,
) -> Result<(StatusCode, Json<CreateOrderResponse>), StatusCode> {
    info!("Creating order for customer: {}", payload.customer_name);
    
    let id = Uuid::new_v4();
    let now = Utc::now();
    let status = "pending".to_string();
    
    // Insert order into database
    let result = state.db.execute(
        "INSERT INTO orders (id, customer_name, total_amount, status, created_at) VALUES ($1, $2, $3, $4, $5)",
        &[&id, &payload.customer_name, &payload.total_amount, &status, &now],
    ).await;
    
    match result {
        Ok(_) => {
            info!("Order created successfully: {}", id);
            let response = CreateOrderResponse {
                id,
                customer_name: payload.customer_name,
                total_amount: payload.total_amount,
                status,
                created_at: now,
            };
            Ok((StatusCode::CREATED, Json(response)))
        }
        Err(e) => {
            error!("Failed to create order: {}", e);
            Err(StatusCode::INTERNAL_SERVER_ERROR)
        }
    }
}

async fn get_order(
    State(state): State<AppState>,
    Path(id): Path<Uuid>,
) -> Result<Json<CreateOrderResponse>, StatusCode> {
    info!("Fetching order: {}", id);
    
    let row = state.db.query_one(
        "SELECT id, customer_name, total_amount, status, created_at FROM orders WHERE id = $1",
        &[&id],
    ).await;
    
    match row {
        Ok(row) => {
            let order = CreateOrderResponse {
                id: row.get(0),
                customer_name: row.get(1),
                total_amount: row.get(2),
                status: row.get(3),
                created_at: row.get(4),
            };
            Ok(Json(order))
        }
        Err(_) => Err(StatusCode::NOT_FOUND),
    }
}

async fn list_orders(
    State(state): State<AppState>,
) -> Result<Json<Vec<CreateOrderResponse>>, StatusCode> {
    info!("Listing all orders");
    
    let rows = state.db.query(
        "SELECT id, customer_name, total_amount, status, created_at FROM orders",
        &[],
    ).await;
    
    match rows {
        Ok(rows) => {
            let orders: Vec<CreateOrderResponse> = rows.iter().map(|row| {
                CreateOrderResponse {
                    id: row.get(0),
                    customer_name: row.get(1),
                    total_amount: row.get(2),
                    status: row.get(3),
                    created_at: row.get(4),
                }
            }).collect();
            Ok(Json(orders))
        }
        Err(_) => Err(StatusCode::INTERNAL_SERVER_ERROR),
    }
}

async fn connect_to_db() -> Result<Client, Box<dyn std::error::Error>> {
    // Read database URL from environment
    let database_url = std::env::var("DATABASE_URL")
        .unwrap_or_else(|_| "postgresql://order_user:order_password@localhost:5432/order_service".to_string());
    
    info!("Connecting to database: {}", database_url.replace(|c: char| c.is_ascii_alphanumeric(), "*"));
    
    let (client, connection) = tokio_postgres::connect(&database_url, NoTls).await?;
    
    // Spawn connection task
    tokio::spawn(async move {
        if let Err(e) = connection.await {
            eprintln!("Connection error: {}", e);
        }
    });
    
    info!("Connected to database successfully");
    Ok(client)
}

#[tokio::main]
async fn main() {
    // Initialize tracing
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::new("info"))
        .init();
    
    info!("Starting order-service");
    
    // Connect to database
    let client = connect_to_db().await.expect("Failed to connect to database");
    let state = AppState {
        db: Arc::new(client),
    };
    
    // Build router
    let app = Router::new()
        .route("/health", get(health_check))
        .route("/orders", post(create_order).get(list_orders))
        .route("/orders/:id", get(get_order))
        .layer(tower_http::trace::TraceLayer::new_for_http())
        .with_state(state);
    
    // Get port from environment
    let port = std::env::var("PORT")
        .ok()
        .and_then(|p| p.parse().ok())
        .unwrap_or(3000);
    
    let addr = SocketAddr::from(([0, 0, 0, 0], port));
    info!("Listening on {}", addr);
    
    // For axum 0.6, use hyper server
    hyper::Server::bind(&addr)
        .serve(app.into_make_service())
        .await
        .unwrap();
}
