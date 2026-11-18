// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.
/* eslint-disable no-undef, @typescript-eslint/no-require-imports -- Node CLI run directly by node, never bundled */
'use strict';

// Header fixture runner. Run from webapp/channels:
//   node src/components/phoenix_shell/header.browser.cjs --prepare
//   node src/components/phoenix_shell/header.browser.cjs --list
//   node src/components/phoenix_shell/header.browser.cjs --check <H01|H02|all>
//   node src/components/phoenix_shell/header.browser.cjs --capture all
//
// --prepare bundles the REAL PhoenixShell (index.tsx) with the REAL phoenix_shell.scss on top of
// the REAL native Mattermost stylesheet (src/sass/styles.scss), in the native order, and records
// the sha256 of every input. --check/--capture serve that bundle on an owned loopback port and
// drive Chromium; they refuse to run when an input changed since --prepare and never build.
// Only the network boundary and three heavy native modules are replaced (see STUBS).
// Fixture output is NOT authenticated production evidence.

const fs = require('fs');
const path = require('path');
const http = require('http');
const crypto = require('crypto');

const CHANNELS = path.resolve(__dirname, '..', '..', '..');
const WEBAPP = path.resolve(CHANNELS, '..');
const ECOSYSTEM = path.resolve(CHANNELS, '..', '..', '..', '..');
const OUT = path.join(ECOSYSTEM, '.local', 'mattermost', 'header-compact');
const BUNDLE = path.join(OUT, 'bundle');
const RESULTS = path.join(OUT, 'results');
const CAPTURES = path.join(OUT, 'captures');
const LOGS = path.join(OUT, 'logs');
const INPUTS_FILE = path.join(BUNDLE, 'inputs.json');
const HOST = '127.0.0.1';
const PORT = 4179;

const CHECKS = {
    H01: 'computed layout',
    H02: 'rendered themes and interaction',
};
const TOLERANCE = 1;
const DESKTOP = {width: 1440, height: 900};
const MOBILE = {width: 390, height: 844};
const SHORT = {width: 390, height: 280};

const SOURCES = ['index.tsx', 'phoenix_shell.scss', 'client.ts', 'theme.ts', 'session_lifecycle.ts', 'header.browser.tsx'].map((f) => path.join(__dirname, f));

// Fixture-only replacements of heavy native modules (they pull the whole Redux/app graph).
// Everything the header's own code does (client.ts, theme.ts, index.tsx) is real.
const STUBS = {
    'redux_store.js': 'export default {dispatch() {}, getState() { return {}; }};\n',
    'initial_loading_screen.js': 'export default {stop() {}};\n',
    'native_utils.js': [
        '// Mirrors what the native applyTheme does for the page: theme keys become CSS variables.',
        'export function applyTheme(theme) {',
        '    const root = document.documentElement;',
        '    for (const [key, value] of Object.entries(theme)) {',
        '        if (typeof value !== "string") {',
        '            continue;',
        '        }',
        '        const name = "--" + key.replace(/[A-Z]/g, (m) => "-" + m.toLowerCase());',
        '        root.style.setProperty(name, value);',
        '        const hex = /^#([0-9a-f]{6})$/i.exec(value);',
        '        if (hex) {',
        '            const n = parseInt(hex[1], 16);',
        '            root.style.setProperty(name + "-rgb", [n >> 16, (n >> 8) & 255, n & 255].join(","));',
        '        }',
        '    }',
        '}',
        '',
    ].join('\n'),
};

function sha256(buffer) {
    return crypto.createHash('sha256').update(buffer).digest('hex');
}

function sha256File(file) {
    return sha256(fs.readFileSync(file));
}

function walk(dir) {
    return fs.readdirSync(dir, {withFileTypes: true}).flatMap((e) => {
        const p = path.join(dir, e.name);
        return e.isDirectory() ? walk(p) : [p];
    }).sort();
}

function directoryDigest(dir) {
    const h = crypto.createHash('sha256');
    for (const file of walk(dir)) {
        h.update(path.relative(dir, file).replace(/\\/g, '/') + '\0' + sha256File(file) + '\n');
    }
    return h.digest('hex');
}

function pkgVersion(name, from = WEBAPP) {
    try {
        return require(require.resolve(name + '/package.json', {paths: [from, CHANNELS]})).version;
    } catch {
        return 'unresolved';
    }
}

function webpackConfig() {
    const MiniCssExtractPlugin = require(require.resolve('mini-css-extract-plugin', {paths: [WEBAPP]}));
    const webpack = require(require.resolve('webpack', {paths: [WEBAPP]}));
    const stub = (name) => path.join(OUT, 'stubs', name);
    return {
        mode: 'development',
        devtool: false,
        context: CHANNELS,
        target: 'web',
        // `native` is the real application stylesheet entry; `fixture` is the header under test.
        entry: {native: './src/sass/styles.scss', fixture: './src/components/phoenix_shell/header.browser.tsx'},
        output: {path: BUNDLE, filename: '[name].js', publicPath: '/', assetModuleFilename: 'files/[hash][ext]', clean: true},
        module: {
            rules: [
                {test: /\.(js|jsx|ts|tsx)$/, exclude: /node_modules/, use: {loader: 'babel-loader', options: {cacheDirectory: false}}},
                {test: /\.(css|scss)$/, exclude: /[\\/]highlight\.js[\\/]/, use: [MiniCssExtractPlugin.loader, 'css-loader']},
                {test: /\.scss$/, use: [{loader: 'sass-loader', options: {sassOptions: {loadPaths: ['src/sass']}}}]},
                {test: /\.(png|eot|tiff|svg|woff2|woff|ttf|gif|mp3|jpg)$/, type: 'asset/resource'},
            ],
        },
        resolve: {
            modules: ['node_modules', path.join(CHANNELS, 'src')],
            alias: {
                'stores/redux_store$': stub('redux_store.js'),
                'components/initial_loading_screen$': stub('initial_loading_screen.js'),
                'utils/utils$': stub('native_utils.js'),
                'mattermost-redux': path.join(CHANNELS, 'src', 'packages', 'mattermost-redux', 'src'),
            },
            extensions: ['.ts', '.tsx', '.js', '.jsx'],
            fallback: {
                crypto: require.resolve('crypto-browserify', {paths: [WEBAPP]}),
                stream: require.resolve('stream-browserify', {paths: [WEBAPP]}),
                buffer: require.resolve('buffer/', {paths: [WEBAPP]}),
            },
        },
        plugins: [
            new webpack.ProvidePlugin({process: require.resolve('process/browser.js', {paths: [WEBAPP]}), Buffer: ['buffer', 'Buffer']}),
            new MiniCssExtractPlugin({filename: '[name].css'}),
        ],
        performance: {hints: false},
    };
}

