// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.
/* eslint-disable no-undef, @typescript-eslint/no-require-imports -- Node CLI run directly by node, never bundled */
'use strict';

// Header fixture runner. Run from webapp/channels:
//   node src/components/phoenix_shell/header.browser.cjs --prepare
//   node src/components/phoenix_shell/header.browser.cjs --list
//   node src/components/phoenix_shell/header.browser.cjs --check <H01|H02|all>
//   node src/components/phoenix_shell/header.browser.cjs --capture all
//
// --prepare bundles the REAL PhoenixShell, GlobalHeader, ChannelHeader, and MobileChannelHeader
// with the REAL phoenix_shell.scss on top of the REAL native Mattermost stylesheet (src/sass/styles.scss),
// in the native order, and records the sha256 of every input. --check/--capture serve that bundle on an
// owned loopback port and drive Chromium; they refuse to run when an input changed since --prepare and
// never build. Only the network boundary and non-header modal/store boundaries are replaced (see STUBS).
// Fixture output is NOT authenticated production evidence.

const fs = require('fs');
const path = require('path');
const http = require('http');
const crypto = require('crypto');

const CHANNELS = path.resolve(__dirname, '..', '..', '..');
const COMPONENTS = path.resolve(__dirname, '..');
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

const SOURCES = [
    path.join(__dirname, 'index.tsx'),
    path.join(__dirname, 'header_slots.tsx'),
    path.join(__dirname, 'phoenix_shell.scss'),
    path.join(__dirname, 'client.ts'),
    path.join(__dirname, 'theme.ts'),
    path.join(__dirname, 'session_lifecycle.ts'),
    path.join(__dirname, 'header.browser.tsx'),
    path.join(__dirname, 'header.browser.cjs'),
    path.join(COMPONENTS, 'global_header', 'global_header.tsx'),
    path.join(COMPONENTS, 'global_header', 'left_controls', 'left_controls.tsx'),
    path.join(COMPONENTS, 'global_header', 'center_controls', 'center_controls.tsx'),
    path.join(COMPONENTS, 'global_header', 'right_controls', 'right_controls.tsx'),
    path.join(COMPONENTS, 'root', 'root.tsx'),
    path.join(COMPONENTS, 'channel_view', 'channel_view.tsx'),
    path.join(COMPONENTS, 'channel_header', 'channel_header.tsx'),
    path.join(COMPONENTS, 'mobile_channel_header', 'mobile_channel_header.tsx'),
    path.join(COMPONENTS, 'channel_layout', 'center_channel', 'center_channel.tsx'),
];

