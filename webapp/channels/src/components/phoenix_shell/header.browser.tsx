// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.

/* eslint-disable formatjs/no-literal-string-in-jsx, react/no-multi-comp -- fixture content, not product copy */

// Browser entry of the header fixture (see header.browser.cjs). It renders the real PhoenixShell,
// GlobalHeader, ChannelHeader and MobileChannelHeader with the real client; only the network and
// modal/store boundaries are replaced by in-page doubles.
// Not part of the product bundle and not authenticated production evidence.

import '@mattermost/compass-icons/css/compass-icons.css';
import React, {useEffect} from 'react';
import {createRoot} from 'react-dom/client';
import {IntlProvider} from 'react-intl';
import {Provider, useDispatch, useSelector} from 'react-redux';
import {MemoryRouter} from 'react-router-dom';
import {legacy_createStore as createStore} from 'redux';

import entitiesReducer from 'mattermost-redux/reducers/entities';

import ChannelHeader from 'components/channel_header';
import GlobalHeader from 'components/global_header/global_header';
import MobileChannelHeader from 'components/mobile_channel_header';

import {getIsMobileView} from 'selectors/views/browser';
import {WindowSizes} from 'utils/constants';

import type {ColorMode, ShellSnapshot} from './client';
import {HeaderSlotContent} from './header_slots';
import PhoenixShell from './index';

type Scenario = {
    tenant: string;
    channelName: string;
    channelHeader: string;
    account: string;
    mode: ColorMode;
    apps: Array<{appCode: string; label: string; state: 'ready' | 'provisioning'; launchUrl?: string}>;
    plugins: Array<{id: string; label: string}>;
    logoutUrl: string | null;
    palette: 'plan' | 'gateway';
};

type Call = {method: string; url: string; ifMatch: string | null; body: string | null};
type PluginCall = {kind: string; id: string; channelId: string};

declare global {
    interface Window {
        phoenixFixture?: Partial<Scenario>;
        phoenixCalls?: Call[];
        phoenixPluginCalls?: PluginCall[];
        phoenixDispatched?: Array<{type: string; [key: string]: unknown}>;
        __phoenixFixtureStore?: unknown;
        chatEscapeSeen?: number;
    }
}

const scenario: Scenario = {
    tenant: 'Tenant A',
    channelName: 'General',
    channelHeader: 'Team discussions',
    account: 'A1',
    mode: 'light',
    apps: [
        {appCode: 'tasks', label: 'Tasks', state: 'ready', launchUrl: 'https://tasks.phoenixsoft.vn'},
        {appCode: 'files', label: 'Files pending', state: 'provisioning'},
        {appCode: 'portal', label: 'Portal', state: 'ready', launchUrl: 'https://portal.phoenixsoft.vn'},
    ],
    plugins: [
        {id: 'plug-1', label: 'Jira'},
    ],
    logoutUrl: 'https://auth.phoenixsoft.vn/oauth2/logout',
    palette: 'plan',
    ...window.phoenixFixture,
};

let version = 7;
let mode: ColorMode = scenario.mode;
const calls: Call[] = [];
const pluginCalls: PluginCall[] = [];
const dispatched: Array<{type: string; [key: string]: unknown}> = [];
window.phoenixCalls = calls;
window.phoenixPluginCalls = pluginCalls;
window.phoenixDispatched = dispatched;
window.chatEscapeSeen = 0;

// Token names and values are the Gateway ThemeDefaults ones (textMuted, surfaceMuted, primaryForeground, ...).
// 'plan' keeps the plan's binary surface/text pair; 'gateway' is the shipped default palette untouched.
const gateway = {
    light: {
        primary: '#056B55',
        primaryForeground: '#FFFFFF',
        surface: '#FFFFFF',
        surfaceMuted: '#EDF3F0',
        headerBackground: '#FFFFFF',
        headerText: '#17211E',
        text: '#17211E',
        textMuted: '#5D6D67',
        border: '#738A80',
        focus: '#056B55',
    },
    dark: {
        primary: '#6CD7BC',
        primaryForeground: '#06251D',
        surface: '#18251F',
        surfaceMuted: '#22332B',
        headerBackground: '#18251F',
        headerText: '#EAF3EF',
        text: '#EAF3EF',
        textMuted: '#B2C5BC',
        border: '#698176',
        focus: '#6CD7BC',
    },
};

const theme = scenario.palette === 'gateway' ? gateway : {
    light: {...gateway.light, surface: '#ffffff', text: '#111111', headerBackground: '#ffffff', headerText: '#111111'},
    dark: {...gateway.dark, surface: '#111111', text: '#ffffff', headerBackground: '#111111', headerText: '#ffffff'},
};