const HTML = [
    '<!doctype html>',
    '<html lang="vi"><head><meta charset="utf-8">',
    '<meta name="viewport" content="width=device-width, initial-scale=1.0">',
    '<title>Phoenix header fixture</title>',
    '<link rel="stylesheet" href="/native.css"><link rel="stylesheet" href="/fixture.css">',
    '</head><body class="font--open_sans enable-animations">',
    '<div id="root"></div><div id="root-portal"></div>',
    '<script src="/fixture.js"></script></body></html>',
    '',
].join('\n');

// What decides whether a prepared bundle is still valid.
function bundleInputs() {
    const files = {};
    for (const f of SOURCES) {
        files[path.basename(f)] = sha256File(f);
    }
    const nativeDir = path.join(CHANNELS, 'src', 'sass');
    const config = JSON.stringify(webpackConfig(), (k, v) => (typeof v === 'function' || v instanceof RegExp ? String(v) : v));
    return {
        files,
        nativeSassDigest: directoryDigest(nativeDir),
        buildConfigDigest: sha256(config + JSON.stringify(STUBS) + HTML),
        versions: {
            react: pkgVersion('react'), webpack: pkgVersion('webpack'), sass: pkgVersion('sass'), 'sass-loader': pkgVersion('sass-loader'),
            bootstrap: pkgVersion('bootstrap'), 'babel-loader': pkgVersion('babel-loader'),
        },
    };
}

function readPrepared() {
    if (!fs.existsSync(INPUTS_FILE)) {
        throw new Error('no prepared bundle: run --prepare first');
    }
    return JSON.parse(fs.readFileSync(INPUTS_FILE, 'utf8'));
}

function assertPrepared() {
    const prepared = readPrepared();
    const now = bundleInputs();
    const stale = [];
    for (const [name, hash] of Object.entries(now.files)) {
        if (prepared.inputs.files[name] !== hash) {
            stale.push(name);
        }
    }
    if (prepared.inputs.nativeSassDigest !== now.nativeSassDigest) {
        stale.push('native sass tree');
    }
    if (prepared.inputs.buildConfigDigest !== now.buildConfigDigest) {
        stale.push('build config');
    }
    if (stale.length) {
        throw new Error('prepared bundle is stale for: ' + stale.join(', ') + ' (run --prepare)');
    }
    return prepared;
}

function resolvePlaywright() {
    const roots = [process.env.PHOENIX_HEADER_PLAYWRIGHT, path.join(ECOSYSTEM, 'e2e'), path.join(ECOSYSTEM, 'scripts', 'mattermost'), WEBAPP].filter(Boolean);
    for (const root of roots) {
        try {
            const main = require.resolve('playwright-core', {paths: [root]});
            return {chromium: require(main).chromium, version: require(require.resolve('playwright-core/package.json', {paths: [root]})).version, from: root};
        } catch {
            // try the next installed location
        }
    }
    throw new Error('no installed playwright-core found (looked in: ' + roots.join(', ') + ')');
}

const TYPES = {'.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.json': 'application/json', '.woff': 'font/woff', '.woff2': 'font/woff2', '.ttf': 'font/ttf', '.png': 'image/png', '.svg': 'image/svg+xml', '.gif': 'image/gif', '.jpg': 'image/jpeg', '.eot': 'application/vnd.ms-fontobject'};

// Serves the prepared bundle on an owned loopback port. A busy port is an error: nothing
// else is ever stopped to free it.
function serve() {
    return new Promise((resolve, reject) => {
        const server = http.createServer((req, res) => {
            const url = new URL(req.url, 'http://x');
            if (url.pathname === '/favicon.ico') {
                res.writeHead(204).end();
                return;
            }
            const rel = url.pathname === '/' ? 'index.html' : decodeURIComponent(url.pathname).replace(/^\/+/, '');
            const file = path.resolve(BUNDLE, rel);
            if (!file.startsWith(BUNDLE + path.sep) || !fs.existsSync(file) || !fs.statSync(file).isFile()) {
                res.writeHead(404).end('not found');
                return;
            }
            res.writeHead(200, {'Content-Type': TYPES[path.extname(file)] || 'application/octet-stream', 'Cache-Control': 'no-store'});
            fs.createReadStream(file).pipe(res);
        });
        server.once('error', (e) => reject(new Error(e.code === 'EADDRINUSE' ? `port ${PORT} is already in use; refusing to start (nothing was stopped)` : e.message)));
        server.listen(PORT, HOST, () => resolve(server));
    });
}

async function withBrowser(fn) {
    const pw = resolvePlaywright();
    const server = await serve();
    let browser;
    try {
        browser = await pw.chromium.launch({headless: true});
        return await fn({browser, pw});
    } finally {
        if (browser) {
            await browser.close();
        }
        await new Promise((r) => server.close(r));
    }
}

async function openPage(browser, viewport, scenario = {}, colorScheme = 'light') {
    const context = await browser.newContext({viewport, colorScheme, deviceScaleFactor: 1});
    const page = await context.newPage();
    const problems = [];
    page.on('pageerror', (e) => problems.push('pageerror: ' + e.message));
    page.on('console', (m) => {
        if (m.type() === 'error') {
            problems.push('console.error: ' + m.text());
        }
    });
    await page.addInitScript((s) => {
        window.phoenixFixture = s;
    }, scenario);
    await page.goto(`http://${HOST}:${PORT}/`);
    await page.waitForSelector('[data-testid="phoenix-header"]', {timeout: 15000});
    await page.evaluate(() => document.fonts.ready);
    return {page, context, problems};
}

const SCENARIOS = {
    short: {tenant: 'Tenant A', account: 'A1'},
    long: {
        tenant: 'A'.repeat(120),
        account: 'B'.repeat(120),
        apps: Array.from({length: 12}, (_, i) => ({appCode: 'app' + i, label: i === 3 ? 'C'.repeat(120) : 'Ứng dụng ' + i, state: 'ready', launchUrl: `https://app${i}.phoenixsoft.vn`})),
    },
};

function collector(id, ctx) {
    const rows = [];
    return {
        rows,
        ok(name, cond, detail) {
            rows.push({check: id, case: ctx.label, name, ok: Boolean(cond), detail: detail === undefined ? undefined : detail});
            return Boolean(cond);
        },
        near(name, actual, expected, tol = TOLERANCE) {
            return this.ok(name, Math.abs(actual - expected) <= tol, {actual, expected, tol});
        },
    };
}

const TRIGGER = {apps: 'button[aria-label="Ứng dụng"]', account: 'button[aria-label="Tài khoản"]'};

