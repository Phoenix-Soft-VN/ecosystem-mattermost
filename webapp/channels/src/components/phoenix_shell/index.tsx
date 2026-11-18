// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.

import React, {useCallback, useContext, useEffect, useId, useLayoutEffect, useRef, useState} from 'react';
import {IntlContext, IntlProvider} from 'react-intl';
import {WithTooltip} from '@mattermost/shared/components/tooltip';
import {UserTypes} from 'mattermost-redux/action_types';
import store from 'stores/redux_store';
import InitialLoadingScreen from 'components/initial_loading_screen';
import {HeaderSlotsProvider, HeaderSlotTarget, useCompactChannelTools, useHasSlotContent} from './header_slots';
import {registerPhoenixSessionLifecycle, NATIVE_SESSION_CLEARED} from './session_lifecycle';

import type {AppTile, ColorMode, Navigation, ShellSnapshot} from './client';
import {ERROR_COPY, ShellError, fetchNavigation, fetchShellContext, patchPreference} from './client';
import type {ResolvedMode} from './theme';
import {applyTheme, clearTheme, resolveMode} from './theme';

import './phoenix_shell.scss';

type Phase =
    | {name: 'loading'}
    | {name: 'inactive'} // the native server does not run the Phoenix boundary (development builds)
    | {name: 'error'; error: ShellError}
    | {name: 'ready'; snapshot: ShellSnapshot; nav: Navigation | null; logoutUrl: string | null};

type PopoverId = 'apps' | 'theme' | 'utilities';

const REVALIDATE_MS = 5000;
const COMPACT_UTILITIES_WIDTH = 1020;
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

function ThemeIcon({resolved}: {resolved: ResolvedMode}) {
    if (resolved === 'dark') {
        return (
            <svg
                viewBox='0 0 24 24'
                width='1em'
                height='1em'
                aria-hidden='true'
                focusable='false'
                fill='none'
                stroke='currentColor'
                strokeWidth='2'
                strokeLinecap='round'
                strokeLinejoin='round'
                data-theme-icon='dark'
            >
                <path d='M21 12.79A9 9 0 1 1 11.21 3a7 7 0 0 0 9.79 9.79Z'/>
            </svg>
        );
    }
    return (
        <svg
            viewBox='0 0 24 24'
            width='1em'
            height='1em'
            aria-hidden='true'
            focusable='false'
            fill='none'
            stroke='currentColor'
            strokeWidth='2'
            strokeLinecap='round'
            strokeLinejoin='round'
            data-theme-icon='light'
        >
            <circle
                cx='12'
                cy='12'
                r='4'
            />
            <path d='M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41'/>
        </svg>
    );
}

function ModeOptionIcon({mode}: {mode: ColorMode}) {
    if (mode === 'dark') {
        return <ThemeIcon resolved='dark'/>;
    }
    if (mode === 'light') {
        return <ThemeIcon resolved='light'/>;
    }
    return (
        <svg
            viewBox='0 0 24 24'
            width='1em'
            height='1em'
            aria-hidden='true'
            focusable='false'
            fill='none'
            stroke='currentColor'
            strokeWidth='2'
            strokeLinecap='round'
            strokeLinejoin='round'
        >
            <rect
                x='3'
                y='4'
                width='18'
                height='12'
                rx='2'
            />
            <path d='M8 20h8M12 16v4'/>
        </svg>
    );
}

function OverflowIcon() {
    return (
        <svg
            viewBox='0 0 24 24'
            width='1em'
            height='1em'
            aria-hidden='true'
            focusable='false'
            fill='currentColor'
        >
            <circle
                cx='5'
                cy='12'
                r='1.8'
            />
            <circle
                cx='12'
                cy='12'
                r='1.8'
            />
            <circle
                cx='19'
                cy='12'
                r='1.8'
            />
        </svg>
    );
}

function EnsureIntl({children}: {children: React.ReactNode}) {
    const intl = useContext(IntlContext);
    if (intl) {
        return <>{children}</>;
    }
    return (
        <IntlProvider
            locale='vi'
            defaultLocale='vi'
        >
            {children}
        </IntlProvider>
    );
}

function isInExternalMenuPortal(node: Node | null): boolean {
    if (!(node instanceof Element)) {
        return false;
    }
    return Boolean(node.closest('#root-portal, .MuiModal-root, .Menu, [role="dialog"]'));
}

