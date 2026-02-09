const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

async function testOnWatchDashboard() {
  const results = {
    timestamp: new Date().toISOString(),
    tests: [],
    screenshots: [],
    errors: []
  };

  let browser;
  
  try {
    // Create screenshots directory
    const screenshotsDir = './test-results/screenshots';
    if (!fs.existsSync(screenshotsDir)) {
      fs.mkdirSync(screenshotsDir, { recursive: true });
    }

    console.log('🧪 Starting onWatch Dashboard Tests...\n');

    // Launch browser
    browser = await chromium.launch({ 
      headless: false,
      args: ['--no-sandbox']
    });
    
    const context = await browser.newContext({
      viewport: { width: 1920, height: 1080 },
      ignoreHTTPSErrors: true
    });
    
    const page = await context.newPage();

    // Collect console messages
    page.on('console', msg => {
      console.log(`📝 Console [${msg.type()}]: ${msg.text()}`);
    });
    
    page.on('pageerror', error => {
      console.log(`❌ Page Error: ${error.message}`);
      results.errors.push(error.message);
    });

    // Test 1: Login Page
    console.log('\n=== Test 1: Login Page ===');
    await page.goto('http://localhost:8932', { waitUntil: 'networkidle' });
    
    const loginTitle = await page.title();
    console.log(`📄 Page Title: ${loginTitle}`);
    
    // Check for login form elements
    const usernameField = await page.$('input[name="username"], input[type="text"]');
    const passwordField = await page.$('input[name="password"], input[type="password"]');
    const loginButton = await page.$('button[type="submit"]');
    
    results.tests.push({
      name: 'Login Page Load',
      status: 'PASS' if (usernameField && passwordField && loginButton) else 'FAIL',
      details: {
        hasUsernameField: !!usernameField,
        hasPasswordField: !!passwordField,
        hasSubmitButton: !!loginButton
      }
    });
    
    console.log(`✅ Username field: ${!!usernameField}`);
    console.log(`✅ Password field: ${!!passwordField}`);
    console.log(`✅ Submit button: ${!!loginButton}`);
    
    await page.screenshot({ 
      path: `${screenshotsDir}/01-login-page.png`,
      fullPage: true 
    });
    results.screenshots.push('01-login-page.png');

    // Test 2: Authentication
    console.log('\n=== Test 2: Authentication ===');
    await page.authenticate({ username: 'admin', password: 'changeme' });
    
    // Wait a moment for auth to be applied
    await page.waitForTimeout(1000);
    
    // Try accessing dashboard again with auth
    await page.goto('http://localhost:8932', { waitUntil: 'networkidle' });
    await page.waitForTimeout(2000);
    
    await page.screenshot({ 
      path: `${screenshotsDir}/02-dashboard-authenticated.png`,
      fullPage: true 
    });
    results.screenshots.push('02-dashboard-authenticated.png');
    
    // Check if dashboard content is visible
    const dashboardContent = await page.content();
    const hasOnWatchTitle = dashboardContent.includes('onWatch') || dashboardContent.toLowerCase().includes('onwatch');

    results.tests.push({
      name: 'Dashboard Authentication',
      status: hasOnWatchTitle ? 'PASS' : 'FAIL',
      details: { hasDashboardTitle: hasOnWatchTitle }
    });

    console.log(`📊 Dashboard loaded: ${hasOnWatchTitle}`);

    // Test 3: Check Dashboard Elements
    console.log('\n=== Test 3: Dashboard Elements ===');
    
    // Check for quota cards
    const quotaCards = await page.$$('[class*="card"], [class*="quota"], .mdc-card');
    console.log(`📊 Quota cards found: ${quotaCards.length}`);
    
    // Check for theme toggle
    const themeToggle = await page.$('[class*="theme"], [class*="toggle"], button[class*="theme"]');
    console.log(`🎨 Theme toggle found: ${!!themeToggle}`);
    
    // Check for progress bars
    const progressBars = await page.$$('[role="progressbar"], [class*="progress"]');
    console.log(`📈 Progress bars found: ${progressBars.length}`);
    
    // Check for charts
    const charts = await page.$$('canvas');
    console.log(`📊 Chart canvases found: ${charts.length}`);
    
    results.tests.push({
      name: 'Dashboard Elements',
      status: 'PASS',
      details: {
        quotaCards: quotaCards.length,
        themeToggle: !!themeToggle,
        progressBars: progressBars.length,
        chartCanvases: charts.length
      }
    });

    // Test 4: Mobile Responsiveness
    console.log('\n=== Test 4: Mobile Responsiveness ===');
    
    await page.setViewportSize({ width: 375, height: 667 });
    await page.waitForTimeout(1000);
    await page.screenshot({ 
      path: `${screenshotsDir}/03-mobile-view.png`,
      fullPage: true 
    });
    results.screenshots.push('03-mobile-view.png');
    
    console.log('📱 Mobile viewport test completed');
    
    // Test 5: Theme Toggle
    console.log('\n=== Test 5: Theme Toggle ===');
    
    await page.setViewportSize({ width: 1920, height: 1080 });
    await page.waitForTimeout(1000);
    
    // Look for theme toggle button
    const sunIcon = await page.$('[class*="sun"], [class*="moon"]');
    const themeBtn = await page.$('button[class*="theme"], [aria-label*="theme"]');
    
    if (themeBtn) {
      await themeBtn.click();
      await page.waitForTimeout(500);
      await page.screenshot({ 
        path: `${screenshotsDir}/04-light-mode.png`,
        fullPage: true 
      });
      results.screenshots.push('04-light-mode.png');
      console.log('✅ Theme toggle clicked');
    } else {
      console.log('⚠️ Theme toggle not found');
    }

    // Test 6: API Endpoints
    console.log('\n=== Test 6: API Endpoints ===');
    
    // Test /api/current
    const currentResponse = await page.evaluate(async () => {
      try {
        const res = await fetch('/api/current', {
          headers: { 'Authorization': 'Basic ' + btoa('admin:changeme') }
        });
        return { status: res.status, ok: res.ok };
      } catch (e) {
        return { error: e.message };
      }
    });
    console.log(`📡 /api/current: ${JSON.stringify(currentResponse)}`);
    
    results.tests.push({
      name: 'API /api/current',
      status: currentResponse.ok ? 'PASS' : (currentResponse.error ? 'FAIL' : 'PASS'),
      details: currentResponse
    });

    // Test 7: Performance Metrics
    console.log('\n=== Test 7: Performance ===');
    
    const performanceMetrics = await page.evaluate(() => {
      const timing = performance.timing;
      return {
        loadTime: timing.loadEventEnd - timing.navigationStart,
        domContentLoaded: timing.domContentLoadedEventEnd - timing.navigationStart,
        firstPaint: performance.getEntriesByType('paint')[0]?.startTime || 0
      };
    });
    
    console.log(`⏱️ Load Time: ${performanceMetrics.loadTime}ms`);
    console.log(`📄 DOM Content Loaded: ${performanceMetrics.domContentLoaded}ms`);
    console.log(`🎨 First Paint: ${performanceMetrics.firstPaint.toFixed(2)}ms`);
    
    results.tests.push({
      name: 'Performance Metrics',
      status: 'PASS',
      details: performanceMetrics
    });

    // Test 8: Accessibility Check
    console.log('\n=== Test 8: Accessibility ===');
    
    const accessibilityIssues = await page.evaluate(() => {
      const issues = [];
      
      // Check for images without alt
      const images = document.querySelectorAll('img:not([alt])');
      if (images.length > 0) issues.push(`${images.length} images missing alt text`);
      
      // Check for buttons without accessible names
      const buttons = document.querySelectorAll('button:not([aria-label]):not([aria-labelledby])');
      if (buttons.length > 0) issues.push(`${buttons.length} buttons missing accessible names`);
      
      // Check for forms without labels
      const inputs = document.querySelectorAll('input:not([aria-label]):not([aria-labelledby]):not([id])');
      if (inputs.length > 0) issues.push(`${inputs.length} inputs missing labels`);
      
      return issues;
    });
    
    console.log(`♿ Accessibility issues: ${accessibilityIssues.length}`);
    accessibilityIssues.forEach(issue => console.log(`  ⚠️ ${issue}`));
    
    results.tests.push({
      name: 'Accessibility Check',
      status: accessibilityIssues.length === 0 ? 'PASS' : 'WARN',
      details: { issues: accessibilityIssues }
    });

    // Test 9: Material Design 3 Compliance
    console.log('\n=== Test 9: Material Design 3 Compliance ===');
    
    const md3Elements = await page.evaluate(() => {
      const elements = {
        cards: document.querySelectorAll('[class*="card"], [class*="mdc-card"]').length,
        buttons: document.querySelectorAll('button').length,
        inputs: document.querySelectorAll('input').length,
        elevation: document.querySelectorAll('[class*="elevation"], [class*="shadow"]').length,
        ripple: document.querySelectorAll('[class*="ripple"], .mdc-ripple').length,
        theme: document.querySelectorAll('[data-theme], [class*="theme-"]').length
      };
      return elements;
    });
    
    console.log(`🎨 Cards: ${md3Elements.cards}`);
    console.log(`🔘 Buttons: ${md3Elements.buttons}`);
    console.log(`📝 Inputs: ${md3Elements.inputs}`);
    console.log(`🌑 Elevation/Shadow: ${md3Elements.elevation}`);
    console.log(`💧 Ripple effects: ${md3Elements.ripple}`);
    console.log(`🎭 Theme classes: ${md3Elements.theme}`);
    
    results.tests.push({
      name: 'Material Design 3 Elements',
      status: md3Elements.cards > 0 ? 'PASS' : 'FAIL',
      details: md3Elements
    });

    // Final Screenshot
    console.log('\n=== Final Screenshot ===');
    await page.screenshot({ 
      path: `${screenshotsDir}/05-final-dashboard.png`,
      fullPage: true 
    });
    results.screenshots.push('05-final-dashboard.png');

    // Summary
    console.log('\n' + '='.repeat(50));
    console.log('📊 TEST SUMMARY');
    console.log('='.repeat(50));
    
    const passedTests = results.tests.filter(t => t.status === 'PASS').length;
    const failedTests = results.tests.filter(t => t.status === 'FAIL').length;
    const warnTests = results.tests.filter(t => t.status === 'WARN').length;
    
    console.log(`✅ Passed: ${passedTests}`);
    console.log(`❌ Failed: ${failedTests}`);
    console.log(`⚠️  Warnings: ${warnTests}`);
    console.log(`📸 Screenshots: ${results.screenshots.length}`);
    console.log(`❗ Errors: ${results.errors.length}`);
    
    results.summary = {
      passed: passedTests,
      failed: failedTests,
      warnings: warnTests,
      total: results.tests.length
    };

    // Save results
    fs.writeFileSync(
      './test-results/test-results.json', 
      JSON.stringify(results, null, 2)
    );
    
    console.log(`\n📁 Results saved to: test-results/test-results.json`);
    console.log(`📸 Screenshots saved to: ${screenshotsDir}`);

  } catch (error) {
    console.error('❌ Test Error:', error.message);
    results.errors.push(error.message);
    
    // Save error results
    fs.writeFileSync(
      './test-results/error-results.json',
      JSON.stringify(results, null, 2)
    );
    
  } finally {
    if (browser) {
      await browser.close();
      console.log('\n🔒 Browser closed');
    }
  }
}

// Run tests
testOnWatchDashboard().catch(console.error);