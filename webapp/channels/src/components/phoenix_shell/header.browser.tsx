// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.

/* eslint-disable formatjs/no-literal-string-in-jsx -- fixture content, not product copy */

// Browser entry of the header fixture (see header.browser.cjs). It renders the real PhoenixShell
// and the real client; only the network boundary is replaced by an in-page Gateway double.
// Not part of the product bundle and not authenticated production evidence.

import React from 'react';
import {createRoot} from 'react-dom/client';

import type {ColorMode, ShellSnapshot} from './client';

import PhoenixShell from './index';

type Scenario = {
    tenant: string;
    account: string;
    mode: ColorMode;
    apps: Array<{appCode: string; label: string; state: 'ready' | 'provisioning'; launchUrl?: string}>;
    logoutUrl: string | null;
    palette: 'plan' | 'gateway';
};

type Call = {method: string; url: string; ifMatch: string | null; body: string | null};

declare global {
    interface Window {
        phoenixFixture?: Partial<Scenario>;
        phoenixCalls?: Call[];
        chatEscapeSeen?: number;
    }
}

const scenario: Scenario = {
    tenant: 'Tenant A',
    account: 'A1',
    mode: 'light',
    apps: [
        {appCode: 'tasks', label: 'Tasks', state: 'ready', launchUrl: 'https://tasks.phoenixsoft.vn'},
        {appCode: 'files', label: 'Files pending', state: 'provisioning'},
        {appCode: 'portal', label: 'Portal', state: 'ready', launchUrl: 'https://portal.phoenixsoft.vn'},
    ],
    logoutUrl: 'https://auth.phoenixsoft.vn/oauth2/logout',
    palette: 'plan',
    ...window.phoenixFixture,
};

let version = 7;
let mode: ColorMode = scenario.mode;
const calls: Call[] = [];
window.phoenixCalls = calls;
window.chatEscapeSeen = 0;

// Token names and values are the Gateway ThemeDefaults ones (textMuted, surfaceMuted, primaryForeground, ...).
// 'plan' keeps the plan's binary surface/text pair; 'gateway' is the shipped default palette untouched.
const gateway = {
    light: {
        primary: '#056B55',
        primaryForeground: '#FFFFFF',
        surface: '#FFFFFF',
        surfaceMuted: '#EDF3F0',
        headerBackground: '#FFFFFF',
        headerText: '#17211E',
        text: '#17211E',
        textMuted: '#5D6D67',
        border: '#738A80',
        focus: '#056B55',
    },
    dark: {
        primary: '#6CD7BC',
        primaryForeground: '#06251D',
        surface: '#18251F',
        surfaceMuted: '#22332B',
        headerBackground: '#18251F',
        headerText: '#EAF3EF',
        text: '#EAF3EF',
        textMuted: '#B2C5BC',
        border: '#698176',
        focus: '#6CD7BC',
    },
};

const theme = scenario.palette === 'gateway' ? gateway : {
    light: {...gateway.light, surface: '#ffffff', text: '#111111', headerBackground: '#ffffff', headerText: '#111111'},
    dark: {...gateway.dark, surface: '#111111', text: '#ffffff', headerBackground: '#111111', headerText: '#ffffff'},
};

function snapshot(): ShellSnapshot {
    return {
        appCode: 'chat',
        tenant: {id: 'tenant-a', displayName: scenario.tenant},
        themeRevision: 1,
        resolvedThemeHash: 'hash-a',
        theme: {color: theme},
        footer: {presentation: 'compact', operator: {displayName: 'Operator'}, tenant: {displayName: scenario.tenant}, links: []},
        account: {displayName: scenario.account, avatarUrl: null},
        preference: {mode, density: 'comfortable'},
    };
}

function reply(body: unknown, headers: Record<string, string> = {}) {
    return new Response(JSON.stringify(body), {status: 200, headers: {'Content-Type': 'application/json', ...headers}});
}

window.fetch = async (input, init) => {
    const url = String(input);
    const headers = new Headers(init?.headers);
    calls.push({method: init?.method ?? 'GET', url, ifMatch: headers.get('If-Match'), body: typeof init?.body === 'string' ? init.body : null});
    if (url === '/phoenix/shell-context') {
        const extra: Record<string, string> = {ETag: `"${version}"`};
        if (scenario.logoutUrl) {
            extra['X-Phoenix-Logout-Url'] = scenario.logoutUrl;
        }
        return reply(snapshot(), extra);
    }
    if (url === '/phoenix/shell-navigation') {
        const apps = scenario.apps.map((a) => ({description: '', iconKey: a.appCode, ...a}));
        return reply({context: 'tenant', currentApp: 'chat', expiry: null, apps}, {ETag: '"nav-1"'});
    }
    if (url === '/phoenix/ui-preferences' && init?.method === 'PATCH') {
        mode = JSON.parse(String(init.body)).mode;
        version += 1;
        return reply({mode, density: 'comfortable', version}, {ETag: `"${version}"`});
    }
    return new Response('{}', {status: 404});
};

// Chat shortcuts live on the document: an Escape that reaches it was not consumed by the header.
document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
        window.chatEscapeSeen = (window.chatEscapeSeen ?? 0) + 1;
    }
});

document.body.classList.add('app__body');
createRoot(document.getElementById('root')!).render(
    <PhoenixShell>
        <div
            id='fixture-chat'
            className='app__content'
            style={{display: 'flex', flexDirection: 'column', justifyContent: 'space-between'}}
        >
            <div id='fixture-content'>{'Chat content'}</div>
            <input
                id='fixture-composer'
                className='form-control'
                aria-label='Composer'
            />
        </div>
    </PhoenixShell>,
);
