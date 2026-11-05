import test from 'node:test';
import assert from 'node:assert/strict';
import {Ipa} from '../src/ipa.js';

test('both direct download and acquisition retry forward the no-update option', async t => {
    const previousFlag = process.env.IPA_REMOVE_APP_STORE_UPDATE_METADATA;
    const previousAcquire = process.env.IPA_ALLOW_APP_ACQUIRE;
    t.after(() => {
        for (const [key, value] of [['IPA_REMOVE_APP_STORE_UPDATE_METADATA', previousFlag], ['IPA_ALLOW_APP_ACQUIRE', previousAcquire]]) {
            if (value === undefined) delete process.env[key]; else process.env[key] = value;
        }
    });
    for (const flag of ['0', '1']) {
        for (const acquisition of [false, true]) {
            process.env.IPA_REMOVE_APP_STORE_UPDATE_METADATA = flag;
            process.env.IPA_ALLOW_APP_ACQUIRE = '1';
            const ipa = Object.create(Ipa.prototype);
            const calls = [];
            ipa.helperApp = async () => ({trackId: 123, price: 0});
            ipa.helperRequest = async (command, input) => {
                calls.push({command, input});
                if (acquisition && calls.length === 1) throw Object.assign(new Error('license'), {code: 'LICENSE_REQUIRED'});
                return {download: {DestinationPath: '/tmp/fixture.ipa'}};
            };
            await ipa.runDownload({dir: '/tmp', APPID: '123', appVerId: '456'});
            const downloads = calls.filter(call => call.command === 'download');
            assert.equal(downloads.length, acquisition ? 2 : 1);
            for (const {input} of downloads) {
                assert.equal(input.removeAppStoreUpdateMetadata, flag === '1');
                assert.equal(input.versionId, '456');
            }
        }
    }
});
