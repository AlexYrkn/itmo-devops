"""Small HTTP service used to exercise the observability stack."""

import asyncio
import json
import logging
import os
import time
import uuid
from contextvars import ContextVar

import httpx
from fastapi import FastAPI, Request
from fastapi.responses import PlainTextResponse
from opentelemetry import trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.trace import Status, StatusCode
from prometheus_client import CONTENT_TYPE_LATEST, Counter, Histogram, generate_latest

SERVICE_NAME = os.getenv("OTEL_SERVICE_NAME", "api")
request_count = Counter("http_requests_total", "HTTP requests", ["method", "path", "status"])
error_count = Counter("http_errors_total", "HTTP 5xx responses", ["path"])
request_duration = Histogram(
    "http_request_duration_seconds",
    "HTTP request duration",
    ["method", "path"],
    buckets=(0.05, 0.1, 0.25, 0.5, 1, 2, 3, 5, 10),
)
trace_id_context: ContextVar[str] = ContextVar("trace_id", default="-")


class JsonFormatter(logging.Formatter):
    def format(self, record: logging.LogRecord) -> str:
        return json.dumps(
            {
                "timestamp": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                "level": record.levelname.lower(),
                "message": record.getMessage(),
                "service": SERVICE_NAME,
                "trace_id": trace_id_context.get(),
            },
            ensure_ascii=False,
        )


handler = logging.StreamHandler()
handler.setFormatter(JsonFormatter())
logger = logging.getLogger("api")
logger.handlers = [handler]
logger.setLevel(logging.INFO)
logger.propagate = False

provider = TracerProvider(resource=Resource.create({"service.name": SERVICE_NAME}))
provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
trace.set_tracer_provider(provider)
tracer = trace.get_tracer(SERVICE_NAME)
app = FastAPI(title="Lab 2 observable API")
FastAPIInstrumentor.instrument_app(app, tracer_provider=provider)


@app.middleware("http")
async def observe_requests(request: Request, call_next):
    path = request.url.path
    started = time.perf_counter()
    active_span = trace.get_current_span()
    span_context = active_span.get_span_context()
    trace_id = f"{span_context.trace_id:032x}" if span_context.is_valid else "-"
    token = trace_id_context.set(trace_id)
    status = 500
    try:
        # This parent span keeps the trace id available to structured logs.
        with tracer.start_as_current_span(f"{request.method} {path}") as request_span:
            ctx = request_span.get_span_context()
            trace_id_context.set(f"{ctx.trace_id:032x}")
            try:
                response = await call_next(request)
                status = response.status_code
                if status >= 500:
                    request_span.set_status(Status(StatusCode.ERROR))
                return response
            except Exception as exc:
                request_span.record_exception(exc)
                request_span.set_status(Status(StatusCode.ERROR, str(exc)))
                logger.exception("request failed")
                raise
            finally:
                elapsed = time.perf_counter() - started
                request_count.labels(request.method, path, str(status)).inc()
                request_duration.labels(request.method, path).observe(elapsed)
                if status >= 500:
                    error_count.labels(path).inc()
                logger.info("request completed method=%s path=%s status=%s duration_seconds=%.3f", request.method, path, status, elapsed)
    finally:
        trace_id_context.reset(token)


@app.get("/health")
async def health():
    return PlainTextResponse("ok")


@app.get("/fail")
async def fail():
    span = trace.get_current_span()
    span.set_status(Status(StatusCode.ERROR, "intentional failure"))
    span.record_exception(RuntimeError("intentional failure"))
    return PlainTextResponse("intentional server error", status_code=500)


@app.get("/slow")
async def slow():
    with tracer.start_as_current_span("slow-op") as span:
        span.set_attribute("operation", "intentional-delay")
        await asyncio.sleep(2)
    return PlainTextResponse("slow response")


@app.get("/load")
async def load():
    async with httpx.AsyncClient(base_url="http://127.0.0.1:8000", timeout=10) as client:
        responses = await asyncio.gather(*(client.get("/health") for _ in range(40)))
    return {"requests_generated": len(responses), "successes": sum(r.status_code == 200 for r in responses)}


@app.get("/metrics")
async def metrics():
    return PlainTextResponse(generate_latest(), media_type=CONTENT_TYPE_LATEST)


@app.post("/alerts")
async def receive_alerts(request: Request):
    payload = await request.json()
    logger.warning("alertmanager webhook received alerts=%s", len(payload.get("alerts", [])))
    return {"received": len(payload.get("alerts", [])), "id": str(uuid.uuid4())}
