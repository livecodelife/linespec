"""
Authentication module - validates tokens with user-service
"""
import os
import httpx
from typing import Optional, Dict, Any

USER_SERVICE_URL = os.getenv(
    "USER_SERVICE_URL",
    "http://user-service.local:3001/api/v1/users/auth"
)


async def verify_token(authorization: str) -> Optional[Dict[str, Any]]:
    """
    Verify JWT token with user-service
    
    Args:
        authorization: Bearer token from Authorization header
        
    Returns:
        User dict with id, email, name if valid, None otherwise
    """
    try:
        async with httpx.AsyncClient() as client:
            response = await client.get(
                USER_SERVICE_URL,
                headers={"Authorization": authorization},
                timeout=5.0
            )
            
            if response.status_code == 200:
                return response.json()
            return None
    except Exception as e:
        # If user-service is unavailable, we could implement fallback logic
        # For now, return None to indicate auth failure
        return None


async def get_user_by_id(user_id: int) -> Optional[Dict[str, Any]]:
    """
    Fetch user details by ID from user-service
    
    Args:
        user_id: User ID to fetch
        
    Returns:
        User dict with id, email, name if found, None otherwise
    """
    service_token = os.getenv("SERVICE_TOKEN", "service_token_xyz789")
    try:
        # Extract base URL from USER_SERVICE_URL (remove /auth suffix)
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
    except Exception as e:
        return None


async def get_current_user(authorization: Optional[str] = None):
    """Dependency to get current authenticated user"""
    if not authorization:
        return None
    return await verify_token(authorization)
