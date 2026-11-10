import test from 'node:test';
import assert from 'node:assert/strict';
import {Ipa} from '../src/ipa.js';

test('native preparation returns private authorization without starting a transfer', async () => {
    const ipa = Object.create(Ipa.prototype);
    ipa.creds = {APPLE_ID: 'fixture@example.invalid'};
    ipa.helperApp = async () => ({id: 123, price: 0});
    const calls = [];
    const plan = {url: 'https://example.invalid/package', payload: 'private-fixture'};
    ipa.helperRequest = async (command, input) => {
        calls.push({command, input});
        return {nativePlan: plan, account: {email: 'fixture@example.invalid'}};
    };
    const result = await ipa.runDownload({dir: '/tmp', APPID: '123', appVerId: '456', prepareOnly: true});
    assert.deepEqual(calls.map(call => call.command), ['prepareNative']);
    assert.equal(calls[0].input.versionId, '456');
    assert.equal(result.nativePlan, plan);
    assert.equal(result.ok, true);
    assert.equal(ipa.out, undefined);
});
