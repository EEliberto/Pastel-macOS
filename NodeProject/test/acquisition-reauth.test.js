import test from 'node:test';
import assert from 'node:assert/strict';
import {Ipa} from '../src/ipa.js';

for (const usedCachedSession of [true, false]) {
    test(`explicit token expiry refreshes once after metadata success (cached=${usedCachedSession})`, async () => {
        let refreshed = 0, attempts = 0;
        const client = {usedCachedSession, async login(options) {assert.equal(options.force, true); refreshed++;}, async persistCurrentSession() {}};
        const result = await Ipa.prototype._withReauth.call(client, async () => {
            if (++attempts === 1) throw Object.assign(new Error('password token is expired'), {code: 'TOKEN_EXPIRED'});
            return 'downloaded';
        });
        assert.equal(result, 'downloaded');
        assert.equal(refreshed, 1);
        assert.equal(attempts, 2);
    });
}
test('persistent purchase rejection stops after one refresh', async () => {
    let refreshed = 0, attempts = 0;
    const error = Object.assign(new Error('password token is expired'), {code: 'TOKEN_EXPIRED'});
    const client = {usedCachedSession: false, async login() {refreshed++;}, async persistCurrentSession() {}};
    await assert.rejects(Ipa.prototype._withReauth.call(client, async () => {attempts++;throw error;}), e => e === error);
    assert.equal(refreshed, 1); assert.equal(attempts, 2);
});
test('missing license does not trigger login', async () => {
    const error = Object.assign(new Error('license is required'), {code:'LICENSE_REQUIRED'});
    const client = {usedCachedSession:true, async login() {assert.fail('unexpected login');}};
    await assert.rejects(Ipa.prototype._withReauth.call(client, async () => {throw error;}), e => e === error);
});
