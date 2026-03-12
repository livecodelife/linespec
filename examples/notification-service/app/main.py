"""
Notification Service - Main FastAPI Application
"""
from fastapi import FastAPI, Depends, HTTPException, Header, Request
from fastapi.responses import JSONResponse
from typing import Optional, List
from contextlib import asynccontextmanager
import asyncio
import logging

from app.database import init_db, get_db, AsyncSessionLocal
from app.models import Notification
from app.auth import verify_token, get_current_user, get_user_by_id
from app.kafka_consumer import start_consumer, stop_consumer, handle_todo_created

# Configure logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Application lifespan manager - startup and shutdown events"""
    # Startup
    logger.info("Starting up notification service...")
    await init_db()
    
    # Start Kafka consumer in background
    consumer_task = asyncio.create_task(start_consumer())
    
    yield
    
    # Shutdown
    logger.info("Shutting down notification service...")
    await stop_consumer()
    consumer_task.cancel()


app = FastAPI(
    title="Notification Service",
    description="Service for managing notifications from todo events",
    version="1.0.0",
    lifespan=lifespan
)


@app.get("/health")
async def health_check():
    """Health check endpoint"""
    return {"status": "healthy"}


@app.get("/api/v1/notifications")
async def list_notifications(
    authorization: Optional[str] = Header(None),
    db: AsyncSessionLocal = Depends(get_db)
):
    """List all notifications for the authenticated user"""
    if not authorization:
        raise HTTPException(status_code=401, detail="Authentication required")
    
    # Verify token with user-service
    user = await verify_token(authorization)
    if not user:
        raise HTTPException(status_code=401, detail="Invalid token")
    
    # Fetch notifications for this user (filter by recipient email)
    from sqlalchemy import select
    result = await db.execute(
        select(Notification)
        .where(Notification.recipient == user["email"])
        .order_by(Notification.created_at.desc())
    )
    notifications = result.scalars().all()
    
    return [
        {
            "id": n.id,
            "content": n.content,
            "recipient": n.recipient,
            "created_at": n.created_at.isoformat() if n.created_at else None,
            "updated_at": n.updated_at.isoformat() if n.updated_at else None,
        }
        for n in notifications
    ]


@app.get("/api/v1/notifications/{notification_id}")
async def get_notification(
    notification_id: int,
    authorization: Optional[str] = Header(None),
    db: AsyncSessionLocal = Depends(get_db)
):
    """Get a single notification by ID (must belong to authenticated user)"""
    if not authorization:
        raise HTTPException(status_code=401, detail="Authentication required")
    
    # Verify token with user-service
    user = await verify_token(authorization)
    if not user:
        raise HTTPException(status_code=401, detail="Invalid token")
    
    # Fetch notification (filter by ID AND recipient to ensure ownership)
    from sqlalchemy import select
    result = await db.execute(
        select(Notification)
        .where(Notification.id == notification_id)
        .where(Notification.recipient == user["email"])
    )
    notification = result.scalar_one_or_none()
    
    if not notification:
        raise HTTPException(status_code=404, detail="Notification not found")
    
    return {
        "id": notification.id,
        "content": notification.content,
        "recipient": notification.recipient,
        "created_at": notification.created_at.isoformat() if notification.created_at else None,
        "updated_at": notification.updated_at.isoformat() if notification.updated_at else None,
    }


@app.post("/api/v1/notifications/events")
async def process_event(
    event_data: dict,
    db: AsyncSessionLocal = Depends(get_db)
):
    """
    Process a todo event (used by test runner to simulate Kafka consumption)
    This endpoint handles todo_created events by fetching user details and storing notifications.
    """
    event_type = event_data.get("event_type")
    
    if event_type == "todo_created":
        await handle_todo_created(event_data, db)
        return {"status": "processed", "event_type": event_type}
    else:
        return {"status": "ignored", "event_type": event_type}


@app.exception_handler(HTTPException)
async def http_exception_handler(request: Request, exc: HTTPException):
    """Custom HTTP exception handler for consistent error responses"""
    return JSONResponse(
        status_code=exc.status_code,
        content={
            "error": exc.detail if exc.status_code != 401 else "Unauthorized",
            "message": exc.detail
        }
    )


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=3002)
