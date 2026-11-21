// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSyncExternalStore} from 'react';

// Same-tenant account switch (account-switch contract, P4). The Gateway hands
// out `accountSwitchUrl` (absolute auth-host /accounts, no query) inside the
// shell navigation response the Phoenix shell already fetches. The native
// user account menu lives outside the shell component tree, so the shell
// publishes that single value here and the menu item subscribes to it.
// null/absent hides the entry.

let current: string | null = null;
const listeners = new Set<() => void>();

export function setAccountSwitchUrl(url: string | null | undefined) {
    const next = accountSwitchLink(url, 'https://x.invalid') ? (url as string) : null;
    if (next === current) {
        return;
    }
    current = next;
    listeners.forEach((l) => l());
}

function subscribe(listener: () => void) {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
}

// Full-page link to the account chooser carrying this origin as return_to
// (validated by the auth host against its own allowlist). Only absolute
// http(s) URLs qualify.
export function accountSwitchLink(url: string | null | undefined, origin: string): string | null {
    if (!url) {
        return null;
    }
    try {
        const parsed = new URL(url);
        if (parsed.protocol !== 'https:' && parsed.protocol !== 'http:') {
            return null;
        }
    } catch {
        return null;
    }
    return `${url}?return_to=${encodeURIComponent(`${origin}/`)}`;
}

export function useAccountSwitchHref(): string | null {
    const url = useSyncExternalStore(subscribe, () => current, () => null);
    return accountSwitchLink(url, window.location.origin);
}
