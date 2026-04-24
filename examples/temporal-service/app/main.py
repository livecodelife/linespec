"""
Temporal Client Service - demonstrates gRPC proxy features:
1. Content-type echo (application/grpc+json for mocked calls)
2. Protobuf descriptor mocks (binary wire format for typed calls)
3. Upstream passthrough (forwards unmocked calls to real upstream)
"""
import json
import logging
import os
import struct
from typing import Optional, Dict, Any

import httpx
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

WORKFLOW_HOST = os.getenv("GRPC_HOST", os.getenv("WORKFLOW_SERVICE_HOST", "grpc-proxy"))
WORKFLOW_PORT = int(os.getenv("GRPC_PORT", os.getenv("WORKFLOW_SERVICE_PORT", "50051")))
WORKFLOW_CONTENT_TYPE = os.getenv("WORKFLOW_CONTENT_TYPE", "application/grpc+json")


class StartWorkflowReq(BaseModel):
    workflow_id: str
    task_queue: str = "default"
    input_data: str = ""


class StartWorkflowResp(BaseModel):
    run_id: str


class GetResultReq(BaseModel):
    workflow_id: str
    run_id: str = ""


class GetResultResp(BaseModel):
    result: str
    completed: bool


class SignalWorkflowReq(BaseModel):
    workflow_id: str
    signal_name: str = "default-signal"
    input_data: str = ""


app = FastAPI(title="Temporal Client Service", version="1.0.0")


def _encode_grpc_frame(body: bytes) -> bytes:
    return struct.pack(">BI", 0, len(body)) + body


def _decode_grpc_frame(data: bytes) -> bytes:
    if len(data) < 5:
        return data
    return data[5:]


def _grpc_call(service_method: str, payload: bytes, content_type: str) -> Dict[str, Any]:
    url = f"http://{WORKFLOW_HOST}:{WORKFLOW_PORT}/{service_method}"
    body = _encode_grpc_frame(payload)
    with httpx.Client(http2=True) as client:
        response = client.post(
            url,
            content=body,
            headers={
                "Content-Type": content_type,
                "Te": "trailers",
            },
            timeout=5.0,
        )
        grpc_status = response.headers.get("grpc-status", "0")
        if grpc_status != "0":
            raise HTTPException(
                status_code=502,
                detail=f"gRPC error {grpc_status}: {response.headers.get('grpc-message', '')}",
            )
        return json.loads(_decode_grpc_frame(response.content))


def _grpc_call_empty(service_method: str, payload: bytes, content_type: str) -> None:
    url = f"http://{WORKFLOW_HOST}:{WORKFLOW_PORT}/{service_method}"
    body = _encode_grpc_frame(payload)
    with httpx.Client(http2=True) as client:
        response = client.post(
            url,
            content=body,
            headers={
                "Content-Type": content_type,
                "Te": "trailers",
            },
            timeout=5.0,
        )
        grpc_status = response.headers.get("grpc-status", "0")
        if grpc_status != "0":
            raise HTTPException(
                status_code=502,
                detail=f"gRPC error {grpc_status}: {response.headers.get('grpc-message', '')}",
            )


@app.get("/health")
async def health_check():
    return {"status": "healthy"}


@app.post("/start", response_model=StartWorkflowResp)
async def start_workflow(req: StartWorkflowReq):
    payload = json.dumps({
        "workflow_id": req.workflow_id,
        "task_queue": req.task_queue,
        "input": req.input_data.encode().decode("latin-1"),
    }).encode()
    result = _grpc_call("workflow.v1.WorkflowService/StartWorkflow", payload, WORKFLOW_CONTENT_TYPE)
    return StartWorkflowResp(run_id=result["run_id"])


@app.post("/result", response_model=GetResultResp)
async def get_result(req: GetResultReq):
    payload = json.dumps({
        "workflow_id": req.workflow_id,
        "run_id": req.run_id,
    }).encode()
    result = _grpc_call("workflow.v1.WorkflowService/GetWorkflowResult", payload, WORKFLOW_CONTENT_TYPE)
    return GetResultResp(result=result.get("result", ""), completed=result.get("completed", False))


@app.post("/signal")
async def signal_workflow(req: SignalWorkflowReq):
    payload = json.dumps({
        "workflow_id": req.workflow_id,
        "signal_name": req.signal_name,
        "input": req.input_data.encode().decode("latin-1"),
    }).encode()
    _grpc_call_empty("workflow.v1.WorkflowService/SignalWorkflow", payload, WORKFLOW_CONTENT_TYPE)
    return {"status": "ok"}
