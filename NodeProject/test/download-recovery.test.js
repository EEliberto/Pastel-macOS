import test from 'node:test';
import assert from 'node:assert/strict';
import {recoverRedownload, validateUpdateResponse, validatedUpdateEndpoint, shouldTryRedownload} from '../src/download-recovery.js';

const endpoint = 'https://downloaddispatch.itunes.apple.com/up/updateProduct';
const unavailable = {_httpStatus: 200, customerMessage: '“Alipay” No Longer Available'};
const success = () => ({_httpStatus: 200, songList: [{URL: 'https://example.com/app.ipa', metadata: {
    itemId: 333206289, softwareVersionExternalIdentifier: 891383286, softwareVersionBundleId: 'com.alipay.iphoneclient',
}}]});
async function run(response, versionID = '891383286') {
    const calls = [];
    const result = await recoverRedownload({appIdentifier: '333206289', versionID,
        loadBag: () => {calls.push('bag'); return {updateProduct: endpoint};},
        request: async (kind, url) => {
            calls.push(kind);
            if (kind === 'update') {assert.equal(url, endpoint); return success();}
            if (response instanceof Error) throw response;
            return response;
        },
    });
    return {result, calls};
}
test('recovers message-only unavailability without changing the requested version', async () => {
    const {result, calls} = await run(unavailable);
    assert.deepEqual(calls, ['redownload', 'bag', 'update']);
    assert.equal(result.songList[0].metadata.softwareVersionExternalIdentifier, 891383286);
});
test('recovers only the empty redownload HTTP 500 transport error', async () => {
    const error = Object.assign(new Error('empty'), {code: 'EMPTY_REDOWNLOAD_RESPONSE'});
    assert.deepEqual((await run(error)).calls, ['redownload', 'bag', 'update']);
    await assert.rejects(run(Object.assign(new Error('expired'), {code: 'TOKEN_EXPIRED'})), /expired/);
    await assert.rejects(run(Object.assign(new Error('bad plist'), {code: 'STORE_FAIL'})), /bad plist/);
});
test('does not mask license, auth, busy, other messages or successful downloads', async () => {
    for (const response of [success(), {...unavailable, failureType: '9610'}, {...unavailable, failureType: '2034'},
        {...unavailable, failureType: '2059'}, {...unavailable, customerMessage: 'Confirm your age'},
        {...unavailable, _httpStatus: 500}]) {
        const {result, calls} = await run(response);
        assert.equal(result, response);
        assert.deepEqual(calls, ['redownload']);
    }
});
test('never uses updateProduct without a version pin', async () => {
    assert.deepEqual((await run(unavailable, '')).calls, ['redownload']);
});
test('rejects wrong app, version, bundle metadata and malformed update responses', () => {
    for (const mutate of [r => r.songList[0].metadata.itemId = 42,
        r => r.songList[0].metadata.softwareVersionExternalIdentifier = 888,
        r => delete r.songList[0].metadata.softwareVersionBundleId,
        r => delete r.songList[0].URL, r => r.songList.push(r.songList[0]), r => r._httpStatus = 500]) {
        const response = success(); mutate(response);
        assert.throws(() => validateUpdateResponse(response, '333206289', '891383286'), /does not match/);
    }
});
test('rejects redirects in bag endpoint spelling before sending credentials', () => {
    for (const value of ['http://downloaddispatch.itunes.apple.com/up/updateProduct', endpoint + '?x=1',
        endpoint + '#x', endpoint.replace('apple.com', 'apple.com.example.com'), undefined]) {
        assert.throws(() => validatedUpdateEndpoint(value), /Invalid/);
    }
});
test('missing update endpoint preserves the original response', async () => {
    assert.equal(await recoverRedownload({appIdentifier: '333206289', versionID: '891383286',
        request: async () => unavailable, loadBag: () => ({})}), unavailable);
});

test('primary fallback retains explicit failures and unrelated customer messages', () => {
    assert.equal(shouldTryRedownload(unavailable), true);
    assert.equal(shouldTryRedownload({_httpStatus: 200}), true);
    assert.equal(shouldTryRedownload({_httpStatus: 200, failureType: '5002'}), true);
    for (const failureType of ['9610', '2059', '2034', '2042']) {
        assert.equal(shouldTryRedownload({...unavailable, failureType}), false);
    }
    assert.equal(shouldTryRedownload({...unavailable, customerMessage: 'Confirm your age'}), false);
    assert.equal(shouldTryRedownload(success()), false);
});