function snapshot(): ShellSnapshot {
    return {
        appCode: 'chat',
        tenant: {id: 'tenant-a', displayName: scenario.tenant},
        themeRevision: 1,
        resolvedThemeHash: 'hash-a',
        theme: {color: theme},
        footer: {presentation: 'compact', operator: {displayName: 'Operator'}, tenant: {displayName: scenario.tenant}, links: []},
        account: {displayName: scenario.account, avatarUrl: null},
        preference: {mode, density: 'comfortable'},
    };
}

function reply(body: unknown, headers: Record<string, string> = {}) {
    return new Response(JSON.stringify(body), {status: 200, headers: {'Content-Type': 'application/json', ...headers}});
}

window.fetch = async (input, init) => {
    const url = String(input);
    const headers = new Headers(init?.headers);
    calls.push({method: init?.method ?? 'GET', url, ifMatch: headers.get('If-Match'), body: typeof init?.body === 'string' ? init.body : null});
    if (url === '/phoenix/shell-context') {
        const extra: Record<string, string> = {ETag: `"${version}"`};
        if (scenario.logoutUrl) {
            extra['X-Phoenix-Logout-Url'] = scenario.logoutUrl;
        }
        return reply(snapshot(), extra);
    }
    if (url === '/phoenix/shell-navigation') {
        const apps = scenario.apps.map((a) => ({description: '', iconKey: a.appCode, ...a}));
        return reply({context: 'tenant', currentApp: 'chat', expiry: null, apps}, {ETag: '"nav-1"'});
    }
    if (url === '/phoenix/ui-preferences' && init?.method === 'PATCH') {
        mode = JSON.parse(String(init.body)).mode;
        version += 1;
        return reply({mode, density: 'comfortable', version}, {ETag: `"${version}"`});
    }
    if (url.includes('/api/v4/')) {
        return reply({});
    }
    return new Response('{}', {status: 404});
};

// Chat shortcuts live on the document: an Escape that reaches it was not consumed by the header.
document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
        window.chatEscapeSeen = (window.chatEscapeSeen ?? 0) + 1;
    }
});

const USER_ID = 'user-1';
const TEAM_ID = 'team-1';
const CHANNEL_ID = 'channel-1';

