// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.

import React, {useCallback, useEffect, useId, useRef, useState} from 'react';
import {UserTypes} from 'mattermost-redux/action_types';
import store from 'stores/redux_store';
import InitialLoadingScreen from 'components/initial_loading_screen';
import {registerPhoenixSessionLifecycle, NATIVE_SESSION_CLEARED} from './session_lifecycle';

import type {AppTile, ColorMode, Navigation, ShellSnapshot} from './client';
import {ERROR_COPY, ShellError, fetchNavigation, fetchShellContext, patchPreference} from './client';
import {applyTheme, clearTheme, resolveMode} from './theme';

import './phoenix_shell.scss';

type Phase =
    | {name: 'loading'}
    | {name: 'inactive'} // the native server does not run the Phoenix boundary (development builds)
    | {name: 'error'; error: ShellError}
    | {name: 'ready'; snapshot: ShellSnapshot; nav: Navigation | null; logoutUrl: string | null};

type PopoverId = 'apps' | 'account';

const REVALIDATE_MS = 5000;
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

function initials(value: string | undefined): string {
    const words = (value || 'Phoenix').trim().split(/\s+/).filter(Boolean);
    return words.slice(0, 2).map((word) => word[0]).join('').toUpperCase() || 'P';
}

function LauncherIcon() {
    return (
        <svg
            viewBox='0 0 24 24'
            width='1em'
            height='1em'
            aria-hidden='true'
            focusable='false'
            fill='currentColor'
        >
            {[6, 12, 18].flatMap((y) => [6, 12, 18].map((x) => (
                <circle
                    key={`${x}-${y}`}
                    cx={x}
                    cy={y}
                    r='1.6'
                />
            )))}
        </svg>
    );
}

// Decorative: the account trigger and panel carry the name for assistive technology.
function Avatar({url, name}: {url: string | null | undefined; name: string}) {
    return safeHttps(url || undefined) ? (
        <img
            className='phoenix-shell__avatar'
            src={url as string}
            alt=''
        />
    ) : <span className='phoenix-shell__account-monogram' aria-hidden='true'>{initials(name)}</span>;
}

type Props = {children: React.ReactNode};

