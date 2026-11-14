// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

import type {GlobalState} from '@mattermost/types/store';
import type {UserProfile} from '@mattermost/types/users';

import {getUsers} from 'mattermost-redux/selectors/entities/common';
import {getTeammateNameDisplaySetting} from 'mattermost-redux/selectors/entities/preferences';
import {displayUsername} from 'mattermost-redux/utils/user_utils';

// The server only understands @username. When the teammate name display prefers nicknames, the
// @mention autocomplete inserts "@Nickname" into the text box so people see names they know, and
// the text is turned back into "@username" right before it leaves the client.

// Mentions picked from the autocomplete this session, keyed by lower-cased display name. They win
// over a lookup in the profile store, which is how two people with the same nickname stay distinct.
const pickedMentions = new Map<string, string>();

const RESERVED = new Set(['all', 'channel', 'here']);
const CODE = /(```[\s\S]*?```|~~~[\s\S]*?~~~|`[^`\n]*`)/;
const BEFORE_BLOCKED = /[\p{L}\p{N}_.@/-]/u;
const AFTER_BLOCKED = /[\p{L}\p{N}_-]/u;

function normalize(name: string) {
    return name.trim().toLowerCase();
}

// The name shown for a user in the text box. Falls back to the username when the display name is
// empty or would not round-trip (it contains an @).
export function mentionDisplayName(user: UserProfile, teammateNameDisplay: string): string {
    const name = displayUsername(user, teammateNameDisplay, false).trim();
    if (!name || name.includes('@') || RESERVED.has(normalize(name))) {
        return user.username;
    }
    return name;
}

export function rememberPickedMention(displayName: string, username: string) {
    if (displayName && username && normalize(displayName) !== normalize(username)) {
        pickedMentions.set(normalize(displayName), username);
    }
}

export function forgetPickedMentionsForTests() {
    pickedMentions.clear();
}

function buildLookup(state: GlobalState) {
    const profiles = Object.values(getUsers(state));
    const setting = getTeammateNameDisplaySetting(state);
    const usernames = new Set(profiles.map((user) => normalize(user.username)));

    // display name -> username; '' marks a name shared by several people.
    const lookup = new Map<string, string>();
    for (const user of profiles) {
        const key = normalize(mentionDisplayName(user, setting));

        // A name that equals someone's username could be a plain @username; never reinterpret it.
        if (!key || usernames.has(key)) {
            continue;
        }
        const existing = lookup.get(key);
        lookup.set(key, existing !== undefined && existing !== user.username ? '' : user.username);
    }

    for (const [key, username] of pickedMentions) {
        if (usernames.has(normalize(username))) {
            lookup.set(key, username);
        }
    }

    for (const [key, username] of Array.from(lookup)) {
        if (!username) {
            lookup.delete(key);
        }
    }
    return lookup;
}

function replaceInText(text: string, lookup: Map<string, string>, maxLength: number) {
    let out = '';
    let i = 0;
    while (i < text.length) {
        const at = text.indexOf('@', i);
        if (at === -1) {
            out += text.slice(i);
            break;
        }
        out += text.slice(i, at);
        i = at;

        const previous = at > 0 ? text[at - 1] : '';
        let matched = false;
        if (!previous || !BEFORE_BLOCKED.test(previous)) {
            const rest = text.slice(at + 1);
            for (let length = Math.min(maxLength, rest.length); length > 0; length--) {
                const username = lookup.get(normalize(rest.slice(0, length)));
                const next = rest[length];
                if (username && rest.slice(0, length).trim() === rest.slice(0, length) && (!next || !AFTER_BLOCKED.test(next))) {
                    out += '@' + username;
                    i = at + 1 + length;
                    matched = true;
                    break;
                }
            }
        }

        if (!matched) {
            out += '@';
            i = at + 1;
        }
    }
    return out;
}

// Turns "@Nickname" back into "@username". Code spans and fenced code stay untouched, names that
// match several people are left alone unless one was picked from the autocomplete, and text that
// already uses a username is never rewritten.
export function resolveDisplayMentions(message: string, state: GlobalState): string {
    if (!message || !message.includes('@')) {
        return message;
    }

    const lookup = buildLookup(state);
    if (lookup.size === 0) {
        return message;
    }

    let maxLength = 0;
    for (const key of lookup.keys()) {
        maxLength = Math.max(maxLength, key.length);
    }

    return message.split(CODE).map((part, index) => (index % 2 === 1 ? part : replaceInText(part, lookup, maxLength))).join('');
}
