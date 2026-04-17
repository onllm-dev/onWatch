"""Shared helper for writing normalized Custom API Integrations usage events to onWatch.

How this works:
1. Your script makes a normal provider API call.
2. Your script extracts usage fields from the provider response.
3. Your script calls `append_usage_event(...)`.
4. This helper appends one JSON line to `~/.onwatch/api-integrations/<integration>.jsonl`
   unless `ONWATCH_API_INTEGRATIONS_DIR` overrides the directory.
5. The onWatch daemon tails that file and stores the event in SQLite.

The event schema written here is already normalised. onWatch validates and
stores it, but it does not need to understand the raw provider response.
"""

import json
import os
from datetime import datetime, timezone
from pathlib import Path


def _api_integrations_dir() -> Path:
    """Return the directory that onWatch tails for API integration usage events."""
    raw = os.getenv("ONWATCH_API_INTEGRATIONS_DIR")
    if raw:
        return Path(raw).expanduser()
    return Path.home() / ".onwatch" / "api-integrations"


def append_usage_event(
    *,
    integration: str,
    provider: str,
    model: str,
    prompt_tokens: int,
    completion_tokens: int,
    total_tokens: int | None = None,
    account: str | None = None,
    request_id: str | None = None,
    cost_usd: float | None = None,
    latency_ms: int | None = None,
    metadata: dict | None = None,
    file_name: str | None = None,
) -> Path:
    """Append one normalized JSONL event and return the file path used.

    Required fields:
    - integration
    - provider
    - model
    - prompt_tokens
    - completion_tokens

    Optional fields are only written when present.
    """
    event = {
        "ts": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        "integration": integration,
        "provider": provider,
        "model": model,
        "prompt_tokens": int(prompt_tokens),
        "completion_tokens": int(completion_tokens),
    }
    if total_tokens is not None:
        event["total_tokens"] = int(total_tokens)
    if account:
        event["account"] = account
    if request_id:
        event["request_id"] = request_id
    if cost_usd is not None:
        event["cost_usd"] = float(cost_usd)
    if latency_ms is not None:
        event["latency_ms"] = int(latency_ms)
    if metadata:
        event["metadata"] = metadata

    api_integrations_dir = _api_integrations_dir()
    api_integrations_dir.mkdir(parents=True, exist_ok=True)
    target = api_integrations_dir / (file_name or f"{integration}.jsonl")
    with target.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(event, separators=(",", ":")))
        handle.write("\n")
    return target