// Everything H01 reads is measured in the page from getComputedStyle / getBoundingClientRect.
function measureLayout() {
    const rect = (el) => {
        if (!el) {
            return null;
        }
        const b = el.getBoundingClientRect();
        return {l: b.left, t: b.top, r: b.right, b: b.bottom, w: b.width, h: b.height};
    };
    const q = (s, root = document) => root.querySelector(s);
    const qa = (s, root = document) => Array.from(root.querySelectorAll(s));
    const header = q('[data-testid="phoenix-header"]');
    const launcher = q('.phoenix-shell__icon-button');
    const trigger = q('.phoenix-shell__account-trigger');
    const radios = qa('[role="radio"]');
    const panel = q('[data-phoenix-popover]');
    const corners = (el) => {
        const s = getComputedStyle(el);
        return [s.borderTopLeftRadius, s.borderTopRightRadius, s.borderBottomRightRadius, s.borderBottomLeftRadius];
    };
    const borders = (el) => {
        const s = getComputedStyle(el);
        return [s.borderTopWidth, s.borderRightWidth, s.borderBottomWidth, s.borderLeftWidth];
    };
    const truncated = (el) => (el ? {scrollWidth: el.scrollWidth, clientWidth: el.clientWidth, textOverflow: getComputedStyle(el).textOverflow, overflow: getComputedStyle(el).overflowX} : null);
    const reach = (el) => {
        if (!el || !panel) {
            return null;
        }
        el.scrollIntoView({block: 'nearest'});
        const p = rect(panel);
        const r = rect(el);
        return r.t >= p.t - 1 && r.b <= p.b + 1 && r.l >= p.l - 1 && r.r <= p.r + 1;
    };
    const out = {
        viewport: {w: window.innerWidth, h: window.innerHeight},
        doc: {scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth},
        header: {rect: rect(header), scrollWidth: header.scrollWidth, clientWidth: header.clientWidth, display: getComputedStyle(header).display, flexWrap: getComputedStyle(header).flexWrap},
        brand: rect(q('.phoenix-shell__brand')),
        controls: rect(q('.phoenix-shell__controls')),
        tenant: truncated(q('.phoenix-shell__tenant')),
        launcher: {rect: rect(launcher), borders: borders(launcher), corners: corners(launcher), dots: qa('circle', launcher).length, background: getComputedStyle(launcher).backgroundColor},
        trigger: {rect: rect(trigger), corners: corners(trigger), textDisplay: getComputedStyle(q('.phoenix-shell__account-text', trigger)).display, name: truncated(q('.phoenix-shell__account-name', trigger)), expanded: trigger.getAttribute('aria-expanded')},
        launcherExpanded: launcher.getAttribute('aria-expanded'),
        panels: qa('[data-phoenix-popover]').map((p) => p.getAttribute('data-phoenix-popover')),
        radioCount: radios.length,
        logoutInDom: qa('.phoenix-shell__logout').length,
        panel: null,
    };
    if (panel) {
        const items = qa('.phoenix-shell__menu-item', panel);
        out.panel = {
            kind: panel.getAttribute('data-phoenix-popover'),
            rect: rect(panel),
            overflowY: getComputedStyle(panel).overflowY,
            scrollHeight: panel.scrollHeight,
            clientHeight: panel.clientHeight,
            scrollWidth: panel.scrollWidth,
            clientWidth: panel.clientWidth,
            modes: radios.map((r) => ({rect: rect(r), borders: borders(r), corners: corners(r)})),
            items: items.map((i) => ({rect: rect(i), truncated: truncated(i)})),
            identityName: truncated(q('.phoenix-shell__identity .phoenix-shell__account-name', panel)),
            logoutRect: rect(q('.phoenix-shell__logout', panel)),
            reachable: [...radios, q('.phoenix-shell__logout', panel), ...items, q('.phoenix-shell__identity', panel)].filter(Boolean).map((el) => reach(el)),
        };
        panel.scrollTop = 0;
    }
    return out;
}

