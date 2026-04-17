"""Anthropic example for onWatch Custom API Integrations ingestion.

Use this file as a pattern for your own script.

What the user keeps:
- your normal provider API call
- your real prompt, model, and application logic

What the user adds:
- `from onwatch_api_integrations import append_usage_event`
- the `append_usage_event(...)` block after the response is returned

The two sections below are always:
- "Real API call" = your existing script logic
- "onWatch block to copy" = the part you append to your script
"""

import os

from anthropic import Anthropic

from onwatch_api_integrations import append_usage_event # the only import you need to add


def main() -> None:
    api_key = os.environ["ANTHROPIC_API_KEY"]

    # --- Real API call -----------------------------------------------------
    # Keep this part as your own real Anthropic request logic.
    client = Anthropic(api_key=api_key)
    response = client.messages.create(
        model="claude-haiku-4-5-20251001",
        max_tokens=128,
        messages=[{"role": "user", "content": "Summarize these notes in one sentence."}],
    )

    # --- onWatch block to copy --------------------------------------------
    # Add this block after your real API call returns.
    # Map the provider response usage fields into the normalised onWatch event.
    output_path = append_usage_event(
        integration="notes-organiser",
        provider="anthropic",
        model=response.model,
        prompt_tokens=response.usage.input_tokens,
        completion_tokens=response.usage.output_tokens,
        total_tokens=response.usage.input_tokens + response.usage.output_tokens,
        request_id=getattr(response, "id", None),
        metadata={"example": True},
    )

    print(f"Wrote onWatch event to: {output_path}")


if __name__ == "__main__":
    main()
