// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.

import React from 'react';
import {act, fireEvent, screen, waitFor, within} from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import * as userAgent from '@mattermost/shared/utils/user_agent';
import type {ChannelType} from '@mattermost/types/channels';
import {Permissions} from 'mattermost-redux/constants';

import * as GlobalActions from 'actions/global_actions';
import * as RhsActions from 'actions/views/rhs';
import ChannelHeader from 'components/channel_header/channel_header';
import GlobalHeader from 'components/global_header/global_header';
import MobileChannelHeader from 'components/mobile_channel_header/mobile_channel_header';
import PhoenixShell from 'components/phoenix_shell';
import * as client from 'components/phoenix_shell/client';
import type {ShellSnapshot} from 'components/phoenix_shell/client';
import {HeaderSlotsProvider} from 'components/phoenix_shell/header_slots';

import {renderWithContext} from 'tests/react_testing_utils';
import {Constants, RHSStates, WindowSizes} from 'utils/constants';
import * as popoutWindows from 'utils/popouts/popout_windows';
import * as productUtils from 'utils/products';
import {TestHelper} from 'utils/test_helper';

jest.mock('components/initial_loading_screen', () => ({__esModule: true, default: {stop: jest.fn()}}));
jest.mock('components/phoenix_shell/client', () => ({
    ...jest.requireActual('components/phoenix_shell/client'),
    fetchShellContext: jest.fn(),
    fetchNavigation: jest.fn(),
    patchPreference: jest.fn(),
    fetchUnreadCount: jest.fn(),
}));
jest.mock('utils/utils', () => ({
    ...jest.requireActual('utils/utils'),
    applyTheme: jest.fn(),
}));
jest.mock('actions/global_actions', () => ({
    ...jest.requireActual('actions/global_actions'),
    emitUserLoggedOutEvent: jest.fn(),
}));

const shell = client.fetchShellContext as jest.MockedFunction<typeof client.fetchShellContext>;
const navigation = client.fetchNavigation as jest.MockedFunction<typeof client.fetchNavigation>;
const preference = client.patchPreference as jest.MockedFunction<typeof client.patchPreference>;
const unreadCount = client.fetchUnreadCount as jest.MockedFunction<typeof client.fetchUnreadCount>;

const snapshot: ShellSnapshot = {
    appCode: 'chat',
    tenant: {id: 'tenant-a', displayName: 'Tenant A'},
    themeRevision: 1,
    resolvedThemeHash: 'hash-a',
    theme: {color: {light: {surface: '#ffffff', text: '#111111'}, dark: {surface: '#111111', text: '#ffffff'}}},
    footer: {presentation: 'compact', operator: {displayName: 'Operator'}, tenant: {displayName: 'Tenant A'}, links: []},
    account: {displayName: 'A1', avatarUrl: null},
    preference: {mode: 'light', density: 'comfortable'},
};

