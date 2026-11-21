// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {render, screen, waitFor} from '@testing-library/react';
import React from 'react';

import {accountSwitchLink, useAccountSwitchHref} from './account_switch';
import * as client from './client';
import type {ShellSnapshot} from './client';

import PhoenixShell from './index';

jest.mock('components/initial_loading_screen', () => ({__esModule: true, default: {stop: jest.fn()}}));
jest.mock('./client', () => ({...jest.requireActual('./client'), fetchShellContext: jest.fn(), fetchNavigation: jest.fn(), patchPreference: jest.fn()}));
jest.mock('utils/utils', () => ({applyTheme: jest.fn()}));
jest.mock('stores/redux_store', () => ({__esModule: true, default: {dispatch: jest.fn()}}));

const snapshot: ShellSnapshot = {
    appCode: 'chat',
    tenant: {id: 'tenant-a', displayName: 'Tenant A'},
    themeRevision: 1,
    resolvedThemeHash: 'hash-a',
    theme: {color: {light: {surface: '#ffffff', text: '#111111'}, dark: {surface: '#111111', text: '#ffffff'}}},
    footer: {presentation: 'compact', operator: {displayName: 'Operator'}, tenant: {displayName: 'Tenant A'}, links: []},
    account: {displayName: 'A1', avatarUrl: null},
    preference: {mode: 'light', density: 'comfortable'},
};
const shell = client.fetchShellContext as jest.MockedFunction<typeof client.fetchShellContext>;
const navigation = client.fetchNavigation as jest.MockedFunction<typeof client.fetchNavigation>;

function Probe() {
    const href = useAccountSwitchHref();
    const addHref = useAccountSwitchHref('add');
    return <><span data-testid='switch-href'>{href ?? 'none'}</span><span data-testid='add-href'>{addHref ?? 'none'}</span></>;
}

beforeEach(() => {
    jest.clearAllMocks();
    shell.mockResolvedValue({body: snapshot, etag: '"7"', logoutUrl: null, status: 200});
    Object.defineProperty(window, 'matchMedia', {writable: true, value: jest.fn(() => ({matches: false, addEventListener: jest.fn(), removeEventListener: jest.fn()}))});
});

describe('accountSwitchLink', () => {
    test('appends an encoded return_to for the app origin', () => {
        expect(accountSwitchLink('https://auth.example.test/accounts', 'https://chat.example.test')).toBe('https://auth.example.test/accounts?return_to=https%3A%2F%2Fchat.example.test%2F');
    });

    test.each([null, undefined, '', 'ftp://x.test/accounts', '/accounts', 'not a url'])('rejects %s', (url) => {
        expect(accountSwitchLink(url, 'https://chat.example.test')).toBeNull();
    });
});

test('shell publishes accountSwitchUrl from the navigation response to the user menu and clears it when null', async () => {
    navigation.mockResolvedValue({body: {context: 'tenant', currentApp: 'chat', tenant: {slug: 'the-youth-academy'}, expiry: null, apps: [], accountSwitchUrl: 'https://auth.example.test/accounts'} as never, etag: '"nav-1"', logoutUrl: null, status: 200});
    const first = render(<><PhoenixShell><Probe/></PhoenixShell></>);
    await waitFor(() => expect(screen.getByTestId('switch-href').textContent).toBe(`https://auth.example.test/accounts?return_to=${encodeURIComponent(`${window.location.origin}/`)}`));
    expect(screen.getByTestId('add-href').textContent).toBe(`https://auth.example.test/accounts/add-start?return_to=${encodeURIComponent(`${window.location.origin}/`)}&tenant_alias=the-youth-academy`);
    first.unmount();

    navigation.mockResolvedValue({body: {context: 'tenant', currentApp: 'chat', expiry: null, apps: [], accountSwitchUrl: null} as never, etag: '"nav-2"', logoutUrl: null, status: 200});
    render(<><PhoenixShell><Probe/></PhoenixShell></>);
    await screen.findByTestId('phoenix-header');
    expect(screen.getByTestId('switch-href').textContent).toBe('none');
    expect(screen.getByTestId('add-href').textContent).toBe('none');
});

test('add account uses the confirmation page and rejects disabled or noncanonical URLs', () => {
  expect(accountSwitchLink('https://auth.example.test/accounts', 'https://chat.example.test', 'add')).toBe('https://auth.example.test/accounts/add-start?return_to=https%3A%2F%2Fchat.example.test%2F');
  for (const bad of [null, undefined, '', 'javascript:alert(1)', '/accounts', 'https://user:pass@auth.example.test/accounts', 'https://auth.example.test/accounts?next=evil', 'https://auth.example.test/accounts#x', 'https://auth.example.test/other']) {
    expect(accountSwitchLink(bad, 'https://chat.example.test', 'add')).toBe(null);
  }
});

test('carries the current tenant only as an add-account selector', () => {
  expect(accountSwitchLink('https://auth.example.test/accounts', 'https://app.example.test', 'add', 'the-youth-academy')).toBe('https://auth.example.test/accounts/add-start?return_to=https%3A%2F%2Fapp.example.test%2F&tenant_alias=the-youth-academy');
  expect(accountSwitchLink('https://auth.example.test/accounts', 'https://app.example.test', 'switch', 'the-youth-academy')).toBe('https://auth.example.test/accounts?return_to=https%3A%2F%2Fapp.example.test%2F');
  for (const tenant of ['', 'acme&return_to=evil', '<script>', 'x'.repeat(64)]) {
    expect(accountSwitchLink('https://auth.example.test/accounts', 'https://app.example.test', 'add', tenant)).toBe('https://auth.example.test/accounts/add-start?return_to=https%3A%2F%2Fapp.example.test%2F');
  }
});
