// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {UserTypes} from 'mattermost-redux/action_types';
import store from 'stores/redux_store';

import type {AppTile, ColorMode, Navigation, ShellSnapshot} from './client';
import {ERROR_COPY, ShellError, fetchNavigation, fetchShellContext, patchPreference} from './client';
import {applyTheme, clearTheme, resolveMode} from './theme';

import './phoenix_shell.scss';

type Phase =
    | {name: 'loading'}
    | {name: 'inactive'} // the native server does not run the Phoenix boundary (development builds)
    | {name: 'error'; error: ShellError}
    | {name: 'ready'; snapshot: ShellSnapshot; nav: Navigation | null; logoutUrl: string | null};

const REVALIDATE_MS = 15000;
const MODES: Array<{value: ColorMode; label: string}> = [
    {value: 'light', label: 'Sáng'},
    {value: 'dark', label: 'Tối'},
    {value: 'system', label: 'Hệ thống'},
];

// Drop everything this browser profile holds for the previous principal.
export async function clearSensitiveBrowserState() {
    store.dispatch({type: UserTypes.LOGOUT_SUCCESS, data: {}});
    window.sessionStorage.clear();
    window.localStorage.clear();
    try {
        if ('caches' in window) {
            const keys = await window.caches.keys();
            await Promise.all(keys.map((k) => window.caches.delete(k)));
        }
        if ('serviceWorker' in navigator) {
            const regs = await navigator.serviceWorker.getRegistrations();
            await Promise.all(regs.map((r) => r.unregister()));
        }
    } catch {
        // best effort: a browser without these APIs holds no such state
    }
}

function safeHttps(url: string | undefined): string | undefined {
    if (!url) {
        return undefined;
    }
    try {
        const value = new URL(url);
        const allowed = value.hostname === 'phoenixsoft.vn' || value.hostname.endsWith('.phoenixsoft.vn') || value.hostname.endsWith('.mm-replace.test');
        return value.protocol === 'https:' && !value.username && !value.password && allowed ? value.href : undefined;
    } catch {
        return undefined;
    }
}

type Props = {children: React.ReactNode};

