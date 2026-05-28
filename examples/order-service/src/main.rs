use axum::{
    extract::{State, Path},
    http::StatusCode,
    response::Json,
    routing::{get, post},
    Router,
};
use chrono::{DateTime, Utc};
use rust_decimal::Decimal;
use serde::{Deserialize, Serialize};
use std::net::SocketAddr;
use std::sync::Arc;
use tokio::sync::Mutex;
use tokio_postgres::{Client, NoTls, Row};
use tokio_postgres::types::ToSql;
use tracing::{info, error};
use uuid::Uuid;

#[derive(Clone)]
struct AppState {
    db: Arc<Mutex<Client>>,
    database_url: Arc<String>,
}

impl AppState {
    async fn reconnect(&self) -> Result<(), tokio_postgres::Error> {
        let (client, conn) = tokio_postgres::connect(&**self.database_url, NoTls).await?;
        tokio::spawn(async move { let _ = conn.await; });
        *self.db.lock().await = client;
        Ok(())
    }

    async fn execute(&self, sql: &str, params: &[&(dyn ToSql + Sync)]) -> Result<u64, tokio_postgres::Error> {
        let result = self.db.lock().await.execute(sql, params).await;
        match result {
            Err(ref e) if e.is_closed() => {
                self.reconnect().await?;
                self.db.lock().await.execute(sql, params).await
            }
            other => other,
        }
    }

    async fn query_one(&self, sql: &str, params: &[&(dyn ToSql + Sync)]) -> Result<Row, tokio_postgres::Error> {
        let result = self.db.lock().await.query_one(sql, params).await;
        match result {
            Err(ref e) if e.is_closed() => {
                self.reconnect().await?;
                self.db.lock().await.query_one(sql, params).await
            }
            other => other,
        }
    }

    async fn query(&self, sql: &str, params: &[&(dyn ToSql + Sync)]) -> Result<Vec<Row>, tokio_postgres::Error> {
        let result = self.db.lock().await.query(sql, params).await;
        match result {
            Err(ref e) if e.is_closed() => {
                self.reconnect().await?;
                self.db.lock().await.query(sql, params).await
            }
            other => other,
        }
    }
}

#[derive(Debug, Serialize, Deserialize)]
struct Order {
    id: Uuid,
    customer_name: String,
    total_amount: Decimal,
    status: String,
    created_at: DateTime<Utc>,
}

#[derive(Debug, Deserialize)]
struct CreateOrderRequest {
    customer_name: String,
    total_amount: Decimal,
}

#[derive(Debug, Serialize)]
struct CreateOrderResponse {
    id: Uuid,
    customer_name: String,
    total_amount: Decimal,
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

    state.execute(
        "INSERT INTO orders (id, customer_name, total_amount, status, created_at) VALUES ($1, $2, $3, $4, $5)",
        &[&id, &payload.customer_name, &payload.total_amount, &status, &now],
    ).await.map_err(|e| {
        error!("Failed to create order: {}", e);
        StatusCode::INTERNAL_SERVER_ERROR
    })?;

    info!("Order created successfully: {}", id);
    Ok((StatusCode::CREATED, Json(CreateOrderResponse {
        id,
        customer_name: payload.customer_name,
        total_amount: payload.total_amount,
        status,
        created_at: now,
    })))
}

async fn get_order(
    State(state): State<AppState>,
    Path(id): Path<Uuid>,
) -> Result<Json<CreateOrderResponse>, StatusCode> {
    info!("Fetching order: {}", id);

    let row = state.query_one(
        "SELECT id, customer_name, total_amount, status, created_at FROM orders WHERE id = $1",
        &[&id],
    ).await.map_err(|_| StatusCode::NOT_FOUND)?;

    Ok(Json(CreateOrderResponse {
        id: row.get(0),
        customer_name: row.get(1),
        total_amount: row.get(2),
        status: row.get(3),
        created_at: row.get(4),
    }))
}

async fn cancel_order(
    State(state): State<AppState>,
    Path(id): Path<Uuid>,
) -> Result<Json<CreateOrderResponse>, StatusCode> {
    info!("Cancelling order: {}", id);

    state.execute("BEGIN", &[]).await
        .map_err(|_| StatusCode::INTERNAL_SERVER_ERROR)?;

    let row = state.query_one(
        "SELECT id, customer_name, status, created_at FROM orders WHERE id = $1",
        &[&id],
    ).await.map_err(|_| StatusCode::NOT_FOUND)?;

    state.execute(
        "UPDATE orders SET status = 'cancelled' WHERE id = $1",
        &[&id],
    ).await.map_err(|_| StatusCode::INTERNAL_SERVER_ERROR)?;

    state.execute("COMMIT", &[]).await
        .map_err(|_| StatusCode::INTERNAL_SERVER_ERROR)?;

    info!("Order {} cancelled successfully", id);
    Ok(Json(CreateOrderResponse {
        id: row.get(0),
        customer_name: row.get(1),
        total_amount: rust_decimal::Decimal::ZERO,
        status: "cancelled".to_string(),
        created_at: row.get(3),
    }))
}

async fn list_orders(
    State(state): State<AppState>,
) -> Result<Json<Vec<CreateOrderResponse>>, StatusCode> {
    info!("Listing all orders");

    let rows = state.query(
        "SELECT id, customer_name, total_amount, status, created_at FROM orders",
        &[],
    ).await.map_err(|_| StatusCode::INTERNAL_SERVER_ERROR)?;

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

async fn connect_to_db(database_url: &str) -> Result<Client, Box<dyn std::error::Error>> {
    info!("Connecting to database");
    let (client, connection) = tokio_postgres::connect(database_url, NoTls).await?;
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
    tracing_subscriber::fmt()
        .with_env_filter(tracing_subscriber::EnvFilter::new("info"))
        .init();

    info!("Starting order-service");

    let database_url = std::env::var("DATABASE_URL")
        .unwrap_or_else(|_| "postgresql://order_user:order_password@localhost:5432/order_service".to_string());

    let client = connect_to_db(&database_url).await.expect("Failed to connect to database");
    let state = AppState {
        db: Arc::new(Mutex::new(client)),
        database_url: Arc::new(database_url),
    };

    let app = Router::new()
        .route("/health", get(health_check))
        .route("/orders", post(create_order).get(list_orders))
        .route("/orders/:id", get(get_order))
        .route("/orders/:id/cancel", post(cancel_order))
        .layer(tower_http::trace::TraceLayer::new_for_http())
        .with_state(state);

    let port = std::env::var("PORT")
        .ok()
        .and_then(|p| p.parse().ok())
        .unwrap_or(3000);

    let addr = SocketAddr::from(([0, 0, 0, 0], port));
    info!("Listening on {}", addr);

    hyper::Server::bind(&addr)
        .serve(app.into_make_service())
        .await
        .unwrap();
}