async function runH01(browser, c) {
    const cases = [];
    for (const [tenantKey, scenario] of Object.entries(SCENARIOS)) {
        for (const [vpKey, vp] of Object.entries({desktop: DESKTOP, mobile: MOBILE})) {
            for (const state of ['closed', 'account', 'apps']) {
                cases.push({tenantKey, scenario, vpKey, vp, state});
            }
        }
    }
    // A viewport shorter than the panel: it must scroll, not clip.
    for (const state of ['account', 'apps']) {
        cases.push({tenantKey: 'long', scenario: SCENARIOS.long, vpKey: 'short', vp: SHORT, state});
    }
    for (const width of [641, 720, 721, 768]) {
        for (const state of ['account', 'apps']) {
            cases.push({tenantKey: 'long', scenario: SCENARIOS.long, vpKey: 'w' + width, vp: {width, height: 844}, state});
        }
    }
    const metrics = [];
    for (const k of cases) {
        const label = `${k.tenantKey}/${k.vpKey}/${k.state}`;
        const t = collector('H01', {label});
        const {page, context, problems} = await openPage(browser, k.vp, k.scenario);
        try {
            if (k.state !== 'closed') {
                await page.click(TRIGGER[k.state]);
                await page.waitForSelector(`[data-phoenix-popover="${k.state}"]`);
            }
            const m = await page.evaluate(measureLayout);
            metrics.push({case: label, ...m});
            const narrow = k.vp.width <= 640;
            t.ok('page has no script or console errors', problems.length === 0, problems);
            t.near('header height', m.header.rect.h, narrow ? 64 : 72);
            t.ok('header is a single non-wrapping row', m.header.display === 'flex' && m.header.flexWrap === 'nowrap' &&
                m.brand.t >= m.header.rect.t - TOLERANCE && m.brand.b <= m.header.rect.b + TOLERANCE &&
                m.controls.t >= m.header.rect.t - TOLERANCE && m.controls.b <= m.header.rect.b + TOLERANCE &&
                m.controls.l >= m.brand.r - TOLERANCE && Math.abs((m.brand.t + m.brand.b) / 2 - (m.controls.t + m.controls.b) / 2) <= TOLERANCE, {brand: m.brand, controls: m.controls});
            t.ok('header and page do not overflow horizontally', m.header.scrollWidth <= m.header.clientWidth && m.doc.scrollWidth <= m.doc.clientWidth, {header: [m.header.scrollWidth, m.header.clientWidth], doc: m.doc});
            t.near('launcher width', m.launcher.rect.w, 44);
            t.near('launcher height', m.launcher.rect.h, 44);
            t.ok('launcher has no border', m.launcher.borders.every((b) => b === '0px'), m.launcher.borders);
            t.ok('launcher radius is 8px', m.launcher.corners.every((r) => r === '8px'), m.launcher.corners);
            t.ok('launcher draws the nine dots', m.launcher.dots === 9, m.launcher.dots);
            t.ok('account trigger is at least 44px', m.trigger.rect.h >= 44 - TOLERANCE && m.trigger.rect.w >= 44 - TOLERANCE, m.trigger.rect);
            t.ok('account trigger is a pill', m.trigger.corners.every((r) => parseFloat(r) >= m.trigger.rect.h / 2 - TOLERANCE), m.trigger.corners);
            t.ok('account trigger stays on screen and bounded', m.trigger.rect.r <= m.viewport.w + TOLERANCE && m.trigger.rect.w <= 262, m.trigger.rect);
            t.ok(narrow ? 'mobile trigger hides identity text' : 'desktop trigger shows identity text', narrow ? m.trigger.textDisplay === 'none' : m.trigger.textDisplay !== 'none', m.trigger.textDisplay);
            if (k.tenantKey === 'long') {
                t.ok('long tenant truncates with an ellipsis', m.tenant.scrollWidth > m.tenant.clientWidth && m.tenant.textOverflow === 'ellipsis' && m.tenant.overflow === 'hidden', m.tenant);
                if (!narrow) {
                    t.ok('long account name truncates in the trigger', m.trigger.name.scrollWidth > m.trigger.name.clientWidth && m.trigger.name.textOverflow === 'ellipsis', m.trigger.name);
                }
            } else {
                t.ok('short tenant is not truncated', m.tenant.scrollWidth <= m.tenant.clientWidth, m.tenant);
            }
            if (k.state === 'closed') {
                t.ok('closed header has no panel, radio or logout', m.panels.length === 0 && m.radioCount === 0 && m.logoutInDom === 0, {panels: m.panels, radios: m.radioCount, logout: m.logoutInDom});
                t.ok('both triggers collapsed', m.launcherExpanded === 'false' && m.trigger.expanded === 'false');
            } else {
                const p = m.panel;
                t.ok('exactly the requested panel is open', m.panels.length === 1 && m.panels[0] === k.state, m.panels);
                t.ok('panel is inside the viewport', p.rect.l >= -TOLERANCE && p.rect.t >= -TOLERANCE && p.rect.r <= m.viewport.w + TOLERANCE && p.rect.b <= m.viewport.h + TOLERANCE, {panel: p.rect, viewport: m.viewport});
                t.ok('panel keeps a 16px viewport allowance', p.rect.w <= m.viewport.w - 16 + TOLERANCE, p.rect.w);
                t.ok('panel scrolls vertically and does not overflow sideways', p.overflowY === 'auto' && p.scrollWidth <= p.clientWidth + TOLERANCE, {overflowY: p.overflowY, w: [p.scrollWidth, p.clientWidth]});
                t.ok('every control is reachable inside the panel', p.reachable.length > 0 && p.reachable.every(Boolean), p.reachable);
                if (k.vpKey === 'short') {
                    t.ok('short viewport really scrolls the panel', p.scrollHeight > p.clientHeight + TOLERANCE, {scrollHeight: p.scrollHeight, clientHeight: p.clientHeight});
                }
                if (k.state === 'account') {
                    t.ok('three modes are present', p.modes.length === 3, p.modes.length);
                    t.ok('modes are at least 44px', p.modes.every((r) => r.rect.h >= 44 - TOLERANCE && r.rect.w >= 44 - TOLERANCE), p.modes.map((r) => r.rect));
                    t.ok('modes have no border and 6px radius', p.modes.every((r) => r.borders.every((b) => b === '0px') && r.corners.every((x) => x === '6px')), p.modes.map((r) => [r.borders, r.corners]));
                    t.ok('logout link is present and at least 44px', m.logoutInDom === 1 && p.logoutRect.h >= 44 - TOLERANCE, p.logoutRect);
                    if (k.tenantKey === 'long') {
                        t.ok('long account name truncates in the panel', p.identityName.scrollWidth > p.identityName.clientWidth, p.identityName);
                    }
                } else {
                    t.ok('launcher expanded and account collapsed', m.launcherExpanded === 'true' && m.trigger.expanded === 'false');
                    t.ok('menu items are at least 44px', p.items.length > 0 && p.items.every((i) => i.rect.h >= 44 - TOLERANCE), p.items.map((i) => i.rect.h));
                    t.ok('pending apps are not offered', p.items.length === (k.tenantKey === 'long' ? 12 : 2), p.items.length);
                    if (k.tenantKey === 'long') {
                        t.ok('long menu label truncates', p.items.some((i) => i.truncated.scrollWidth > i.truncated.clientWidth && i.truncated.textOverflow === 'ellipsis'));
                    }
                }
            }
        } finally {
            await context.close();
        }
        c.push(...t.rows);
    }
    return {metrics, cases: cases.length};
}

// ---------- H02 ----------

// Reads colour + contrast facts of one element in its current state.
function readColors(selector) {
    const {parse, effective, ratio, lum} = (function helpers() {
        const parseColor = (value) => {
            const m = /rgba?\(([^)]+)\)/.exec(value);
            if (!m) {
                return {r: 255, g: 255, b: 255, a: 0};
            }
            const [r, g, b, a = 1] = m[1].split(/[ ,/]+/).filter(Boolean).map(Number);
            return {r, g, b, a};
        };
        const eff = (el) => {
            const layers = [];
            for (let n = el; n; n = n.parentElement) {
                const c = parseColor(getComputedStyle(n).backgroundColor);
                layers.push(c);
                if (c.a >= 1) {
                    break;
                }
            }
            let out = {r: 255, g: 255, b: 255};
            for (let i = layers.length - 1; i >= 0; i--) {
                const c = layers[i];
                out = {r: c.r * c.a + out.r * (1 - c.a), g: c.g * c.a + out.g * (1 - c.a), b: c.b * c.a + out.b * (1 - c.a)};
            }
            return out;
        };
        const luminance = (c) => {
            const f = (v) => {
                const s = v / 255;
                return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
            };
            return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b);
        };
        const contrast = (a, b) => {
            const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
            return (hi + 0.05) / (lo + 0.05);
        };
        return {parse: parseColor, effective: eff, ratio: contrast, lum: luminance};
    }());
    const el = document.querySelector(selector);
    if (!el) {
        return null;
    }
    const s = getComputedStyle(el);
    const bg = effective(el);
    const fg = parse(s.color);
    return {
        selector,
        color: s.color,
        backgroundColor: s.backgroundColor,
        effectiveBackground: `rgb(${Math.round(bg.r)}, ${Math.round(bg.g)}, ${Math.round(bg.b)})`,
        backgroundLuminance: lum(bg),
        contrast: ratio({r: fg.r, g: fg.g, b: fg.b}, bg),
        textDecoration: s.textDecorationLine,
        outlineStyle: s.outlineStyle,
        outlineWidth: s.outlineWidth,
        outlineColor: s.outlineColor,
        focusVisible: el.matches(':focus-visible'),
        outlineContrast: s.outlineStyle === 'none' ? 0 : (() => {
            const oc = parse(s.outlineColor);
            let surface = {r: 255, g: 255, b: 255};
            const root = document.querySelector('.phoenix-shell');
            if (root) {
                surface = effective(root);
            }
            return ratio({r: oc.r, g: oc.g, b: oc.b}, surface);
        })(),
    };
}