export default function PhoenixShell({children}: Props) {
    const [phase, setPhase] = useState<Phase>({name: 'loading'});
    const [prefersDark, setPrefersDark] = useState(() => window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false);
    const [openPopover, setOpenPopover] = useState<PopoverId | null>(null);
    const openRef = useRef<PopoverId | null>(null);
    const controlsRef = useRef<HTMLDivElement>(null);
    const triggerRefs = useRef<Record<PopoverId, HTMLButtonElement | null>>({apps: null, account: null});
    const restoreTimer = useRef<number | undefined>(undefined);
    const choiceQueue = useRef<Promise<void>>(Promise.resolve());
    const appsPanelId = useId();
    const accountPanelId = useId();
    const accountDescriptionId = useId();
    const modesLabelId = useId();
    const navTag = useRef<string | null>(null);
    const preferenceTag = useRef<string | null>(null);
    const prevTenant = useRef<string | null>(null);
    const requestActive = useRef(false);
    const authorityFailed = useRef(false);

    const load = useCallback(async () => {
        if (requestActive.current) {return;}
        requestActive.current = true;
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
            if (authorityFailed.current) {
                // A verified recovery starts native bootstrap with the retained
                // HttpOnly SSO session, after all prior-user state was purged.
                window.location.reload();
                return;
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
            authorityFailed.current = true;
            clearTheme(document.documentElement);
            setPhase({name: 'error', error});
            InitialLoadingScreen.stop('phoenix-authority');
            await clearSensitiveBrowserState();
        } finally {
            requestActive.current = false;
        }
    }, []);

    useEffect(() => {
        const release = registerPhoenixSessionLifecycle();
        const cleared = () => {void load();};
        window.addEventListener(NATIVE_SESSION_CLEARED, cleared);
        load();
        const t = window.setInterval(load, REVALIDATE_MS);
        return () => {
            window.clearInterval(t);
            window.removeEventListener(NATIVE_SESSION_CLEARED, cleared);
            release();
        };
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

    // Requests run in order so a quick second keyboard selection never carries a stale ETag.
    const select = useCallback((value: ColorMode) => {
        const run = () => choose(value);
        choiceQueue.current = choiceQueue.current.then(run, run);
    }, [choose]);

    const focusTrigger = useCallback((id: PopoverId) => {
        const trigger = triggerRefs.current[id];
        if (trigger?.isConnected) {
            trigger.focus({preventScroll: true});
        }
    }, []);

    const closePopover = useCallback((restoreFocus: boolean) => {
        const current = openRef.current;
        if (!current) {
            return;
        }
        openRef.current = null;
        setOpenPopover(null);
        if (restoreFocus) {
            focusTrigger(current);
        }
    }, [focusTrigger]);

    const togglePopover = useCallback((id: PopoverId) => {
        if (openRef.current === id) {
            closePopover(true);
            return;
        }
        openRef.current = id;
        setOpenPopover(id);
    }, [closePopover]);

    // A newly opened panel takes keyboard focus; the previous trigger never gets it back.
    useEffect(() => {
        if (!openPopover) {
            return;
        }
        const panel = controlsRef.current?.querySelector<HTMLElement>(`[data-phoenix-popover="${openPopover}"]`);
        const target = panel?.querySelector<HTMLElement>(openPopover === 'account' ? '[role="radio"][tabindex="0"]' : '[role="menuitem"]');
        (target ?? panel)?.focus({preventScroll: true});
    }, [openPopover]);

    // An outside press closes the open panel; both triggers keep their own click handling.
    useEffect(() => {
        if (!openPopover) {
            return undefined;
        }
        const onPointerDown = (event: MouseEvent) => {
            const open = openRef.current;
            const target = event.target as Node | null;
            if (!open || !target) {
                return;
            }
            const panel = controlsRef.current?.querySelector(`[data-phoenix-popover="${open}"]`);
            if (panel?.contains(target) || triggerRefs.current.apps?.contains(target) || triggerRefs.current.account?.contains(target)) {
                return;
            }
            closePopover(false);

            // The press moves focus to a focusable target after this handler runs, so only
            // a press that leaves focus nowhere returns it to the trigger.
            window.clearTimeout(restoreTimer.current);
            restoreTimer.current = window.setTimeout(() => {
                if (!document.activeElement || document.activeElement === document.body) {
                    focusTrigger(open);
                }
            }, 0);
        };
        document.addEventListener('mousedown', onPointerDown);
        return () => document.removeEventListener('mousedown', onPointerDown);
    }, [openPopover, closePopover, focusTrigger]);

    useEffect(() => () => window.clearTimeout(restoreTimer.current), []);

    // Panels exist only in the ready branch; a failed revalidation must not leave one armed.
    useEffect(() => {
        if (phase.name !== 'ready' && openRef.current) {
            openRef.current = null;
            setOpenPopover(null);
        }
    }, [phase.name]);

    const onControlsKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
        if (event.key === 'Escape' && openRef.current) {
            // Handled here: the Chat shortcuts behind the header must not also see it.
            event.stopPropagation();
            closePopover(true);
        }
    };

    const onControlsBlur = (event: React.FocusEvent<HTMLDivElement>) => {
        const next = event.relatedTarget as Node | null;
        if (openRef.current && next && !controlsRef.current?.contains(next)) {
            closePopover(false);
        }
    };

    const onMenuKeyDown = (event: React.KeyboardEvent<HTMLUListElement>) => {
        const items = Array.from(event.currentTarget.querySelectorAll<HTMLElement>('[role="menuitem"]'));
        const from = items.indexOf(event.target as HTMLElement);
        let to = -1;
        if (event.key === 'ArrowDown') {
            to = (from + 1) % items.length;
        } else if (event.key === 'ArrowUp') {
            to = (from <= 0 ? items.length : from) - 1;
        } else if (event.key === 'Home') {
            to = 0;
        } else if (event.key === 'End') {
            to = items.length - 1;
        }
        if (items.length && to >= 0) {
            event.preventDefault();
            items[to].focus();
        }
    };

    const onModesKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
        const step = ({ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1} as Record<string, number>)[event.key];
        const radios = Array.from(event.currentTarget.querySelectorAll<HTMLElement>('[role="radio"]'));
        const from = radios.indexOf(event.target as HTMLElement);
        if (!step || from < 0) {
            return;
        }
        event.preventDefault();
        const to = (from + step + radios.length) % radios.length;
        radios[to].focus();
        select(MODES[to].value);
    };

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
    const tenantName = snapshot.tenant?.displayName || 'Phoenix Soft';
    const accountName = snapshot.account?.displayName || 'Tài khoản';

    return (
        <div className={`phoenix-shell phoenix-shell--${resolved}`}>
            <header
                className='phoenix-shell__header'
                data-testid='phoenix-header'
            >
                <div className='phoenix-shell__brand'>
                    {safeHttps(logo || undefined) ? (
                        <img
                            className='phoenix-shell__logo'
                            src={logo as string}
                            alt={tenantName}
                        />
                    ) : <span className='phoenix-shell__monogram' aria-hidden='true'>{initials(tenantName)}</span>}
                    <span className='phoenix-shell__brand-text'>
                        <span className='phoenix-shell__tenant' title={tenantName}>{tenantName}</span>
                        <span className='phoenix-shell__app-name'>{'Chat'}</span>
                    </span>
                </div>
                {/* Delegated Escape/blur handling: the focusable controls inside own the interaction. */}
                {/* eslint-disable-next-line jsx-a11y/no-static-element-interactions */}
                <div
                    className='phoenix-shell__controls'
                    ref={controlsRef}
                    onKeyDown={onControlsKeyDown}
                    onBlur={onControlsBlur}
                >
                    <div className='phoenix-shell__popover-root'>
                        <button
                            className='phoenix-shell__icon-button'
                            ref={(el) => {
                                triggerRefs.current.apps = el;
                            }}
                            type='button'
                            aria-label='Ứng dụng'
                            aria-haspopup='menu'
                            aria-expanded={openPopover === 'apps'}
                            aria-controls={openPopover === 'apps' ? appsPanelId : undefined}
                            onClick={() => togglePopover('apps')}
                        ><LauncherIcon/></button>
                        {openPopover === 'apps' && (
                            <ul
                                id={appsPanelId}
                                className='phoenix-shell__popover phoenix-shell__menu'
                                role='menu'
                                aria-label='Ứng dụng'
                                data-phoenix-popover='apps'
                                tabIndex={-1}
                                onKeyDown={onMenuKeyDown}
                            >
                                {apps.map((a) => (
                                    <li
                                        key={a.appCode}
                                        role='none'
                                    >
                                        <a
                                            className='phoenix-shell__menu-item'
                                            role='menuitem'
                                            tabIndex={-1}
                                            href={a.launchUrl}
                                            rel='noopener noreferrer'
                                        >{a.label}</a>
                                    </li>
                                ))}
                                {apps.length === 0 && (
                                    <li
                                        className='phoenix-shell__menu-empty'
                                        role='none'
                                    >{'Bạn chưa được cấp ứng dụng'}</li>
                                )}
                            </ul>
                        )}
                    </div>
                    <span
                        className='phoenix-shell__divider'
                        aria-hidden='true'
                    />
                    <div className='phoenix-shell__popover-root'>
                        <button
                            className='phoenix-shell__account-trigger'
                            ref={(el) => {
                                triggerRefs.current.account = el;
                            }}
                            type='button'
                            title={`${accountName} — ${tenantName}`}
                            aria-label='Tài khoản'
                            aria-describedby={accountDescriptionId}
                            aria-haspopup='dialog'
                            aria-expanded={openPopover === 'account'}
                            aria-controls={openPopover === 'account' ? accountPanelId : undefined}
                            onClick={() => togglePopover('account')}
                        >
                            <Avatar
                                url={snapshot.account?.avatarUrl}
                                name={accountName}
                            />
                            <span
                                id={accountDescriptionId}
                                className='phoenix-shell__account-text'
                            >
                                <span className='phoenix-shell__account-name'>{accountName}</span>{' '}
                                <span className='phoenix-shell__account-tenant'>{tenantName}</span>
                            </span>
                        </button>
                        {openPopover === 'account' && (
                            <div
                                id={accountPanelId}
                                className='phoenix-shell__popover phoenix-shell__account-panel'
                                role='dialog'
                                aria-label='Tài khoản'
                                aria-modal='false'
                                data-phoenix-popover='account'
                                tabIndex={-1}
                            >
                                <div className='phoenix-shell__identity'>
                                    <Avatar
                                        url={snapshot.account?.avatarUrl}
                                        name={accountName}
                                    />
                                    <span className='phoenix-shell__account-text'>
                                        <span className='phoenix-shell__account-name'>{accountName}</span>
                                        <span className='phoenix-shell__account-tenant'>{tenantName}</span>
                                    </span>
                                </div>
                                <div className='phoenix-shell__section'>
                                    <span
                                        id={modesLabelId}
                                        className='phoenix-shell__section-label'
                                    >{'Giao diện'}</span>
                                    {/* Arrow keys are delegated from the radios, which are the focusable elements. */}
                                    {/* eslint-disable-next-line jsx-a11y/interactive-supports-focus */}
                                    <div
                                        className='phoenix-shell__modes'
                                        role='radiogroup'
                                        aria-labelledby={modesLabelId}
                                        onKeyDown={onModesKeyDown}
                                    >
                                        {MODES.map((m) => {
                                            const checked = snapshot.preference.mode === m.value;
                                            return (
                                                <button
                                                    key={m.value}
                                                    className='phoenix-shell__mode'
                                                    type='button'
                                                    role='radio'
                                                    tabIndex={checked ? 0 : -1}
                                                    aria-checked={checked}
                                                    onClick={() => select(m.value)}
                                                >{m.label}</button>
                                            );
                                        })}
                                    </div>
                                </div>
                                {safeHttps(logoutUrl || undefined) && (
                                    <a
                                        className='phoenix-shell__logout'
                                        href={logoutUrl as string}
                                    >{'Đăng xuất'}</a>
                                )}
                            </div>
                        )}
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
