"""
Temporal Client Service - demonstrates gRPC proxy features:
1. Binary protobuf (application/grpc) WITH body matching via descriptor decode
2. Upstream passthrough (forwards unmocked calls to real upstream)
"""
import json
import logging
import os
import struct
from typing import Dict, Any

import httpx
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

from app import workflow_pb2

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


_USE_BINARY_PROTO = not WORKFLOW_CONTENT_TYPE.endswith("+json")


def _serialize_request(msg) -> bytes:
    """Serialize a protobuf message to the wire format matching WORKFLOW_CONTENT_TYPE."""
    if _USE_BINARY_PROTO:
        return msg.SerializeToString()
    return msg.SerializeToString() if False else json.dumps(_proto_to_dict(msg)).encode()


def _proto_to_dict(msg) -> dict:
    """Convert a simple protobuf message to a plain dict for JSON serialization."""
    result = {}
    for field in msg.DESCRIPTOR.fields:
        value = getattr(msg, field.name)
        if value or field.type == field.TYPE_BOOL:
            if isinstance(value, bytes):
                result[field.name] = value.decode("latin-1")
            else:
                result[field.name] = value
    return result


def _grpc_call(service_method: str, proto_msg, response_cls) -> Dict[str, Any]:
    url = f"http://{WORKFLOW_HOST}:{WORKFLOW_PORT}/{service_method}"
    if _USE_BINARY_PROTO:
        payload = proto_msg.SerializeToString()
    else:
        payload = json.dumps(_proto_to_dict(proto_msg)).encode()
    body = _encode_grpc_frame(payload)
    with httpx.Client(http2=True) as client:
        response = client.post(
            url,
            content=body,
            headers={
                "Content-Type": WORKFLOW_CONTENT_TYPE,
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
        raw = _decode_grpc_frame(response.content)
        if _USE_BINARY_PROTO:
            resp = response_cls()
            resp.ParseFromString(raw)
            return _proto_to_dict(resp)
        return json.loads(raw)


def _grpc_call_empty(service_method: str, proto_msg) -> None:
    url = f"http://{WORKFLOW_HOST}:{WORKFLOW_PORT}/{service_method}"
    if _USE_BINARY_PROTO:
        payload = proto_msg.SerializeToString()
    else:
        payload = json.dumps(_proto_to_dict(proto_msg)).encode()
    body = _encode_grpc_frame(payload)
    with httpx.Client(http2=True) as client:
        response = client.post(
            url,
            content=body,
            headers={
                "Content-Type": WORKFLOW_CONTENT_TYPE,
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
    msg = workflow_pb2.StartWorkflowRequest(
        workflow_id=req.workflow_id,
        task_queue=req.task_queue,
        input=req.input_data.encode(),
    )
    result = _grpc_call("workflow.v1.WorkflowService/StartWorkflow", msg, workflow_pb2.StartWorkflowResponse)
    return StartWorkflowResp(run_id=result["run_id"])


@app.post("/result", response_model=GetResultResp)
async def get_result(req: GetResultReq):
    msg = workflow_pb2.GetWorkflowResultRequest(
        workflow_id=req.workflow_id,
        run_id=req.run_id,
    )
    result = _grpc_call("workflow.v1.WorkflowService/GetWorkflowResult", msg, workflow_pb2.GetWorkflowResultResponse)
    return GetResultResp(result=result.get("result", ""), completed=result.get("completed", False))


@app.post("/signal")
async def signal_workflow(req: SignalWorkflowReq):
    msg = workflow_pb2.SignalWorkflowRequest(
        workflow_id=req.workflow_id,
        signal_name=req.signal_name,
        input=req.input_data.encode(),
    )
    _grpc_call_empty("workflow.v1.WorkflowService/SignalWorkflow", msg)
    return {"status": "ok"}
