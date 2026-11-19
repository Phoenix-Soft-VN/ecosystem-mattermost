// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.
import React from 'react';
import {act, fireEvent, render, screen, waitFor, within} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import PhoenixShell from './index';
import * as client from './client';
import type {ShellSnapshot} from './client';
import {NATIVE_SESSION_CLEARED} from './session_lifecycle';
import NativeClient from '../../../../platform/client/src/client4';

jest.mock('components/initial_loading_screen', () => ({__esModule: true, default: {stop: jest.fn()}}));
jest.mock('./client', () => ({...jest.requireActual('./client'), fetchShellContext: jest.fn(), fetchNavigation: jest.fn(), patchPreference: jest.fn()}));
jest.mock('utils/utils', () => ({applyTheme: jest.fn()}));
jest.mock('stores/redux_store', () => ({__esModule: true, default: {dispatch: jest.fn()}}));

const snapshot: ShellSnapshot = {
    appCode: 'chat', tenant: {id: 'tenant-a', displayName: 'Tenant A'}, themeRevision: 1, resolvedThemeHash: 'hash-a',
    theme: {color: {light: {surface: '#ffffff', text: '#111111'}, dark: {surface: '#111111', text: '#ffffff'}}},
    footer: {presentation: 'compact', operator: {displayName: 'Operator'}, tenant: {displayName: 'Tenant A'}, links: []},
    account: {displayName: 'A1', avatarUrl: null}, preference: {mode: 'light', density: 'comfortable'},
};
const LOGOUT = 'https://auth.phoenixsoft.vn/oauth2/logout';
const shell = client.fetchShellContext as jest.MockedFunction<typeof client.fetchShellContext>;
const navigation = client.fetchNavigation as jest.MockedFunction<typeof client.fetchNavigation>;
const preference = client.patchPreference as jest.MockedFunction<typeof client.patchPreference>;

function renderShell() {
    return render(
        <PhoenixShell>
            <div>{'Chat content'}</div>
            <input aria-label='Composer'/>
        </PhoenixShell>,
    );
}

async function ready() {
    return screen.findByTestId('phoenix-header');
}

const appsTrigger = () => screen.getByRole('button', {name: 'Ứng dụng'});
const themeTrigger = () => screen.getByRole('button', {name: /^Giao diện: (Sáng|Tối|Hệ thống)$/});

function mockNavigation(apps: Array<Record<string, unknown>>) {
    navigation.mockResolvedValue({body: {context: 'tenant', currentApp: 'chat', expiry: null, apps} as never, etag: '"nav-1"', logoutUrl: null, status: 200});
}

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
    } finally {
        uuid.mockRestore();
        fetch.mockRestore();
    }
});

beforeEach(() => {
    jest.clearAllMocks();
    shell.mockResolvedValue({body: snapshot, etag: '"7"', logoutUrl: LOGOUT, status: 200});
    mockNavigation([
        {appCode: 'tasks', label: 'Tasks', description: '', iconKey: 'tasks', state: 'ready', launchUrl: 'https://tasks.phoenixsoft.vn'},
        {appCode: 'files', label: 'Files pending', description: '', iconKey: 'files', state: 'provisioning'},
    ]);
    Object.defineProperty(window, 'matchMedia', {writable: true, value: jest.fn(() => ({matches: false, addEventListener: jest.fn(), removeEventListener: jest.fn()}))});
});

// Break: using a native mode instead of the persisted Gateway preference or dropping its version.
test('Gateway dark selection persists and applies after reload', async () => {
    const user = userEvent.setup();
    preference.mockResolvedValue({body: {mode: 'dark', density: 'comfortable', version: 8}, etag: '"8"', logoutUrl: null, status: 200});
    const view = renderShell();
    await ready();
    await user.click(themeTrigger());
    await user.click(screen.getByRole('menuitemradio', {name: 'Tối'}));
    await waitFor(() => expect(document.documentElement.dataset.phoenixMode).toBe('dark'));
    expect(preference).toHaveBeenCalledWith({mode: 'dark'}, '"7"');
    expect(document.documentElement.style.getPropertyValue('--phoenix-color-surface')).toBe('#111111');
    view.unmount();
    shell.mockResolvedValue({body: {...snapshot, preference: {mode: 'dark', density: 'comfortable'}}, etag: '"8"', logoutUrl: null, status: 200});
    renderShell();
    await ready();
    expect(screen.getByRole('button', {name: 'Giao diện: Tối'})).toBeInTheDocument();
    await user.click(themeTrigger());
    expect(screen.getByRole('menuitemradio', {name: 'Tối'})).toHaveAttribute('aria-checked', 'true');
});

