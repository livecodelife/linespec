"""
gRPC+JSON client for user-service.

Communicates over HTTP/2 cleartext (h2c) using the gRPC+JSON wire format:
  POST /<package>.<Service>/<Method>
  Content-Type: application/grpc+json
  Body: 5-byte frame prefix (0x00 + 4-byte big-endian length) followed by JSON body

This matches the LineSpec gRPC proxy (P3-E) which speaks the same encoding,
so tests can intercept and assert on these calls without a compiled .proto.
"""
import json
import logging
import os
import struct
from typing import Optional, Dict, Any

import httpx

logger = logging.getLogger(__name__)

GRPC_HOST = os.getenv("GRPC_HOST", "user-service.local")
GRPC_PORT = int(os.getenv("GRPC_PORT", "50051"))


def _encode_grpc_frame(body: bytes) -> bytes:
    """Prepend the 5-byte gRPC frame header (compressed=0, length)."""
    return struct.pack(">BI", 0, len(body)) + body


def _decode_grpc_frame(data: bytes) -> bytes:
    """Strip the 5-byte gRPC frame header and return the body."""
    if len(data) < 5:
        return data
    return data[5:]


async def get_user(user_id: int) -> Optional[Dict[str, Any]]:
    """
    Call users.UserService/GetUser via gRPC+JSON.

    Returns the user dict on success, None if not found or on error.
    """
    url = f"http://{GRPC_HOST}:{GRPC_PORT}/users.UserService/GetUser"
    payload = json.dumps({"user_id": user_id}).encode()
    body = _encode_grpc_frame(payload)

    try:
        async with httpx.AsyncClient(http2=True) as client:
            response = await client.post(
                url,
                content=body,
                headers={
                    "Content-Type": "application/grpc+json",
                    "Te": "trailers",
                },
                timeout=5.0,
            )

        grpc_status = response.headers.get("grpc-status", "0")
        if grpc_status != "0":
            logger.error(
                "gRPC GetUser(%d) returned status %s: %s",
                user_id,
                grpc_status,
                response.headers.get("grpc-message", ""),
            )
            return None

        response_body = _decode_grpc_frame(response.content)
        return json.loads(response_body)

    except Exception as exc:
        logger.error("gRPC GetUser(%d) failed: %s", user_id, exc)
        return None
