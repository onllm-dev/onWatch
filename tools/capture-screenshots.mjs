#!/usr/bin/env node
// Playwright script to capture onWatch dashboard screenshots
// Usage: npx playwright test --config tools/capture-screenshots.mjs
//   or:  node tools/capture-screenshots.mjs  (requires playwright installed)

import { chromium } from 'playwright';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const SCREENSHOTS_DIR = join(__dirname, '..', 'docs', 'screenshots');
const BASE_URL = 'http://localhost:9211';
const USERNAME = 'admin';
const PASSWORD = process.env.ONWATCH_ADMIN_PASS || 'changeme';

// Providers to capture: { filename prefix, tab data-provider value }
const PROVIDERS = [
  { name: 'anthropic', tab: 'anthropic' },
  { name: 'synthetic', tab: 'synthetic' },
  { name: 'zai', tab: 'zai' },
  { name: 'copilot', tab: 'copilot' },
  { name: 'codex', tab: 'codex' },
  { name: 'antigravity', tab: 'antigravity' },
  { name: 'gemini', tab: 'gemini' },
  { name: 'all', tab: 'both' },
];

const THEMES = ['light', 'dark'];

const VIEWPORT = { width: 1280, height: 900 };

async function run() {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ viewport: VIEWPORT });
  const page = await context.newPage();

  // Login
  console.log('Logging in...');
  await page.goto(`${BASE_URL}/login`);
  await page.fill('#username', USERNAME);
  await page.fill('#password', PASSWORD);
  await page.click('button[type="submit"]');
  await page.waitForURL(`${BASE_URL}/`);
  console.log('Logged in successfully.');

  for (const provider of PROVIDERS) {
    // Skip providers whose tab doesn't exist on this instance
    const tabButton = page.locator(`button.provider-tab[data-provider="${provider.tab}"]`);
    if (await tabButton.count() === 0) {
      console.log(`Skipping provider: ${provider.name} (not available)`);
      continue;
    }

    // Click the provider tab
    console.log(`Switching to provider: ${provider.name}`);
    await tabButton.click();

    // Wait for content to load — quota cards or both-view to render
    await page.waitForTimeout(2000);

    for (const theme of THEMES) {
      // Set theme by evaluating JS directly (more reliable than clicking toggle)
      await page.evaluate((t) => {
        document.documentElement.setAttribute('data-theme', t);
        localStorage.setItem('onwatch-theme', t);
      }, theme);
      await page.waitForTimeout(500);

      // Scroll to top before capturing
      await page.evaluate(() => window.scrollTo(0, 0));
      await page.waitForTimeout(300);

      const filename = `${provider.name}-${theme}.png`;
      const filepath = join(SCREENSHOTS_DIR, filename);

      await page.screenshot({
        path: filepath,
        fullPage: true,
      });

      console.log(`  Captured: ${filename}`);
    }
  }

  // ── Menubar Screenshots ──
  console.log('\nCapturing menubar screenshots...');
  const MENUBAR_VIEWPORT = { width: 360, height: 600 };
  const menubarContext = await browser.newContext({ viewport: MENUBAR_VIEWPORT });
  const menubarPage = await menubarContext.newPage();
  await menubarPage.goto(`${BASE_URL}/menubar`);
  await menubarPage.waitForTimeout(2000);

  const MENUBAR_VIEWS = ['standard', 'detailed'];

  for (const view of MENUBAR_VIEWS) {
    // Set view mode
    await menubarPage.evaluate((v) => {
      localStorage.setItem('menubar_view_mode', v);
    }, view);
    await menubarPage.reload();
    await menubarPage.waitForTimeout(2000);

    for (const theme of THEMES) {
      await menubarPage.evaluate((t) => {
        document.documentElement.setAttribute('data-theme', t);
        localStorage.setItem('menubar_theme_mode', t);
      }, theme);
      await menubarPage.waitForTimeout(500);

      const filename = `menubar-${view}-${theme}.png`;
      const filepath = join(SCREENSHOTS_DIR, filename);
      await menubarPage.screenshot({ path: filepath });
      console.log(`  Captured: ${filename}`);
    }
  }

  await menubarContext.close();
  await browser.close();
  console.log('\nAll screenshots captured successfully!');
}

run().catch((err) => {
  console.error('Screenshot capture failed:', err);
  process.exit(1);
});