const MUTED = {light: 'rgb(93, 109, 103)', dark: 'rgb(178, 197, 188)'};
const THEMES = {
    light: {mode: 'light', scheme: 'light', surface: 'rgb(255, 255, 255)', text: 'rgb(17, 17, 17)', resolved: 'light', muted: MUTED.light},
    dark: {mode: 'dark', scheme: 'light', surface: 'rgb(17, 17, 17)', text: 'rgb(255, 255, 255)', resolved: 'dark', muted: MUTED.dark},
    'system-dark': {mode: 'system', scheme: 'dark', surface: 'rgb(17, 17, 17)', text: 'rgb(255, 255, 255)', resolved: 'dark', muted: MUTED.dark},
    'system-light': {mode: 'system', scheme: 'light', surface: 'rgb(255, 255, 255)', text: 'rgb(17, 17, 17)', resolved: 'light', muted: MUTED.light},
    // The shipped Gateway default palette, untouched (ThemeDefaults.java).
    'gateway-light': {mode: 'light', scheme: 'light', palette: 'gateway', surface: 'rgb(255, 255, 255)', text: 'rgb(23, 33, 30)', resolved: 'light', muted: MUTED.light},
    'gateway-dark': {mode: 'dark', scheme: 'light', palette: 'gateway', surface: 'rgb(24, 37, 31)', text: 'rgb(234, 243, 239)', resolved: 'dark', muted: MUTED.dark},
};

// UA ButtonFace values; plain white is also wrong on a dark surface.
const BUTTON_FACES = ['rgb(239, 239, 239)', 'rgb(233, 233, 237)'];

// Bounded wait that names itself and the page state when it gives up.
async function until(page, label, fn, arg) {
    try {
        await page.waitForFunction(fn, arg, {timeout: 5000});
    } catch {
        const state = await page.evaluate(() => ({active: document.activeElement && (document.activeElement.getAttribute('aria-label') || document.activeElement.id || document.activeElement.tagName), panels: Array.from(document.querySelectorAll('[data-phoenix-popover]')).map((p) => p.getAttribute('data-phoenix-popover')), mode: document.documentElement.dataset.phoenixMode}));
        throw new Error(label + ' timed out; ' + JSON.stringify(state));
    }
}

async function focused(page) {
    return page.evaluate(() => {
        const a = document.activeElement;
        return a ? {tag: a.tagName, role: a.getAttribute('role'), label: a.getAttribute('aria-label'), text: (a.textContent || '').trim().slice(0, 40), checked: a.getAttribute('aria-checked'), cls: a.className, id: a.id} : null;
    });
}

