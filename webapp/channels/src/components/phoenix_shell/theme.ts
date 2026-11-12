// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

import type {ColorMode, ShellSnapshot} from './client';
import {Preferences} from 'mattermost-redux/constants';
import {applyTheme as applyNativeTheme} from 'utils/utils';

// The Gateway snapshot is the only theme authority. The mode is mapped once per snapshot/preference
// change and on an OS scheme change event; there is no timer and no native theme menu.

export type ResolvedMode = 'light' | 'dark';

export function resolveMode(mode: ColorMode, prefersDark: boolean): ResolvedMode {
    if (mode === 'system') {
        return prefersDark ? 'dark' : 'light';
    }
    return mode;
}

const HEX = /^#[0-9a-fA-F]{3,8}$/;

function flatten(prefix: string, value: unknown, out: Record<string, string>) {
    if (typeof value === 'string' && HEX.test(value)) {
        out[prefix] = value;
        return;
    }
    if (value && typeof value === 'object') {
        for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
            flatten(prefix + '-' + k, v, out);
        }
    }
}

// Only validated hex colors become CSS variables; anything else in the snapshot is ignored.
export function themeVariables(snapshot: ShellSnapshot, resolved: ResolvedMode): Record<string, string> {
    const vars: Record<string, string> = {};
    flatten('--phoenix-color', snapshot.theme.color[resolved], vars);
    return vars;
}

export function applyTheme(root: HTMLElement, snapshot: ShellSnapshot, mode: ColorMode, prefersDark: boolean) {
    const resolved = resolveMode(mode, prefersDark);
    clearTheme(root);
    for (const [k, v] of Object.entries(themeVariables(snapshot, resolved))) {
        root.style.setProperty(k, v);
    }
    root.setAttribute('data-phoenix-mode', resolved);
    root.setAttribute('data-phoenix-density', snapshot.preference.density);
    const colors = snapshot.theme.color[resolved];
    const mapping: Record<string, string> = {
        centerChannelBg: 'background', centerChannelColor: 'text',
        sidebarBg: 'navBackground', sidebarText: 'navText', sidebarUnreadText: 'navText',
        sidebarTextHoverBg: 'surfaceMuted', sidebarTextActiveBorder: 'primary',
        sidebarTextActiveColor: 'primaryForeground', sidebarHeaderBg: 'headerBackground',
        sidebarHeaderTextColor: 'headerText', onlineIndicator: 'success', awayIndicator: 'warning',
        dndIndicator: 'danger', mentionColor: 'primaryForeground',
        mentionHighlightBg: 'primarySubtle', mentionHighlightLink: 'link',
        newMessageSeparator: 'primary', linkColor: 'link', buttonBg: 'primary',
        buttonColor: 'primaryForeground', errorTextColor: 'danger',
        sidebarTeamBarBg: 'navBackground', mentionBg: 'primary',
    };
    const native = {...Preferences.THEMES.denim};
    for (const [target, source] of Object.entries(mapping)) {
        const value = colors[source];
        if (typeof value === 'string' && HEX.test(value)) {
            (native as unknown as Record<string, unknown>)[target] = value;
        }
    }
    // Use the native lifecycle so modal, composer, sidebar and portaled surfaces agree.
    applyNativeTheme(native);
    return resolved;
}

export function clearTheme(root: HTMLElement) {
    for (const name of Array.from(root.style)) {
        if (name.startsWith('--phoenix-')) {
            root.style.removeProperty(name);
        }
    }
    root.removeAttribute('data-phoenix-mode');
    root.removeAttribute('data-phoenix-density');
}