// Break: dropping brand, tenantapp launcher, or theme icon trigger, or keeping a duplicate Phoenix account button.
test('header shows brand, app launcher, and single theme icon trigger without duplicate account UI', async () => {
    renderShell();
    const header = await ready();

    expect(screen.getByTitle('Tenant A')).toBeInTheDocument();
    expect(within(header).getByText('Chat')).toBeInTheDocument();
    expect(appsTrigger()).toBeInTheDocument();
    expect(screen.getByRole('button', {name: 'Giao diện: Sáng'})).toBeInTheDocument();
    expect(screen.queryByRole('button', {name: 'Tài khoản'})).toBeNull();
    expect(screen.queryByRole('dialog', {name: 'Tài khoản'})).toBeNull();
});

// Break: rendering unauthorized launcher targets, or leaving prior-user content after revoke/outage.
test('launcher uses ready navigation and authority failure removes sensitive content', async () => {
    const user = userEvent.setup();
    const view = renderShell();
    await ready();
    await user.click(appsTrigger());
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

// Break: keeping the Phoenix account trigger/dialog duplicated by UserAccountMenu, omitting the
// localized theme label/tooltip/haspopup, or fetching again just to open the theme menu.
test('U03 icon theme menu: closed header has launcher and theme icon trigger with tooltip and menu options', async () => {
    const user = userEvent.setup();
    renderShell();
    const header = await ready();
    const requests = [shell.mock.calls.length, navigation.mock.calls.length];

    expect(within(header).getByTitle('Tenant A')).toBeVisible();
    expect(within(header).getAllByText('Chat')).toHaveLength(1);
    expect(within(header).getByText('TA')).toBeVisible();
    expect(appsTrigger()).toHaveAttribute('aria-expanded', 'false');
    expect(appsTrigger()).toHaveAttribute('aria-haspopup', 'menu');
    const trigger = screen.getByRole('button', {name: 'Giao diện: Sáng'});
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
    expect(trigger).toHaveAttribute('aria-haspopup', 'menu');
    expect(within(header).getAllByRole('button')).toHaveLength(2);
    expect(within(header).queryByRole('link')).toBeNull();
    expect(screen.queryByRole('menuitemradio')).toBeNull();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.queryByRole('menu')).toBeNull();
    expect(screen.queryByRole('button', {name: 'Tài khoản'})).toBeNull();
    expect(screen.queryByRole('button', {name: /thông báo|notification|bell/i})).toBeNull();

    // Focusing the theme icon trigger exposes its shared WithTooltip content.
    fireEvent.focus(trigger);
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Giao diện: Sáng');

    await user.click(trigger);
    const menu = screen.getByRole('menu', {name: 'Giao diện'});
    expect(menu).toBeVisible();
    expect(trigger).toHaveAttribute('aria-expanded', 'true');
    expect(trigger).toHaveAttribute('aria-controls', menu.id);
    expect(within(menu).getAllByRole('menuitemradio').map((r) => r.textContent)).toEqual(['Sáng', 'Tối', 'Hệ thống']);
    expect(within(menu).getByRole('menuitemradio', {name: 'Sáng'})).toHaveAttribute('aria-checked', 'true');
    expect(within(menu).getByRole('menuitemradio', {name: 'Tối'})).toHaveAttribute('aria-checked', 'false');
    expect(within(menu).getByRole('menuitemradio', {name: 'Hệ thống'})).toHaveAttribute('aria-checked', 'false');
    expect([shell.mock.calls.length, navigation.mock.calls.length, preference.mock.calls.length]).toEqual([...requests, 0]);
});

// Break: two panels open together, focus returning to the wrong/old trigger, a click inside closing the
// panel, or a document listener surviving unmount.
test('C02 panels are exclusive and dismiss with focus return', async () => {
    const user = userEvent.setup();
    renderShell();
    await ready();
    const open = () => screen.queryAllByRole('dialog').length + screen.queryAllByRole('menu').length;

    await user.click(appsTrigger());
    expect(screen.getByRole('menuitem', {name: 'Tasks'})).toHaveFocus();
    await user.click(themeTrigger());
    expect(open()).toBe(1);
    expect(screen.queryByRole('menu', {name: 'Ứng dụng'})).toBeNull();
    expect(appsTrigger()).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByRole('menuitemradio', {name: 'Sáng'})).toHaveFocus();
    await user.click(appsTrigger());
    expect(open()).toBe(1);
    expect(screen.queryByRole('menu', {name: 'Giao diện'})).toBeNull();
    expect(screen.getByRole('menuitem', {name: 'Tasks'})).toHaveFocus();

    // Escape closes each panel and returns focus to its own trigger.
    await user.keyboard('{Escape}');
    expect(open()).toBe(0);
    expect(appsTrigger()).toHaveFocus();
    await user.click(themeTrigger());
    await user.keyboard('{Escape}');
    expect(open()).toBe(0);
    expect(themeTrigger()).toHaveFocus();

    // An outside press closes the panel and, when nothing else takes focus, returns it to the trigger.
    const dismissByPress = async (trigger: () => HTMLElement, menuName: string) => {
        await user.click(trigger());
        await user.click(screen.getByRole('menu', {name: menuName}));
        expect(screen.getByRole('menu', {name: menuName})).toBeVisible();
        await user.click(screen.getByText('Chat content'));
        expect(open()).toBe(0);
        await waitFor(() => expect(trigger()).toHaveFocus());

        // Clicking the trigger again closes it too.
        await user.click(trigger());
        expect(open()).toBe(1);
        await user.click(trigger());
        expect(open()).toBe(0);
        expect(trigger()).toHaveFocus();
    };
    await dismissByPress(appsTrigger, 'Ứng dụng');
    await dismissByPress(themeTrigger, 'Giao diện');

    // Pressing another focusable control keeps focus there instead of stealing it back.
    await user.click(themeTrigger());
    await user.click(screen.getByRole('textbox', {name: 'Composer'}));
    expect(open()).toBe(0);
    expect(screen.getByRole('textbox', {name: 'Composer'})).toHaveFocus();
});

