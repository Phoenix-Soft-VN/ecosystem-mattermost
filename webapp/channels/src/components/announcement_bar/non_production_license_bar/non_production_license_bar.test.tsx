// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import {renderWithContext} from 'tests/react_testing_utils';

import NonProductionLicenseAnnouncementBar from './index';

describe('Phoenix non-production license announcement', () => {
    it.each(['true', 'false', undefined])('does not render the banner when source IsNonProduction=%s', (flag) => {
        const license = flag === undefined ? {IsLicensed: 'true'} : {IsLicensed: 'true', IsNonProduction: flag};
        const {container} = renderWithContext(
            <NonProductionLicenseAnnouncementBar/>,
            {entities: {general: {license}}},
        );

        expect(container.querySelector('.announcement-bar')).toBeNull();
        expect(container.textContent).not.toContain('Non-production license. Test or staging use only.');
    });
});