function buildInitialState() {
    const isMobile = window.innerWidth <= 768;
    const channelObj = {
        id: CHANNEL_ID,
        team_id: TEAM_ID,
        name: 'general',
        display_name: scenario.channelName,
        header: scenario.channelHeader,
        purpose: 'General channel',
        type: 'O',
        delete_at: 0,
        shared: false,
        group_constrained: false,
        props: {},
    };
    const userObj = {
        id: USER_ID,
        username: 'a1',
        first_name: scenario.account,
        last_name: '',
        nickname: scenario.account,
        email: 'a1@phoenixsoft.vn',
        roles: 'system_user',
        locale: 'en',
        timezone: {useAutomaticTimezone: 'true', automaticTimezone: 'Asia/Ho_Chi_Minh', manualTimezone: ''},
        props: {},
        last_picture_update: 0,
    };
    const memberObj = {
        channel_id: CHANNEL_ID,
        user_id: USER_ID,
        roles: 'channel_user',
        msg_count: 10,
        mention_count: 0,
        mention_count_root: 0,
        msg_count_root: 10,
        notify_props: {mark_unread: 'all', desktop: 'default', push: 'default', email: 'default'},
    };

    const headerButtons = scenario.plugins.map((p) => ({
        id: p.id,
        pluginId: p.id,
        icon: <i className='icon icon-power-plug-outline' aria-hidden='true'/>,
        tooltipText: `Plugin ${p.label}`,
        dropdownText: `Plugin ${p.label}`,
        action: (ch: {id: string}) => {
            pluginCalls.push({kind: 'channel-button', id: p.id, channelId: ch?.id ?? CHANNEL_ID});
        },
    }));

    const channelMenuItems = scenario.plugins.map((p) => ({
        id: `menu-${p.id}`,
        pluginId: p.id,
        text: `Plugin ${p.label}`,
        action: (channelId: string) => {
            pluginCalls.push({kind: 'channel-menu', id: p.id, channelId: channelId ?? CHANNEL_ID});
        },
    }));

    const mobileHeaderButtons = scenario.plugins.map((p) => ({
        id: `mobile-${p.id}`,
        pluginId: p.id,
        icon: <i className='icon icon-power-plug-outline' aria-hidden='true'/>,
        dropdownText: `Plugin ${p.label}`,
        action: (ch: {id: string}) => {
            pluginCalls.push({kind: 'mobile-plugin', id: p.id, channelId: ch?.id ?? CHANNEL_ID});
        },
    }));

    const baseEntities = entitiesReducer(undefined, {type: '@@INIT'} as any);

    return {
        entities: {
            ...baseEntities,
            general: {
                ...baseEntities.general,
                config: {
                    EnableFileAttachments: 'true',
                    EnableCustomEmoji: 'false',
                    EnableCustomUserStatuses: 'true',
                    EnableUserAccessTokens: 'false',
                    EnableCrossTeamSearch: 'false',
                    BuildEnterpriseReady: 'true',
                    HelpLink: 'https://docs.mattermost.com',
                    ReportAProblemLink: 'https://mattermost.com/report',
                    EnableAskCommunityLink: 'true',
                    SiteName: 'Phoenix Chat',
                },
                license: {
                    IsLicensed: 'true',
                    Cloud: 'false',
                    LDAPGroups: 'false',
                },
            },
            users: {
                ...baseEntities.users,
                currentUserId: USER_ID,
                profiles: {[USER_ID]: userObj},
                statuses: {[USER_ID]: 'online'},
            },
            teams: {
                ...baseEntities.teams,
                currentTeamId: TEAM_ID,
                teams: {
                    [TEAM_ID]: {
                        id: TEAM_ID,
                        name: 'team-a',
                        display_name: scenario.tenant,
                        description: '',
                        type: 'O',
                        delete_at: 0,
                    },
                },
                myMembers: {
                    [TEAM_ID]: {
                        team_id: TEAM_ID,
                        user_id: USER_ID,
                        roles: 'team_user',
                        msg_count: 0,
                        mention_count: 0,
                        mention_count_root: 0,
                        msg_count_root: 0,
                    },
                },
            },
            channels: {
                ...baseEntities.channels,
                currentChannelId: CHANNEL_ID,
                channels: {[CHANNEL_ID]: channelObj},
                channelsInTeam: {[TEAM_ID]: new Set([CHANNEL_ID])},
                myMembers: {[CHANNEL_ID]: memberObj},
                stats: {
                    [CHANNEL_ID]: {
                        channel_id: CHANNEL_ID,
                        member_count: 5,
                        guest_count: 0,
                        pinnedpost_count: 2,
                        files_count: 1,
                    },
                },
                messageCounts: {
                    [CHANNEL_ID]: {total: 10, root: 10},
                },
                roles: {[CHANNEL_ID]: new Set(['channel_user'])},
            },
            roles: {
                roles: {
                    system_user: {
                        permissions: [
                            'view_team',
                            'read_channel',
                            'manage_public_channel_properties',
                            'manage_public_channel_members',
                        ],
                    },
                    team_user: {
                        permissions: [
                            'view_team',
                            'read_channel',
                            'manage_public_channel_properties',
                            'manage_public_channel_members',
                        ],
                    },
                    channel_user: {
                        permissions: [
                            'read_channel',
                            'manage_public_channel_properties',
                            'manage_public_channel_members',
                        ],
                    },
                },
            },
        },
        views: {
            browser: {
                windowSize: isMobile ? WindowSizes.MOBILE_VIEW : WindowSizes.DESKTOP_VIEW,
                focused: true,
            },
            productMenu: {
                switcherOpen: false,
            },
            rhs: {
                rhsState: null,
                isSidebarOpen: false,
                isSidebarExpanded: false,
                isMenuOpen: false,
                searchTerms: '',
                searchTeam: TEAM_ID,
                searchType: '',
            },
            lhs: {
                isOpen: false,
            },
            modals: {
                modalState: {},
            },
            channelSidebar: {
                unreadFilterEnabled: false,
            },
            marketplace: {},
            notice: {
                hasBeenDismissed: {},
            },
            threads: {},
        },
        plugins: {
            components: {
                Product: [
                    {
                        id: 'boards',
                        pluginId: 'focalboard',
                        switcherIcon: 'product-boards',
                        switcherText: 'Boards',
                        baseURL: '/boards',
                        switcherLinkURL: '/boards',
                        mainComponent: () => null,
                        headerCentreComponent: () => null,
                        headerRightComponent: () => null,
                    },
                ],
                ProductSwitcherMenuItem: [],
                ChannelHeaderButton: headerButtons,
                ChannelHeader: channelMenuItems,
                MobileChannelHeaderButton: mobileHeaderButtons,
                ChannelHeaderIcon: [],
                CallButton: [],
                SearchButtons: [],
                UserGuideDropdown: [],
            },
            plugins: {},
        },
        requests: {
            channels: {
                getChannels: {status: 'not_started', error: null},
            },
        },
        websocket: {
            connected: true,
        },
    };
}

