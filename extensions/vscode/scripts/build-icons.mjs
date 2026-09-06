// Builds media/onwatch-icons.woff from media/icons/*.svg and syncs
// media/codepoints.json plus contributes.icons in package.json.
//
// Codepoints are assigned from U+E001 upward in alphabetical order of the SVG
// stem, so they stay stable across regenerations as long as no icon is removed
// from the middle of the alphabet. Stroke-only SVGs are converted to filled
// outlines first because svgicons2svgfont ignores strokes.
import { generateFonts } from "fantasticon";
import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import SVGFixer from "oslllo-svg-fixer";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const iconsDir = join(root, "media", "icons");
const mediaDir = join(root, "media");
const fontName = "onwatch-icons";
const FIRST_CODEPOINT = 0xe001;

const stems = readdirSync(iconsDir)
  .filter((f) => f.endsWith(".svg"))
  .map((f) => f.replace(/\.svg$/, ""))
  .sort();

const codepoints = Object.fromEntries(stems.map((stem, i) => [stem, FIRST_CODEPOINT + i]));

// 1. Normalise strokes to fills into a temp dir.
const fixedDir = mkdtempSync(join(tmpdir(), "onwatch-icons-"));
await SVGFixer(iconsDir, fixedDir, { showProgressBar: false, throwIfDestinationDoesNotExist: false }).fix();

// 2. Generate the woff plus a temporary SVG font used to verify glyph outlines.
const result = await generateFonts({
  inputDir: fixedDir,
  outputDir: mediaDir,
  name: fontName,
  fontTypes: ["woff", "svg"],
  assetTypes: [],
  codepoints,
  fontHeight: 1000,
  normalize: true,
  descent: 100,
  round: 10e3,
  getIconId: ({ basename }) => basename,
});

// 3. Verify every glyph has outline data.
const svgFont = readFileSync(join(mediaDir, `${fontName}.svg`), "utf8");
const empty = [];
for (const stem of stems) {
  const cp = codepoints[stem];
  const unicode = `&#x${cp.toString(16)};`;
  const re = new RegExp(`<glyph[^>]*unicode="${unicode}"[^>]*>`, "i");
  const match = svgFont.match(re);
  const d = match ? /\sd="([^"]*)"/.exec(match[0])?.[1] ?? "" : "";
  if (d.trim().length < 10) {
    empty.push(stem);
  }
}
rmSync(join(mediaDir, `${fontName}.svg`), { force: true });
rmSync(fixedDir, { recursive: true, force: true });

if (empty.length > 0) {
  console.error(`Glyphs with no outline data: ${empty.join(", ")}`);
  process.exit(1);
}

// 4. Write codepoints.json and sync package.json.
writeFileSync(join(mediaDir, "codepoints.json"), `${JSON.stringify(result.codepoints, null, 2)}\n`);

const pkgPath = join(root, "package.json");
const pkg = JSON.parse(readFileSync(pkgPath, "utf8"));
const label = (stem) =>
  ({
    all: "All providers",
    "api-integrations": "API Integrations",
    anthropic: "Anthropic",
    antigravity: "Antigravity",
    copilot: "GitHub Copilot",
    cursor: "Cursor",
    deepseek: "DeepSeek",
    gemini: "Gemini",
    grok: "Grok",
    kimi: "Kimi",
    minimax: "MiniMax",
    moonshot: "Moonshot",
    openai: "OpenAI",
    opencode: "OpenCode",
    openrouter: "OpenRouter",
    synthetic: "Synthetic",
    zai: "Z.ai",
  })[stem] ?? stem;
pkg.contributes ??= {};
pkg.contributes.icons = Object.fromEntries(
  stems.map((stem) => [
    `onwatch-${stem}`,
    {
      description: `onWatch ${label(stem)} mark`,
      default: { fontPath: `./media/${fontName}.woff`, fontCharacter: `\\${codepoints[stem].toString(16).toUpperCase()}` },
    },
  ]),
);
writeFileSync(pkgPath, `${JSON.stringify(pkg, null, 2)}\n`);

console.log(`Built media/${fontName}.woff with ${stems.length} glyphs: ${stems.join(", ")}`);
