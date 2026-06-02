"""
Database configuration and session management
"""
from sqlalchemy.ext.asyncio import create_async_engine, AsyncSession, async_sessionmaker
from sqlalchemy.orm import declarative_base
import os

# Database configuration
DATABASE_URL = os.getenv(
    "DATABASE_URL",
    "postgresql+asyncpg://notification_user:notification_password@db:5432/notification_service"
)

# Create async engine.
# pool_pre_ping issues a lightweight liveness check before handing out a pooled
# connection, so a connection killed out-of-band (e.g. the LineSpec proxy calling
# ResetConnections between tests) is transparently detected and replaced instead of
# surfacing as a 500 on the next request.
engine = create_async_engine(DATABASE_URL, echo=True, pool_pre_ping=True)

# Create session factory
AsyncSessionLocal = async_sessionmaker(
    engine,
    class_=AsyncSession,
    expire_on_commit=False
)

# Base class for models
Base = declarative_base()


async def init_db():
    """Initialize database tables"""
    async with engine.begin() as conn:
        await conn.run_sync(Base.metadata.create_all)


async def get_db():
    """Dependency to get database session"""
    async with AsyncSessionLocal() as session:
        try:
            yield session
            await session.commit()
        except Exception:
            await session.rollback()
            raise
        finally:
            await session.close()