// Break: leaving document mousedown listener or focus timer active after PhoenixShell unmounts.
test('C02 unmount leaves no document listener or pending focus return behind', async () => {
    const user = userEvent.setup();
    const added = jest.spyOn(document, 'addEventListener');
    const removed = jest.spyOn(document, 'removeEventListener');
    const focus = jest.spyOn(HTMLElement.prototype, 'focus');
    const presses = (spy: jest.SpyInstance) => spy.mock.calls.filter(([type]) => type === 'mousedown').map(([, handler]) => handler);
    try {
        const first = renderShell();
        await ready();
        const base = presses(added).length;
        await user.click(themeTrigger());
        const armed = presses(added).slice(base);
        expect(armed.length).toBeGreaterThan(0);
        first.unmount();
        for (const handler of armed) {
            expect(presses(removed)).toContain(handler);
        }
        expect(() => fireEvent.mouseDown(document.body)).not.toThrow();

        const second = renderShell();
        await ready();
        await user.click(appsTrigger());
        fireEvent.mouseDown(document.body);
        second.unmount();
        const calls = focus.mock.calls.length;
        await new Promise((resolve) => setTimeout(resolve, 30));
        expect(focus.mock.calls.length).toBe(calls);
    } finally {
        added.mockRestore();
        removed.mockRestore();
        focus.mockRestore();
    }
});

