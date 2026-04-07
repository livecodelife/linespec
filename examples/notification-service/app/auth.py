"""
Authentication module - validates tokens with user-service.

Token verification checks Redis first (auth:cache:<token>) before
making an HTTP call to user-service, reducing latency for repeated
requests with the same token.
"""
import os
import httpx
from typing import Optional, Dict, Any

from app.cache import get_cached_user, set_cached_user

USER_SERVICE_URL = os.getenv(
    "USER_SERVICE_URL",
    "http://user-service.local:3001/api/v1/users/auth"
)


async def verify_token(authorization: str) -> Optional[Dict[str, Any]]:
    """
    Verify JWT token with user-service.

    Checks Redis cache first. On a cache miss, calls user-service and
    caches the result for subsequent requests.

    Args:
        authorization: Bearer token from Authorization header

    Returns:
        User dict with id, email, name if valid, None otherwise
    """
    # Extract the raw token from "Bearer <token>"
    token = authorization.removeprefix("Bearer ").strip()

    # Cache hit — skip the HTTP round-trip.
    cached = await get_cached_user(token)
    if cached is not None:
        return cached

    try:
        async with httpx.AsyncClient() as client:
            response = await client.get(
                USER_SERVICE_URL,
                headers={"Authorization": authorization},
                timeout=5.0
            )

            if response.status_code == 200:
                user = response.json()
                await set_cached_user(token, user)
                return user
            return None
    except Exception:
        return None


async def get_user_by_id(user_id: int) -> Optional[Dict[str, Any]]:
    """
    Fetch user details by ID from user-service.

    Args:
        user_id: User ID to fetch

    Returns:
        User dict with id, email, name if found, None otherwise
    """
    service_token = os.getenv("SERVICE_TOKEN", "service_token_xyz789")
    try:
        base_url = USER_SERVICE_URL.replace("/api/v1/users/auth", "")
        async with httpx.AsyncClient() as client:
            response = await client.get(
                f"{base_url}/api/v1/users/{user_id}",
                headers={"Authorization": f"Bearer {service_token}"},
                timeout=5.0
            )

            if response.status_code == 200:
                return response.json()
            return None
    except Exception:
        return None


async def get_current_user(authorization: Optional[str] = None):
    """Dependency to get current authenticated user"""
    if not authorization:
        return None
    return await verify_token(authorization)
