// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ClientLicense} from '@mattermost/types/config';

import {LicenseSkus} from 'utils/constants';

import {it as check} from './admin_definition_helpers';

// Phoenix experiment: licensed, licensedForFeature (non-Cloud) and minLicenseTier never deny based on
// the client license; Cloud and exact-SKU classification keeps its real meaning.
describe('AdminDefinitionHelpers license checks (Phoenix permissive license)', () => {
    const noLicenseInputs: Array<[string, ClientLicense | undefined]> = [
        ['undefined', undefined],
        ['not licensed', {IsLicensed: 'false'}],
        ['low tier professional', {IsLicensed: 'true', SkuShortName: LicenseSkus.Professional, LDAP: 'false'}],
        ['expired', {IsLicensed: 'true', ExpiresAt: '1', SkuShortName: LicenseSkus.Starter, LDAP: 'false'}],
    ];

    describe.each(noLicenseInputs)('with %s license', (_name, license) => {
        it('licensed, licensedForFeature and minLicenseTier are enabled', () => {
            expect(check.licensed({}, {}, license)).toBe(true);
            expect(check.licensedForFeature('LDAP')({}, {}, license)).toBe(true);
            expect(check.minLicenseTier(LicenseSkus.Enterprise)({}, {}, license)).toBe(true);
            expect(check.minLicenseTier(LicenseSkus.EnterpriseAdvanced)({}, {}, license)).toBe(true);
        });
    });

    it('Cloud classification is real: Cloud=false is not a Cloud license, Cloud=true is', () => {
        expect(check.cloudLicensed({}, {}, {IsLicensed: 'true', Cloud: 'false'})).toBe(false);
        expect(check.cloudLicensed({}, {}, {IsLicensed: 'true', Cloud: 'true'})).toBe(true);
        expect(check.cloudLicensed({}, {}, undefined)).toBe(false);
    });

    it('licensedForFeature("Cloud") keeps the real Cloud classification', () => {
        const cloudFeature = check.licensedForFeature('Cloud');
        expect(cloudFeature({}, {}, undefined)).toBe(false);
        expect(cloudFeature({}, {}, {IsLicensed: 'true', Cloud: 'false'})).toBe(false);
        expect(cloudFeature({}, {}, {IsLicensed: 'true', Cloud: 'true'})).toBe(true);
    });

    it('every non-Cloud feature is enabled independently of the license, including on a Cloud license', () => {
        const cloud = {IsLicensed: 'true', Cloud: 'true', LDAP: 'false', MFA: 'false'};
        expect(check.licensedForFeature('LDAP')({}, {}, cloud)).toBe(true);
        expect(check.licensedForFeature('MFA')({}, {}, cloud)).toBe(true);
        expect(check.licensedForFeature('LDAP')({}, {}, {IsLicensed: 'true', Cloud: 'false', LDAP: 'false'})).toBe(true);
        expect(check.licensedForFeature('SAML')({}, {}, undefined)).toBe(true);
    });

    it('exact-SKU classification is not widened, so Entry/Starter do not enable conflicting modes', () => {
        expect(check.licensedForSku(LicenseSkus.Entry)({}, {}, {IsLicensed: 'true', SkuShortName: LicenseSkus.Entry})).toBe(true);
        expect(check.licensedForSku(LicenseSkus.Entry)({}, {}, {IsLicensed: 'true', SkuShortName: LicenseSkus.EnterpriseAdvanced})).toBe(false);
        expect(check.licensedForSku(LicenseSkus.Starter)({}, {}, undefined)).toBe(false);
        expect(check.isFreeEdition({}, {}, {IsLicensed: 'true', SkuShortName: LicenseSkus.Entry})).toBe(true);
        expect(check.isFreeEdition({}, {}, {IsLicensed: 'true', SkuShortName: LicenseSkus.EnterpriseAdvanced})).toBe(false);
        expect(check.licensedForCloudStarter({}, {}, {IsLicensed: 'true', Cloud: 'false', SkuShortName: LicenseSkus.Starter})).toBe(false);
        expect(check.licensedForCloudStarter({}, {}, {IsLicensed: 'true', Cloud: 'true', SkuShortName: LicenseSkus.Starter})).toBe(true);
    });
});