function fixtureReducer(state = buildInitialState(), action: any) {
    if (action && typeof action.type === 'string' && !action.type.startsWith('@@redux/')) {
        dispatched.push(action);
    }
    switch (action?.type) {
    case 'PRODUCT_SWITCHER_OPEN':
    case 'SET_PRODUCT_SWITCHER_OPEN':
        return {
            ...state,
            views: {
                ...state.views,
                productMenu: {switcherOpen: Boolean(action.open)},
            },
        };
    case 'BROWSER_WINDOW_RESIZED':
        return {
            ...state,
            views: {
                ...state.views,
                browser: {...state.views.browser, windowSize: action.data ?? action.windowSize ?? state.views.browser.windowSize},
            },
        };
    case 'UPDATE_RHS_STATE':
        return {
            ...state,
            views: {
                ...state.views,
                rhs: {
                    ...state.views.rhs,
                    rhsState: action.state ?? null,
                    isSidebarOpen: Boolean(action.state),
                },
            },
        };
    case 'TOGGLE_RHS_MENU':
        return {
            ...state,
            views: {
                ...state.views,
                rhs: {
                    ...state.views.rhs,
                    isMenuOpen: !state.views.rhs.isMenuOpen,
                },
            },
        };
    case 'CLOSE_RHS':
    case 'CLOSE_RHS_MENU':
        return {
            ...state,
            views: {
                ...state.views,
                rhs: {
                    ...state.views.rhs,
                    rhsState: null,
                    isSidebarOpen: false,
                    isMenuOpen: false,
                },
            },
        };
    case 'TOGGLE_LHS':
        return {
            ...state,
            views: {
                ...state.views,
                lhs: {isOpen: !state.views.lhs.isOpen},
            },
        };
    case 'CLOSE_LHS':
        return {
            ...state,
            views: {
                ...state.views,
                lhs: {isOpen: false},
            },
        };
    default:
        return state;
    }
}

const store = createStore(fixtureReducer as any);
const originalDispatch = store.dispatch;
store.dispatch = ((action: any) => {
    if (typeof action === 'function') {
        return action(store.dispatch, store.getState);
    }
    return originalDispatch(action);
}) as typeof store.dispatch;
window.__phoenixFixtureStore = store;

function FixtureWorkspace() {
    const dispatch = useDispatch();
    const isMobileView = useSelector(getIsMobileView);

    useEffect(() => {
        const onResize = () => {
            dispatch({
                type: 'BROWSER_WINDOW_RESIZED',
                data: window.innerWidth <= 768 ? WindowSizes.MOBILE_VIEW : WindowSizes.DESKTOP_VIEW,
            });
        };
        window.addEventListener('resize', onResize);
        return () => window.removeEventListener('resize', onResize);
    }, [dispatch]);

    return (
        <div className='phoenix-shell__app'>
            <GlobalHeader/>
            <div className='main-wrapper'>
                <div
                    id='channel_view'
                    className='channel-view'
                >
                    <div className='inner-wrap channel__wrap'>
                        {isMobileView && (
                            <HeaderSlotContent name='channel'>
                                <MobileChannelHeader/>
                            </HeaderSlotContent>
                        )}
                        <div className='row main'>
                            <div
                                id='fixture-chat'
                                className='app__content'
                                style={{display: 'flex', flexDirection: 'column', justifyContent: 'space-between'}}
                            >
                                <HeaderSlotContent name='channel'>
                                    <ChannelHeader/>
                                </HeaderSlotContent>
                                <div id='fixture-content'>{'Chat content'}</div>
                                <input
                                    id='fixture-composer'
                                    className='form-control'
                                    aria-label='Composer'
                                />
                            </div>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    );
}

document.body.classList.add('app__body');
createRoot(document.getElementById('root')!).render(
    <Provider store={store}>
        <IntlProvider
            locale='en'
            defaultLocale='en'
            messages={{}}
        >
            <MemoryRouter initialEntries={['/team-a/channels/general']}>
                <PhoenixShell>
                    <FixtureWorkspace/>
                </PhoenixShell>
            </MemoryRouter>
        </IntlProvider>
    </Provider>,
);
