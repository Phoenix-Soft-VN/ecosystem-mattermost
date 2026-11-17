// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Product} from '@mattermost/types/cloud';
import type {ClientLicense} from '@mattermost/types/config';

import {CloudProducts, LicenseSkus, SelfHostedProducts} from 'utils/constants';

// Phoenix experiment: the server (model.NewPermissiveLicense) always reports an unexpiring,
// fully entitled license. Expiry, trial and minimum-tier helpers follow it so the webapp never
// gates the UI on license state. Cloud/Entry/Starter/Gov classification helpers below keep
// their real meaning.
const PERMISSIVE_LICENSE_DAYS_TO_EXPIRE = 36500;

/* eslint-disable @typescript-eslint/no-unused-vars -- the license argument is intentionally ignored; callers keep their signatures */

export function isLicenseExpiring(_license: ClientLicense) {
    return false;
}

export function daysToLicenseExpire(_license: ClientLicense) {
    return PERMISSIVE_LICENSE_DAYS_TO_EXPIRE;
}

export function isLicenseExpired(_license: ClientLicense) {
    return false;
}

export function isLicensePastGracePeriod(_license: ClientLicense) {
    return false;
}

export function isTrialLicense(_license: ClientLicense) {
    return false;
}

/* eslint-enable @typescript-eslint/no-unused-vars */

export function isCloudLicense(license: ClientLicense) {
    return license?.Cloud === 'true';
}

export function getIsStarterLicense(license: ClientLicense) {
    return license?.SkuShortName === LicenseSkus.Starter;
}

export function getIsGovSku(license: ClientLicense) {
    return license?.IsGovSku === 'true';
}

export const isEnterpriseLicense = (license?: ClientLicense) => {
    switch (license?.SkuShortName) {
    case LicenseSkus.Enterprise:
    case LicenseSkus.E20:
    case LicenseSkus.EnterpriseAdvanced:
    case LicenseSkus.Entry:
        return true;
    }

    return false;
};

export const isNonEnterpriseLicense = (license?: ClientLicense) => !isEnterpriseLicense(license);

// Delegated Granular Administration is gated behind the LDAPGroups license feature and is
// not available on the Entry SKU. Mirrors the gating used by the Delegated Granular
// Administration screen in admin_definition (system_roles).
export const isLicensedForDelegatedAdministration = (license?: ClientLicense) => {
    return Boolean(license?.IsLicensed === 'true' && license.LDAPGroups === 'true' && license.SkuShortName !== LicenseSkus.Entry);
};

export const licenseSKUWithFirstLetterCapitalized = (license: ClientLicense) => {
    const sku = license.SkuShortName;
    return sku.charAt(0).toUpperCase() + sku.slice(1);
};

export function isEnterpriseOrCloudOrSKUStarterFree(license: ClientLicense, subscriptionProduct: Product | undefined, isEnterpriseReady: boolean) {
    const isCloud = license?.Cloud === 'true';
    const isCloudStarterFree = isCloud && subscriptionProduct?.sku === CloudProducts.STARTER;

    const isSelfHostedStarter = isEnterpriseReady && (license.IsLicensed === 'false');

    const isStarterSKULicense = license.IsLicensed === 'true' && license.SelfHostedProducts === SelfHostedProducts.STARTER;

    return isCloudStarterFree || isSelfHostedStarter || isStarterSKULicense;
}

/* eslint-disable @typescript-eslint/no-unused-vars -- the license argument is intentionally ignored; callers keep their signatures */
export function isMinimumProfessionalLicense(_license?: ClientLicense): boolean {
    return true;
}

export function isMinimumEnterpriseLicense(_license?: ClientLicense): boolean {
    return true;
}

export function isMinimumEnterpriseAdvancedLicense(_license?: ClientLicense): boolean {
    return true;
}
/* eslint-enable @typescript-eslint/no-unused-vars */
