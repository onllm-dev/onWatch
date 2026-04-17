"""OpenRouter example for onWatch Custom API Integrations ingestion.

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

from openai import OpenAI

from onwatch_api_integrations import append_usage_event # the only import you need to add


def main() -> None:
    api_key = os.environ["OPENROUTER_API_KEY"]

    # --- Real API call -----------------------------------------------------
    # Keep this part as your own real OpenRouter request logic.
    client = OpenAI(
        base_url="https://openrouter.ai/api/v1",
        api_key=api_key,
    )
    response = client.chat.completions.create(
        model="openai/gpt-4.1-mini",
        messages=[{"role": "user", "content": "Summarize these notes in one sentence."}],
    )

    # --- onWatch block to copy --------------------------------------------
    # Add this block after your real API call returns.
    # Map the provider response usage fields into the normalised onWatch event.
    usage = response.usage
    output_path = append_usage_event(
        integration="notes-organiser",
        provider="openrouter",
        model=response.model,
        prompt_tokens=usage.prompt_tokens,
        completion_tokens=usage.completion_tokens,
        total_tokens=usage.total_tokens,
        request_id=response.id,
        metadata={"example": True},
    )

    print(f"Wrote onWatch event to: {output_path}")


if __name__ == "__main__":
    main()
