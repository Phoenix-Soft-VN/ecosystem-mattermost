// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import classNames from 'classnames';
import React from 'react';
import type {JSX, ReactNode} from 'react';
import {FormattedMessage} from 'react-intl';
import {useSelector} from 'react-redux';

import type {UserProfile} from '@mattermost/types/users';

import {getTeammateNameDisplaySetting} from 'mattermost-redux/selectors/entities/preferences';
import {displayUsername, isGuest} from 'mattermost-redux/utils/user_utils';

import usePrefixedIds, {joinIds} from 'components/common/hooks/usePrefixedIds';
import CustomStatusEmoji from 'components/custom_status/custom_status_emoji';
import SharedUserIndicator from 'components/shared_user_indicator';
import StatusIcon from 'components/status_icon';
import AgentTag from 'components/widgets/tag/agent_tag';
import BotTag from 'components/widgets/tag/bot_tag';
import GuestTag from 'components/widgets/tag/guest_tag';
import ImportedInactiveTag from 'components/widgets/tag/imported_inactive_tag';
import Tag from 'components/widgets/tag/tag';
import Avatar from 'components/widgets/users/avatar';

import * as Utils from 'utils/utils';

import {SuggestionContainer} from '../suggestion';
import type {SuggestionProps} from '../suggestion';

export interface Item extends UserProfile {
    isCurrentUser: boolean;
    isAgent?: boolean;
    isDefaultAgent?: boolean;
}

interface Group extends Item {
    display_name: string;
    name: string;
    member_count: number;
}

function isGroup(o: unknown): o is Group {
    return Boolean(o && typeof o === 'object' && 'display_name' in o);
}

const AtMentionSuggestion = React.forwardRef<HTMLLIElement, SuggestionProps<Item>>((props, ref) => {
    const {id, item} = props;
    const teammateNameDisplay = useSelector(getTeammateNameDisplaySetting);

    const ids = usePrefixedIds(id, {
        atMention: null,
        description: null,
        youElement: null,
        status: null,
        botTag: null,
        sharedIcon: null,
        guestTag: null,
        groupMembers: null,
        importedInactiveTag: null,
    });

    // itemname is the handle that gets inserted and used for lookups; displayName is only what we show.
    let itemname: string;
    let displayName: string | undefined;
    let description: ReactNode;
    let icon: JSX.Element;
    let customStatus: ReactNode;
    if (item.username === 'all') {
        itemname = 'all';
        description = (
            <FormattedMessage
                id='suggestion.mention.all'
                defaultMessage='Notifies everyone in this channel'
            />
        );
        icon = (
            <span
                className='suggestion-list__icon suggestion-list__icon--large'
                aria-hidden='true'
            >
                <i className='icon icon-account-multiple-outline'/>
            </span>
        );
    } else if (item.username === 'channel') {
        itemname = 'channel';
        description = (
            <FormattedMessage
                id='suggestion.mention.channel'
                defaultMessage='Notifies everyone in this channel'
            />
        );
        icon = (
            <span
                className='suggestion-list__icon suggestion-list__icon--large'
                aria-hidden='true'
            >
                <i className='icon icon-account-multiple-outline'/>
            </span>
        );
    } else if (item.username === 'here') {
        itemname = 'here';
        description = (
            <FormattedMessage
                id='suggestion.mention.here'
                defaultMessage='Notifies everyone online in this channel'
            />
        );
        icon = (
            <span
                className='suggestion-list__icon suggestion-list__icon--large'
                aria-hidden='true'
            >
                <i className='icon icon-account-multiple-outline'/>
            </span>
        );
    } else if (isGroup(item)) {
        itemname = item.name;
        description = (
            <span className='ml-1'>{'- '}{item.display_name}</span>
        );
        icon = (
            <span
                className='suggestion-list__icon suggestion-list__icon--large'
                aria-hidden='true'
            >
                <i className='icon icon-account-multiple-outline'/>
            </span>
        );
    } else {
        itemname = item.username;
        displayName = displayUsername(item, teammateNameDisplay);

        // The inserted mention stays @username, so keep the handle visible when the label differs.
        const handle = displayName === item.username ? '' : `@${item.username}`;
        let names = '';
        if (item.isCurrentUser) {
            if (item.first_name || item.last_name) {
                names = Utils.getFullName(item);
            }
        } else if (item.first_name || item.last_name || item.nickname) {
            names = `${Utils.getFullName(item)} ${item.nickname ? `(${item.nickname})` : ''}`.trim();
        }
        const descriptionText = [handle, names].filter(Boolean).join(' ');
        if (descriptionText) {
            description = <span>{descriptionText}</span>;
        }

        icon = (
            <span className='status-wrapper style--none'>
                <span className='profile-icon'>
                    <Avatar
                        username={item && item.username}
                        size='sm'
                        url={Utils.imageURLForUser(item.id, item.last_picture_update)}
                        alt=''
                    />
                </span>
                <StatusIcon
                    id={ids.status}
                    status={item && item.status}
                />
            </span>
        );

        customStatus = (
            <CustomStatusEmoji
                showTooltip={true}
                userID={item.id}
                emojiSize={15}
                emojiStyle={{
                    margin: '0 4px 4px',
                }}
            />
        );
    }

    const youElement = item.isCurrentUser ? (
        <span id={ids.youElement}>
            <FormattedMessage
                id='suggestion.user.isCurrent'
                defaultMessage='(you)'
            />
        </span>
    ) : null;

    const sharedIcon = item.remote_id ? (
        <span id={ids.sharedIcon}>
            <SharedUserIndicator
                className='shared-user-icon'
            />
        </span>
    ) : null;

    let countBadge;
    if (isGroup(item)) {
        countBadge = (
            <span
                id={ids.groupMembers}
                className='suggestion-list__group-count'
            >
                <Tag
                    text={
                        <FormattedMessage
                            id='suggestion.group.members'
                            defaultMessage='{member_count} {member_count, plural, one {member} other {members}}'
                            values={{
                                member_count: item.member_count,
                            }}
                        />
                    }
                />
            </span>
        );
    }

    return (
        <SuggestionContainer
            ref={ref}
            {...props}
            className={classNames({'suggestion-list__item--default-agent': item.isDefaultAgent})}
            aria-labelledby={ids.atMention}
            aria-describedby={joinIds(ids.description, ids.youElement, ids.status, ids.botTag, ids.sharedIcon, ids.guestTag, ids.groupMembers, ids.importedInactiveTag)}
            data-testid={`mentionSuggestion_${itemname}`}
        >
            {icon}
            <span className='suggestion-list__ellipsis'>
                <span
                    id={ids.atMention}
                    data-testid='suggestion-list__main'
                    className='suggestion-list__main'
                >
                    {'@' + (displayName ?? itemname)}
                </span>
                {item.is_bot && <span id={ids.botTag}>{item.isAgent ? <AgentTag/> : <BotTag/>}</span>}
                {description && <span id={ids.description}>{description}</span>}
                {youElement}
                {customStatus}
                {sharedIcon}
                {isGuest(item.roles) && <GuestTag/>}
                {item.props?.importedInactive === 'true' && <span id={ids.importedInactiveTag}><ImportedInactiveTag/></span>}
            </span>
            {countBadge}
        </SuggestionContainer>
    );
});

AtMentionSuggestion.displayName = 'AtMentionSuggestion';
export default AtMentionSuggestion;