// Break: a local/native preference replacing the Gateway value, or dropping the ETag between requests.
test('C03 preference persists through the Gateway path with the current ETag', async () => {
    const user = userEvent.setup();
    preference.mockResolvedValueOnce({body: {mode: 'dark', density: 'comfortable', version: 8}, etag: '"8"', logoutUrl: null, status: 200});
    preference.mockResolvedValueOnce({body: {mode: 'light', density: 'comfortable', version: 9}, etag: '"9"', logoutUrl: null, status: 200});
    preference.mockResolvedValueOnce({body: {mode: 'system', density: 'comfortable', version: 10}, etag: '"10"', logoutUrl: null, status: 200});
    const view = renderShell();
    await ready();
    await user.click(themeTrigger());

    await user.click(screen.getByRole('menuitemradio', {name: 'Tối'}));
    await waitFor(() => expect(screen.getByRole('menuitemradio', {name: 'Tối'})).toHaveAttribute('aria-checked', 'true'));
    expect(preference).toHaveBeenLastCalledWith({mode: 'dark'}, '"7"');
    expect(screen.getByRole('button', {name: 'Giao diện: Tối'})).toBeInTheDocument();
    await user.click(screen.getByRole('menuitemradio', {name: 'Sáng'}));
    await waitFor(() => expect(screen.getByRole('menuitemradio', {name: 'Sáng'})).toHaveAttribute('aria-checked', 'true'));
    expect(preference).toHaveBeenLastCalledWith({mode: 'light'}, '"8"');
    await user.click(screen.getByRole('menuitemradio', {name: 'Hệ thống'}));
    await waitFor(() => expect(screen.getByRole('menuitemradio', {name: 'Hệ thống'})).toHaveAttribute('aria-checked', 'true'));
    expect(preference).toHaveBeenLastCalledWith({mode: 'system'}, '"9"');
    expect(preference).toHaveBeenCalledTimes(3);
    expect(window.localStorage.length).toBe(0);

    // Reload: the persisted mode, not a local copy, decides what is checked after the panel reopens.
    view.unmount();
    shell.mockResolvedValue({body: {...snapshot, preference: {mode: 'dark', density: 'comfortable'}}, etag: '"11"', logoutUrl: LOGOUT, status: 200});
    renderShell();
    await ready();
    await user.click(themeTrigger());
    expect(screen.getByRole('menuitemradio', {name: 'Tối'})).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('menuitemradio', {name: 'Sáng'})).toHaveAttribute('aria-checked', 'false');
    await user.keyboard('{Escape}');
    await user.click(themeTrigger());
    expect(screen.getByRole('menuitemradio', {name: 'Tối'})).toHaveAttribute('aria-checked', 'true');
});

// Break: showing a pending/unsafe launcher target or unsafe footer link.
test('C04 only granted, safe navigation and footer URLs are linked', async () => {
    const user = userEvent.setup();
    mockNavigation([
        {appCode: 'tasks', label: 'Tasks', description: '', iconKey: 'tasks', state: 'ready', launchUrl: 'https://tasks.phoenixsoft.vn'},
        {appCode: 'files', label: 'Files pending', description: '', iconKey: 'files', state: 'provisioning'},
        {appCode: 'bad1', label: 'Script', description: '', iconKey: 'x', state: 'ready', launchUrl: 'javascript:alert(1)'}, // eslint-disable-line no-script-url
        {appCode: 'bad2', label: 'Evil', description: '', iconKey: 'x', state: 'ready', launchUrl: 'https://evil.example'},
    ]);
    const view = renderShell();
    await ready();
    await user.click(appsTrigger());
    expect(screen.getAllByRole('menuitem').map((a) => [a.textContent, a.getAttribute('href')])).toEqual([['Tasks', 'https://tasks.phoenixsoft.vn']]);
    view.unmount();
});

