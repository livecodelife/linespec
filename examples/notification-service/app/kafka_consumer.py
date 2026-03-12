"""
Kafka consumer for todo-events topic
"""
import asyncio
import json
import logging
from aiokafka import AIOKafkaConsumer
from sqlalchemy import insert
import os

from app.database import AsyncSessionLocal
from app.models import Notification
from app.auth import get_user_by_id

logger = logging.getLogger(__name__)

# Kafka configuration
KAFKA_BROKERS = os.getenv("KAFKA_BROKERS", "kafka:29092")
KAFKA_TOPIC = os.getenv("KAFKA_TOPIC", "todo-events")
KAFKA_GROUP_ID = os.getenv("KAFKA_GROUP_ID", "notification-service-group")

consumer = None


async def start_consumer():
    """Start the Kafka consumer"""
    global consumer
    
    logger.info(f"Starting Kafka consumer for topic: {KAFKA_TOPIC}")
    
    consumer = AIOKafkaConsumer(
        KAFKA_TOPIC,
        bootstrap_servers=KAFKA_BROKERS,
        group_id=KAFKA_GROUP_ID,
        value_deserializer=lambda m: json.loads(m.decode('utf-8')),
        auto_offset_reset='latest'
    )
    
    await consumer.start()
    
    try:
        # Consume messages
        async for msg in consumer:
            await process_message(msg)
    finally:
        await consumer.stop()


async def stop_consumer():
    """Stop the Kafka consumer"""
    global consumer
    if consumer:
        logger.info("Stopping Kafka consumer...")
        await consumer.stop()


async def process_message(msg):
    """Process a Kafka message"""
    try:
        event_data = msg.value
        event_type = event_data.get("event_type")
        
        logger.info(f"Received event: {event_type}")
        
        if event_type == "todo_created":
            await handle_todo_created(event_data)
        else:
            logger.info(f"Ignoring event type: {event_type}")
            
    except Exception as e:
        logger.error(f"Error processing message: {e}")


async def handle_todo_created(event_data, db_session=None):
    """
    Handle todo_created event
    
    1. Extract todo data from event
    2. Fetch user details from user-service
    3. Create notification record
    
    Args:
        event_data: Dictionary containing event data
        db_session: Optional database session (for testing). If None, creates new session.
    """
    try:
        todo_id = event_data.get("todo_id")
        title = event_data.get("title")
        user_id = event_data.get("user_id")
        
        logger.info(f"Processing todo_created event: todo_id={todo_id}, user_id={user_id}")
        
        # Fetch user details from user-service
        user = await get_user_by_id(user_id)
        if not user:
            logger.error(f"User not found: {user_id}")
            return
        
        # Create notification content
        content = f"{title} created"
        recipient = user.get("email")
        
        # Store in database (use provided session or create new one)
        if db_session:
            notification = Notification(
                content=content,
                recipient=recipient
            )
            db_session.add(notification)
            await db_session.flush()
            logger.info(f"Created notification: id={notification.id}, recipient={recipient}, content={content}")
        else:
            async with AsyncSessionLocal() as session:
                notification = Notification(
                    content=content,
                    recipient=recipient
                )
                session.add(notification)
                await session.commit()
                logger.info(f"Created notification: id={notification.id}, recipient={recipient}, content={content}")
            
    except Exception as e:
        logger.error(f"Error handling todo_created event: {e}")
