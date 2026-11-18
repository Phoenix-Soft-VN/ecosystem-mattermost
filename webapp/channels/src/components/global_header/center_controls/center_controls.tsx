// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {type JSX} from 'react';
import {useSelector} from 'react-redux';
import styled from 'styled-components';

import type {ProductIdentifier} from '@mattermost/types/products';

import {getIsMobileView} from 'selectors/views/browser';

import {useHasHeaderSlots, useMobileHeaderSlot} from 'components/phoenix_shell/header_slots';

import Pluggable from 'plugins/pluggable';
import {isChannels} from 'utils/products';

import type {GlobalState} from 'types/store';

import GlobalSearchNav from './global_search_nav/global_search_nav';
import UserGuideDropdown from './user_guide_dropdown';

const CenterControlsContainer = styled.div`
    display: flex;
    align-items: center;
    height: 40px;
    justify-content: center;
    flex-grow: 1;
    flex-basis: 40%;

    > * + * {
        margin-left: 8px;
    }

    .phoenix-shell__header & {
        height: 44px;
        flex: 0 1 auto;
        flex-basis: auto;
        min-width: 0;
        gap: 4px;

        > * + * {
            margin-left: 0;
        }
    }
`;

export type Props = {
    productId?: ProductIdentifier;
};

const CenterControls = ({productId = null}: Props): JSX.Element => {
    const hasHeaderSlots = useHasHeaderSlots();
    const mobileSlotActive = useMobileHeaderSlot();
    const isMobileView = useSelector((state: GlobalState) => Boolean(state?.views?.browser && getIsMobileView(state)));
    const hideDesktopSearch = hasHeaderSlots && (isMobileView || mobileSlotActive);

    return (
        <CenterControlsContainer className='global-header__center-controls'>
            {isChannels(productId) ? (
                <>
                    {!hideDesktopSearch && <GlobalSearchNav/>}
                    <UserGuideDropdown/>
                </>
            ) : (
                <Pluggable
                    pluggableName={'Product'}
                    subComponentName={'headerCentreComponent'}
                    pluggableId={productId}
                />
            )}
        </CenterControlsContainer>
    );
};

export default CenterControls;