export default function PhoenixShell({children}: Props) {
    const [phase, setPhase] = useState<Phase>({name: 'loading'});
    const [prefersDark, setPrefersDark] = useState(() => window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false);
    const [menuOpen, setMenuOpen] = useState(false);
    const navTag = useRef<string | null>(null);
    const preferenceTag = useRef<string | null>(null);
    const prevTenant = useRef<string | null>(null);

    const load = useCallback(async () => {
        try {
            const ctx = await fetchShellContext();
            let nav: Navigation | null = null;
            try {
                const n = await fetchNavigation(navTag.current);
                if (n.body) {
                    nav = n.body;
                    navTag.current = n.etag;
                }
            } catch {
                nav = null; // launcher is optional; the shell itself stays usable
            }
            const snapshot = ctx.body as ShellSnapshot;
            preferenceTag.current = ctx.etag;
            const tenantId = snapshot.tenant?.id ?? null;
            if (prevTenant.current && prevTenant.current !== tenantId) {
                // A different principal/tenant took over this browser: nothing of the old one may remain.
                await clearSensitiveBrowserState();
                window.location.reload();
                return;
            }
            prevTenant.current = tenantId;
            setPhase((p) => ({name: 'ready', snapshot, nav: nav ?? (p.name === 'ready' ? p.nav : null), logoutUrl: ctx.logoutUrl ?? (p.name === 'ready' ? p.logoutUrl : null)}));
        } catch (e) {
            const error = e instanceof ShellError ? e : new ShellError(503, 'PHOENIX_AUTHORITY_UNAVAILABLE');
            clearTheme(document.documentElement);
            await clearSensitiveBrowserState();
            setPhase({name: 'error', error});
        }
    }, []);

    useEffect(() => {
        load();
        const t = window.setInterval(load, REVALIDATE_MS);
        return () => window.clearInterval(t);
    }, [load]);

    useEffect(() => {
        const mq = window.matchMedia?.('(prefers-color-scheme: dark)');
        if (!mq) {
            return undefined;
        }
        const onChange = (ev: MediaQueryListEvent) => setPrefersDark(ev.matches);
        mq.addEventListener('change', onChange);
        return () => mq.removeEventListener('change', onChange);
    }, []);

    const mode: ColorMode | null = phase.name === 'ready' ? phase.snapshot.preference.mode : null;
    useEffect(() => {
        if (phase.name === 'ready') {
            applyTheme(document.documentElement, phase.snapshot, phase.snapshot.preference.mode, prefersDark);
        }
    }, [phase, prefersDark, mode]);

    const choose = useCallback(async (value: ColorMode) => {
        try {
            const res = await patchPreference({mode: value}, preferenceTag.current);
            preferenceTag.current = res.etag;
            if (res.body) {
                setPhase((p) => p.name === 'ready' ? {...p, snapshot: {...p.snapshot, preference: {...p.snapshot.preference, mode: res.body!.mode}}} : p);
            }
        } catch (e) {
            if (e instanceof ShellError) {
                setPhase({name: 'error', error: e});
            }
        }
    }, []);

    if (phase.name === 'loading') {
        return <div className='phoenix-shell phoenix-shell--loading' role='status'>{'Đang tải…'}</div>;
    }
    if (phase.name === 'inactive') {
        return <>{children}</>;
    }
    if (phase.name === 'error') {
        const retryable = phase.error.kind === 'pending' || phase.error.kind === 'unavailable';
        return (
            <div className='phoenix-shell phoenix-shell--message'>
                <main
                    className='phoenix-shell__notice'
                    role='alert'
                >
                    <p data-testid='phoenix-error-copy'>{ERROR_COPY[phase.error.kind]}</p>
                    {retryable ? (
                        <button
                            type='button'
                            onClick={() => {
                                setPhase({name: 'loading'});
                                load();
                            }}
                        >{'Thử lại'}</button>
                    ) : (
                        <a href='/phoenix/auth/login'>{'Đăng nhập'}</a>
                    )}
                </main>
            </div>
        );
    }

    const {snapshot, nav, logoutUrl} = phase;
    const resolved = resolveMode(snapshot.preference.mode, prefersDark);
    const logo = resolved === 'dark' ? snapshot.tenant?.logoDarkUrl || snapshot.tenant?.logoLightUrl : snapshot.tenant?.logoLightUrl || snapshot.tenant?.logoDarkUrl;
    const apps: AppTile[] = (nav?.apps ?? []).filter((a) => a.state === 'ready' && safeHttps(a.launchUrl));
    const footerParties = [snapshot.footer.operator, snapshot.footer.tenant].filter(Boolean);

    return (
        <div className={`phoenix-shell phoenix-shell--${resolved}`}>
            <header
                className='phoenix-shell__header'
                data-testid='phoenix-header'
            >
                <div className='phoenix-shell__brand'>
                    {safeHttps(logo || undefined) && (
                        <img
                            className='phoenix-shell__logo'
                            src={logo as string}
                            alt=''
                        />
                    )}
                    <span className='phoenix-shell__tenant'>{snapshot.tenant?.displayName}</span>
                </div>
                <div className='phoenix-shell__controls'>
                    <div className='phoenix-shell__launcher'>
                        <button
                            type='button'
                            aria-haspopup='true'
                            aria-expanded={menuOpen}
                            onClick={() => setMenuOpen((o) => !o)}
                        >{'Ứng dụng'}</button>
                        {menuOpen && (
                            <ul
                                className='phoenix-shell__menu'
                                role='menu'
                            >
                                {apps.map((a) => (
                                    <li
                                        key={a.appCode}
                                        role='none'
                                    >
                                        <a
                                            role='menuitem'
                                            href={a.launchUrl}
                                            rel='noopener noreferrer'
                                        >{a.label}</a>
                                    </li>
                                ))}
                            </ul>
                        )}
                    </div>
                    <div
                        className='phoenix-shell__modes'
                        role='radiogroup'
                        aria-label='Giao diện'
                    >
                        {MODES.map((m) => (
                            <button
                                key={m.value}
                                type='button'
                                role='radio'
                                aria-checked={snapshot.preference.mode === m.value}
                                onClick={() => choose(m.value)}
                            >{m.label}</button>
                        ))}
                    </div>
                    <div className='phoenix-shell__account'>
                        <span>{snapshot.account?.displayName}</span>
                        {safeHttps(logoutUrl || undefined) && <a href={logoutUrl as string}>{'Đăng xuất'}</a>}
                    </div>
                </div>
            </header>
            <main className='phoenix-shell__main'>{children}</main>
            {snapshot.footer.presentation !== 'empty' && (
                <footer
                    className='phoenix-shell__footer'
                    data-testid='phoenix-footer'
                >
                    {footerParties.map((p, i) => (
                        <span key={i}>{(p?.displayName || p?.name) ?? ''}{(p?.contacts ?? []).map((c) => ` ${c.label ?? ''} ${c.value ?? ''}`).join('')}</span>
                    ))}
                    {snapshot.footer.links.filter((l) => safeHttps(l.url)).map((l) => (
                        <a
                            key={l.url}
                            href={l.url}
                            rel='noopener noreferrer'
                        >{l.label}</a>
                    ))}
                </footer>
            )}
        </div>
    );
}
