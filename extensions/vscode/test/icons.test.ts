import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { GLYPHS, hasGlyph, providerIcon, providerIconId } from "../src/icons";

const ROOT = join(__dirname, "..");

describe("providerIconId", () => {
  it("maps aliases to the daemon's icon stems", () => {
    expect(providerIconId("anthropic", "anthropic")).toBe("anthropic");
    expect(providerIconId("claude", "claude")).toBe("anthropic");
    expect(providerIconId("codex", "codex")).toBe("openai");
    expect(providerIconId("openai", "openai")).toBe("openai");
    expect(providerIconId("chatgpt", "chatgpt")).toBe("openai");
    expect(providerIconId("zai", "zai")).toBe("zai");
    expect(providerIconId("glm", "glm")).toBe("zai");
    expect(providerIconId("zhipu", "zhipu")).toBe("zai");
    expect(providerIconId("gemini", "gemini")).toBe("gemini");
    expect(providerIconId("google", "google")).toBe("gemini");
    expect(providerIconId("copilot", "copilot")).toBe("copilot");
    expect(providerIconId("github", "github")).toBe("copilot");
    expect(providerIconId("grok", "grok")).toBe("grok");
    expect(providerIconId("xai", "xai")).toBe("grok");
    expect(providerIconId("kimi", "kimi")).toBe("kimi");
    expect(providerIconId("moonshot", "moonshot")).toBe("moonshot");
    expect(providerIconId("both", "both")).toBe("all");
    expect(providerIconId("all", "all")).toBe("all");
    expect(providerIconId("api_integrations", "api_integrations")).toBe("api-integrations");
    expect(providerIconId("api-integrations", "api-integrations")).toBe("api-integrations");
  });

  it("uses the id directly when a glyph exists for it", () => {
    for (const stem of ["antigravity", "cursor", "deepseek", "minimax", "opencode", "openrouter", "synthetic"]) {
      expect(providerIconId(stem, stem)).toBe(stem);
    }
  });

  it("strips profile suffixes and falls back to base_provider", () => {
    expect(providerIconId("codex", "codex:work")).toBe("openai");
    expect(providerIconId("", "anthropic:personal")).toBe("anthropic");
    expect(providerIconId(undefined, "kimi:x")).toBe("kimi");
  });

  it("is case-insensitive and falls back to all for unknown ids", () => {
    expect(providerIconId("Anthropic", "Anthropic")).toBe("anthropic");
    expect(providerIconId("unknown-thing", "unknown-thing")).toBe("all");
    expect(providerIconId("", "")).toBe("all");
  });
});

describe("providerIcon", () => {
  it("renders a theme icon reference for known glyphs", () => {
    expect(providerIcon("anthropic", "anthropic")).toBe("$(onwatch-anthropic)");
    expect(providerIcon("codex", "codex:work")).toBe("$(onwatch-openai)");
    expect(providerIcon("nope", "nope")).toBe("$(onwatch-all)");
  });

  it("falls back to pulse only when the font has no glyph", () => {
    expect(hasGlyph("anthropic")).toBe(true);
    expect(hasGlyph("nonexistent")).toBe(false);
    expect(providerIcon("anthropic", "anthropic", new Set())).toBe("$(pulse)");
  });
});

describe("font assets stay in sync", () => {
  const codepoints = JSON.parse(readFileSync(join(ROOT, "media", "codepoints.json"), "utf8")) as Record<string, number>;
  const pkg = JSON.parse(readFileSync(join(ROOT, "package.json"), "utf8")) as { contributes: { icons: Record<string, { default: { fontPath: string; fontCharacter: string } }> } };

  it("GLYPHS matches media/codepoints.json", () => {
    expect([...GLYPHS].sort()).toEqual(Object.keys(codepoints).sort());
  });

  it("assigns stable codepoints from E001 in alphabetical order", () => {
    const stems = Object.keys(codepoints).sort();
    stems.forEach((stem, i) => expect(codepoints[stem]).toBe(0xe001 + i));
  });

  it("contributes one icon per glyph with the matching character", () => {
    for (const [stem, cp] of Object.entries(codepoints)) {
      const entry = pkg.contributes.icons[`onwatch-${stem}`];
      expect(entry, `missing contributes.icons entry for ${stem}`).toBeDefined();
      expect(entry.default.fontPath).toBe("./media/onwatch-icons.woff");
      expect(entry.default.fontCharacter).toBe(`\\${cp.toString(16).toUpperCase()}`);
    }
    expect(Object.keys(pkg.contributes.icons)).toHaveLength(Object.keys(codepoints).length);
  });

  it("has a source SVG for every glyph", () => {
    const svgs = readdirSync(join(ROOT, "media", "icons")).filter((f) => f.endsWith(".svg")).map((f) => f.replace(/\.svg$/, ""));
    for (const stem of GLYPHS) {
      expect(svgs).toContain(stem);
    }
  });
});
