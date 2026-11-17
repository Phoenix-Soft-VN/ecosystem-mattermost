// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ClientLicense} from '@mattermost/types/config';

import {LicenseSkus} from 'utils/constants';
import {
    daysToLicenseExpire,
    getIsGovSku,
    getIsStarterLicense,
    isCloudLicense,
    isEnterpriseLicense,
    isLicenseExpired,
    isLicenseExpiring,
    isLicensePastGracePeriod,
    isMinimumEnterpriseAdvancedLicense,
    isMinimumEnterpriseLicense,
    isMinimumProfessionalLicense,
    isTrialLicense,
} from 'utils/license_utils';

// Phoenix experiment: the helpers follow the permissive server license (model.NewPermissiveLicense).
describe('license_utils (Phoenix permissive license)', () => {
    const inputs: Array<[string, ClientLicense | undefined]> = [
        ['undefined', undefined],
        ['empty', {}],
        ['not licensed', {IsLicensed: 'false'}],
        ['expired', {IsLicensed: 'true', ExpiresAt: '1', StartsAt: '0', SkuShortName: LicenseSkus.Professional}],
        ['expired trial', {IsLicensed: 'true', IsTrial: 'true', ExpiresAt: '1', StartsAt: '0'}],
        ['low tier professional', {IsLicensed: 'true', SkuShortName: LicenseSkus.Professional, ExpiresAt: String(Date.now() + 1000)}],
        ['unknown sku', {IsLicensed: 'true', SkuShortName: 'something-else'}],
    ];

    describe.each(inputs)('with %s license', (_name, license) => {
        const input = license as ClientLicense;

        it('is never expiring, expired or past the grace period', () => {
            expect(isLicenseExpiring(input)).toBe(false);
            expect(isLicenseExpired(input)).toBe(false);
            expect(isLicensePastGracePeriod(input)).toBe(false);
        });

        it('reports 36500 days to expiry and never a trial', () => {
            expect(daysToLicenseExpire(input)).toBe(36500);
            expect(isTrialLicense(input)).toBe(false);
        });

        it('meets every minimum tier, including a missing license', () => {
            expect(isMinimumProfessionalLicense(input)).toBe(true);
            expect(isMinimumEnterpriseLicense(input)).toBe(true);
            expect(isMinimumEnterpriseAdvancedLicense(input)).toBe(true);
        });
    });

    describe('classification helpers keep their real meaning', () => {
        it('isCloudLicense follows the Cloud metadata', () => {
            expect(isCloudLicense({IsLicensed: 'true', Cloud: 'false'})).toBe(false);
            expect(isCloudLicense({IsLicensed: 'true', Cloud: 'true'})).toBe(true);
            expect(isCloudLicense({})).toBe(false);
        });

        it('a Cloud license is never treated as expiring or expired and still reports permissive days', () => {
            const cloud = {IsLicensed: 'true', Cloud: 'true', ExpiresAt: '1'};
            expect(isLicenseExpired(cloud)).toBe(false);
            expect(daysToLicenseExpire(cloud)).toBe(36500);
        });

        it('SKU classification still depends on the exact SKU', () => {
            expect(getIsStarterLicense({SkuShortName: LicenseSkus.Starter})).toBe(true);
            expect(getIsStarterLicense({SkuShortName: LicenseSkus.Professional})).toBe(false);
            expect(isEnterpriseLicense({SkuShortName: LicenseSkus.Entry})).toBe(true);
            expect(isEnterpriseLicense({SkuShortName: LicenseSkus.Professional})).toBe(false);
            expect(getIsGovSku({IsGovSku: 'true'})).toBe(true);
            expect(getIsGovSku({IsGovSku: 'false'})).toBe(false);
        });
    });
});