// Break: leaving an open theme menu, chat content, or applied theme variables after terminal 401 revocation.
test('C04 terminal 401 revocation while a panel is open removes content, panel and theme once', async () => {
    const user = userEvent.setup();
    const store = require('stores/redux_store').default;
    renderShell();
    await ready();
    await user.click(themeTrigger());
    expect(screen.getByRole('menu', {name: 'Giao diện'})).toBeVisible();
    shell.mockRejectedValue(new client.ShellError(401, 'SESSION_ENDED'));
    act(() => {
        window.dispatchEvent(new Event(NATIVE_SESSION_CLEARED));
    });
    expect(await screen.findByRole('alert')).toHaveTextContent('Phiên đăng nhập không hợp lệ. Vui lòng đăng nhập lại.');
    expect(screen.queryByRole('menu')).toBeNull();
    expect(screen.queryByText('Chat content')).toBeNull();
    expect(screen.queryByRole('menuitemradio')).toBeNull();
    expect(document.documentElement.hasAttribute('data-phoenix-mode')).toBe(false);
    expect(store.dispatch).toHaveBeenCalledTimes(1);
});

test('does not poll every 5s and keeps LKG / session alive on 503, 401 ASSERTION_INVALID, and 403 without LOGOUT_SUCCESS', async () => {
    jest.useFakeTimers();
    const store = require('stores/redux_store').default;
    try {
        renderShell();
        await ready();
        expect(shell).toHaveBeenCalledTimes(1);

        await act(async () => {
            jest.advanceTimersByTime(20_000);
        });
        expect(shell).toHaveBeenCalledTimes(1);

        shell.mockRejectedValueOnce(new client.ShellError(503, 'PHOENIX_AUTHORITY_UNAVAILABLE'));
        await act(async () => {
            window.dispatchEvent(new Event(NATIVE_SESSION_CLEARED));
        });
        expect(screen.getByText('Chat content')).toBeInTheDocument();
        expect(store.dispatch).not.toHaveBeenCalled();

        shell.mockRejectedValueOnce(new client.ShellError(401, 'ASSERTION_INVALID'));
        await act(async () => {
            window.dispatchEvent(new Event(NATIVE_SESSION_CLEARED));
        });
        expect(screen.getByText('Chat content')).toBeInTheDocument();
        expect(store.dispatch).not.toHaveBeenCalled();

        shell.mockRejectedValueOnce(new client.ShellError(403, 'APP_NOT_ALLOWED'));
        await act(async () => {
            window.dispatchEvent(new Event(NATIVE_SESSION_CLEARED));
        });
        expect(await screen.findByRole('alert')).toHaveTextContent('Bạn chưa được cấp quyền sử dụng Chat.');
        expect(store.dispatch).not.toHaveBeenCalled();
    } finally {
        jest.useRealTimers();
    }
});

