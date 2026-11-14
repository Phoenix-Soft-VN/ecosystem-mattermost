// Copyright (c) Phoenix-Soft-VN. Licensed under the same terms as the surrounding fork (see LICENSE.txt).

import type {GlobalState} from '@mattermost/types/store';
import type {UserProfile} from '@mattermost/types/users';

import {forgetPickedMentionsForTests, mentionDisplayName, rememberPickedMention, resolveDisplayMentions} from './mention_display';

function user(username: string, nickname: string, first = '', last = ''): UserProfile {
    return {id: 'id-' + username, username, nickname, first_name: first, last_name: last} as UserProfile;
}

function stateWith(users: UserProfile[], display = 'nickname_full_name') {
    return {
        entities: {
            general: {config: {TeammateNameDisplay: display}, license: {}},
            preferences: {myPreferences: {}},
            users: {profiles: Object.fromEntries(users.map((u) => [u.id, u]))},
        },
    } as unknown as GlobalState;
}

describe('resolveDisplayMentions', () => {
    const bob = user('u05e97796959c98fbb279ec87', 'Bob Test');
    const carol = user('ucarol', 'Carol Test');
    const state = stateWith([bob, carol]);

    beforeEach(() => forgetPickedMentionsForTests());

    test('turns @nickname back into @username', () => {
        expect(resolveDisplayMentions('hi @Bob Test, meet @Carol Test.', state)).toBe('hi @u05e97796959c98fbb279ec87, meet @ucarol.');
    });

    test('is case-insensitive and handles the end of the message', () => {
        expect(resolveDisplayMentions('@bob test', state)).toBe('@u05e97796959c98fbb279ec87');
    });

    test('leaves existing @username mentions and special mentions alone', () => {
        const text = '@ucarol @all @channel @here @someone';
        expect(resolveDisplayMentions(text, state)).toBe(text);
    });

    test('does not match inside a longer word or after other characters', () => {
        expect(resolveDisplayMentions('@Bob Testing', state)).toBe('@Bob Testing');
        expect(resolveDisplayMentions('mail me at x@Bob Test', state)).toBe('mail me at x@Bob Test');
    });

    test('keeps code spans and fenced code untouched', () => {
        const text = 'use `@Bob Test` or\n```\n@Carol Test\n```\nbut @Bob Test';
        expect(resolveDisplayMentions(text, state)).toBe('use `@Bob Test` or\n```\n@Carol Test\n```\nbut @u05e97796959c98fbb279ec87');
    });

    test('prefers the longest matching name', () => {
        const long = user('ulong', 'Bob Test Junior');
        expect(resolveDisplayMentions('@Bob Test Junior and @Bob Test', stateWith([bob, long]))).toBe('@ulong and @u05e97796959c98fbb279ec87');
    });

    test('leaves a name shared by several people alone unless one was picked', () => {
        const twin = user('utwin', 'Bob Test');
        const ambiguous = stateWith([bob, twin]);
        expect(resolveDisplayMentions('@Bob Test', ambiguous)).toBe('@Bob Test');

        rememberPickedMention('Bob Test', 'utwin');
        expect(resolveDisplayMentions('@Bob Test', ambiguous)).toBe('@utwin');
    });

    test('never reinterprets a name that is also somebody\'s username', () => {
        const alice = user('alice', 'Alice');
        const decoy = user('udecoy', 'alice');
        expect(resolveDisplayMentions('@alice', stateWith([alice, decoy]))).toBe('@alice');
    });

    test('ignores picked mentions whose user is not known any more', () => {
        rememberPickedMention('Ghost', 'ughost');
        expect(resolveDisplayMentions('@Ghost', state)).toBe('@Ghost');
    });

    test('does nothing when the display setting shows usernames', () => {
        expect(resolveDisplayMentions('@Bob Test', stateWith([bob], 'username'))).toBe('@Bob Test');
    });
});

describe('mentionDisplayName', () => {
    test('uses the nickname, then the full name, then the username', () => {
        expect(mentionDisplayName(user('u1', 'Nick', 'First', 'Last'), 'nickname_full_name')).toBe('Nick');
        expect(mentionDisplayName(user('u2', '', 'First', 'Last'), 'nickname_full_name')).toBe('First Last');
        expect(mentionDisplayName(user('u3', '', '', ''), 'nickname_full_name')).toBe('u3');
        expect(mentionDisplayName(user('u4', 'Nick'), 'username')).toBe('u4');
    });

    test('falls back to the username for names that cannot round-trip', () => {
        expect(mentionDisplayName(user('u5', 'a@b'), 'nickname_full_name')).toBe('u5');
        expect(mentionDisplayName(user('u6', 'All'), 'nickname_full_name')).toBe('u6');
    });
});
