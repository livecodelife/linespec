"""
Redis cache for user data.

Tokens are cached under auth:cache:<token> with a TTL so that
the notification-service can serve most auth checks without
hitting user-service on every request.
"""
import json
import logging
import os
from typing import Optional, Dict, Any

import redis.asyncio as aioredis

logger = logging.getLogger(__name__)

REDIS_URL = os.getenv("REDIS_URL", "redis://localhost:6379")
CACHE_TTL = int(os.getenv("AUTH_CACHE_TTL", "300"))  # 5 minutes

_client: Optional[aioredis.Redis] = None


def _get_client() -> aioredis.Redis:
    global _client
    if _client is None:
        _client = aioredis.from_url(REDIS_URL, decode_responses=True)
    return _client


async def get_cached_user(token: str) -> Optional[Dict[str, Any]]:
    """Return cached user for the given auth token, or None on miss/error."""
    key = f"auth:cache:{token}"
    try:
        raw = await _get_client().get(key)
        if raw is None:
            return None
        return json.loads(raw)
    except Exception as exc:
        logger.warning("Redis cache GET failed for key %s: %s", key, exc)
        return None


async def set_cached_user(token: str, user: Dict[str, Any]) -> None:
    """Cache a user dict for the given auth token with CACHE_TTL seconds TTL."""
    key = f"auth:cache:{token}"
    try:
        await _get_client().set(key, json.dumps(user), ex=CACHE_TTL)
    except Exception as exc:
        logger.warning("Redis cache SET failed for key %s: %s", key, exc)