function makeBaseState(overrides: Record<string, any> = {}) {
    const user = TestHelper.getUserMock({id: 'user_id', username: 'a1', roles: 'system_user'});
    const team = TestHelper.getTeamMock({id: 'team_id', name: 'team-a', display_name: 'Team A'});
    const channel = TestHelper.getChannelMock({
        id: 'channel_id',
        team_id: 'team_id',
        name: 'general',
        display_name: 'General',
        header: 'Channel description',
        type: Constants.OPEN_CHANNEL as ChannelType,
        delete_at: 0,
    });
    const member = TestHelper.getChannelMembershipMock({
        channel_id: 'channel_id',
        user_id: 'user_id',
        roles: 'channel_user',
    });

    return {
        entities: {
            general: {
                config: {
                    EnableFileAttachments: 'true',
                    EnableCustomUserStatuses: 'true',
                    BuildEnterpriseReady: 'true',
                    HelpLink: 'https://docs.mattermost.com',
                    ReportAProblemLink: 'https://mattermost.com/report',
                    EnableAskCommunityLink: 'true',
                },
                license: {
                    IsLicensed: 'true',
                    Cloud: 'false',
                    LDAPGroups: 'false',
                },
            },
            users: {
                currentUserId: user.id,
                profiles: {[user.id]: user},
                statuses: {[user.id]: 'online'},
                mySessions: [],
            },
            teams: {
                currentTeamId: team.id,
                teams: {[team.id]: team},
                myMembers: {
                    [team.id]: {
                        team_id: team.id,
                        user_id: user.id,
                        roles: 'team_user',
                        msg_count: 0,
                        mention_count: 0,
                        mention_count_root: 0,
                        msg_count_root: 0,
                    },
                },
            },
            channels: {
                currentChannelId: channel.id,
                channels: {[channel.id]: channel},
                channelsInTeam: {[team.id]: new Set([channel.id])},
                myMembers: {[channel.id]: member},
                stats: {
                    [channel.id]: {
                        channel_id: channel.id,
                        member_count: 4,
                        guest_count: 0,
                        pinnedpost_count: 2,
                        files_count: 1,
                    },
                },
                messageCounts: {[channel.id]: {total: 0, root: 0}},
                roles: {[channel.id]: new Set(['channel_user'])},
            },
            roles: {
                roles: {
                    system_user: {
                        permissions: [
                            Permissions.MANAGE_PUBLIC_CHANNEL_PROPERTIES,
                            Permissions.MANAGE_PUBLIC_CHANNEL_MEMBERS,
                        ],
                    },
                    team_user: {permissions: []},
                    channel_user: {
                        permissions: [
                            Permissions.MANAGE_PUBLIC_CHANNEL_PROPERTIES,
                            Permissions.MANAGE_PUBLIC_CHANNEL_MEMBERS,
                        ],
                    },
                },
            },
            preferences: {myPreferences: {}},
            apps: {main: {bindings: [], forms: {}}, rhs: {bindings: [], forms: {}}, pluginEnabled: false},
            cloud: {limits: {limitsLoaded: false, limits: {}}, subscription: {}, errors: {}},
            channelBookmarks: {byChannelId: {}},
            sharedChannels: {remotes: {}},
            emojis: {customEmoji: {}, nonExistentEmoji: new Set()},
            posts: {posts: {}},
            threads: {counts: {}, countsIncludingDirect: {}},
            typing: {},
            usage: {
                files: {totalStorage: 0, totalStorageLoaded: true},
                messages: {history: 0, historyLoaded: true},
                teams: {active: 1, cloudArchived: 0, teamsLoaded: true},
            },
        },
        views: {
            browser: {windowSize: WindowSizes.DESKTOP_VIEW, focused: true},
            productMenu: {switcherOpen: false},
            rhs: {
                rhsState: RHSStates.CHANNEL_INFO,
                isSidebarOpen: false,
                isSidebarExpanded: false,
                isMenuOpen: false,
                searchTerms: '',
                searchTeam: 'team_id',
                searchType: '',
            },
            lhs: {isOpen: false},
            modals: {modalState: {}},
            channelSidebar: {unreadFilterEnabled: false},
            marketplace: {},
            notice: {hasBeenDismissed: {}},
        },
        plugins: {
            components: {
                Product: [],
                ProductSwitcherMenuItem: [],
                ChannelHeaderButton: [],
                ChannelHeader: [],
                MobileChannelHeaderButton: [],
                ChannelHeaderIcon: [],
                CallButton: [],
                SearchButtons: [],
                UserGuideDropdown: [],
            },
            plugins: {},
        },
        requests: {
            channels: {getChannels: {status: 'not_started', error: null}},
        },
        websocket: {connected: true},
        ...overrides,
    };
}

