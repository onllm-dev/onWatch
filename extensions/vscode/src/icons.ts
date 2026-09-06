// Provider mark lookup for the contributed "onwatch-icons" font. No "vscode" import.
// Keep GLYPHS in sync with media/codepoints.json (checked by test/icons.test.ts).

export const GLYPHS: ReadonlySet<string> = new Set([
  "all",
  "anthropic",
  "antigravity",
  "api-integrations",
  "copilot",
  "cursor",
  "deepseek",
  "gemini",
  "grok",
  "kimi",
  "minimax",
  "moonshot",
  "ollama",
  "openai",
  "opencode",
  "openrouter",
  "synthetic",
  "zai",
]);

const ALIASES: Record<string, string> = {
  claude: "anthropic",
  anthropic: "anthropic",
  openai: "openai",
  codex: "openai",
  chatgpt: "openai",
  glm: "zai",
  zhipu: "zai",
  zai: "zai",
  google: "gemini",
  gemini: "gemini",
  github: "copilot",
  copilot: "copilot",
  xai: "grok",
  grok: "grok",
  moonshot: "moonshot",
  ollama: "ollama",
  kimi: "kimi",
  both: "all",
  all: "all",
  api_integrations: "api-integrations",
  "api-integrations": "api-integrations",
};

function normalize(id: string | undefined): string {
  return (id ?? "").split(":")[0].trim().toLowerCase();
}

export function hasGlyph(stem: string, glyphs: ReadonlySet<string> = GLYPHS): boolean {
  return glyphs.has(stem);
}

/** Icon stem (file name without .svg) for a provider, mirroring the dashboard's mapping. */
export function providerIconId(baseProvider: string | undefined, providerId: string | undefined): string {
  for (const candidate of [normalize(providerId), normalize(baseProvider)]) {
    if (candidate === "") {
      continue;
    }
    const alias = ALIASES[candidate];
    if (alias) {
      return alias;
    }
    if (GLYPHS.has(candidate)) {
      return candidate;
    }
  }
  return "all";
}

/** `$(onwatch-<stem>)` for use in status bar text and theme-icon markdown; `$(pulse)` if the font lacks the glyph. */
export function providerIcon(baseProvider: string | undefined, providerId: string | undefined, glyphs: ReadonlySet<string> = GLYPHS): string {
  const stem = providerIconId(baseProvider, providerId);
  return hasGlyph(stem, glyphs) ? `$(onwatch-${stem})` : "$(pulse)";
}
