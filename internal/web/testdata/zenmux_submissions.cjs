const { chromium } = require(process.env.ZENMUX_PLAYWRIGHT_MODULE);
const { expect } = require(process.env.ZENMUX_PLAYWRIGHT_MODULE + '/test');
const assert = require('node:assert/strict');

const [base, executablePath] = process.argv.slice(2);
const usage = { data: { tokensByModel: [{ bizTime: '20260901', modelSlug: 'sample/model', requestCounts: '2', tokens: '9' }] } };
const endpoints = { data: { endpointUsages: [{ endpointSlug: 'sample/edge', requestCounts: '2' }] } };
const gates = new Set();
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); gates.add(resolve); return { promise, resolve }; };

(async () => {
  const browser = await chromium.launch({ executablePath, headless: true, args: ['--no-sandbox', '--disable-background-networking', '--disable-component-update', '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1, EXCLUDE localhost'] });
  async function scenario(name, action) {
    const context = await browser.newContext();
    const page = await context.newPage();
    const calls = [];
    let onUsage;
    await page.route('**/*', async route => {
      const url = new URL(route.request().url());
      if (url.hostname !== '127.0.0.1' && url.hostname !== 'localhost') return route.abort();
      if (!url.pathname.startsWith('/api/')) return route.continue();
      if (url.pathname === '/api/zenmux/usage') {
        calls.push(url.search);
        return onUsage(route, url, calls.length);
      }
      const payload = url.pathname === '/api/history' ? [] : {};
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(payload) });
    });
    try {
      console.log('RUN ' + name);
      await action({page, calls, setHandler: handler => { onUsage = handler; }});
      console.log('PASS ' + name);
    } finally {
      for (const release of gates) release();
      gates.clear();
      await context.close();
    }
  }
  const reply = (route, payload) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(payload) });
  const open = async page => {
    await page.goto(base + '/?provider=zenmux', { waitUntil: 'domcontentloaded' });
    return page.getByRole('region', { name: 'Model usage (aggregated)' });
  };
  const modelReady = panel => panel.getByRole('cell', {name: 'sample/model', exact: true}).waitFor();
  try {
    await scenario('usage submission while endpoint request is pending', async ({page, calls, setHandler}) => {
      const started = deferred(), release = deferred();
      setHandler(async (route, url, n) => {
        if (n === 1) return reply(route, usage);
        assert.equal(url.searchParams.get('model'), 'sample/model');
        started.resolve(); await release.promise; return reply(route, endpoints);
      });
      const panel = await open(page); await modelReady(panel);
      await panel.getByRole('button', {name: 'Show endpoints'}).click(); await started.promise;
      await panel.getByRole('button', {name: 'Show usage'}).click();
      await panel.getByRole('status').getByText('Request in progress. Please wait.').waitFor();
      release.resolve();
      await panel.getByRole('cell', {name: 'sample/edge', exact: true}).waitFor();
      assert.equal(calls.length, 2, 'usage submission duplicated the endpoint request');
    });

    await scenario('endpoint action unavailable while usage request is pending', async ({page, calls, setHandler}) => {
      const started = deferred(), release = deferred();
      setHandler(async (route, url, n) => {
        assert.equal(url.searchParams.has('model'), false);
        if (n === 1) return reply(route, usage);
        started.resolve(); await release.promise; return reply(route, usage);
      });
      const panel = await open(page); await modelReady(panel);
      await panel.getByRole('button', {name: 'Show usage'}).click(); await started.promise;
      assert.equal(await panel.getByRole('button', {name: 'Show endpoints'}).count(), 0, 'an endpoint action remained available');
      release.resolve(); await modelReady(panel);
      await panel.getByRole('button', {name: 'Show endpoints'}).waitFor();
      assert.equal(calls.length, 2, 'pending usage triggered another query');
    });

    await scenario('rejected network promise allows retry', async ({page, calls, setHandler}) => {
      setHandler((route, url, n) => n === 1 ? route.abort('failed') : reply(route, usage));
      const panel = await open(page);
      await panel.getByRole('status').getByText('Account usage is unavailable. Try again later.').waitFor();
      await panel.getByRole('button', {name: 'Show usage'}).click();
      await modelReady(panel);
      assert.equal(calls.length, 2, 'retry did not issue exactly one new request');
    });

    await scenario('duplicate endpoint clicks preserve the first result', async ({page, calls, setHandler}) => {
      const started = deferred(), release = deferred();
      setHandler(async (route, url, n) => {
        if (n === 1) return reply(route, usage);
        started.resolve(); await release.promise; return reply(route, endpoints);
      });
      const panel = await open(page); await modelReady(panel);
      const button = panel.getByRole('button', {name: 'Show endpoints'});
      await button.click(); await started.promise; await button.click();
      release.resolve(); await panel.getByRole('cell', {name: 'sample/edge', exact: true}).waitFor();
      assert.equal(calls.length, 2, 'duplicate endpoint click issued a request');
    });

    await scenario('duplicate usage submissions preserve the first result', async ({page, calls, setHandler}) => {
      const started = deferred(), release = deferred();
      setHandler(async (route, url, n) => { started.resolve(); await release.promise; return reply(route, usage); });
      const panel = await open(page); await started.promise;
      await panel.getByRole('button', {name: 'Show usage'}).click();
      await panel.getByRole('status').getByText('Request in progress. Please wait.').waitFor();
      release.resolve(); await modelReady(panel);
      assert.equal(calls.length, 1, 'duplicate form submit issued a request');
    });

    await scenario('changed filter invalidates a pending result and can be retried', async ({page, calls, setHandler}) => {
      const started = deferred(), release = deferred();
      setHandler(async (route, url, n) => {
        if (n === 1) { started.resolve(); await release.promise; }
        else assert.equal(url.searchParams.get('bill_type'), 'metered');
        return reply(route, usage);
      });
      const panel = await open(page); await started.promise;
      await panel.getByLabel('Billing').selectOption('metered');
      await panel.getByRole('button', {name: 'Show usage'}).click();
      await panel.getByRole('status').getByText('Request in progress. Please wait.').waitFor();
      // Fallback: this unnamed form exposes loading state via aria-busy.
      const form = panel.locator('form');
      release.resolve(); await expect(form).toHaveAttribute('aria-busy', 'false');
      assert.equal(await panel.getByRole('cell', {name:'sample/model', exact:true}).count(), 0, 'stale result was rendered');
      await panel.getByRole('button', {name:'Show usage'}).click(); await modelReady(panel);
      assert.equal(calls.length, 2);
    });

    await scenario('model pagination shows the next page without refetching', async ({page, calls, setHandler}) => {
      const rows = Array.from({length:52}, (_, i) => ({bizTime:'20260901', modelSlug:'model-'+String(i).padStart(2,'0'), requestCounts:String(100-i), tokens:'0'}));
      setHandler(route => reply(route, {data:{tokensByModel:rows}}));
      const panel = await open(page);
      await panel.getByText('Page 1 of 2').waitFor();
      await expect(panel.getByRole('cell', {name:'model-00', exact:true})).toBeVisible();
      await panel.getByRole('button', {name:'Next'}).click();
      await panel.getByText('Page 2 of 2').waitFor();
      await expect(panel.getByRole('cell', {name:'model-50', exact:true})).toBeVisible();
      await expect(panel.getByRole('cell', {name:'model-51', exact:true})).toBeVisible();
      await expect(panel.getByRole('cell', {name:'model-00', exact:true})).toHaveCount(0);
      assert.equal(calls.length, 1);
    });

    await scenario('interval view uses existing usage results', async ({page, calls, setHandler}) => {
      setHandler(route => reply(route, usage));
      const panel = await open(page); await modelReady(panel);
      await panel.getByLabel('View').selectOption('interval');
      await panel.getByRole('columnheader', {name:'Time bucket'}).waitFor();
      await panel.getByRole('cell', {name:'2026-09-01', exact:true}).waitFor();
      assert.equal(calls.length, 1);
    });

    await scenario('model change discards pending endpoint result', async ({page, calls, setHandler}) => {
      const started = deferred(), release = deferred();
      const twoModels = {data:{tokensByModel:[...usage.data.tokensByModel, {bizTime:'20260901',modelSlug:'sample/other',requestCounts:'1',tokens:'0'}]}};
      setHandler(async (route, url, n) => {
        if (n === 1) return reply(route, twoModels);
        if (n === 2) { started.resolve(); await release.promise; return reply(route, endpoints); }
        assert.equal(url.searchParams.get('model'), 'sample/other');
        return reply(route, {data:{endpointUsages:[{endpointSlug:'sample/new-edge'}]}});
      });
      const panel = await open(page); await modelReady(panel);
      await panel.getByRole('button', {name:'Show endpoints'}).click(); await started.promise;
      await panel.getByLabel('Provider endpoints for model').selectOption('sample/other');
      await panel.getByRole('status').getByText('Model changed. Select Show endpoints.').waitFor();
      // Fallback: this unnamed form exposes loading state via aria-busy.
      release.resolve(); await expect(panel.locator('form')).toHaveAttribute('aria-busy', 'false');
      assert.equal(await panel.getByRole('cell', {name:'sample/edge', exact:true}).count(), 0);
      await panel.getByRole('button', {name:'Show endpoints'}).click();
      await panel.getByRole('cell', {name:'sample/new-edge', exact:true}).waitFor();
      assert.equal(calls.length, 3);
    });

    await scenario('empty usage removes endpoint and pagination actions', async ({page, calls, setHandler}) => {
      setHandler((route, url, n) => reply(route, n === 1 ? usage : {data:{tokensByModel:[]}}));
      const panel = await open(page); await modelReady(panel);
      await panel.getByRole('button', {name:'Show usage'}).click();
      await panel.getByText('No model usage was returned for this period and billing filter.').waitFor();
      assert.equal(await panel.getByRole('button', {name:'Show endpoints'}).count(), 0);
      assert.equal(await panel.getByRole('button', {name:'Next'}).count(), 0);
      assert.equal(calls.length, 2);
    });

    await scenario('HTTP error allows another public submission', async ({page, calls, setHandler}) => {
      setHandler((route, url, n) => n === 1
        ? route.fulfill({status:429, contentType:'application/json', body:JSON.stringify({error:'Please wait before requesting Zenmux usage again'})})
        : reply(route, usage));
      const panel = await open(page);
      await panel.getByRole('status').getByText('Please wait before requesting Zenmux usage again').waitFor();
      await panel.getByRole('button', {name:'Show usage'}).click(); await modelReady(panel);
      assert.equal(calls.length, 2);
    });
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error.stack); process.exitCode = 1; });