function makeChannelHeaderProps(overrides: Record<string, any> = {}) {
    const channel = TestHelper.getChannelMock({
        id: 'channel_id',
        team_id: 'team_id',
        name: 'general',
        display_name: 'General',
        header: 'Channel description',
        type: Constants.OPEN_CHANNEL as ChannelType,
        delete_at: 0,
    });
    const channelMember = TestHelper.getChannelMembershipMock({
        channel_id: 'channel_id',
        user_id: 'user_id',
    });
    const currentUser = TestHelper.getUserMock({id: 'user_id'});
    const team = TestHelper.getTeamMock({id: 'team_id', name: 'team-a'});

    return {
        actions: {
            showPinnedPosts: jest.fn(),
            showChannelFiles: jest.fn(),
            closeRightHandSide: jest.fn(),
            getCustomEmojisInText: jest.fn(),
            updateChannelNotifyProps: jest.fn(),
            showChannelMembers: jest.fn(),
            fetchChannelRemotes: jest.fn(),
        },
        team,
        channel,
        channelMember,
        currentUser,
        isCustomStatusEnabled: false,
        isCustomStatusExpired: false,
        isFileAttachmentsEnabled: true,
        lastActivityTimestamp: 1632146562846,
        isLastActiveEnabled: false,
        memberCount: 4,
        dmUser: undefined,
        gmMembers: undefined,
        rhsState: null,
        isChannelMuted: true,
        hasGuests: false,
        pinnedPostsCount: 2,
        customStatus: undefined,
        timestampUnits: ['now', 'minute', 'hour'],
        hideGuestTags: false,
        remoteNames: [],
        isChannelAutotranslated: false,
        hasPendingJoinRequests: false,
        ...overrides,
    };
}

beforeEach(() => {
    jest.clearAllMocks();
    shell.mockResolvedValue({
        body: snapshot,
        etag: '"7"',
        logoutUrl: 'https://auth.phoenixsoft.vn/oauth2/logout',
        status: 200,
    });
    navigation.mockResolvedValue({
        body: {
            context: 'tenant',
            currentApp: 'chat',
            expiry: null,
            apps: [
                {appCode: 'tasks', label: 'Tasks', description: '', iconKey: 'tasks', state: 'ready', launchUrl: 'https://tasks.phoenixsoft.vn'},
            ],
        },
        etag: '"nav-1"',
        logoutUrl: null,
        status: 200,
    });
    unreadCount.mockResolvedValue({
        body: {count: 0, status: 'ready'},
        etag: null,
        logoutUrl: null,
        status: 200,
    });
    Object.defineProperty(window, 'matchMedia', {
        writable: true,
        value: jest.fn(() => ({matches: false, addEventListener: jest.fn(), removeEventListener: jest.fn()})),
    });
    Object.defineProperty(window, 'innerWidth', {writable: true, configurable: true, value: 1440});
});

