"""Gemini example for onWatch Custom API Integrations ingestion.

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

from google import genai

from onwatch_api_integrations import append_usage_event # the only import you need to add


def main() -> None:
    api_key = os.environ["GEMINI_API_KEY"]

    # --- Real API call -----------------------------------------------------
    # Keep this part as your own real Gemini request logic.
    client = genai.Client(api_key=api_key)
    response = client.models.generate_content(
        model="gemini-2.5-flash",
        contents="Summarize these notes in one sentence.",
    )

    # --- onWatch block to copy --------------------------------------------
    # Add this block after your real API call returns.
    # Map the provider response usage fields into the normalised onWatch event.
    usage = response.usage_metadata
    output_path = append_usage_event(
        integration="notes-organiser",
        provider="gemini",
        model="gemini-2.5-flash",
        prompt_tokens=usage.prompt_token_count,
        completion_tokens=usage.candidates_token_count,
        total_tokens=usage.total_token_count,
        metadata={"example": True},
    )

    print(f"Wrote onWatch event to: {output_path}")


if __name__ == "__main__":
    main()
