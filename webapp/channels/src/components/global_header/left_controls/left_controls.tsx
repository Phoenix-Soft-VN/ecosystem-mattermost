// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {type JSX} from 'react';
import styled from 'styled-components';

import {isDesktopApp} from '@mattermost/shared/utils/user_agent';

import HistoryButtons from './history_buttons';
import ProductMenu from './product_menu';

const LeftControlsContainer = styled.div`
    display: flex;
    align-items: center;
    height: 40px;
    flex-shrink: 0;
    flex-basis: 30%;

    > * + * {
        margin-left: 12px;
    }

    .phoenix-shell__header & {
        height: 44px;
        flex: 0 0 auto;
        flex-basis: auto;
        min-width: 0;
        gap: 4px;

        > * + * {
            margin-left: 0;
        }
    }
`;

const LeftControls = (): JSX.Element => (
    <LeftControlsContainer className='global-header__left-controls'>
        <ProductMenu/>
        {isDesktopApp() && <HistoryButtons/>}
    </LeftControlsContainer>
);

export default LeftControls;
