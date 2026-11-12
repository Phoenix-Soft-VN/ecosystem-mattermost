// Copyright (c) Phoenix-Soft-VN. Licensed under the surrounding fork terms.
// Internal ownership only: never grants access or replaces the server authority.
let owners = 0;
export const NATIVE_SESSION_CLEARED = 'phoenix-native-session-cleared';
export function registerPhoenixSessionLifecycle() {
    owners += 1;
    return () => {owners = Math.max(0, owners - 1);};
}
export function phoenixOwnsSessionLifecycle() {return owners > 0;}
