#!/usr/bin/env python3
"""
Generate 30 days of fake API Integrations JSONL telemetry for testing.

"""

from __future__ import annotations

import argparse
import json
import math
import random
import uuid
from collections import defaultdict
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from pathlib import Path


DEFAULT_OUTPUT = Path.home() / ".onwatch" / "api-integrations" / "pr-demo-seed.jsonl"


@dataclass(frozen=True)
class IntegrationSpec:
    name: str
    providers: tuple[str, ...]
    models: tuple[str, ...]
    base_calls_per_day: int
    prompt_range: tuple[int, int]
    completion_range: tuple[int, int]
    active_hours: tuple[int, int]
    weekday_boost: float = 1.0
    weekend_boost: float = 0.7
    metadata: dict | None = None


INTEGRATIONS: tuple[IntegrationSpec, ...] = (
    IntegrationSpec(
        name="model-train-fine-tune",
        providers=("openai", "openrouter"),
        models=("gpt-4.1", "gpt-4.1-mini", "openai/gpt-4.1"),
        base_calls_per_day=24,
        prompt_range=(1800, 7200),
        completion_range=(350, 1400),
        active_hours=(7, 22),
        weekday_boost=1.35,
        weekend_boost=0.45,
        metadata={"pipeline": "fine-tune", "team": "ml"},
    ),
    IntegrationSpec(
        name="email-bot",
        providers=("anthropic", "openai"),
        models=("claude-3-7-sonnet", "gpt-4.1-mini"),
        base_calls_per_day=36,
        prompt_range=(280, 1400),
        completion_range=(120, 600),
        active_hours=(6, 20),
        weekday_boost=1.15,
        weekend_boost=0.85,
        metadata={"category": "ops"},
    ),
    IntegrationSpec(
        name="notes-compactor",
        providers=("mistral", "anthropic"),
        models=("mistral-small-latest", "claude-3-5-haiku"),
        base_calls_per_day=18,
        prompt_range=(900, 3600),
        completion_range=(180, 900),
        active_hours=(8, 23),
        weekday_boost=0.95,
        weekend_boost=1.25,
        metadata={"category": "knowledge"},
    ),
    IntegrationSpec(
        name="support-triage",
        providers=("openrouter", "gemini"),
        models=("anthropic/claude-3.5-haiku", "gemini-2.5-flash"),
        base_calls_per_day=28,
        prompt_range=(420, 1900),
        completion_range=(140, 760),
        active_hours=(5, 21),
        weekday_boost=1.2,
        weekend_boost=0.75,
        metadata={"category": "support"},
    ),
    IntegrationSpec(
        name="invoice-reconciler",
        providers=("gemini", "openai"),
        models=("gemini-2.5-pro", "gpt-4.1-mini"),
        base_calls_per_day=10,
        prompt_range=(1100, 4800),
        completion_range=(160, 720),
        active_hours=(4, 18),
        weekday_boost=1.25,
        weekend_boost=0.3,
        metadata={"category": "finance"},
    ),
    IntegrationSpec(
        name="changelog-writer",
        providers=("anthropic", "mistral", "openrouter"),
        models=("claude-3-7-sonnet", "mistral-small-latest", "openai/gpt-4.1-mini"),
        base_calls_per_day=14,
        prompt_range=(650, 2200),
        completion_range=(260, 1100),
        active_hours=(9, 23),
        weekday_boost=1.05,
        weekend_boost=0.95,
        metadata={"category": "release"},
    ),
)


PROVIDER_COST_PER_1K = {
    "anthropic": (0.0030, 0.0150),
    "openai": (0.0020, 0.0080),
    "mistral": (0.0010, 0.0030),
    "openrouter": (0.0022, 0.0095),
    "gemini": (0.0013, 0.0050),
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Generate fake API Integrations JSONL data for onWatch screenshots.")
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT, help=f"Output JSONL path (default: {DEFAULT_OUTPUT})")
    parser.add_argument("--days", type=int, default=30, help="How many days of history to generate")
    parser.add_argument("--seed", type=int, default=42, help="Random seed for reproducible output")
    parser.add_argument("--overwrite", action="store_true", help="Overwrite the output file instead of appending")
    parser.add_argument("--account", default="demo", help="Account name to assign to generated events")
    return parser.parse_args()


def clamp(value: float, lower: float, upper: float) -> float:
    return max(lower, min(upper, value))


def hourly_intensity(ts: datetime, spec: IntegrationSpec) -> float:
    start_hour, end_hour = spec.active_hours
    hour = ts.hour + ts.minute / 60.0
    if hour < start_hour or hour > end_hour:
      return 0.08

    span = max(1.0, float(end_hour - start_hour))
    phase = (hour - start_hour) / span
    workday_curve = 0.55 + 0.75 * math.sin(math.pi * phase)
    weekday_factor = spec.weekday_boost if ts.weekday() < 5 else spec.weekend_boost
    weekly_wave = 0.92 + 0.18 * math.sin((ts.timetuple().tm_yday / 7.0) * math.pi)
    return clamp(workday_curve * weekday_factor * weekly_wave, 0.05, 2.2)