async function runH02(browser, c) {
    const metrics = [];
    let cases = 0;
    for (const [vpKey, vp] of Object.entries({desktop: DESKTOP, mobile: MOBILE})) {
        for (const [themeKey, theme] of Object.entries(THEMES)) {
            cases += 1;
            const label = `${vpKey}/${themeKey}`;
            const t = collector('H02', {label});
            const {page, context, problems} = await openPage(browser, vp, {mode: theme.mode, palette: theme.palette || 'plan'}, theme.scheme);
            const note = {case: label};
            try {
                const dark = theme.resolved === 'dark';
                const notFace = (eff) => !BUTTON_FACES.includes(eff) && !(dark && eff === 'rgb(255, 255, 255)');
                // Surfaces and text resolved from the Gateway snapshot, not from UA defaults.
                const base = await page.evaluate(() => {
                    const s = getComputedStyle(document.querySelector('.phoenix-shell'));
                    const h = getComputedStyle(document.querySelector('[data-testid="phoenix-header"]'));
                    const f = document.querySelector('[data-testid="phoenix-footer"]');
                    return {shell: [s.backgroundColor, s.color], header: [h.backgroundColor, h.color], footer: f ? [getComputedStyle(f).backgroundColor, getComputedStyle(f).color] : null, mode: document.documentElement.dataset.phoenixMode, cls: document.querySelector('.phoenix-shell').className};
                });
                note.base = base;
                t.ok(`shell surface ${theme.surface} text ${theme.text}`, base.shell[0] === theme.surface && base.shell[1] === theme.text, base.shell);
                t.ok('header uses the same surface and text', base.header[0] === theme.surface && base.header[1] === theme.text, base.header);
                t.ok('footer uses the same surface and text', base.footer && base.footer[0] === theme.surface && base.footer[1] === theme.text, base.footer);
                t.ok('resolved mode applied to the document', base.mode === theme.resolved && base.cls.includes('phoenix-shell--' + theme.resolved), base.mode);

                // Keyboard: Tab reaches the launcher; Enter opens the menu; arrows move; Escape returns.
                await page.keyboard.press('Tab');
                let f = await focused(page);
                t.ok('Tab reaches the launcher', f && f.label === 'Ứng dụng', f);
                const ring = await page.evaluate(readColors, TRIGGER.apps);
                note.launcherFocus = ring;
                t.ok('launcher focus ring is visible', ring.focusVisible && ring.outlineStyle === 'solid' && ring.outlineWidth === '2px' && ring.outlineContrast >= 3, ring);
                await page.keyboard.press('Enter');
                await page.waitForSelector('[data-phoenix-popover="apps"]');
                f = await focused(page);
                t.ok('Enter opens the menu and focuses the first item', f && f.role === 'menuitem' && f.text === 'Tasks', f);
                await page.keyboard.press('ArrowDown');
                t.ok('ArrowDown moves to the next item', (await focused(page)).text === 'Portal');
                await page.keyboard.press('ArrowDown');
                t.ok('ArrowDown wraps to the first item', (await focused(page)).text === 'Tasks');
                await page.keyboard.press('ArrowUp');
                t.ok('ArrowUp wraps to the last item', (await focused(page)).text === 'Portal');
                await page.keyboard.press('Home');
                t.ok('Home moves to the first item', (await focused(page)).text === 'Tasks');
                const itemFocus = await page.evaluate(readColors, '.phoenix-shell__menu-item:focus');
                note.itemFocus = itemFocus;
                t.ok('focused menu item is legible with a focus ring and no link underline', itemFocus && itemFocus.contrast >= 4.5 && itemFocus.outlineStyle === 'solid' && itemFocus.textDecoration === 'none', itemFocus);
                await page.mouse.move(0, 0);
                const itemBox = await page.locator('.phoenix-shell__menu-item').nth(1).boundingBox();
                await page.mouse.move(itemBox.x + 8, itemBox.y + itemBox.height / 2);
                const itemHover = await page.evaluate(readColors, '.phoenix-shell__menu-item:hover');
                note.itemHover = itemHover;
                t.ok('hovered menu item is legible and not a UA button face', itemHover && itemHover.contrast >= 4.5 && notFace(itemHover.effectiveBackground) && itemHover.textDecoration === 'none' && (dark ? itemHover.backgroundLuminance < 0.25 : true), itemHover);
                const before = await page.evaluate(() => window.chatEscapeSeen);
                await page.keyboard.press('Escape');
                f = await focused(page);
                t.ok('Escape closes the menu and returns focus to the launcher', f && f.label === 'Ứng dụng' && (await page.locator('[data-phoenix-popover]').count()) === 0, f);
                t.ok('Escape is consumed by the header, not Chat shortcuts', (await page.evaluate(() => window.chatEscapeSeen)) === before);

                // Account: Tab, Space, focus lands on the persisted mode, Tab to logout.
                await page.mouse.move(0, 0);
                await page.keyboard.press('Tab');
                f = await focused(page);
                t.ok('Tab reaches the account trigger', f && f.label === 'Tài khoản', f);
                const trig = await page.evaluate(readColors, TRIGGER.account);
                note.triggerFocus = trig;
                t.ok('account trigger focus ring is visible', trig.focusVisible && trig.outlineStyle === 'solid' && trig.outlineContrast >= 3 && trig.contrast >= 4.5, trig);
                await page.keyboard.press('Space');
                await page.waitForSelector('[data-phoenix-popover="account"]');
                f = await focused(page);
                t.ok('Space opens the account panel and focuses the persisted mode', f && f.role === 'radio' && f.checked === 'true' && f.text === (theme.mode === 'light' ? 'Sáng' : theme.mode === 'dark' ? 'Tối' : 'Hệ thống'), f);
                const radioFocus = await page.evaluate(readColors, '[role="radio"]:focus');
                note.radioFocus = radioFocus;
                t.ok('focused selected mode is legible with a visible ring', radioFocus && radioFocus.contrast >= 4.5 && radioFocus.outlineStyle === 'solid' && radioFocus.outlineContrast >= 3 && notFace(radioFocus.effectiveBackground), radioFocus);
                const unchecked = await page.evaluate(readColors, '[role="radio"][aria-checked="false"]');
                note.radioRest = unchecked;
                t.ok('unselected mode is transparent over the surface, not ButtonFace', unchecked && unchecked.effectiveBackground === theme.surface && unchecked.contrast >= 4.5, unchecked);
                await page.keyboard.press('Tab');
                f = await focused(page);
                t.ok('Tab moves from the modes to the logout link', f && f.tag === 'A' && f.text === 'Đăng xuất', f);
                const logout = await page.evaluate(readColors, '.phoenix-shell__logout:focus');
                note.logoutFocus = logout;
                t.ok('focused logout link is legible, ringed and not underlined', logout && logout.contrast >= 4.5 && logout.outlineStyle === 'solid' && logout.textDecoration === 'none', logout);
                await page.keyboard.press('Shift+Tab');
                t.ok('Shift+Tab returns to the selected mode (no focus trap)', (await focused(page)).role === 'radio');
                const colors = await page.evaluate(() => ({
                    muted: Array.from(document.querySelectorAll('.phoenix-shell__app-name, .phoenix-shell__account-tenant, .phoenix-shell__section-label')).map((e) => e.className),
                }));
                note.mutedClasses = colors.muted;
                const mutedChecks = ['.phoenix-shell__app-name', '.phoenix-shell__section-label', '.phoenix-shell__identity .phoenix-shell__account-tenant', '.phoenix-shell__tenant', '.phoenix-shell__identity .phoenix-shell__account-name'];
                for (const sel of mutedChecks) {
                    const r = await page.evaluate(readColors, sel);
                    t.ok(`text ${sel} has contrast >= 4.5`, r && r.contrast >= 4.5, r);
                }
                // Secondary text must use the canonical Gateway textMuted token, not a fallback.
                for (const sel of ['.phoenix-shell__section-label', '.phoenix-shell__identity .phoenix-shell__account-tenant']) {
                    const r = await page.evaluate(readColors, sel);
                    t.ok(`text ${sel} is the Gateway textMuted colour ${theme.muted}`, r && r.color === theme.muted, r);
                }
                {
                    const r = await page.evaluate(readColors, '.phoenix-shell__app-name');
                    t.ok('brand app label is the Gateway textMuted colour', r && r.color === theme.muted, r);
                }
                const selected = await page.evaluate(readColors, '[role="radio"][aria-checked="true"]');
                note.radioSelected = selected;
                t.ok('selected mode is legible', selected && selected.contrast >= 4.5 && selected.effectiveBackground !== theme.surface, selected);

                // Hover over an unselected mode and over logout.
                const radioBox = await page.locator('[role="radio"][aria-checked="false"]').first().boundingBox();
                await page.mouse.move(radioBox.x + radioBox.width / 2, radioBox.y + radioBox.height / 2);
                const radioHover = await page.evaluate(readColors, '[role="radio"]:hover');
                note.radioHover = radioHover;
                t.ok('hovered mode is legible and not a UA button face', radioHover && radioHover.contrast >= 4.5 && radioHover.effectiveBackground !== theme.surface && (dark ? radioHover.backgroundLuminance < 0.25 : true), radioHover);
                const logoutBox = await page.locator('.phoenix-shell__logout').boundingBox();
                await page.mouse.move(logoutBox.x + 20, logoutBox.y + logoutBox.height / 2);
                const logoutHover = await page.evaluate(readColors, '.phoenix-shell__logout:hover');
                note.logoutHover = logoutHover;
                t.ok('hovered logout link is legible, not underlined, not a UA button face', logoutHover && logoutHover.contrast >= 4.5 && logoutHover.textDecoration === 'none' && (dark ? logoutHover.backgroundLuminance < 0.25 : true), logoutHover);

                // Pointer: outside click closes and returns focus; the panels are mutually exclusive.
                // Middle of the chat area, between the messages and the composer: on mobile the panel covers the top.
                const chat = await page.locator('#fixture-chat').boundingBox();
                await page.mouse.click(chat.x + chat.width - 20, chat.y + chat.height / 2);
                await until(page, 'wait#1', () => document.querySelectorAll('[data-phoenix-popover]').length === 0);
                await until(page, 'wait#2', () => document.activeElement && document.activeElement.getAttribute('aria-label') === 'Tài khoản');
                t.ok('outside click closes the account panel and returns focus to its trigger', true);
                await page.click(TRIGGER.apps);
                await page.waitForSelector('[data-phoenix-popover="apps"]');
                await page.click(TRIGGER.account);
                await page.waitForSelector('[data-phoenix-popover="account"]');
                let both = await page.evaluate(() => Array.from(document.querySelectorAll('[data-phoenix-popover]')).map((p) => p.getAttribute('data-phoenix-popover')));
                f = await focused(page);
                t.ok('opening account closes the launcher and focuses the new panel', both.length === 1 && both[0] === 'account' && f.role === 'radio', {both, f});
                await page.click(TRIGGER.apps);
                await page.waitForSelector('[data-phoenix-popover="apps"]');
                both = await page.evaluate(() => Array.from(document.querySelectorAll('[data-phoenix-popover]')).map((p) => p.getAttribute('data-phoenix-popover')));
                f = await focused(page);
                t.ok('opening the launcher closes the account panel and focuses the menu', both.length === 1 && both[0] === 'apps' && f.role === 'menuitem', {both, f});
                await page.click(TRIGGER.apps);
                await until(page, 'wait#3', () => document.querySelectorAll('[data-phoenix-popover]').length === 0);
                f = await focused(page);
                t.ok('clicking the trigger again closes and keeps focus on it', f && f.label === 'Ứng dụng', f);
                await page.click(TRIGGER.apps);
                await page.waitForSelector('[data-phoenix-popover="apps"]');
                await page.click('#fixture-composer');
                await until(page, 'wait#4', () => document.querySelectorAll('[data-phoenix-popover]').length === 0);
                f = await focused(page);
                t.ok('outside click on a focusable control keeps focus there', f && f.id === 'fixture-composer', f);

                if (themeKey === 'light') {
                    // Live theme change through the keyboard: the real choose() path and ETag.
                    await page.focus(TRIGGER.account);
                    await page.keyboard.press('Enter');
                    await page.waitForSelector('[data-phoenix-popover="account"]');
                    await page.keyboard.press('ArrowRight');
                    await until(page, 'wait#5', () => document.documentElement.dataset.phoenixMode === 'dark');
                    const after = await page.evaluate(() => ({surface: getComputedStyle(document.querySelector('.phoenix-shell')).backgroundColor, panel: getComputedStyle(document.querySelector('[data-phoenix-popover="account"]')).backgroundColor, checked: document.querySelector('[role="radio"][aria-checked="true"]').textContent, calls: window.phoenixCalls.filter((x) => x.method === 'PATCH')}));
                    note.afterArrow = after;
                    t.ok('ArrowRight selects Tối through PATCH with the page ETag and repaints dark', after.surface === 'rgb(17, 17, 17)' && after.panel === 'rgb(17, 17, 17)' && after.checked === 'Tối' && after.calls.length === 1 && after.calls[0].body === '{"mode":"dark"}' && after.calls[0].ifMatch === '"7"', after);
                    f = await focused(page);
                    t.ok('focus follows the selected mode and the panel stays open', f && f.role === 'radio' && f.text === 'Tối');
                }
                t.ok('page has no script or console errors', problems.length === 0, problems);
            } catch (e) {
                e.message = `[H02 ${label}] ` + e.message;
                throw e;
            } finally {
                await context.close();
            }
            metrics.push(note);
            c.push(...t.rows);
        }
    }
    return {metrics, cases};
}

