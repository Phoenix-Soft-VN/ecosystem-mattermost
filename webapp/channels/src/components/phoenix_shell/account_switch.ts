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
let currentTenant: string | null = null;
const listeners = new Set<() => void>();

export function setAccountSwitchUrl(url: string | null | undefined, tenantAlias?: string | null) {
    const next = accountSwitchLink(url, 'https://x.invalid') ? (url as string) : null;
    const nextTenant = next && tenantAlias && (/^[a-z0-9-]{1,63}$/).test(tenantAlias) ? tenantAlias : null;
    if (next === current && nextTenant === currentTenant) {
        return;
    }
    current = next;
    currentTenant = nextTenant;
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
export function accountSwitchLink(url: string | null | undefined, origin: string, action: 'switch' | 'add' = 'switch', tenantAlias?: string | null): string | null {
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
    let destination = url;
    if (action === 'add') {
        const target = new URL(url);
        if (target.pathname !== '/accounts' || target.search || target.hash || target.username || target.password) {
            return null;
        }
        destination = new URL('/accounts/add-start', target).toString();
    }
    return `${destination}?return_to=${encodeURIComponent(`${origin}/`)}` + (action === 'add' && tenantAlias && (/^[a-z0-9-]{1,63}$/).test(tenantAlias) ? `&tenant_alias=${encodeURIComponent(tenantAlias)}` : '');
}

export function useAccountSwitchHref(action: 'switch' | 'add' = 'switch'): string | null {
    return useSyncExternalStore(subscribe, () => accountSwitchLink(current, window.location.origin, action, currentTenant), () => null);
}