// Fixture-only replacements of heavy non-header boundaries (store singleton, modals, markdown).
// All header owners, menus, triggers, slots, client.ts, theme.ts, and index.tsx are 100% real.
const STUBS = {
    'redux_store.js': [
        'export default {',
        '    dispatch(a) { return window.__phoenixFixtureStore ? window.__phoenixFixtureStore.dispatch(a) : a; },',
        '    getState() { return window.__phoenixFixtureStore ? window.__phoenixFixtureStore.getState() : {views: {browser: {}}, entities: {general: {config: {}, license: {}}, users: {profiles: {}}, teams: {teams: {}}, channels: {channels: {}}, preferences: {myPreferences: {}}}}; },',
        '    subscribe(fn) { return window.__phoenixFixtureStore ? window.__phoenixFixtureStore.subscribe(fn) : () => {}; },',
        '};',
        '',
    ].join('\n'),
    'initial_loading_screen.js': 'export default {stop() {}};\n',
    'emotion_styled.js': [
        'import React from "react";',
        'export default function styled(Component, opts) {',
        '    const shouldForward = opts && opts.shouldForwardProp;',
        '    return function() {',
        '        return React.forwardRef(function Styled(props, ref) {',
        '            if (!shouldForward) return React.createElement(Component, {...props, ref});',
        '            const next = {};',
        '            for (const [k, v] of Object.entries(props)) {',
        '                if (shouldForward(k)) next[k] = v;',
        '            }',
        '            return React.createElement(Component, {...next, ref});',
        '        });',
        '    };',
        '}',
        '',
    ].join('\n'),
    'empty_modal.js': [
        'export default function EmptyModal() { return null; }',
        'export const CustomizeYourExperienceTour = () => null;',
        'export const CompleteYourProfileTour = () => null;',
        'export const useShowOnboardingTutorialStep = () => false;',
        'export const OnboardingTourSteps = {CUSTOMIZE_EXPERIENCE: 0};',
        'export const OnboardingTourStepsForGuestUsers = {CUSTOMIZE_EXPERIENCE: 0};',
        'export const OnboardingTaskCategory = "onboarding";',
        'export const OnboardingTasksName = {VISIT_SYSTEM_CONSOLE: "visit_system_console", COMPLETE_YOUR_PROFILE: "complete_your_profile"};',
        'export const TaskNameMapToSteps = {visit_system_console: {STARTED: 1, FINISHED: 2}, complete_your_profile: {STARTED: 1, FINISHED: 2}};',
        'export const useHandleOnBoardingTaskData = () => () => {};',
        '',
    ].join('\n'),
    'markdown_stub.js': [
        'import React from "react";',
        'export default function Markdown(props) {',
        '    return React.createElement("p", {className: "markdown__paragraph-inline"}, props && props.message);',
        '}',
        '',
    ].join('\n'),
    'global_actions_stub.js': [
        'export function emitUserLoggedOutEvent() {',
        '    if (window.phoenixDispatched) {',
        '        window.phoenixDispatched.push({type: "EMIT_USER_LOGGED_OUT_EVENT"});',
        '    }',
        '}',
        'export function showMobileSubMenuModal() {}',
        'export function toggleSideBarRightMenuAction() { return () => {}; }',
        '',
    ].join('\n'),
    'native_utils.js': [
        '// Mirrors what the native applyTheme and utility helpers do for the header components.',
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
        'export function isEmptyObject(object) {',
        '    return !object || Object.keys(object).length === 0;',
        '}',
        'export function handleFormattedTextClick() {}',
        'export function localizeMessage(msg) { return msg && msg.defaultMessage ? msg.defaultMessage : String(msg || ""); }',
        'export function getDisplayNameByUser(state, user) { return user ? (user.nickname || user.first_name || user.username || "") : ""; }',
        'export function imageURLForUser() { return ""; }',
        'export function isMac() { return false; }',
        'export function cmdOrCtrlPressed(e) { return Boolean(e && (e.ctrlKey || e.metaKey)); }',
        'export function isKeyPressed(e, code) { return Boolean(e && (e.key === (code && code[0]) || e.keyCode === (code && code[1]))); }',
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
                {test: /\.(js|jsx|ts|tsx)$/, exclude: /node_modules|[\\/]stubs[\\/]/, use: {loader: 'babel-loader', options: {cacheDirectory: false}}},
                {test: /\.(css|scss)$/, use: [MiniCssExtractPlugin.loader, 'css-loader']},
                {test: /\.scss$/, use: [{loader: 'sass-loader', options: {sassOptions: {loadPaths: ['src/sass']}}}]},
                {test: /\.(png|eot|tiff|svg|woff2|woff|ttf|gif|mp3|jpg)$/, type: 'asset/resource'},
            ],
        },
        resolve: {
            modules: [path.join(CHANNELS, 'node_modules'), path.join(WEBAPP, 'node_modules'), 'node_modules', path.join(CHANNELS, 'src')],
            alias: {
                'stores/redux_store$': stub('redux_store.js'),
                'components/initial_loading_screen$': stub('initial_loading_screen.js'),
                'utils/utils$': stub('native_utils.js'),
                'actions/global_actions$': stub('global_actions_stub.js'),
                'components/markdown$': stub('markdown_stub.js'),
                'components/user_settings/modal$': stub('empty_modal.js'),
                'components/custom_status/custom_status_modal$': stub('empty_modal.js'),
                'components/custom_status/expiry_menu_items$': stub('empty_modal.js'),
                'components/keyboard_shortcuts/keyboard_shortcuts_modal/keyboard_shortcuts_modal$': stub('empty_modal.js'),
                'components/edit_channel_header_modal$': stub('empty_modal.js'),
                'components/edit_channel_purpose_modal$': stub('empty_modal.js'),
                'components/rename_channel_modal$': stub('empty_modal.js'),
                'components/convert_channel_modal$': stub('empty_modal.js'),
                'components/delete_channel_modal$': stub('empty_modal.js'),
                'components/leave_channel_modal$': stub('empty_modal.js'),
                'components/channel_notifications_modal$': stub('empty_modal.js'),
                'components/channel_invite_modal$': stub('empty_modal.js'),
                'components/channel_members_modal$': stub('empty_modal.js'),
                'components/channel_settings_modal/channel_settings_modal$': stub('empty_modal.js'),
                'components/channel_bookmarks/channel_bookmarks_create_modal$': stub('empty_modal.js'),
                'components/more_direct_channels$': stub('empty_modal.js'),
                'components/invitation_modal$': stub('empty_modal.js'),
                'components/team_settings_modal$': stub('empty_modal.js'),
                'components/team_members_modal$': stub('empty_modal.js'),
                'components/leave_team_modal$': stub('empty_modal.js'),
                'components/create_user_groups_modal$': stub('empty_modal.js'),
                'components/user_groups_modal$': stub('empty_modal.js'),
                'components/about_build_modal$': stub('empty_modal.js'),
                'components/user_profile_modal$': stub('empty_modal.js'),
                'components/profile_popover$': stub('empty_modal.js'),
                'components/user_group_popover$': stub('empty_modal.js'),
                'components/channel_move_to_sub_menu$': stub('empty_modal.js'),
                'components/channel_attributes/channel_attribute_labels$': stub('empty_modal.js'),
                'components/new_search/search_box$': stub('empty_modal.js'),
                'components/tours$': stub('empty_modal.js'),
                'components/tours/onboarding_tour$': stub('empty_modal.js'),
                'components/onboarding_tasks$': stub('empty_modal.js'),
                'components/app_bar/new_channel_with_board_tour_tip$': stub('empty_modal.js'),
                '@emotion/styled$': stub('emotion_styled.js'),
                'mattermost-redux': path.join(CHANNELS, 'src', 'packages', 'mattermost-redux', 'src'),
                '@mattermost/shared': path.join(WEBAPP, 'platform', 'shared', 'src'),
                '@mattermost/types': path.join(WEBAPP, 'platform', 'types', 'src'),
                '@mattermost/client/lib': path.join(WEBAPP, 'platform', 'client', 'src'),
                '@mattermost/client': path.join(WEBAPP, 'platform', 'client', 'src'),
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
        const rel = path.relative(COMPONENTS, f).replace(/\\/g, '/');
        files[rel] = sha256File(f);
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
        if (!prepared.inputs.files || prepared.inputs.files[name] !== hash) {
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

const EMPTY_PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=', 'base64');

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
            if (url.pathname.startsWith('/api/v4/')) {
                if (url.pathname.endsWith('/image')) {
                    res.writeHead(200, {'Content-Type': 'image/png', 'Cache-Control': 'no-store'}).end(EMPTY_PNG);
                } else {
                    res.writeHead(200, {'Content-Type': 'application/json', 'Cache-Control': 'no-store'}).end('{}');
                }
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
    try {
        await page.waitForSelector('[data-testid="phoenix-header"]', {timeout: 15000});
    } catch (e) {
        const html = await page.evaluate(() => document.body.innerHTML.slice(0, 500));
        await context.close();
        throw new Error(`${e.message}; problems=${JSON.stringify(problems)}; html=${JSON.stringify(html)}`);
    }
    await page.evaluate(() => document.fonts.ready);
    return {page, context, problems};
}

const SCENARIOS = {
    short: {
        tenant: 'Tenant A',
        channelName: 'General',
        channelHeader: 'Team discussions',
        account: 'A1',
    },
    long: {
        tenant: 'A'.repeat(120),
        channelName: 'C'.repeat(120),
        channelHeader: 'D'.repeat(120),
        account: 'B'.repeat(120),
        apps: Array.from({length: 12}, (_, i) => ({appCode: 'app' + i, label: i === 3 ? 'E'.repeat(120) : 'Ứng dụng ' + i, state: 'ready', launchUrl: `https://app${i}.phoenixsoft.vn`})),
        plugins: [
            {id: 'plug-1', label: 'Jira'},
            {id: 'plug-2', label: 'GitLab'},
            {id: 'plug-3', label: 'Zoom'},
        ],
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

const TRIGGER = {
    apps: 'button[aria-label="Ứng dụng"]',
    theme: 'button[aria-label^="Giao diện:"]',
    utilities: 'button[aria-label="Tiện ích"]',
};

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
    const launcher = q('button[aria-label="Ứng dụng"]', header);
    const themeTrigger = q('button[aria-label^="Giao diện:"]', header);
    const utilitiesTrigger = q('button[aria-label="Tiện ích"]', header);
    const radios = qa('[role="menuitemradio"]');
    const panel = q('[data-phoenix-popover]');
    const corners = (el) => {
        const s = getComputedStyle(el);
        return [s.borderTopLeftRadius, s.borderTopRightRadius, s.borderBottomRightRadius, s.borderBottomLeftRadius];
    };
    const borders = (el) => {
        const s = getComputedStyle(el);
        return [s.borderTopWidth, s.borderRightWidth, s.borderBottomWidth, s.borderLeftWidth];
    };
    const truncated = (el) => (el ? {
        scrollWidth: el.scrollWidth,
        clientWidth: el.clientWidth,
        textOverflow: getComputedStyle(el).textOverflow,
        overflow: getComputedStyle(el).overflowX,
        whiteSpace: getComputedStyle(el).whiteSpace,
    } : null);
    const reach = (el) => {
        if (!el || !panel) {
            return null;
        }
        el.scrollIntoView({block: 'nearest'});
        const p = rect(panel);
        const r = rect(el);
        return r.t >= p.t - 1 && r.b <= p.b + 1 && r.l >= p.l - 1 && r.r <= p.r + 1;
    };

    const topGroups = [
        q('[data-header-slot="left"]', header),
        q('.phoenix-shell__brand', header),
        q('[data-header-slot="channel"]', header),
        q(':scope > [data-header-slot="utilities"]', header),
        q('.phoenix-shell__controls', header),
    ].filter((el) => el && getComputedStyle(el).display !== 'none' && el.getBoundingClientRect().width > 0);

    const headerButtons = qa('button, #searchFormContainer', header).filter((el) => {
        if (panel && panel.contains(el)) {
            return false;
        }
        const s = getComputedStyle(el);
        return s.display !== 'none' && s.visibility !== 'hidden';
    });

    const out = {
        viewport: {w: window.innerWidth, h: window.innerHeight},
        doc: {scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth},
        bannerCount: qa('header, [role="banner"]').length,
        extraHeaderRows: qa('#global-header, .row.header').length,
        header: {
            rect: rect(header),
            scrollWidth: header.scrollWidth,
            clientWidth: header.clientWidth,
            display: getComputedStyle(header).display,
            flexWrap: getComputedStyle(header).flexWrap,
        },
        groups: topGroups.map((g) => ({
            name: g.getAttribute('data-header-slot') || g.className,
            rect: rect(g),
        })),
        brand: rect(q('.phoenix-shell__brand', header)),
        controls: rect(q('.phoenix-shell__controls', header)),
        composer: rect(q('#fixture-composer')),
        tenant: truncated(q('.phoenix-shell__tenant', header)),
        channelTitle: truncated(q('#channelHeaderTitle', header)),
        channelDesc: truncated(q('.header-description__text p.markdown__paragraph-inline', header) || q('.header-description__text', header)),
        launcher: {
            rect: rect(launcher),
            borders: borders(launcher),
            corners: corners(launcher),
            dots: qa('circle', launcher).length,
            background: getComputedStyle(launcher).backgroundColor,
        },
        themeTrigger: {
            rect: rect(themeTrigger),
            borders: borders(themeTrigger),
            corners: corners(themeTrigger),
            label: themeTrigger.getAttribute('aria-label'),
            haspopup: themeTrigger.getAttribute('aria-haspopup'),
            expanded: themeTrigger.getAttribute('aria-expanded'),
        },
        utilitiesTrigger: utilitiesTrigger ? {
            rect: rect(utilitiesTrigger),
            expanded: utilitiesTrigger.getAttribute('aria-expanded'),
        } : null,
        launcherExpanded: launcher.getAttribute('aria-expanded'),
        headerButtons: headerButtons.map((b) => ({
            id: b.id || b.getAttribute('aria-label') || b.className,
            rect: rect(b),
        })),
        panels: qa('[data-phoenix-popover]').map((p) => p.getAttribute('data-phoenix-popover')),
        radioCount: radios.length,
        panel: null,
    };
    if (panel) {
        const items = qa('.phoenix-shell__menu-item', panel);
        const utilButtons = qa('button, #searchFormContainer', panel);
        out.panel = {
            kind: panel.getAttribute('data-phoenix-popover'),
            rect: rect(panel),
            overflowY: getComputedStyle(panel).overflowY,
            scrollHeight: panel.scrollHeight,
            clientHeight: panel.clientHeight,
            scrollWidth: panel.scrollWidth,
            clientWidth: panel.clientWidth,
            modes: radios.map((r) => ({rect: rect(r), borders: borders(r), corners: corners(r), label: r.textContent})),
            items: items.map((i) => ({rect: rect(i), truncated: truncated(i)})),
            utilButtons: utilButtons.map((b) => ({id: b.id || b.getAttribute('aria-label') || b.textContent, rect: rect(b)})),
            reachable: [...radios, ...items, ...utilButtons].filter(Boolean).map((el) => reach(el)),
        };
        panel.scrollTop = 0;
    }
    return out;
}

async function runH01(browser, c) {
    const cases = [];
    for (const [tenantKey, scenario] of Object.entries(SCENARIOS)) {
        for (const [vpKey, vp] of Object.entries({desktop: DESKTOP, mobile: MOBILE})) {
            for (const modeKey of ['light', 'dark']) {
                const states = vpKey === 'mobile' ? ['closed', 'theme', 'apps', 'utilities'] : ['closed', 'theme', 'apps'];
                for (const state of states) {
                    cases.push({tenantKey, scenario: {...scenario, mode: modeKey}, vpKey, vp, modeKey, state});
                }
            }
        }
    }
    // A viewport shorter than the panel: it must scroll, not clip.
    for (const state of ['theme', 'apps', 'utilities']) {
        cases.push({tenantKey: 'long', scenario: {...SCENARIOS.long, mode: 'light'}, vpKey: 'short', vp: SHORT, modeKey: 'light', state});
    }
    for (const width of [641, 720, 721, 768]) {
        for (const state of ['theme', 'apps', 'utilities']) {
            cases.push({tenantKey: 'long', scenario: {...SCENARIOS.long, mode: 'light'}, vpKey: 'w' + width, vp: {width, height: 844}, modeKey: 'light', state});
        }
    }
    const metrics = [];
    for (const k of cases) {
        const label = `${k.tenantKey}/${k.vpKey}/${k.modeKey}/${k.state}`;
        const t = collector('H01', {label});
        const {page, context, problems} = await openPage(browser, k.vp, k.scenario, k.modeKey);
        try {
            if (k.state !== 'closed') {
                await page.click(TRIGGER[k.state]);
                await page.waitForSelector(`[data-phoenix-popover="${k.state}"]`);
            }
            const m = await page.evaluate(measureLayout);
            metrics.push({case: label, ...m});
            const narrow = k.vp.width <= 640;
            const headerCenterY = (m.header.rect.t + m.header.rect.b) / 2;
            t.ok('page has no script or console errors', problems.length === 0, problems);
            t.ok('exactly one banner and no extra header rows', m.bannerCount === 1 && m.extraHeaderRows === 0, {bannerCount: m.bannerCount, extraHeaderRows: m.extraHeaderRows});
            t.near('header height', m.header.rect.h, narrow ? 64 : 72);
            t.ok('header is a single non-wrapping row', m.header.display === 'flex' && m.header.flexWrap === 'nowrap' &&
                m.groups.length >= 4 &&
                m.groups.every((g, idx) => g.rect.t >= m.header.rect.t - TOLERANCE &&
                    g.rect.b <= m.header.rect.b + TOLERANCE &&
                    Math.abs((g.rect.t + g.rect.b) / 2 - headerCenterY) <= 4 &&
                    (idx === 0 || g.rect.l >= m.groups[idx - 1].rect.r - TOLERANCE)), m.groups);
            t.ok('header and page do not overflow horizontally', m.header.scrollWidth <= m.header.clientWidth + TOLERANCE && m.doc.scrollWidth <= m.doc.clientWidth + TOLERANCE, {header: [m.header.scrollWidth, m.header.clientWidth], doc: m.doc});
            t.ok('composer stays below the header', m.composer && m.composer.t >= m.header.rect.b - TOLERANCE, {composer: m.composer, header: m.header.rect});
            t.ok('all visible header buttons meet 44x44 minimum target', m.headerButtons.length > 0 && m.headerButtons.every((b) => b.rect.w >= 44 - TOLERANCE && b.rect.h >= 44 - TOLERANCE), m.headerButtons);
            t.near('launcher width', m.launcher.rect.w, 44);
            t.near('launcher height', m.launcher.rect.h, 44);
            t.ok('launcher has no border', m.launcher.borders.every((b) => b === '0px'), m.launcher.borders);
            t.ok('launcher radius is 8px', m.launcher.corners.every((r) => r === '8px'), m.launcher.corners);
            t.ok('launcher draws the nine dots', m.launcher.dots === 9, m.launcher.dots);
            t.near('theme trigger width', m.themeTrigger.rect.w, 44);
            t.near('theme trigger height', m.themeTrigger.rect.h, 44);
            t.ok('theme trigger has menu popup and localized label', m.themeTrigger.haspopup === 'menu' && /^Giao diện: (Sáng|Tối|Hệ thống)$/.test(m.themeTrigger.label || ''), m.themeTrigger);
            t.ok('tenant and channel title have computed nowrap and ellipsis', m.tenant && m.tenant.whiteSpace === 'nowrap' && m.tenant.textOverflow === 'ellipsis' && m.channelTitle && m.channelTitle.whiteSpace === 'nowrap' && m.channelTitle.textOverflow === 'ellipsis', {tenant: m.tenant, channelTitle: m.channelTitle});
            if (k.tenantKey === 'long') {
                t.ok('long tenant truncates with an ellipsis', m.tenant.scrollWidth > m.tenant.clientWidth && m.tenant.textOverflow === 'ellipsis' && m.tenant.overflow === 'hidden', m.tenant);
                t.ok('long channel title truncates with an ellipsis', m.channelTitle.scrollWidth > m.channelTitle.clientWidth && m.channelTitle.textOverflow === 'ellipsis' && m.channelTitle.overflow === 'hidden', m.channelTitle);
                if (k.vpKey === 'desktop') {
                    t.ok('long channel description truncates on desktop', m.channelDesc && m.channelDesc.scrollWidth > m.channelDesc.clientWidth && m.channelDesc.textOverflow === 'ellipsis', m.channelDesc);
                }
            } else {
                t.ok('short tenant is not truncated', m.tenant.scrollWidth <= m.tenant.clientWidth + TOLERANCE, m.tenant);
                t.ok('short channel title is not truncated', m.channelTitle.scrollWidth <= m.channelTitle.clientWidth + TOLERANCE, m.channelTitle);
            }
            if (k.state === 'closed') {
                t.ok('closed header has no open panel or radio', m.panels.length === 0 && m.radioCount === 0, {panels: m.panels, radios: m.radioCount});
                t.ok('triggers collapsed', m.launcherExpanded === 'false' && m.themeTrigger.expanded === 'false' && (!m.utilitiesTrigger || m.utilitiesTrigger.expanded === 'false'));
            } else {
                const p = m.panel;
                t.ok('exactly the requested panel is open', m.panels.length === 1 && m.panels[0] === k.state, m.panels);
                t.ok('panel is inside the viewport', p.rect.l >= -TOLERANCE && p.rect.t >= -TOLERANCE && p.rect.r <= m.viewport.w + TOLERANCE && p.rect.b <= m.viewport.h + TOLERANCE, {panel: p.rect, viewport: m.viewport});
                t.ok('panel keeps a 16px viewport allowance', p.rect.w <= m.viewport.w - 16 + TOLERANCE, p.rect.w);
                t.ok('panel scrolls vertically and does not overflow sideways', p.overflowY === 'auto' && p.scrollWidth <= p.clientWidth + TOLERANCE, {overflowY: p.overflowY, w: [p.scrollWidth, p.clientWidth]});
                t.ok('every control is reachable inside the panel', p.reachable.length > 0 && p.reachable.every(Boolean), p.reachable);
                if (k.vpKey === 'short' && k.state === 'apps') {
                    t.ok('short viewport really scrolls the apps panel', p.scrollHeight > p.clientHeight + TOLERANCE, {scrollHeight: p.scrollHeight, clientHeight: p.clientHeight});
                }
                if (k.state === 'theme') {
                    t.ok('three theme options are present', p.modes.length === 3 && p.modes.map((r) => r.label).join(',') === 'Sáng,Tối,Hệ thống', p.modes);
                    t.ok('theme options are at least 44px', p.modes.every((r) => r.rect.h >= 44 - TOLERANCE && r.rect.w >= 44 - TOLERANCE), p.modes.map((r) => r.rect));
                    t.ok('theme options have no border and 6px radius', p.modes.every((r) => r.borders.every((b) => b === '0px') && r.corners.every((x) => x === '6px')), p.modes.map((r) => [r.borders, r.corners]));
                } else if (k.state === 'apps') {
                    t.ok('launcher expanded and theme collapsed', m.launcherExpanded === 'true' && m.themeTrigger.expanded === 'false');
                    t.ok('menu items are at least 44px', p.items.length > 0 && p.items.every((i) => i.rect.h >= 44 - TOLERANCE), p.items.map((i) => i.rect.h));
                    t.ok('pending apps are not offered', p.items.length === (k.tenantKey === 'long' ? 12 : 2), p.items.length);
                    if (k.tenantKey === 'long') {
                        t.ok('long menu label truncates', p.items.some((i) => i.truncated.scrollWidth > i.truncated.clientWidth && i.truncated.textOverflow === 'ellipsis'));
                    }
                } else if (k.state === 'utilities') {
                    t.ok('overflow utilities panel exposes mobile and global actions >= 44x44', p.utilButtons.length >= 6 && p.utilButtons.every((b) => b.rect.w >= 44 - TOLERANCE && b.rect.h >= 44 - TOLERANCE), p.utilButtons);
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

                // Verify unique ownership of controls in the header (and inside utilities overflow on mobile).
                if (vpKey === 'mobile') {
                    await page.click(TRIGGER.utilities);
                    await page.waitForSelector('[data-phoenix-popover="utilities"]');
                }
                const ownership = await page.evaluate(() => {
                    const header = document.querySelector('[data-testid="phoenix-header"]');
                    const count = (sel) => header.querySelectorAll(sel).length;
                    return {
                        productSwitch: count('#product_switch_menu'),
                        channelMenu: count('#channelHeaderDropdownButton'),
                        search: count('#searchFormContainer, button.navbar-search'),
                        help: count('button[aria-label="Help"]'),
                        mentions: count('button[aria-label="Recent mentions"]'),
                        bookmarks: count('button[aria-label="Saved messages"]'),
                        settings: count('button[aria-label="Settings"]'),
                        accountMenu: count('#userAccountMenuButton'),
                        appsLauncher: count('button[aria-label="Ứng dụng"]'),
                        themeTrigger: count('button[aria-label^="Giao diện:"]'),
                        duplicatePhoenixAccount: count('.phoenix-shell__account-trigger, [data-phoenix-popover="account"]'),
                    };
                });
                note.ownership = ownership;
                t.ok('each global, channel, launcher, and theme action appears exactly once with no duplicate account trigger',
                    ownership.productSwitch === 1 &&
                    ownership.channelMenu === 1 &&
                    ownership.search === 1 &&
                    ownership.help === 1 &&
                    ownership.mentions === 1 &&
                    ownership.bookmarks === 1 &&
                    ownership.settings === 1 &&
                    ownership.accountMenu === 1 &&
                    ownership.appsLauncher === 1 &&
                    ownership.themeTrigger === 1 &&
                    ownership.duplicatePhoenixAccount === 0,
                    ownership,
                );
                if (vpKey === 'mobile') {
                    await page.keyboard.press('Escape');
                    await until(page, 'close-utilities', () => document.querySelectorAll('[data-phoenix-popover]').length === 0);
                }

                // Exercise product switcher, channel menu, plugin actions, and global account menu on light case.
                if (themeKey === 'light') {
                    // Product switcher menu
                    await page.click('#product_switch_menu');
                    await page.waitForSelector('#product-switcher-menu-dropdown');
                    const productItems = await page.evaluate(() => Array.from(document.querySelectorAll('#product-switcher-menu-dropdown [role="menuitem"], #product-switcher-menu-dropdown .MenuItem')).map((el) => (el.textContent || '').trim()));
                    t.ok('product switcher menu opens with Channels and Boards', productItems.some((x) => x.includes('Channels')) && productItems.some((x) => x.includes('Boards')), productItems);
                    await page.click('#product_switch_menu');

                    if (vpKey === 'desktop') {
                        // Desktop channel dropdown menu, plugin button, and UserAccountMenu popover
                        await page.click('#channelHeaderDropdownButton');
                        await page.waitForSelector('#channelHeaderDropdownMenu');
                        const channelMenuOpen = await page.evaluate(() => Boolean(document.querySelector('#channelHeaderDropdownMenu')));
                        t.ok('channel dropdown menu opens on click', channelMenuOpen);
                        await page.keyboard.press('Escape');
                        await until(page, 'close-channel-menu', () => !document.querySelector('#channelHeaderDropdownMenu'));
                        await page.click('#plug-1ChannelHeaderButton');
                        const plugCalls = await page.evaluate(() => window.phoenixPluginCalls || []);
                        t.ok('plugin action fires callback with channel id', plugCalls.length >= 1 && plugCalls[0].id === 'plug-1' && plugCalls[0].channelId === 'channel-1', plugCalls);

                        await page.click('#userAccountMenuButton');
                        await page.waitForSelector('#userAccountMenu');
                        const userMenuText = await page.evaluate(() => (document.querySelector('#userAccountMenu')?.textContent || ''));
                        t.ok('native UserAccountMenu opens with status, profile, and Log out', userMenuText.includes('Online') && userMenuText.includes('Profile') && userMenuText.includes('Log out'), userMenuText);
                        await page.keyboard.press('Escape');
                        await until(page, 'close-user-menu', () => !document.querySelector('#userAccountMenu'));
                    } else {
                        // Mobile channel dropdown modal trigger, utilities overflow plugin button, and UserAccountMenu modal trigger
                        await page.click('#channelHeaderDropdownButton');
                        const channelModalDispatched = await page.evaluate(() => (window.phoenixDispatched || []).some((a) => a.type === 'MODAL_OPEN' && a.modalId === 'channelHeaderDropdownMenu'));
                        t.ok('channel dropdown menu opens on click', channelModalDispatched);

                        await page.click(TRIGGER.utilities);
                        await page.waitForSelector('[data-phoenix-popover="utilities"]');
                        await page.click('.phoenix-mobile-utilities li.flex-parent--center button.navbar-right__icon');
                        const plugCalls = await page.evaluate(() => window.phoenixPluginCalls || []);
                        t.ok('plugin action fires callback with channel id', plugCalls.length >= 1 && plugCalls[0].id === 'plug-1' && plugCalls[0].channelId === 'channel-1', plugCalls);

                        await page.click('#userAccountMenuButton');
                        const userModalDispatched = await page.evaluate(() => (window.phoenixDispatched || []).some((a) => a.type === 'MODAL_OPEN' && a.modalId === 'userAccountMenu'));
                        t.ok('native UserAccountMenu opens with status, profile, and Log out', userModalDispatched);
                        await page.keyboard.press('Escape');
                        await until(page, 'close-utilities-after-account', () => document.querySelectorAll('[data-phoenix-popover]').length === 0);
                    }
                }

                // Keyboard on Launcher: focus launcher; Enter opens menu; arrows move; Escape returns.
                await page.focus(TRIGGER.apps);
                await page.keyboard.press('Tab');
                await page.keyboard.press('Shift+Tab');
                let f = await focused(page);
                t.ok('focus reaches the launcher', f && f.label === 'Ứng dụng', f);
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

                // Theme trigger: Tab from launcher reaches theme icon; Space opens menu; focus lands on persisted mode.
                await page.mouse.move(0, 0);
                await page.keyboard.press('Tab');
                f = await focused(page);
                const expectedModeLabel = theme.mode === 'light' ? 'Sáng' : theme.mode === 'dark' ? 'Tối' : 'Hệ thống';
                t.ok('Tab reaches the theme icon trigger', f && f.label === `Giao diện: ${expectedModeLabel}`, f);
                const trig = await page.evaluate(readColors, TRIGGER.theme);
                note.triggerFocus = trig;
                t.ok('theme trigger focus ring is visible', trig.focusVisible && trig.outlineStyle === 'solid' && trig.outlineContrast >= 3 && trig.contrast >= 4.5, trig);
                await page.keyboard.press('Space');
                await page.waitForSelector('[data-phoenix-popover="theme"]');
                f = await focused(page);
                t.ok('Space opens the theme menu and focuses the persisted mode', f && f.role === 'menuitemradio' && f.checked === 'true' && f.text === expectedModeLabel, f);
                const radioFocus = await page.evaluate(readColors, '[role="menuitemradio"]:focus');
                note.radioFocus = radioFocus;
                t.ok('focused selected mode is legible with a visible ring', radioFocus && radioFocus.contrast >= 4.5 && radioFocus.outlineStyle === 'solid' && radioFocus.outlineContrast >= 3 && notFace(radioFocus.effectiveBackground), radioFocus);
                const unchecked = await page.evaluate(readColors, '[role="menuitemradio"][aria-checked="false"]');
                note.radioRest = unchecked;
                t.ok('unselected mode is transparent over the surface, not ButtonFace', unchecked && unchecked.effectiveBackground === theme.surface && unchecked.contrast >= 4.5, unchecked);

                for (const sel of ['.phoenix-shell__app-name', '.phoenix-shell__tenant']) {
                    const r = await page.evaluate(readColors, sel);
                    t.ok(`text ${sel} has contrast >= 4.5`, r && r.contrast >= 4.5, r);
                }
                {
                    const r = await page.evaluate(readColors, '.phoenix-shell__app-name');
                    t.ok('brand app label is the Gateway textMuted colour', r && r.color === theme.muted, r);
                }
                const selected = await page.evaluate(readColors, '[role="menuitemradio"][aria-checked="true"]');
                note.radioSelected = selected;
                t.ok('selected mode is legible', selected && selected.contrast >= 4.5 && selected.effectiveBackground !== theme.surface, selected);

                // Hover over an unselected mode.
                const radioBox = await page.locator('[role="menuitemradio"][aria-checked="false"]').first().boundingBox();
                await page.mouse.move(radioBox.x + radioBox.width / 2, radioBox.y + radioBox.height / 2);
                const radioHover = await page.evaluate(readColors, '[role="menuitemradio"]:hover');
                note.radioHover = radioHover;
                t.ok('hovered mode is legible and not a UA button face', radioHover && radioHover.contrast >= 4.5 && radioHover.effectiveBackground !== theme.surface && (dark ? radioHover.backgroundLuminance < 0.25 : true), radioHover);

                // Pointer: outside click closes and returns focus; the panels are mutually exclusive.
                const chat = await page.locator('#fixture-chat').boundingBox();
                await page.mouse.click(chat.x + chat.width - 20, chat.y + chat.height / 2);
                await until(page, 'wait#1', () => document.querySelectorAll('[data-phoenix-popover]').length === 0);
                await until(page, 'wait#2', () => document.activeElement && (document.activeElement.getAttribute('aria-label') || '').startsWith('Giao diện:'));
                t.ok('outside click closes the theme menu and returns focus to its trigger', true);
                await page.click(TRIGGER.apps);
                await page.waitForSelector('[data-phoenix-popover="apps"]');
                await page.click(TRIGGER.theme);
                await page.waitForSelector('[data-phoenix-popover="theme"]');
                let both = await page.evaluate(() => Array.from(document.querySelectorAll('[data-phoenix-popover]')).map((p) => p.getAttribute('data-phoenix-popover')));
                f = await focused(page);
                t.ok('opening theme menu closes the launcher and focuses the new panel', both.length === 1 && both[0] === 'theme' && f.role === 'menuitemradio', {both, f});
                await page.click(TRIGGER.apps);
                await page.waitForSelector('[data-phoenix-popover="apps"]');
                both = await page.evaluate(() => Array.from(document.querySelectorAll('[data-phoenix-popover]')).map((p) => p.getAttribute('data-phoenix-popover')));
                f = await focused(page);
                t.ok('opening the launcher closes the theme menu and focuses the menu', both.length === 1 && both[0] === 'apps' && f.role === 'menuitem', {both, f});
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
                    // Live theme change through the keyboard: the real choose() path and sequential ETags ("7" -> "8").
                    await page.focus(TRIGGER.theme);
                    await page.keyboard.press('Enter');
                    await page.keyboard.press('ArrowRight');
                    await until(page, 'wait#5', () => document.documentElement.dataset.phoenixMode === 'dark');
                    const afterDark = await page.evaluate(() => ({
                        surface: getComputedStyle(document.querySelector('.phoenix-shell')).backgroundColor,
                        panel: getComputedStyle(document.querySelector('[data-phoenix-popover="theme"]')).backgroundColor,
                        checked: document.querySelector('[role="menuitemradio"][aria-checked="true"]').textContent,
                        triggerLabel: document.querySelector('button[aria-label^="Giao diện:"]').getAttribute('aria-label'),
                        calls: window.phoenixCalls.filter((x) => x.method === 'PATCH'),
                    }));
                    note.afterArrowDark = afterDark;
                    t.ok('ArrowRight selects Tối through PATCH with ETag "7" and repaints dark',
                        afterDark.surface === 'rgb(17, 17, 17)' &&
                        afterDark.panel === 'rgb(17, 17, 17)' &&
                        afterDark.checked === 'Tối' &&
                        afterDark.triggerLabel === 'Giao diện: Tối' &&
                        afterDark.calls.length === 1 &&
                        afterDark.calls[0].body === '{"mode":"dark"}' &&
                        afterDark.calls[0].ifMatch === '"7"',
                        afterDark,
                    );
                    f = await focused(page);
                    t.ok('focus follows Tối and the menu stays open', f && f.role === 'menuitemradio' && f.text === 'Tối');

                    await page.keyboard.press('ArrowRight');
                    await until(page, 'wait#6', () => document.querySelector('button[aria-label^="Giao diện:"]').getAttribute('aria-label') === 'Giao diện: Hệ thống');
                    const afterSystem = await page.evaluate(() => ({
                        checked: document.querySelector('[role="menuitemradio"][aria-checked="true"]').textContent,
                        calls: window.phoenixCalls.filter((x) => x.method === 'PATCH'),
                    }));
                    note.afterArrowSystem = afterSystem;
                    t.ok('second ArrowRight selects Hệ thống using returned ETag "8"',
                        afterSystem.checked === 'Hệ thống' &&
                        afterSystem.calls.length === 2 &&
                        afterSystem.calls[1].body === '{"mode":"system"}' &&
                        afterSystem.calls[1].ifMatch === '"8"',
                        afterSystem,
                    );
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

    // Early risk check: the real shell mounts with the real compiled CSS and portaled slots.
    const early = await withBrowser(async ({browser, pw}) => {
        const {page, context, problems} = await openPage(browser, DESKTOP);
        try {
            const m = await page.evaluate(() => {
                const l = document.querySelector('button[aria-label="Ứng dụng"]');
                const s = getComputedStyle(l);
                const h = document.querySelector('[data-testid="phoenix-header"]').getBoundingClientRect();
                const banners = document.querySelectorAll('header, [role="banner"]').length;
                const css = Array.from(document.styleSheets).map((sh) => sh.href && sh.href.split('/').pop());
                return {launcher: {width: l.getBoundingClientRect().width, height: l.getBoundingClientRect().height, borderTopWidth: s.borderTopWidth, minHeight: s.minHeight, borderRadius: s.borderTopLeftRadius}, header: {height: h.height, banners}, stylesheets: css};
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
    if (early.problems.length || early.header.height !== 72 || early.header.banners !== 1) {
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
                for (const state of ['closed', 'theme', 'apps']) {
                    jobs.push({file: `${vpKey}-${themeKey}-${state}`, vpKey, vp, themeKey, state, scenario: SCENARIOS.short});
                }
            }
        }
        jobs.push(
            {file: 'extra-long-desktop-light-theme', vpKey: 'desktop', vp: DESKTOP, themeKey: 'light', state: 'theme', scenario: SCENARIOS.long},
            {file: 'extra-long-mobile-dark-theme', vpKey: 'mobile', vp: MOBILE, themeKey: 'dark', state: 'theme', scenario: SCENARIOS.long},
            {file: 'extra-long-mobile-light-apps', vpKey: 'mobile', vp: MOBILE, themeKey: 'light', state: 'apps', scenario: SCENARIOS.long},
            {file: 'extra-mobile-light-utilities', vpKey: 'mobile', vp: MOBILE, themeKey: 'light', state: 'utilities', scenario: SCENARIOS.short},
            {file: 'extra-short-viewport-light-apps', vpKey: 'short', vp: SHORT, themeKey: 'light', state: 'apps', scenario: SCENARIOS.long},
            {file: 'extra-boundary-641-long-light-apps', vpKey: 'w641', vp: {width: 641, height: 844}, themeKey: 'light', state: 'apps', scenario: SCENARIOS.long},
            {file: 'extra-gateway-dark-desktop-theme', vpKey: 'desktop', vp: DESKTOP, themeKey: 'dark', state: 'theme', scenario: {...SCENARIOS.short, palette: 'gateway'}},
            {file: 'extra-gateway-dark-mobile-theme', vpKey: 'mobile', vp: MOBILE, themeKey: 'dark', state: 'theme', scenario: {...SCENARIOS.short, palette: 'gateway'}},
        );
        for (const job of jobs) {
            const {page, context, problems} = await openPage(browser, job.vp, {...job.scenario, mode: job.themeKey}, job.themeKey);
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