// ---------- commands ----------

function runtimeIdentity(pw, browser) {
    return {
        node: process.version, platform: process.platform, playwright: pw.version, playwrightFrom: path.relative(ECOSYSTEM, pw.from).replace(/\\/g, '/') || '.',
        chromium: browser.version(), url: `http://${HOST}:${PORT}/`, headless: true,
    };
}

function printRows(rows) {
    const failed = rows.filter((r) => !r.ok);
    for (const r of failed) {
        console.log(`FAIL ${r.check} [${r.case}] ${r.name} ${JSON.stringify(r.detail)}`);
    }
    return failed;
}

async function prepare() {
    const started = Date.now();
    for (const dir of [OUT, RESULTS, CAPTURES, LOGS, path.join(OUT, 'stubs')]) {
        fs.mkdirSync(dir, {recursive: true});
    }
    for (const [name, text] of Object.entries(STUBS)) {
        fs.writeFileSync(path.join(OUT, 'stubs', name), text);
    }
    const inputs = bundleInputs();
    const webpack = require(require.resolve('webpack', {paths: [WEBAPP]}));
    const stats = await new Promise((resolve, reject) => {
        process.chdir(CHANNELS);
        webpack(webpackConfig(), (err, s) => (err ? reject(err) : resolve(s)));
    });
    const info = stats.toString({colors: false, modules: false, children: false, assets: true, warnings: true, errors: true});
    fs.writeFileSync(path.join(LOGS, 'prepare-webpack.log'), info);
    if (stats.hasErrors()) {
        console.log(info.split('\n').filter((l) => /ERROR|Module not found|SassError/.test(l)).slice(0, 20).join('\n'));
        throw new Error('webpack build failed; see logs/prepare-webpack.log');
    }
    fs.writeFileSync(path.join(BUNDLE, 'index.html'), HTML);
    const outputs = {};
    for (const name of ['index.html', 'native.css', 'fixture.css', 'fixture.js']) {
        outputs[name] = sha256File(path.join(BUNDLE, name));
    }
    fs.writeFileSync(INPUTS_FILE, JSON.stringify({preparedAt: new Date().toISOString(), inputs, outputs}, null, 2));

    // Early risk check: the real shell mounts with the real compiled CSS.
    const early = await withBrowser(async ({browser, pw}) => {
        const {page, context, problems} = await openPage(browser, DESKTOP);
        try {
            const m = await page.evaluate(() => {
                const l = document.querySelector('.phoenix-shell__icon-button');
                const s = getComputedStyle(l);
                const h = document.querySelector('[data-testid="phoenix-header"]').getBoundingClientRect();
                const css = Array.from(document.styleSheets).map((sh) => sh.href && sh.href.split('/').pop());
                return {launcher: {width: l.getBoundingClientRect().width, height: l.getBoundingClientRect().height, borderTopWidth: s.borderTopWidth, minHeight: s.minHeight, borderRadius: s.borderTopLeftRadius}, header: {height: h.height}, stylesheets: css};
            });
            return {identity: runtimeIdentity(pw, browser), problems, ...m};
        } finally {
            await context.close();
        }
    });
    fs.writeFileSync(path.join(RESULTS, 'prepare-early-risk.json'), JSON.stringify(early, null, 2));
    console.log(`PREP ok in ${((Date.now() - started) / 1000).toFixed(1)}s`);
    console.log('early risk:', JSON.stringify({launcher: early.launcher, header: early.header, stylesheets: early.stylesheets, problems: early.problems}));
    console.log('inputs:', JSON.stringify(inputs.files));
    console.log('native sass digest:', inputs.nativeSassDigest);
    if (early.problems.length || early.header.height !== 72) {
        throw new Error('early risk check failed');
    }
}

