// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.

import {InitialLoadingScreenClass} from './initial_loading_screen_class';

jest.mock('@mattermost/shared/utils/user_agent', () => ({isDesktopApp: () => false}));
jest.mock('utils/performance_telemetry', () => ({Measure: {SplashScreen: 'splash'}, measureAndReport: jest.fn()}));

describe('initial loading screen error recovery', () => {
    beforeEach(() => {
        jest.useFakeTimers();
        document.body.innerHTML = '<link id="initialLoadingScreenCSS"><div id="initialPageLoadingScreen"><div id="initialPageLoadingAnimation"></div></div><a href="/phoenix/auth/login">Sign in</a>';
    });

    afterEach(() => {
        jest.clearAllTimers();
        jest.useRealTimers();
        document.body.innerHTML = '';
    });

    test('removes the blocking overlay immediately without an animationend event', () => {
        const loader = new InitialLoadingScreenClass();
        loader.stop('phoenix-authority', true);
        expect(document.getElementById('initialPageLoadingScreen')).toBeNull();
        expect(document.getElementById('initialPageLoadingAnimation')).toBeNull();
        expect(document.getElementById('initialLoadingScreenCSS')).toBeNull();
        expect(document.querySelector('a')?.textContent).toBe('Sign in');
        expect(() => loader.stop('phoenix-authority', true)).not.toThrow();
    });

    test('a pending animated stop cannot recreate the overlay after immediate recovery', () => {
        const loader = new InitialLoadingScreenClass();
        loader.stop('normal-page');
        expect(document.getElementById('initialPageLoadingScreen')).not.toBeNull();
        loader.stop('phoenix-authority', true);
        jest.runAllTimers();
        expect(document.getElementById('initialPageLoadingScreen')).toBeNull();
    });
});
