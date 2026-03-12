"""
SQLAlchemy models for notifications
"""
from sqlalchemy import Column, Integer, String, DateTime, func
from sqlalchemy.orm import declarative_base
from app.database import Base


class Notification(Base):
    """Notification model representing stored notifications"""
    __tablename__ = "notifications"
    
    id = Column(Integer, primary_key=True, index=True)
    content = Column(String(255), nullable=False)
    recipient = Column(String(255), nullable=False, index=True)
    created_at = Column(DateTime(timezone=True), server_default=func.now())
    updated_at = Column(DateTime(timezone=True), server_default=func.now(), onupdate=func.now())
    
    def __repr__(self):
        return f"<Notification(id={self.id}, content='{self.content}', recipient='{self.recipient}')>"