function selected(arg) {
    const ids = !arg || arg === 'all' ? Object.keys(CHECKS) : arg.split(',');
    const unknown = ids.filter((i) => !CHECKS[i]);
    if (unknown.length || ids.length === 0) {
        throw Object.assign(new Error(`unknown or empty selector: ${arg}; available: ${Object.keys(CHECKS).join(', ')}`), {usage: true});
    }
    return ids;
}

async function check(arg) {
    const ids = selected(arg);
    const prepared = assertPrepared();
    const started = Date.now();
    const rows = [];
    const metrics = {};
    const run = await withBrowser(async ({browser, pw}) => {
        const identity = runtimeIdentity(pw, browser);
        for (const id of ids) {
            const t0 = Date.now();
            const res = id === 'H01' ? await runH01(browser, rows) : await runH02(browser, rows);
            metrics[id] = {...res, elapsedMs: Date.now() - t0};
        }
        return identity;
    });
    for (const id of ids) {
        const own = rows.filter((r) => r.check === id);
        const failed = own.filter((r) => !r.ok);
        fs.writeFileSync(path.join(RESULTS, `check-${id}.json`), JSON.stringify({check: id, title: CHECKS[id], runtime: run, inputs: prepared.inputs.files, outputs: prepared.outputs, assertions: own.length, failed: failed.length, cases: metrics[id].cases, elapsedMs: metrics[id].elapsedMs, rows: own, metrics: metrics[id].metrics}, null, 2));
        console.log(`${id} ${CHECKS[id]}: ${own.length - failed.length}/${own.length} assertions passed over ${metrics[id].cases} cases in ${(metrics[id].elapsedMs / 1000).toFixed(1)}s`);
    }
    if (rows.length === 0) {
        throw new Error('selector matched zero assertions');
    }
    const failed = printRows(rows);
    console.log(`elapsed ${((Date.now() - started) / 1000).toFixed(1)}s; runtime ${JSON.stringify(run)}`);
    process.exitCode = failed.length ? 1 : 0;
}

async function capture(arg) {
    if (arg !== 'all') {
        throw Object.assign(new Error('capture supports only: all'), {usage: true});
    }
    const prepared = assertPrepared();
    fs.mkdirSync(CAPTURES, {recursive: true});
    const shots = [];
    const started = Date.now();
    const identity = await withBrowser(async ({browser, pw}) => {
        const open = async (state, page) => {
            if (state === 'closed') {
                return;
            }
            // Open by keyboard so the focus ring is part of the picture.
            await page.focus(TRIGGER[state]);
            await page.keyboard.press('Enter');
            await page.waitForSelector(`[data-phoenix-popover="${state}"]`);
        };
        const jobs = [];
        for (const [vpKey, vp] of Object.entries({desktop: DESKTOP, mobile: MOBILE})) {
            for (const themeKey of ['light', 'dark']) {
                for (const state of ['closed', 'account', 'apps']) {
                    jobs.push({file: `${vpKey}-${themeKey}-${state}`, vpKey, vp, themeKey, state, scenario: SCENARIOS.short});
                }
            }
        }
        jobs.push(
            {file: 'extra-long-desktop-light-account', vpKey: 'desktop', vp: DESKTOP, themeKey: 'light', state: 'account', scenario: SCENARIOS.long},
            {file: 'extra-long-mobile-dark-account', vpKey: 'mobile', vp: MOBILE, themeKey: 'dark', state: 'account', scenario: SCENARIOS.long},
            {file: 'extra-long-mobile-light-apps', vpKey: 'mobile', vp: MOBILE, themeKey: 'light', state: 'apps', scenario: SCENARIOS.long},
            {file: 'extra-short-viewport-light-account', vpKey: 'short', vp: SHORT, themeKey: 'light', state: 'account', scenario: SCENARIOS.short},
            {file: 'extra-boundary-641-long-light-apps', vpKey: 'w641', vp: {width: 641, height: 844}, themeKey: 'light', state: 'apps', scenario: SCENARIOS.long},
            {file: 'extra-gateway-dark-desktop-account', vpKey: 'desktop', vp: DESKTOP, themeKey: 'dark', state: 'account', scenario: {...SCENARIOS.short, palette: 'gateway'}},
            {file: 'extra-gateway-dark-mobile-account', vpKey: 'mobile', vp: MOBILE, themeKey: 'dark', state: 'account', scenario: {...SCENARIOS.short, palette: 'gateway'}},
        );
        for (const job of jobs) {
            const {page, context, problems} = await openPage(browser, job.vp, {...job.scenario, mode: job.themeKey});
            try {
                await open(job.state, page);
                await page.mouse.move(0, 0);
                await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
                const file = path.join(CAPTURES, job.file + '.png');
                await page.screenshot({path: file});
                const m = await page.evaluate(measureLayout);
                const png = fs.readFileSync(file);
                const w = png.readUInt32BE(16);
                const h = png.readUInt32BE(20);
                shots.push({file: job.file + '.png', viewport: job.vp, theme: job.themeKey, state: job.state, bytes: png.length, sha256: sha256(png), pixels: [w, h], problems, header: m.header.rect, panel: m.panel && m.panel.rect});
            } finally {
                await context.close();
            }
        }
        return runtimeIdentity(pw, browser);
    });
    const bad = shots.filter((s) => s.pixels[0] !== s.viewport.width || s.pixels[1] !== s.viewport.height || s.bytes < 2000 || s.problems.length);
    fs.writeFileSync(path.join(CAPTURES, 'manifest.json'), JSON.stringify({runtime: identity, inputs: prepared.inputs.files, outputs: prepared.outputs, elapsedMs: Date.now() - started, shots}, null, 2));
    console.log(`CAPTURE ${shots.length} images (${shots.filter((s) => !s.file.startsWith('extra-')).length} required + extras) in ${((Date.now() - started) / 1000).toFixed(1)}s -> ${CAPTURES}`);
    for (const b of bad) {
        console.log('BAD', b.file, JSON.stringify({pixels: b.pixels, bytes: b.bytes, problems: b.problems}));
    }
    process.exitCode = bad.length ? 1 : 0;
}

async function main() {
    const [cmd, arg] = process.argv.slice(2);
    if (cmd === '--list') {
        for (const [id, title] of Object.entries(CHECKS)) {
            console.log(`${id} ${title}`);
        }
        return;
    }
    if (cmd === '--prepare') {
        await prepare();
    } else if (cmd === '--check') {
        await check(arg);
    } else if (cmd === '--capture') {
        await capture(arg);
    } else {
        throw Object.assign(new Error('usage: --prepare | --list | --check <H01|H02|all> | --capture all'), {usage: true});
    }
}

main().catch((e) => {
    console.error('ERROR: ' + e.message);
    process.exitCode = e.usage ? 2 : 1;
});
