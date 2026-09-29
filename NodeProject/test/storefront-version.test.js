import test from 'node:test';
import assert from 'node:assert/strict';
import {extractPlatformVersion, extractStorefrontVersionId} from '../src/catalog.js';

test('extracts the matching App Store external version identifier', () => {
    const html = String.raw`
        <script>{"buyParams":"productType=C&price=0&salableAdamId=6477489729&pricingParameters=STDQ&appExtVrsId=890788137"}</script>
        <script>{"buyParams":"productType=C&salableAdamId=123456789&appExtVrsId=111222333"}</script>
    `;
    assert.equal(extractStorefrontVersionId(html, '6477489729'), '890788137');
});

test('decodes escaped ampersands in serialized storefront data', () => {
    const html = String.raw`{"buyParams":"productType=C\u0026salableAdamId=42\u0026appExtVrsId=99887766"}`;
    assert.equal(extractStorefrontVersionId(html, '42'), '99887766');
});

test('reads the current external version from the Apple platform catalog', () => {
    assert.deepEqual(extractPlatformVersion({results: {'6448311069': {
        name: 'ChatGPT',
        offers: [{version: {display: '1.2026.265', externalId: 891874656}}],
    }}}, '6448311069'), {
        appId: '6448311069',
        name: 'ChatGPT',
        latestVersion: '1.2026.265',
        latestVersionId: '891874656',
        versionIds: ['891874656'],
        fallbackCurrentOnly: true,
    });
});

test('falls back to buyParams in Apple platform catalog offers', () => {
    assert.equal(extractPlatformVersion({results: {'1': {
        offers: [{buyParams: 'salableAdamId=1&appExtVrsId=456'}],
    }}}, '1').latestVersionId, '456');
});
