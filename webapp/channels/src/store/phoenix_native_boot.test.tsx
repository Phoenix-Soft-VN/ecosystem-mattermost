// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {UserTypes} from 'mattermost-redux/action_types';

import configureStore from './index';
import {registerPhoenixSessionLifecycle,NATIVE_SESSION_CLEARED} from 'components/phoenix_shell/session_lifecycle';
import * as cookies from 'actions/views/cookie';

// Override the global mock in src/tests/redux-persist_mock.ts because we need to call the callback passed to persistStore
jest.mock('redux-persist', () => {
    const {combineReducers} = require('redux');
    const real = jest.requireActual('redux-persist');

    return {
        ...real,
        createTransform: () => {
            return {};
        },

        persistReducer: jest.fn().mockImplementation((config, reducers) => reducers),
        persistCombineReducers: (persistConfig: any, reducers: any) => combineReducers(reducers),
        persistStore: (store: unknown, persistorOptions: unknown, callback: () => void) => {
            setTimeout(callback);

            return {
                pause: () => {},
                purge: () => Promise.resolve(),
                resume: () => {},
            };
        },
    };
});

describe('Phoenix native persistence ownership',()=>{
 beforeEach(()=>jest.useFakeTimers());afterEach(()=>jest.useRealTimers());
    // Mutation: native purge clears cookies/navigates instead of preserving the boundary.
    test('Phoenix native purge notifies the mounted boundary without native cookie teardown', async () => {
        const release=registerPhoenixSessionLifecycle();
        const cleared=jest.spyOn(cookies,'clearUserCookie');
        const observed=jest.fn();window.addEventListener(NATIVE_SESSION_CLEARED,observed);
        try {
            const store=configureStore();
            await jest.runAllTimersAsync();
            store.dispatch({type:UserTypes.LOGOUT_SUCCESS,data:{}});
            await jest.runAllTimersAsync();
            expect(cleared).not.toHaveBeenCalled();
            expect(observed).toHaveBeenCalledTimes(1);
            expect(store.getState().entities.users.currentUserId).toBe('');
        } finally {release();cleared.mockRestore();window.removeEventListener(NATIVE_SESSION_CLEARED,observed);}
    });

});
