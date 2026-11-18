// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import {HeaderSlotsProvider, HeaderSlotTarget} from 'components/phoenix_shell/header_slots';

import {renderWithContext, screen} from 'tests/react_testing_utils';
import {TestHelper} from 'utils/test_helper';

import ChannelHeaderMobile from './mobile_channel_header';

describe('components/ChannelHeaderMobile/ChannelHeaderMobile', () => {
    global.document.querySelector = jest.fn().mockReturnValue({
        addEventListener: jest.fn(),
        removeEventListener: jest.fn(),
    });

    const user = TestHelper.getUserMock({
        id: 'user_id',
    });
    const channel = TestHelper.getChannelMock({
        type: 'O',
        id: 'channel_id',
        display_name: 'display_name',
        team_id: 'team_id',
    });
    const actions = {
        closeLhs: jest.fn(),
        closeRhs: jest.fn(),
        closeRhsMenu: jest.fn(),
    };

    describe('components/ChannelHeaderMenu/MenuItem/ChannelHeaderMobile', () => {
        test('renders the component correctly', () => {
            renderWithContext(
                <div
                    className='inner-wrap'
                    data-testid='wrapper'
                >
                    <ChannelHeaderMobile
                        channel={channel}
                        isMobileView={false}
                        user={user}
                        actions={actions}
                    />
                </div>,
            );

            let menuItem = screen.getByText('Toggle sidebar');
            expect(menuItem).toBeInTheDocument();

            menuItem = screen.getByLabelText('Info');
            expect(menuItem).toBeInTheDocument();

            menuItem = screen.getByLabelText('Search');
            expect(menuItem).toBeInTheDocument();

            menuItem = screen.getByText('Toggle right sidebar');
            expect(menuItem).toBeInTheDocument();

            const wrapper = screen.getByTestId('wrapper');
            expect(wrapper).toBeInTheDocument();
        });

        test('renders the component correctly, global threads', () => {
            renderWithContext(
                <div
                    className='inner-wrap'
                    data-testid='wrapper'
                >
                    <ChannelHeaderMobile
                        channel={channel}
                        isMobileView={false}
                        inGlobalThreads={true}
                        user={user}
                        actions={actions}
                    />
                </div>,
            );

            const menuItem = screen.getByText('Followed threads');
            expect(menuItem).toBeInTheDocument();
        });

        test('renders the component correctly, in drafts', () => {
            renderWithContext(
                <div
                    className='inner-wrap'
                    data-testid='wrapper'
                >
                    <ChannelHeaderMobile
                        channel={channel}
                        isMobileView={false}
                        inDrafts={true}
                        user={user}
                        actions={actions}
                    />
                </div>,
            );

            const menuItem = screen.getByText('Drafts');
            expect(menuItem).toBeInTheDocument();
        });

        // Break: leaving .navbar-fixed-top row in-place when HeaderSlotsProvider is active.
        test('U04 portals mobile identity and utilities into header slots when unified shell is active', () => {
            const {container} = renderWithContext(
                <HeaderSlotsProvider>
                    <header data-testid='phoenix-host'>
                        <HeaderSlotTarget name='channel'/>
                        <HeaderSlotTarget name='utilities'/>
                    </header>
                    <div
                        className='inner-wrap'
                        data-testid='wrapper'
                    >
                        <ChannelHeaderMobile
                            channel={channel}
                            isMobileView={true}
                            user={user}
                            actions={actions}
                        />
                    </div>
                </HeaderSlotsProvider>,
            );

            expect(container.querySelector('.navbar-fixed-top')).toBeNull();
            expect(container.querySelector('[data-header-slot="channel"] .phoenix-mobile-channel')).not.toBeNull();
            expect(container.querySelector('[data-header-slot="utilities"] .phoenix-mobile-utilities')).not.toBeNull();
            expect(screen.getByText('Toggle sidebar')).toBeInTheDocument();
            expect(screen.getByLabelText('Search')).toBeInTheDocument();
            expect(screen.getByText('Toggle right sidebar')).toBeInTheDocument();
        });
    });
});