type Props = {children: React.ReactNode};

export default function PhoenixShell({children}: Props) {
    const [phase, setPhase] = useState<Phase>({name: 'loading'});
    const [prefersDark, setPrefersDark] = useState(() => window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false);
    const [viewportWidth, setViewportWidth] = useState(() => (typeof window !== 'undefined' ? window.innerWidth : 1440));
    const [openPopover, setOpenPopover] = useState<PopoverId | null>(null);
    const openRef = useRef<PopoverId | null>(null);
    const controlsRef = useRef<HTMLDivElement>(null);
    const triggerRefs = useRef<Record<PopoverId, HTMLButtonElement | null>>({apps: null, theme: null, utilities: null});
    const restoreTimer = useRef<number | undefined>(undefined);
    const choiceQueue = useRef<Promise<void>>(Promise.resolve());
    const appsPanelId = useId();
    const themePanelId = useId();
    const utilitiesPanelId = useId();
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

    useEffect(() => {
        const onResize = () => setViewportWidth(window.innerWidth);
        window.addEventListener('resize', onResize);
        return () => window.removeEventListener('resize', onResize);
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
        let target: HTMLElement | null | undefined = null;
        if (openPopover === 'theme') {
            target = panel?.querySelector<HTMLElement>('[role="menuitemradio"][aria-checked="true"]');
        } else if (openPopover === 'apps') {
            target = panel?.querySelector<HTMLElement>('[role="menuitem"]');
        } else if (openPopover === 'utilities') {
            target = panel?.querySelector<HTMLElement>('button, [href], [tabindex="0"]');
        }
        (target ?? panel)?.focus({preventScroll: true});
    }, [openPopover]);

    // An outside press closes the open panel; triggers keep their own click handling.
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
            if (
                panel?.contains(target) ||
                triggerRefs.current.apps?.contains(target) ||
                triggerRefs.current.theme?.contains(target) ||
                triggerRefs.current.utilities?.contains(target) ||
                (open === 'utilities' && isInExternalMenuPortal(target))
            ) {
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

    const isCompactUtilities = viewportWidth <= COMPACT_UTILITIES_WIDTH;
    useEffect(() => {
        if (!isCompactUtilities && openRef.current === 'utilities') {
            openRef.current = null;
            setOpenPopover(null);
        }
    }, [isCompactUtilities]);

    // Native DOM keydown/focusout listeners on controlsRef ensure portaled utilities
    // children (which live under <main> in the React tree but inside controlsRef in the DOM)
    // still close the popover on Escape/blur and stop Escape before document shortcuts.
    useEffect(() => {
        const el = controlsRef.current;
        if (!el || !openPopover) {
            return undefined;
        }
        const onNativeKeyDown = (event: KeyboardEvent) => {
            if (event.key === 'Escape' && openRef.current) {
                event.stopPropagation();
                closePopover(true);
            }
        };
        const onNativeFocusOut = (event: FocusEvent) => {
            const next = event.relatedTarget as Node | null;
            if (openRef.current && next && !el.contains(next)) {
                if (openRef.current === 'utilities' && isInExternalMenuPortal(next)) {
                    return;
                }
                closePopover(false);
            }
        };
        el.addEventListener('keydown', onNativeKeyDown);
        el.addEventListener('focusout', onNativeFocusOut);
        return () => {
            el.removeEventListener('keydown', onNativeKeyDown);
            el.removeEventListener('focusout', onNativeFocusOut);
        };
    }, [openPopover, closePopover]);

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
            if (openRef.current === 'utilities' && isInExternalMenuPortal(next)) {
                return;
            }
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
        const radios = Array.from(event.currentTarget.querySelectorAll<HTMLElement>('[role="menuitemradio"]'));
        const from = radios.indexOf(event.target as HTMLElement);
        if (from < 0 || radios.length === 0) {
            return;
        }
        let to = -1;
        if (event.key === 'ArrowRight' || event.key === 'ArrowDown') {
            to = (from + 1) % radios.length;
        } else if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') {
            to = (from - 1 + radios.length) % radios.length;
        } else if (event.key === 'Home') {
            to = 0;
        } else if (event.key === 'End') {
            to = radios.length - 1;
        }
        if (to >= 0) {
            event.preventDefault();
            radios[to].focus();
            select(MODES[to].value);
        }
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

    const {snapshot, nav} = phase;
    const resolved = resolveMode(snapshot.preference.mode, prefersDark);
    const logo = resolved === 'dark' ? snapshot.tenant?.logoDarkUrl || snapshot.tenant?.logoLightUrl : snapshot.tenant?.logoLightUrl || snapshot.tenant?.logoDarkUrl;
    const apps: AppTile[] = (nav?.apps ?? []).filter((a) => a.state === 'ready' && safeHttps(a.launchUrl));
    const footerParties = [snapshot.footer.operator, snapshot.footer.tenant].filter(Boolean);
    const tenantName = snapshot.tenant?.displayName || 'Phoenix Soft';
    const currentModeLabel = MODES.find((m) => m.value === snapshot.preference.mode)?.label ?? 'Sáng';
    const themeTriggerLabel = `Giao diện: ${currentModeLabel}`;

    return (
        <EnsureIntl>
            <HeaderSlotsProvider>
                <div className={`phoenix-shell phoenix-shell--${resolved}`}>
                    <PhoenixHeaderBar
                        logo={logo}
                        tenantName={tenantName}
                        apps={apps}
                        snapshot={snapshot}
                        resolved={resolved}
                        themeTriggerLabel={themeTriggerLabel}
                        viewportWidth={viewportWidth}
                        isCompactUtilities={isCompactUtilities}
                        openPopover={openPopover}
                        appsPanelId={appsPanelId}
                        themePanelId={themePanelId}
                        utilitiesPanelId={utilitiesPanelId}
                        controlsRef={controlsRef}
                        triggerRefs={triggerRefs}
                        togglePopover={togglePopover}
                        onControlsKeyDown={onControlsKeyDown}
                        onControlsBlur={onControlsBlur}
                        onMenuKeyDown={onMenuKeyDown}
                        onModesKeyDown={onModesKeyDown}
                        select={select}
                    />
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
            </HeaderSlotsProvider>
        </EnsureIntl>
    );
}

