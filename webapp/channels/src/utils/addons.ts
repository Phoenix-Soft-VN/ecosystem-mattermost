// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ClientLicense} from '@mattermost/types/config';

// Phoenix experiment: mirror the server's permissive HasAddOn helper. Empty
// names are still invalid; actual plugin availability is checked separately.
export function licenseHasAddOn(_license: ClientLicense | undefined, addOn: string): boolean {
    return Boolean(addOn);
}

// Phoenix experiment: suppress license-only plugin configuration restrictions.
export function isUnlicensedAddOn(requiredAddOn: string | undefined, license: ClientLicense | undefined): boolean {
    return Boolean(requiredAddOn && !licenseHasAddOn(license, requiredAddOn));
}
