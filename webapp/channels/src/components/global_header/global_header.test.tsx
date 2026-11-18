// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import GlobalHeader from 'components/global_header/global_header';
import {HeaderSlotsProvider, HeaderSlotTarget} from 'components/phoenix_shell/header_slots';

import {renderWithContext, screen} from 'tests/react_testing_utils';
import * as productUtils from 'utils/products';

import * as hooks from './hooks';

jest.mock('./hooks');
jest.mock('utils/products');

// Mock child components to avoid deep dependency issues
jest.mock('./left_controls/left_controls', () => () => <div id='mock-left-controls'/>);
jest.mock('./center_controls/center_controls', () => ({productId}: {productId?: string | null}) => (
    <div id='mock-center-controls'>{productId}</div>
));
jest.mock('./right_controls/right_controls', () => ({productId}: {productId?: string | null}) => (
    <div id='mock-right-controls'>{productId}</div>
));

describe('components/global/global_header', () => {
    test('should not render when user is not logged in', () => {
        jest.spyOn(hooks, 'useIsLoggedIn').mockReturnValue(false);
        jest.spyOn(productUtils, 'useCurrentProductId').mockReturnValue(null);

        const {container} = renderWithContext(<GlobalHeader/>);

        expect(container.firstChild).toBeNull();
    });

    describe('when user is logged in', () => {
        beforeEach(() => {
            jest.spyOn(hooks, 'useIsLoggedIn').mockReturnValue(true);
            jest.spyOn(productUtils, 'useCurrentProductId').mockReturnValue(null);
        });

        test('should render header', () => {
            renderWithContext(<GlobalHeader/>);

            expect(screen.getByRole('banner')).toBeInTheDocument();
        });

        test('should pass product id to child components', () => {
            jest.spyOn(productUtils, 'useCurrentProductId').mockReturnValue('product_id');

            renderWithContext(<GlobalHeader/>);

            expect(screen.getByRole('banner')).toBeInTheDocument();

            // productId is passed to both CenterControls and RightControls
            expect(screen.getAllByText('product_id')).toHaveLength(2);
        });

        // Break: rendering #global-header banner alongside PhoenixShell instead of portaling into left/utilities slots.
        test('U01 portals controls into left and utilities slots without rendering a duplicate banner', () => {
            const {container} = renderWithContext(
                <HeaderSlotsProvider>
                    <header data-testid='host-header'>
                        <HeaderSlotTarget name='left'/>
                        <HeaderSlotTarget name='utilities'/>
                    </header>
                    <GlobalHeader/>
                </HeaderSlotsProvider>,
            );

            expect(container.querySelector('#global-header')).toBeNull();
            expect(screen.getAllByRole('banner')).toHaveLength(1);
            expect(container.querySelector('[data-header-slot="left"] #mock-left-controls')).not.toBeNull();
            expect(container.querySelector('[data-header-slot="utilities"] #mock-center-controls')).not.toBeNull();
            expect(container.querySelector('[data-header-slot="utilities"] #mock-right-controls')).not.toBeNull();
        });
    });
});
