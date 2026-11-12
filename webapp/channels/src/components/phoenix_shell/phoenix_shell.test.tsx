// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.
import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import PhoenixShell from './index';
import * as client from './client';
import type {ShellSnapshot} from './client';
import NativeClient from '../../../../platform/client/src/client4';

jest.mock('./client', () => ({...jest.requireActual('./client'), fetchShellContext: jest.fn(), fetchNavigation: jest.fn(), patchPreference: jest.fn()}));
jest.mock('utils/utils', () => ({applyTheme: jest.fn()}));
jest.mock('stores/redux_store', () => ({__esModule: true, default: {dispatch: jest.fn()}}));

const snapshot: ShellSnapshot = {
    appCode: 'chat', tenant: {id: 'tenant-a', displayName: 'Tenant A'}, themeRevision: 1, resolvedThemeHash: 'hash-a',
    theme: {color: {light: {surface: '#ffffff', text: '#111111'}, dark: {surface: '#111111', text: '#ffffff'}}},
    footer: {presentation: 'compact', operator: {displayName: 'Operator'}, tenant: {displayName: 'Tenant A'}, links: []},
    account: {displayName: 'A1', avatarUrl: null}, preference: {mode: 'light', density: 'comfortable'},
};
const shell = client.fetchShellContext as jest.MockedFunction<typeof client.fetchShellContext>;
const navigation = client.fetchNavigation as jest.MockedFunction<typeof client.fetchNavigation>;
const preference = client.patchPreference as jest.MockedFunction<typeof client.patchPreference>;

// Break: omit the per-intent key or regenerate it inside a transport retry.
test('task request has one browser intent key in its serialized HTTP body', async () => {
    const native = new NativeClient();
    const fetch = jest.spyOn(native, 'doFetch').mockResolvedValue({});
    const uuid = jest.spyOn(crypto, 'randomUUID').mockReturnValue('921ef203-41db-4cd7-8f56-411dfd576a61');
    try {
        await native.executeCommand('/task Prepare acceptance', {channel_id: 'own-channel'});
        const [, request] = fetch.mock.calls[0];
        expect(JSON.parse(request.body as string)).toEqual({command: '/task Prepare acceptance', channel_id: 'own-channel', trigger_id: '921ef203-41db-4cd7-8f56-411dfd576a61'});
        expect(uuid).toHaveBeenCalledTimes(1);
        fetch.mockClear();
        await native.executeCommand('/search ordinary', {channel_id: 'own-channel'});
        expect(JSON.parse(fetch.mock.calls[0][1].body as string)).toEqual({command: '/search ordinary', channel_id: 'own-channel'});
    } finally { uuid.mockRestore(); fetch.mockRestore(); }
});

beforeEach(() => {
    jest.clearAllMocks();
    shell.mockResolvedValue({body: snapshot, etag: '"7"', logoutUrl: 'https://auth.phoenixsoft.vn/oauth2/logout', status: 200});
    navigation.mockResolvedValue({body: {context: 'tenant', currentApp: 'chat', expiry: null, apps: [
        {appCode: 'tasks', label: 'Tasks', description: '', iconKey: 'tasks', state: 'ready', launchUrl: 'https://tasks.phoenixsoft.vn'},
        {appCode: 'files', label: 'Files pending', description: '', iconKey: 'files', state: 'provisioning'},
    ]}, etag: '"nav-1"', logoutUrl: null, status: 200});
    Object.defineProperty(window, 'matchMedia', {writable: true, value: jest.fn(() => ({matches: false, addEventListener: jest.fn(), removeEventListener: jest.fn()}))});
});

// Break: using a native mode instead of the persisted Gateway preference or dropping its version.
test('Gateway dark selection persists and applies after reload', async () => {
    preference.mockResolvedValue({body: {mode: 'dark', density: 'comfortable', version: 8}, etag: '"8"', logoutUrl: null, status: 200});
    const view = render(<PhoenixShell><div>{'Chat content'}</div></PhoenixShell>);
    await screen.findByTestId('phoenix-header');
    fireEvent.click(screen.getByRole('radio', {name: 'Tối'}));
    await waitFor(() => expect(document.documentElement.dataset.phoenixMode).toBe('dark'));
    expect(preference).toHaveBeenCalledWith({mode: 'dark'}, '"7"');
    expect(document.documentElement.style.getPropertyValue('--phoenix-color-surface')).toBe('#111111');
    view.unmount();
    shell.mockResolvedValue({body: {...snapshot, preference: {mode: 'dark', density: 'comfortable'}}, etag: '"8"', logoutUrl: null, status: 200});
    render(<PhoenixShell><div>{'Chat content'}</div></PhoenixShell>);
    await screen.findByTestId('phoenix-header');
    expect(screen.getByRole('radio', {name: 'Tối'})).toHaveAttribute('aria-checked', 'true');
});

// Break: rendering unauthorized launcher targets, or leaving prior-user content after revoke/outage.
test('launcher uses ready navigation and authority failure removes sensitive content', async () => {
    const view = render(<PhoenixShell><div>{'Sensitive chat A'}</div></PhoenixShell>);
    await screen.findByTestId('phoenix-header');
    fireEvent.click(screen.getByRole('button', {name: 'Ứng dụng'}));
    expect(screen.getByRole('menuitem', {name: 'Tasks'})).toHaveAttribute('href', 'https://tasks.phoenixsoft.vn');
    expect(screen.queryByRole('menuitem', {name: 'Files pending'})).toBeNull();
    view.unmount();
    shell.mockRejectedValue(new client.ShellError(403, 'CHAT_ACCESS_DENIED'));
    render(<PhoenixShell><div>{'Sensitive chat A'}</div></PhoenixShell>);
    expect(await screen.findByRole('alert')).toHaveTextContent('Bạn chưa được cấp quyền sử dụng Chat.');
    expect(screen.queryByText('Sensitive chat A')).toBeNull();
    expect(document.documentElement.hasAttribute('data-phoenix-mode')).toBe(false);
});

// Break: treating a missing enforced BFF as permission to render a fallback native shell.
test('missing authority surface fails closed and pending has a real retry', async () => {
    shell.mockRejectedValueOnce(new client.ShellError(409, 'CHAT_PROVISIONING_PENDING'));
    render(<PhoenixShell><div>{'Sensitive chat'}</div></PhoenixShell>);
    expect(await screen.findByRole('alert')).toHaveTextContent('Tài khoản Chat đang được chuẩn bị. Vui lòng thử lại sau.');
    shell.mockRejectedValueOnce(new client.ShellError(503, 'PHOENIX_AUTHORITY_UNAVAILABLE'));
    await act(async () => fireEvent.click(screen.getByRole('button', {name: 'Thử lại'})));
    expect(await screen.findByRole('alert')).toHaveTextContent('Chưa thể xác minh quyền truy cập. Vui lòng thử lại sau.');
    expect(screen.queryByText('Sensitive chat')).toBeNull();
});