describe('Unified Chat Header (Phase 1)', () => {
    // Break: rendering GlobalHeader or ChannelHeader as a separate banner row, duplicating controls
    // across PhoenixShell and Mattermost headers, or keeping hidden desktop copies when toggling mobile.
    test('U01 single header and action ownership across desktop, web/desktop history, and mobile breakpoint toggle', async () => {
        const user = userEvent.setup();
        const isDesktopSpy = jest.spyOn(userAgent, 'isDesktopApp').mockReturnValue(false);
        const chProps = makeChannelHeaderProps();
        const mobileActions = {closeLhs: jest.fn(), closeRhs: jest.fn(), closeRhsMenu: jest.fn()};

        const {rerender, container} = renderWithContext(
            <PhoenixShell>
                <GlobalHeader/>
                <ChannelHeader {...chProps}/>
            </PhoenixShell>,
            makeBaseState(),
        );

        const header = await screen.findByTestId('phoenix-header');
        expect(screen.getAllByRole('banner')).toHaveLength(1);
        expect(screen.getByRole('banner')).toBe(header);
        expect(container.querySelector('#global-header')).toBeNull();

        // Single instance of brand, tenant app launcher, product switch, search, help, mentions, bookmarks, settings, account, and theme.
        expect(within(header).getByTitle('Tenant A')).toBeInTheDocument();
        expect(within(header).getAllByText('Chat')).toHaveLength(1);
        expect(within(header).getAllByRole('button', {name: 'Ứng dụng'})).toHaveLength(1);
        expect(within(header).getAllByRole('button', {name: 'Product switch menu'})).toHaveLength(1);
        expect(within(header).getAllByRole('button', {name: 'Search'})).toHaveLength(1);
        expect(within(header).getAllByRole('button', {name: 'Help'})).toHaveLength(1);
        expect(within(header).getAllByRole('button', {name: 'Recent mentions'})).toHaveLength(1);
        expect(within(header).getAllByRole('button', {name: 'Saved messages'})).toHaveLength(1);
        expect(within(header).getAllByRole('button', {name: 'Settings'})).toHaveLength(1);
        expect(within(header).getAllByRole('button', {name: "User's account menu"})).toHaveLength(1);
        expect(within(header).getAllByRole('button', {name: 'Giao diện: Sáng'})).toHaveLength(1);
        expect(within(header).queryByRole('button', {name: 'Tài khoản'})).toBeNull();

        // Web has no desktop history buttons.
        expect(within(header).queryByRole('button', {name: 'Back'})).toBeNull();
        expect(within(header).queryByRole('button', {name: 'Forward'})).toBeNull();

        // Opening menus preserves distinct roles between trigger (button) and menu (menu).
        await user.click(within(header).getByRole('button', {name: 'Ứng dụng'}));
        expect(screen.getByRole('dialog', {name: 'Ứng dụng của bạn'})).toBeInTheDocument();
        expect(screen.getByRole('button', {name: 'Ứng dụng'})).toBeInTheDocument();
        await user.keyboard('{Escape}');

        // Desktop app mode renders Back and Forward history buttons once.
        isDesktopSpy.mockReturnValue(true);
        rerender(
            <PhoenixShell>
                <GlobalHeader/>
                <ChannelHeader {...chProps}/>
            </PhoenixShell>,
        );
        expect(within(header).getAllByRole('button', {name: 'Back'})).toHaveLength(1);
        expect(within(header).getAllByRole('button', {name: 'Forward'})).toHaveLength(1);

        // Toggling to mobile mounts MobileChannelHeader and suppresses parallel desktop ChannelHeader and GlobalSearchNav copies.
        rerender(
            <PhoenixShell>
                <GlobalHeader/>
                <MobileChannelHeader
                    channel={chProps.channel}
                    isMobileView={true}
                    isMuted={true}
                    user={chProps.currentUser}
                    actions={mobileActions}
                />
                <ChannelHeader {...chProps}/>
            </PhoenixShell>,
        );

        expect(screen.getAllByRole('banner')).toHaveLength(1);
        expect(within(header).queryByRole('region', {name: 'channel header region'})).toBeNull();
        expect(within(header).getAllByRole('button', {name: 'Search'})).toHaveLength(1);
        expect(within(header).getAllByRole('button', {name: 'general channel menu'})).toHaveLength(1);

        isDesktopSpy.mockRestore();
    });

    // Break: dropping connected ChannelHeader/GlobalHeader props, losing plugin callbacks or permission guards,
    // or failing to render product headerCentreComponent/headerRightComponent and UserAccountMenu actions.
    test('U02 native actions, plugin retention, permission guards, product components, and UserAccountMenu logout', async () => {
        const user = userEvent.setup();
        const popoutSpy = jest.spyOn(popoutWindows, 'popoutChannel').mockResolvedValue({});
        jest.spyOn(popoutWindows, 'getPopoutChannelTitle').mockReturnValue({id: 'channel_popout.title', defaultMessage: 'Channel Popout'});
        jest.spyOn(popoutWindows, 'canPopout').mockReturnValue(true);
        jest.spyOn(popoutWindows, 'isChannelPopoutWindow').mockReturnValue(false);

        const pluginButtonAction = jest.fn();
        const pluginMenuAction = jest.fn();
        const callButtonAction = jest.fn();
        const state = makeBaseState({
            plugins: {
                components: {
                    Product: [
                        {
                            id: 'boards_prod',
                            pluginId: 'focalboard',
                            switcherIcon: 'product-boards',
                            switcherText: 'Boards',
                            baseURL: '/boards',
                            switcherLinkURL: '/boards',
                            mainComponent: () => null,
                            headerCentreComponent: () => <div data-testid='product-centre'>{'Boards Centre'}</div>,
                            headerRightComponent: () => <div data-testid='product-right'>{'Boards Right'}</div>,
                        },
                    ],
                    ProductSwitcherMenuItem: [],
                    ChannelHeaderButton: [
                        {
                            id: 'plug_btn_1',
                            pluginId: 'jira',
                            icon: <i className='icon-power-plug-outline'/>,
                            tooltipText: 'Jira Plugin Button',
                            dropdownText: 'Jira Plugin Button',
                            action: pluginButtonAction,
                        },
                    ],
                    ChannelHeader: [
                        {
                            id: 'plug_menu_1',
                            pluginId: 'jira',
                            text: 'Jira Menu Action',
                            action: pluginMenuAction,
                        },
                    ],
                    MobileChannelHeaderButton: [],
                    ChannelHeaderIcon: [
                        {
                            id: 'plug_icon_1',
                            pluginId: 'jira',
                            component: () => <span data-testid='plugin-header-icon'>{'PluginIcon'}</span>,
                        },
                    ],
                    CallButton: [
                        {
                            id: 'call_btn_1',
                            pluginId: 'calls',
                            button: <button type='button'>{'Start Call'}</button>,
                            dropdownButton: <span>{'Start Call'}</span>,
                            action: callButtonAction,
                        },
                    ],
                    SearchButtons: [],
                    UserGuideDropdown: [],
                },
                plugins: {},
            },
        });

        const chProps = makeChannelHeaderProps();
        const {unmount} = renderWithContext(
            <PhoenixShell>
                <GlobalHeader/>
                <ChannelHeader {...chProps}/>
            </PhoenixShell>,
            state,
        );

        const header = await screen.findByTestId('phoenix-header');

        // Channel title, description, plugin icon, and call button render inside the unified header.
        expect(within(header).getByText('General')).toBeInTheDocument();
        expect(within(header).getByText('Channel description')).toBeInTheDocument();
        expect(within(header).getByTestId('plugin-header-icon')).toBeInTheDocument();
        await user.click(within(header).getByRole('button', {name: 'Start Call'}));
        expect(callButtonAction).toHaveBeenCalledTimes(1);

        // Invoke mute, members, pinned, files, plugin button, popout, and info callbacks.
        await user.click(within(header).getByRole('button', {name: 'Unmute'}));
        expect(chProps.actions.updateChannelNotifyProps).toHaveBeenCalledTimes(1);
        expect(chProps.actions.updateChannelNotifyProps).toHaveBeenCalledWith('user_id', 'channel_id', {mark_unread: 'all'});

        await user.click(within(header).getByRole('button', {name: 'Members'}));
        expect(chProps.actions.showChannelMembers).toHaveBeenCalledTimes(1);
        expect(chProps.actions.showChannelMembers).toHaveBeenCalledWith('channel_id');

        await user.click(within(header).getByRole('button', {name: 'Pinned messages'}));
        expect(chProps.actions.showPinnedPosts).toHaveBeenCalledTimes(1);

        await user.click(within(header).getByRole('button', {name: 'Channel files'}));
        expect(chProps.actions.showChannelFiles).toHaveBeenCalledTimes(1);
        expect(chProps.actions.showChannelFiles).toHaveBeenCalledWith('channel_id');

        await user.click(within(header).getByRole('button', {name: 'Jira Plugin Button'}));
        expect(pluginButtonAction).toHaveBeenCalledTimes(1);
        expect(pluginButtonAction).toHaveBeenCalledWith(chProps.channel, chProps.channelMember);

        await user.click(within(header).getByRole('button', {name: 'Open in new window'}));
        expect(popoutSpy).toHaveBeenCalledTimes(1);

        expect(within(header).getByRole('button', {name: 'View Info'})).toBeInTheDocument();

        // Open channel menu and invoke plugin menu item via More actions submenu.
        await user.click(within(header).getByRole('button', {name: 'general channel menu'}));
        await user.hover(await screen.findByText('More actions'));
        const pluginMenuItem = await screen.findByText('Jira Menu Action');
        fireEvent.click(pluginMenuItem);
        await waitFor(() => expect(pluginMenuAction).toHaveBeenCalledWith('channel_id'));

        // Open UserAccountMenu and invoke Log out.
        await user.click(within(header).getByRole('button', {name: "User's account menu"}));
        const logOutItem = await screen.findByText('Log out');
        fireEvent.click(logOutItem);
        await waitFor(() => expect(GlobalActions.emitUserLoggedOutEvent).toHaveBeenCalledTimes(1));

        // Product route preserves headerCentreComponent and headerRightComponent in the utilities slot.
        unmount();
        const productSpy = jest.spyOn(productUtils, 'useCurrentProductId').mockReturnValue('boards_prod');
        renderWithContext(
            <PhoenixShell>
                <GlobalHeader/>
            </PhoenixShell>,
            state,
        );
        const productHeader = await screen.findByTestId('phoenix-header');
        expect(within(productHeader).getByTestId('product-centre')).toBeInTheDocument();
        expect(within(productHeader).getByTestId('product-right')).toBeInTheDocument();
        expect(within(productHeader).queryByRole('button', {name: 'Recent mentions'})).toBeNull();
        expect(within(productHeader).queryByRole('button', {name: 'Thông báo'})).toBeNull();

        productSpy.mockRestore();
        popoutSpy.mockRestore();
    });

    // Break: allowing unauthorized users to see "Add a channel header" when MANAGE_PUBLIC_CHANNEL_PROPERTIES is missing.
    test('U02 permission-disabled channel header edit action stays unavailable', async () => {
        const noPermState = makeBaseState({
            entities: {
                ...makeBaseState().entities,
                roles: {
                    roles: {
                        system_user: {permissions: []},
                        team_user: {permissions: []},
                        channel_user: {permissions: []},
                    },
                },
            },
        });
        const emptyHeaderProps = makeChannelHeaderProps({
            channel: TestHelper.getChannelMock({
                id: 'channel_id',
                team_id: 'team_id',
                name: 'general',
                display_name: 'General',
                header: '',
                type: Constants.OPEN_CHANNEL as ChannelType,
                delete_at: 0,
            }),
        });

        renderWithContext(
            <PhoenixShell>
                <GlobalHeader/>
                <ChannelHeader {...emptyHeaderProps}/>
            </PhoenixShell>,
            noPermState,
        );

        const header = await screen.findByTestId('phoenix-header');
        expect(within(header).queryByText('Add a channel header')).toBeNull();
    });

    // Break: leaving stale channel content in the slot after route changes, rendering two search buttons at 390px,
    // or portaling popout/inactive headers instead of keeping native controls in-place.
    test('U04 route transitions A -> B -> product -> inactive, 390px mobile ownership, and popout native fallback', async () => {
        const user = userEvent.setup();
        const channelAProps = makeChannelHeaderProps({
            channel: TestHelper.getChannelMock({
                id: 'channel_a',
                team_id: 'team_id',
                name: 'alpha',
                display_name: 'Alpha Channel',
                header: 'Alpha header',
                type: Constants.OPEN_CHANNEL as ChannelType,
                delete_at: 0,
            }),
        });
        const channelBProps = makeChannelHeaderProps({
            channel: TestHelper.getChannelMock({
                id: 'channel_b',
                team_id: 'team_id',
                name: 'beta',
                display_name: 'Beta Channel',
                header: 'Beta header',
                type: Constants.OPEN_CHANNEL as ChannelType,
                delete_at: 0,
            }),
        });

        const {rerender, unmount} = renderWithContext(
            <PhoenixShell>
                <GlobalHeader/>
                <ChannelHeader {...channelAProps}/>
            </PhoenixShell>,
            makeBaseState(),
        );

        const header = await screen.findByTestId('phoenix-header');
        expect(within(header).getByText('Alpha header')).toBeInTheDocument();

        // Route A -> B updates the channel slot without stale Channel A content.
        rerender(
            <PhoenixShell>
                <GlobalHeader/>
                <ChannelHeader {...channelBProps}/>
            </PhoenixShell>,
        );
        expect(within(header).queryByText('Alpha header')).toBeNull();
        expect(within(header).getByText('Beta header')).toBeInTheDocument();

        // Route B -> Product cleans the channel slot completely.
        rerender(
            <PhoenixShell>
                <GlobalHeader/>
            </PhoenixShell>,
        );
        expect(within(header).queryByText('Beta header')).toBeNull();
        expect(within(header).queryByRole('region', {name: 'channel header region'})).toBeNull();

        // At 390px mobile: retain CollapseLhsButton, ShowSearchButton, CollapseRhsButton, mobile menu, threads/drafts headings, and one search owner.
        act(() => {
            Object.defineProperty(window, 'innerWidth', {writable: true, configurable: true, value: 390});
            window.dispatchEvent(new Event('resize'));
        });

        const mobileActions = {closeLhs: jest.fn(), closeRhs: jest.fn(), closeRhsMenu: jest.fn()};
        rerender(
            <PhoenixShell>
                <GlobalHeader/>
                <MobileChannelHeader
                    channel={channelBProps.channel}
                    isMobileView={true}
                    isMuted={true}
                    user={channelBProps.currentUser}
                    actions={mobileActions}
                />
                <ChannelHeader {...channelBProps}/>
            </PhoenixShell>,
        );

        expect(within(header).getByText('Toggle sidebar')).toBeInTheDocument();
        expect(within(header).getByRole('button', {name: 'general channel menu'})).toBeInTheDocument();

        // Open compact utilities overflow at 390px to verify ShowSearchButton, CollapseRhsButton, Info, and single search owner.
        await user.click(within(header).getByRole('button', {name: 'Tiện ích'}));
        expect(within(header).getByLabelText('Info')).toBeInTheDocument();
        expect(within(header).getByText('Toggle right sidebar')).toBeInTheDocument();
        expect(within(header).getAllByRole('button', {name: 'Search'})).toHaveLength(1);
        await user.keyboard('{Escape}');

        // Threads and Drafts mobile headings.
        rerender(
            <PhoenixShell>
                <GlobalHeader/>
                <MobileChannelHeader
                    channel={channelBProps.channel}
                    isMobileView={true}
                    inGlobalThreads={true}
                    user={channelBProps.currentUser}
                    actions={mobileActions}
                />
            </PhoenixShell>,
        );
        expect(within(header).getByText('Followed threads')).toBeInTheDocument();

        rerender(
            <PhoenixShell>
                <GlobalHeader/>
                <MobileChannelHeader
                    channel={channelBProps.channel}
                    isMobileView={true}
                    inDrafts={true}
                    user={channelBProps.currentUser}
                    actions={mobileActions}
                />
            </PhoenixShell>,
        );
        expect(within(header).getByText('Drafts')).toBeInTheDocument();
        unmount();

        // Inactive/popout fallback keeps native controls in-place with role="banner".
        const popoutView = renderWithContext(
            <PhoenixShell>
                <HeaderSlotsProvider active={false}>
                    <div data-testid='popout-host'>
                        <ChannelHeader {...channelAProps}/>
                    </div>
                </HeaderSlotsProvider>
            </PhoenixShell>,
            makeBaseState(),
        );
        await screen.findByTestId('phoenix-header');
        const popoutHost = popoutView.getByTestId('popout-host');
        expect(within(popoutHost).getByRole('banner', {name: 'channel header region'})).toBeInTheDocument();
    });

    // Break (C01 + C07): hiding/misplacing the bell, formatting 0/null/unavailable/7/100/9999 wrong,
    // not dispatching showMentions() on click, or showing the bell on boards_prod.
    test('C01_C07 Phoenix bell badge formatting, position before launcher, showMentions click, and hidden on boards_prod', async () => {
        const user = userEvent.setup();
        const showMentionsSpy = jest.spyOn(RhsActions, 'showMentions').mockReturnValue({type: 'MOCK_SHOW_MENTIONS'} as any);

        // 1. 0, null, unavailable -> badge hidden.
        for (const payload of [
            {count: 0, status: 'ready' as const},
            {count: null, status: 'ready' as const},
            {count: 5, status: 'unavailable' as const},
        ]) {
            unreadCount.mockResolvedValueOnce({body: payload, etag: null, logoutUrl: null, status: 200});
            const view = renderWithContext(
                <PhoenixShell>
                    <GlobalHeader/>
                </PhoenixShell>,
                makeBaseState(),
            );
            const header = await screen.findByTestId('phoenix-header');
            await waitFor(() => expect(unreadCount).toHaveBeenCalled());
            expect(within(header).getByRole('button', {name: 'Thông báo'})).toBeInTheDocument();
            expect(within(header).queryByTestId('phoenix-unread-badge')).toBeNull();
            view.unmount();
        }

        // 2. count=7 -> "7", aria-label="7 chưa đọc", bell appears before "Ứng dụng", click calls showMentions().
        unreadCount.mockResolvedValueOnce({body: {count: 7, status: 'ready'}, etag: null, logoutUrl: null, status: 200});
        const view7 = renderWithContext(
            <PhoenixShell>
                <GlobalHeader/>
            </PhoenixShell>,
            makeBaseState(),
        );
        const header7 = await screen.findByTestId('phoenix-header');
        const badge7 = await within(header7).findByTestId('phoenix-unread-badge');
        expect(badge7).toHaveTextContent('7');
        expect(badge7).toHaveAttribute('aria-label', '7 chưa đọc');

        const bellBtn = within(header7).getByRole('button', {name: 'Thông báo'});
        const appsBtn = within(header7).getByRole('button', {name: 'Ứng dụng'});
        expect(Boolean(bellBtn.compareDocumentPosition(appsBtn) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true);

        await user.click(bellBtn);
        expect(showMentionsSpy).toHaveBeenCalledTimes(1);
        view7.unmount();

        // 3. 100 and 9999 -> "99+" with exact aria-label.
        for (const n of [100, 9999]) {
            unreadCount.mockResolvedValueOnce({body: {count: n, status: 'ready'}, etag: null, logoutUrl: null, status: 200});
            const viewHigh = renderWithContext(
                <PhoenixShell>
                    <GlobalHeader/>
                </PhoenixShell>,
                makeBaseState(),
            );
            const headerHigh = await screen.findByTestId('phoenix-header');
            const badgeHigh = await within(headerHigh).findByTestId('phoenix-unread-badge');
            expect(badgeHigh).toHaveTextContent('99+');
            expect(badgeHigh).toHaveAttribute('aria-label', `${n} chưa đọc`);
            viewHigh.unmount();
        }

        // 4. Hidden on boards_prod.
        const productSpy = jest.spyOn(productUtils, 'useCurrentProductId').mockReturnValue('boards_prod');
        unreadCount.mockResolvedValueOnce({body: {count: 7, status: 'ready'}, etag: null, logoutUrl: null, status: 200});
        renderWithContext(
            <PhoenixShell>
                <GlobalHeader/>
            </PhoenixShell>,
            makeBaseState(),
        );
        const boardsHeader = await screen.findByTestId('phoenix-header');
        expect(within(boardsHeader).queryByRole('button', {name: 'Thông báo'})).toBeNull();

        productSpy.mockRestore();
        showMentionsSpy.mockRestore();
    });
});
