// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';

import {AccountMultipleOutlineIcon} from '@mattermost/compass-icons/components';

import * as Menu from 'components/menu';
import {useAccountSwitchHref} from 'components/phoenix_shell/account_switch';

// Same-tenant account switch (Phoenix). Rendered only when the Gateway
// shell navigation carried accountSwitchUrl; full-page navigation.
export default function UserAccountSwitchMenuItem() {
    const href = useAccountSwitchHref();
    const addHref = useAccountSwitchHref('add');
    if (!href) {
        return null;
    }

    return (
        <>
            {addHref && (
                <Menu.Item
                    leadingElement={
                        <AccountMultipleOutlineIcon
                            size={18}
                            aria-hidden='true'
                        />
                    }
                    labels={
                        <FormattedMessage
                            id='userAccountMenu.addAccountMenuItem.label'
                            defaultMessage='Add account'
                            description='Menu item that opens the Phoenix confirmation page to add another account in the same workspace.'
                        />
                    }
                    onClick={() => window.location.assign(addHref)}
                />
            )}
            <Menu.Item
                leadingElement={
                    <AccountMultipleOutlineIcon
                        size={18}
                        aria-hidden='true'
                    />
                }
                labels={
                    <FormattedMessage
                        id='userAccountMenu.switchAccountMenuItem.label'
                        defaultMessage='Switch account'
                    />
                }
                onClick={() => window.location.assign(href)}
            />
        </>
    );
}