def campaign_multiplier(day_index: int, total_days: int, spec: IntegrationSpec) -> float:
    progress = day_index / max(1, total_days - 1)
    if spec.name == "model-train-fine-tune":
        return 0.85 + 0.95 * progress
    if spec.name == "support-triage":
        return 0.9 + 0.3 * math.sin(progress * math.pi * 3.0)
    if spec.name == "notes-compactor":
        return 0.8 + 0.35 * math.cos(progress * math.pi * 2.0)
    if spec.name == "invoice-reconciler":
        month_end_push = 1.0 + (0.8 if progress > 0.7 else 0.0)
        return month_end_push
    return 1.0


def estimate_cost(provider: str, prompt_tokens: int, completion_tokens: int) -> float | None:
    rates = PROVIDER_COST_PER_1K.get(provider)
    if not rates:
        return None
    prompt_rate, completion_rate = rates
    cost = (prompt_tokens / 1000.0) * prompt_rate + (completion_tokens / 1000.0) * completion_rate
    return round(cost, 6)


def jittered_timestamp(base: datetime, rng: random.Random) -> datetime:
    return base + timedelta(
        minutes=rng.randint(0, 58),
        seconds=rng.randint(0, 59),
    )


def choose_calls_for_hour(spec: IntegrationSpec, ts: datetime, day_index: int, total_days: int, rng: random.Random) -> int:
    baseline_per_hour = spec.base_calls_per_day / 24.0
    intensity = hourly_intensity(ts, spec)
    campaign = campaign_multiplier(day_index, total_days, spec)
    noise = rng.uniform(0.75, 1.35)
    expected = baseline_per_hour * intensity * campaign * noise
    floor = int(expected)
    remainder = expected - floor
    return floor + (1 if rng.random() < remainder else 0)


def generate_events(days: int, seed: int, account: str) -> list[dict]:
    rng = random.Random(seed)
    now = datetime.now(UTC).replace(minute=0, second=0, microsecond=0)
    start = now - timedelta(days=days)
    events: list[dict] = []
    provider_mix: dict[str, int] = defaultdict(int)

    for day_index in range(days):
        for hour in range(24):
            slot = start + timedelta(days=day_index, hours=hour)
            for spec in INTEGRATIONS:
                calls = choose_calls_for_hour(spec, slot, day_index, days, rng)
                if calls <= 0:
                    continue
                for _ in range(calls):
                    provider = rng.choice(spec.providers)
                    model_candidates = [m for m in spec.models if provider in m or "/" not in m]
                    model = rng.choice(model_candidates or spec.models)
                    prompt_tokens = rng.randint(*spec.prompt_range)
                    completion_tokens = rng.randint(*spec.completion_range)

                    # Periodic spikes for prettier graphs.
                    if spec.name in {"model-train-fine-tune", "notes-compactor"} and rng.random() < 0.08:
                        prompt_tokens = int(prompt_tokens * rng.uniform(1.4, 2.1))
                        completion_tokens = int(completion_tokens * rng.uniform(1.2, 1.8))
                    elif spec.name == "email-bot" and rng.random() < 0.12:
                        completion_tokens = int(completion_tokens * rng.uniform(1.3, 1.9))

                    total_tokens = prompt_tokens + completion_tokens
                    latency_ms = rng.randint(500, 4200)
                    timestamp = jittered_timestamp(slot, rng)
                    provider_mix[provider] += 1

                    metadata = dict(spec.metadata or {})
                    metadata["batch"] = "pr-demo"
                    metadata["env"] = "synthetic"

                    event = {
                        "ts": timestamp.isoformat().replace("+00:00", "Z"),
                        "integration": spec.name,
                        "provider": provider,
                        "account": account,
                        "model": model,
                        "request_id": f"demo-{uuid.uuid4().hex[:16]}",
                        "prompt_tokens": prompt_tokens,
                        "completion_tokens": completion_tokens,
                        "total_tokens": total_tokens,
                        "latency_ms": latency_ms,
                        "metadata": metadata,
                    }
                    cost = estimate_cost(provider, prompt_tokens, completion_tokens)
                    if cost is not None:
                        event["cost_usd"] = cost
                    events.append(event)

    events.sort(key=lambda item: item["ts"])
    return events


def main() -> None:
    args = parse_args()
    events = generate_events(days=args.days, seed=args.seed, account=args.account)

    args.output.parent.mkdir(parents=True, exist_ok=True)
    mode = "w" if args.overwrite else "a"
    with args.output.open(mode, encoding="utf-8") as handle:
        for event in events:
            handle.write(json.dumps(event, separators=(",", ":")))
            handle.write("\n")

    print(f"Wrote {len(events)} fake API integration events to {args.output}")
    print("Next steps:")
    print(f"  1. Keep onWatch running so it tails {args.output}")
    print("  2. Refresh the API Integrations tab after ingestion completes")
    print("  3. Use a new output filename if you want another seeded dataset without duplicate suppression")


if __name__ == "__main__":
    main()