type HeaderBarProps = {
    logo: string | null | undefined;
    tenantName: string;
    apps: AppTile[];
    snapshot: ShellSnapshot;
    resolved: ResolvedMode;
    themeTriggerLabel: string;
    viewportWidth: number;
    isCompactUtilities: boolean;
    openPopover: PopoverId | null;
    appsPanelId: string;
    themePanelId: string;
    utilitiesPanelId: string;
    controlsRef: React.RefObject<HTMLDivElement | null>;
    triggerRefs: React.MutableRefObject<Record<PopoverId, HTMLButtonElement | null>>;
    togglePopover: (id: PopoverId) => void;
    onControlsKeyDown: (event: React.KeyboardEvent<HTMLDivElement>) => void;
    onControlsBlur: (event: React.FocusEvent<HTMLDivElement>) => void;
    onMenuKeyDown: (event: React.KeyboardEvent<HTMLUListElement>) => void;
    onModesKeyDown: (event: React.KeyboardEvent<HTMLDivElement>) => void;
    select: (value: ColorMode) => void;
};

function PhoenixHeaderBar({
    logo,
    tenantName,
    apps,
    snapshot,
    resolved,
    themeTriggerLabel,
    viewportWidth,
    isCompactUtilities,
    openPopover,
    appsPanelId,
    themePanelId,
    utilitiesPanelId,
    controlsRef,
    triggerRefs,
    togglePopover,
    onControlsKeyDown,
    onControlsBlur,
    onMenuKeyDown,
    onModesKeyDown,
    select,
}: HeaderBarProps) {
    const headerRef = useRef<HTMLElement | null>(null);
    const [overflowUtilities, setOverflowUtilities] = useState(false);
    const [compactChannelTools, setCompactChannelTools] = useCompactChannelTools();
    const hasUtilities = useHasSlotContent('utilities');
    const effectiveCompactUtilities = isCompactUtilities || overflowUtilities;
    const showOverflowUtilities = effectiveCompactUtilities && hasUtilities;

    useEffect(() => {
        setOverflowUtilities(false);
        setCompactChannelTools(false);
    }, [viewportWidth, setCompactChannelTools]);

    useLayoutEffect(() => {
        const el = headerRef.current;
        if (!el || el.clientWidth === 0) {
            return;
        }
        const channelSlot = el.querySelector<HTMLElement>('[data-header-slot="channel"]');
        const channelOverflows = Boolean(channelSlot && channelSlot.scrollWidth > channelSlot.clientWidth + 1);
        if (el.scrollWidth > el.clientWidth + 1 || channelOverflows) {
            if (!effectiveCompactUtilities) {
                setOverflowUtilities(true);
            } else if (!compactChannelTools) {
                setCompactChannelTools(true);
            }
        }
    });

    return (
        <header
            ref={headerRef}
            className='phoenix-shell__header'
            data-testid='phoenix-header'
        >
            <HeaderSlotTarget name='left'/>
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
            <HeaderSlotTarget name='channel'/>
            {!effectiveCompactUtilities && <HeaderSlotTarget name='utilities'/>}
            {/* Delegated Escape/blur handling: the focusable controls inside own the interaction. */}
            {/* eslint-disable-next-line jsx-a11y/no-static-element-interactions */}
            <div
                className='phoenix-shell__controls'
                ref={controlsRef}
                onKeyDown={onControlsKeyDown}
                onBlur={onControlsBlur}
            >
                {showOverflowUtilities && (
                    <div
                        key='utilities'
                        className='phoenix-shell__popover-root'
                    >
                        <WithTooltip
                            title='Tiện ích'
                            disabled={openPopover !== null}
                        >
                            <button
                                className='phoenix-shell__icon-button phoenix-shell__utilities-trigger'
                                ref={(el) => {
                                    triggerRefs.current.utilities = el;
                                }}
                                type='button'
                                aria-label='Tiện ích'
                                aria-haspopup='menu'
                                aria-expanded={openPopover === 'utilities'}
                                aria-controls={openPopover === 'utilities' ? utilitiesPanelId : undefined}
                                onClick={() => togglePopover('utilities')}
                            ><OverflowIcon/></button>
                        </WithTooltip>
                        {openPopover === 'utilities' && (
                            <div
                                id={utilitiesPanelId}
                                className='phoenix-shell__popover phoenix-shell__utilities-panel'
                                role='group'
                                aria-label='Tiện ích'
                                data-phoenix-popover='utilities'
                                tabIndex={-1}
                            >
                                <HeaderSlotTarget name='utilities'/>
                            </div>
                        )}
                    </div>
                )}
                <div
                    key='apps'
                    className='phoenix-shell__popover-root'
                >
                    <WithTooltip
                        title='Ứng dụng'
                        disabled={openPopover !== null}
                    >
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
                    </WithTooltip>
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
                <div
                    key='theme'
                    className='phoenix-shell__popover-root'
                >
                    <WithTooltip
                        title={themeTriggerLabel}
                        disabled={openPopover !== null}
                    >
                        <button
                            className='phoenix-shell__icon-button phoenix-shell__theme-trigger'
                            ref={(el) => {
                                triggerRefs.current.theme = el;
                            }}
                            type='button'
                            aria-label={themeTriggerLabel}
                            aria-haspopup='menu'
                            aria-expanded={openPopover === 'theme'}
                            aria-controls={openPopover === 'theme' ? themePanelId : undefined}
                            onClick={() => togglePopover('theme')}
                        ><ThemeIcon resolved={resolved}/></button>
                    </WithTooltip>
                    {openPopover === 'theme' && (
                        <div
                            id={themePanelId}
                            className='phoenix-shell__popover phoenix-shell__theme-menu'
                            role='menu'
                            aria-label='Giao diện'
                            data-phoenix-popover='theme'
                            tabIndex={-1}
                            onKeyDown={onModesKeyDown}
                        >
                            {MODES.map((m) => {
                                const checked = snapshot.preference.mode === m.value;
                                return (
                                    <button
                                        key={m.value}
                                        className='phoenix-shell__mode'
                                        type='button'
                                        role='menuitemradio'
                                        tabIndex={checked ? 0 : -1}
                                        aria-checked={checked}
                                        onClick={() => select(m.value)}
                                    >
                                        <ModeOptionIcon mode={m.value}/>
                                        <span>{m.label}</span>
                                    </button>
                                );
                            })}
                        </div>
                    )}
                </div>
            </div>
        </header>
    );
}
