// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

// Browser client of the native Phoenix BFF. It never sends tenant, sub or sid: the server derives
// them from the immutable session binding. Nothing is persisted in browser storage.

export type ColorMode = 'system' | 'light' | 'dark';
export type Density = 'comfortable' | 'compact';

export type ShellSnapshot = {
    schemaVersion?: number;
    appCode: string;
    tenant: null | {id: string; displayName: string; logoLightUrl?: string | null; logoDarkUrl?: string | null};
    themeRevision: number;
    resolvedThemeHash: string;
    theme: {color: {light: Record<string, unknown>; dark: Record<string, unknown>}};
    footer: {
        presentation: 'compact' | 'full' | 'public' | 'empty';
        operator: {name?: string; displayName?: string; contacts?: Array<{label?: string; value?: string}>};
        tenant: null | {name?: string; displayName?: string; contacts?: Array<{label?: string; value?: string}>};
        links: Array<{label: string; url: string}>;
    };
    account?: null | {displayName: string | null; avatarUrl: string | null};
    preference: {mode: ColorMode; density: Density};
};

export type AppTile = {appCode: string; label: string; description: string; iconKey: string; state: 'ready' | 'provisioning' | 'disabled'; launchUrl?: string};
export type Navigation = {context: string; apps: AppTile[]; currentApp: string; expiry: string | null};

export type ShellErrorKind = 'invalid' | 'denied' | 'pending' | 'unavailable' | 'conflict' | 'unknown';

export class ShellError extends Error {
    kind: ShellErrorKind;
    status: number;
    code: string;
    constructor(status: number, code: string) {
        super(code);
        this.status = status;
        this.code = code;
        this.kind = classify(status, code);
    }
}

// Design §6.7: error status -> customer copy kind.
function classify(status: number, code: string): ShellErrorKind {
    if (code === 'CHAT_PROVISIONING_PENDING' || status === 409 && code !== 'VERSION_CONFLICT') {
        return 'pending';
    }
    if (code === 'VERSION_CONFLICT') {
        return 'conflict';
    }
    if (status === 401) {
        return 'invalid';
    }
    if (status === 403) {
        return 'denied';
    }
    if (status === 503 || status === 502 || status === 504) {
        return 'unavailable';
    }
    return 'unknown';
}

export const ERROR_COPY: Record<ShellErrorKind, string> = {
    invalid: 'Phiên đăng nhập không hợp lệ. Vui lòng đăng nhập lại.',
    denied: 'Bạn chưa được cấp quyền sử dụng Chat.',
    pending: 'Tài khoản Chat đang được chuẩn bị. Vui lòng thử lại sau.',
    unavailable: 'Chưa thể xác minh quyền truy cập. Vui lòng thử lại sau.',
    conflict: 'Cài đặt đã được thay đổi ở nơi khác. Vui lòng tải lại.',
    unknown: 'Đã có lỗi xảy ra. Vui lòng thử lại sau.',
};

function csrfToken(): string {
    const m = (/(?:^|;\s*)MMCSRF=([^;]+)/).exec(document.cookie);
    return m ? decodeURIComponent(m[1]) : '';
}

async function call<T>(url: string, init: RequestInit = {}): Promise<{body: T | null; etag: string | null; logoutUrl: string | null; status: number}> {
    const res = await fetch(url, {credentials: 'same-origin', cache: 'no-store', ...init});
    if (res.status === 304) {
        return {body: null, etag: res.headers.get('ETag'), logoutUrl: res.headers.get('X-Phoenix-Logout-Url'), status: 304};
    }
    if (!res.ok) {
        let code = '';
        try {
            const j = await res.json();
            code = j.code || j.error || '';
        } catch {
            // body is not JSON; the status alone classifies the failure
        }
        throw new ShellError(res.status, code);
    }
    return {body: await res.json() as T, etag: res.headers.get('ETag'), logoutUrl: res.headers.get('X-Phoenix-Logout-Url'), status: res.status};
}

export async function fetchShellContext() {
    return call<ShellSnapshot>('/phoenix/shell-context');
}

export async function fetchNavigation(etag?: string | null) {
    return call<Navigation>('/phoenix/shell-navigation', etag ? {headers: {'If-None-Match': etag}} : {});
}

export async function patchPreference(pref: {mode?: ColorMode; density?: Density}, version?: string | null) {
    const headers: Record<string, string> = {'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken()};
    if (version) {
        headers['If-Match'] = version;
    }
    return call<{mode: ColorMode; density: Density; version?: number}>('/phoenix/ui-preferences', {method: 'PATCH', headers, body: JSON.stringify(pref)});
}