// Break: panels reachable only by mouse, arrows that do not move/select with sequential ETags,
// losing focus on responsive resize/theme updates, or Escape leaking to Chat shortcuts.
test('U03 keyboard: Tab, Enter/Space, arrows with sequential ETags, resize focus retention, Escape and leaving the panel', async () => {
    const user = userEvent.setup();
    mockNavigation([
        {appCode: 'tasks', label: 'Tasks', description: '', iconKey: 'tasks', state: 'ready', launchUrl: 'https://tasks.phoenixsoft.vn'},
        {appCode: 'portal', label: 'Portal', description: '', iconKey: 'portal', state: 'ready', launchUrl: 'https://portal.phoenixsoft.vn'},
    ]);
    preference.mockResolvedValueOnce({body: {mode: 'dark', density: 'comfortable', version: 8}, etag: '"8"', logoutUrl: null, status: 200});
    preference.mockResolvedValueOnce({body: {mode: 'system', density: 'comfortable', version: 9}, etag: '"9"', logoutUrl: null, status: 200});
    const chatEscape = jest.fn();
    const followed: string[] = [];
    const onKey = (event: KeyboardEvent) => event.key === 'Escape' && chatEscape();
    const onClick = (event: MouseEvent) => {
        const link = (event.target as HTMLElement).closest('a');
        if (link) {
            event.preventDefault();
            followed.push(link.getAttribute('href') as string);
        }
    };
    document.addEventListener('keydown', onKey);
    document.addEventListener('click', onClick);
    try {
        renderShell();
        await ready();

        // Launcher: Tab, Enter, arrows with wrap, Enter follows the link, Escape returns focus.
        await user.tab();
        expect(appsTrigger()).toHaveFocus();
        await user.keyboard('{Enter}');
        expect(screen.getByRole('menuitem', {name: 'Tasks'})).toHaveFocus();
        await user.keyboard('{ArrowDown}');
        expect(screen.getByRole('menuitem', {name: 'Portal'})).toHaveFocus();
        await user.keyboard('{ArrowDown}');
        expect(screen.getByRole('menuitem', {name: 'Tasks'})).toHaveFocus();
        await user.keyboard('{ArrowUp}');
        expect(screen.getByRole('menuitem', {name: 'Portal'})).toHaveFocus();
        await user.keyboard('{Home}');
        expect(screen.getByRole('menuitem', {name: 'Tasks'})).toHaveFocus();
        await user.keyboard('{End}');
        expect(screen.getByRole('menuitem', {name: 'Portal'})).toHaveFocus();
        await user.keyboard('{Enter}');
        expect(followed).toEqual(['https://portal.phoenixsoft.vn']);
        await user.keyboard('{Escape}');
        expect(screen.queryByRole('menu')).toBeNull();
        expect(appsTrigger()).toHaveFocus();

        // Theme menu: Tab, Space opens menu, arrows select dark ("7") then system ("8") and retain focus across resize.
        await user.tab();
        expect(screen.getByRole('button', {name: 'Giao diện: Sáng'})).toHaveFocus();
        await user.keyboard(' ');
        expect(screen.getByRole('menuitemradio', {name: 'Sáng'})).toHaveFocus();
        await user.keyboard('{ArrowRight}');
        expect(screen.getByRole('menuitemradio', {name: 'Tối'})).toHaveFocus();
        await waitFor(() => expect(screen.getByRole('menuitemradio', {name: 'Tối'})).toHaveAttribute('aria-checked', 'true'));
        expect(preference).toHaveBeenNthCalledWith(1, {mode: 'dark'}, '"7"');
        expect(screen.getByRole('menuitemradio', {name: 'Tối'})).toHaveFocus();

        await user.keyboard('{ArrowDown}');
        expect(screen.getByRole('menuitemradio', {name: 'Hệ thống'})).toHaveFocus();
        await waitFor(() => expect(screen.getByRole('menuitemradio', {name: 'Hệ thống'})).toHaveAttribute('aria-checked', 'true'));
        expect(preference).toHaveBeenNthCalledWith(2, {mode: 'system'}, '"8"');

        // Responsive resize update while menu is open retains focus on the active option.
        act(() => {
            Object.defineProperty(window, 'innerWidth', {writable: true, configurable: true, value: 390});
            window.dispatchEvent(new Event('resize'));
        });
        expect(screen.getByRole('menuitemradio', {name: 'Hệ thống'})).toHaveFocus();

        await user.keyboard('{Escape}');
        expect(screen.queryByRole('menu')).toBeNull();
        expect(screen.getByRole('button', {name: 'Giao diện: Hệ thống'})).toHaveFocus();
        expect(chatEscape).not.toHaveBeenCalled();

        // Non-modal: Tab walks out of the panel, which closes without stealing focus back.
        await user.keyboard('{Enter}');
        expect(screen.getByRole('menuitemradio', {name: 'Hệ thống'})).toHaveFocus();
        await user.tab();
        expect(screen.getByRole('textbox', {name: 'Composer'})).toHaveFocus();
        expect(screen.queryByRole('menu')).toBeNull();
    } finally {
        document.removeEventListener('keydown', onKey);
        document.removeEventListener('click', onClick);
        Object.defineProperty(window, 'innerWidth', {writable: true, configurable: true, value: 1024});
    }
});
