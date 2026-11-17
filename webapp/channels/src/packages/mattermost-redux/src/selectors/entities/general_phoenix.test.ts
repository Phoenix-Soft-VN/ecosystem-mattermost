// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ClientLicense} from '@mattermost/types/config';
import type {GlobalState} from '@mattermost/types/store';

import {getLicense, isCloudLicense, isFreeEdition} from './general';

const stateWithLicense = (license?: ClientLicense) => ({
    entities: {general: {license}},
}) as unknown as GlobalState;

describe('Phoenix getLicense', () => {
    it.each([undefined, {}])('provides a valid Advanced license before license data is loaded (%p)', (input) => {
        const state = stateWithLicense(input);
        const license = getLicense(state);

        expect(license).toMatchObject({
            IsLicensed: 'true',
            SkuShortName: 'advanced',
            Users: '1000000',
            ExpiresAt: '4102444800000',
            IsTrial: 'false',
            IsNonProduction: 'true',
            Cloud: 'false',
        });
        expect(Object.values(license).every((value) => typeof value === 'string')).toBe(true);
        expect(isFreeEdition(state)).toBe(false);
        expect(isCloudLicense(state)).toBe(false);
    });

    it('replaces expired entry entitlements without mutating Redux state', () => {
        const input = Object.freeze({
            IsLicensed: 'false',
            SkuShortName: 'entry',
            ExpiresAt: '1',
            LDAP: 'false',
            Compliance: 'false',
        });
        const state = stateWithLicense(input);

        expect(getLicense(state)).toMatchObject({
            IsLicensed: 'true',
            SkuShortName: 'advanced',
            ExpiresAt: '4102444800000',
            LDAP: 'true',
            Compliance: 'true',
        });
        expect(isFreeEdition(state)).toBe(false);
        expect(input).toEqual({
            IsLicensed: 'false',
            SkuShortName: 'entry',
            ExpiresAt: '1',
            LDAP: 'false',
            Compliance: 'false',
        });
    });

    it.each(['false', 'true'])('preserves Cloud=%s and other classification metadata', (cloud) => {
        const state = stateWithLicense({Cloud: cloud, IsGovSku: 'true', AddOns: 'crossguard', CustomMetadata: 'retained'});

        expect(getLicense(state)).toMatchObject({Cloud: cloud, IsGovSku: 'true', AddOns: 'crossguard', CustomMetadata: 'retained'});
        expect(isCloudLicense(state)).toBe(cloud === 'true');
    });

    it('keeps its reference across unrelated state changes and updates on new license data', () => {
        const source = {Cloud: 'false'};
        const initial = getLicense(stateWithLicense(source));

        expect(getLicense(stateWithLicense(source))).toBe(initial);

        const updatedState = stateWithLicense({Cloud: 'true'});
        expect(getLicense(updatedState)).not.toBe(initial);
        expect(isCloudLicense(updatedState)).toBe(true);
        expect(initial.Cloud).toBe('false');
    });
});
