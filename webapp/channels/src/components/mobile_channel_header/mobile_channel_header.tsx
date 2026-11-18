// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';
import {FormattedMessage} from 'react-intl';

import type {Channel} from '@mattermost/types/channels';
import type {UserProfile} from '@mattermost/types/users';

import {HeaderSlotContent, useHasHeaderSlots, useMobileHeaderSlot} from 'components/phoenix_shell/header_slots';

import ChannelInfoButton from './channel_info_button';
import CollapseLhsButton from './collapse_lhs_button';
import CollapseRhsButton from './collapse_rhs_button';
import ShowSearchButton from './show_search_button';
import UnmuteChannelButton from './unmute_channel_button';

import ChannelHeaderMenu from '../channel_header_menu/channel_header_menu';
import MobileChannelHeaderPlugins from '../channel_header_menu/menu_items/mobile_channel_header_plugins';

type Props = {
    channel?: Channel;

    inGlobalThreads?: boolean;
    inDrafts?: boolean;
    isMobileView: boolean;
    isMuted?: boolean;
    isRHSOpen?: boolean;
    user: UserProfile;
    actions: {
        closeLhs: () => void;
        closeRhs: () => void;
        closeRhsMenu: () => void;
    };
};

export default class MobileChannelHeader extends React.PureComponent<Props> {
    componentDidMount() {
        document.querySelector('.inner-wrap')?.addEventListener('click', this.hideSidebars);
    }

    componentWillUnmount() {
        document.querySelector('.inner-wrap')?.removeEventListener('click', this.hideSidebars);
    }

    hideSidebars = (e: Event) => {
        if (this.props.isMobileView) {
            if (this.props.isRHSOpen) {
                this.props.actions.closeRhs();
            }

            const target = e.target as HTMLElement | undefined;

            if (target && target.className !== 'navbar-toggle' && target.className !== 'icon-bar') {
                this.props.actions.closeLhs();
                this.props.actions.closeRhsMenu();
            }
        }
    };

    render() {
        const {user, channel, isMuted, inGlobalThreads, inDrafts} = this.props;

        let heading;
        if (inGlobalThreads) {
            heading = (
                <FormattedMessage
                    id='globalThreads.heading'
                    defaultMessage='Followed threads'
                />
            );
        } else if (inDrafts) {
            heading = (
                <FormattedMessage
                    id='drafts.heading'
                    defaultMessage='Drafts'
                />
            );
        } else if (channel) {
            heading = (
                <>
                    <ChannelHeaderMenu
                        isMobile={true}
                    />

                    {isMuted && (
                        <UnmuteChannelButton
                            user={user}
                            channel={channel}
                        />
                    )}
                </>
            );
        }

        const fallback = (
            <div className='row header'>
                <div id='navbar_wrapper'>
                    <nav
                        id='navbar'
                        className='navbar navbar-default navbar-fixed-top'
                        role='navigation'
                    >
                        <div className='container-fluid theme'>
                            <div className='navbar-header'>
                                <CollapseLhsButton/>
                                <div className={classNames('navbar-brand', {GlobalThreads___title: inGlobalThreads})}>
                                    {heading}
                                </div>
                                <div className='spacer'/>
                                {channel && (
                                    <ChannelInfoButton
                                        channel={channel}
                                    />
                                )}
                                {channel && (
                                    <MobileChannelHeaderPlugins
                                        channel={channel}
                                        isDropdown={false}
                                    />
                                )}
                                <ShowSearchButton/>
                                <CollapseRhsButton/>
                            </div>
                        </div>
                    </nav>
                </div>
            </div>
        );

        const slotHeading = (!inGlobalThreads && !inDrafts && channel) ? (
            <ChannelHeaderMenu
                isMobile={true}
            />
        ) : heading;

        const channelIdentity = (
            <div
                id='navbar'
                className='phoenix-mobile-channel'
                role='navigation'
            >
                <CollapseLhsButton/>
                <div className={classNames('navbar-brand', {GlobalThreads___title: inGlobalThreads})}>
                    {slotHeading}
                </div>
                {!inGlobalThreads && !inDrafts && channel && isMuted && (
                    <UnmuteChannelButton
                        user={user}
                        channel={channel}
                    />
                )}
            </div>
        );

        const mobileUtilities = (
            <div className='phoenix-mobile-utilities'>
                {channel && (
                    <ChannelInfoButton
                        channel={channel}
                    />
                )}
                {channel && (
                    <MobileChannelHeaderPlugins
                        channel={channel}
                        isDropdown={false}
                    />
                )}
                <ShowSearchButton/>
                <CollapseRhsButton/>
            </div>
        );

        return (
            <MobileChannelHeaderSlotAdapter
                channelIdentity={channelIdentity}
                mobileUtilities={mobileUtilities}
                fallback={fallback}
            />
        );
    }
}

function MobileChannelHeaderSlotAdapter({
    channelIdentity,
    mobileUtilities,
    fallback,
}: {
    channelIdentity: React.ReactNode;
    mobileUtilities: React.ReactNode;
    fallback: React.ReactNode;
}) {
    const hasSlots = useHasHeaderSlots();
    useMobileHeaderSlot(hasSlots);

    if (!hasSlots) {
        return <>{fallback}</>;
    }

    return (
        <>
            <HeaderSlotContent name='channel'>
                {channelIdentity}
            </HeaderSlotContent>
            <HeaderSlotContent name='utilities'>
                {mobileUtilities}
            </HeaderSlotContent>
        </>
    );
}
