import test from 'node:test';
import assert from 'node:assert/strict';
import {appInfoWithCurrentVersionFallback} from '../src/ipa.js';

test('pins the current App Store version after an empty unpinned redownload', async () => {
    const calls = [];
    const response = {songList: [{metadata: {softwareVersionExternalIdentifier: '123'}}]};
    const result = await appInfoWithCurrentVersionFallback({
        appId: '333206289',
        country: 'hk',
        listVersions: true,
        appInfo: async (appId, versionId, _auth, options) => {
            calls.push({appId, versionId, options});
            if (!versionId) {
                throw Object.assign(new Error('Empty Apple redownload response'), {
                    code: 'EMPTY_REDOWNLOAD_RESPONSE',
                });
            }
            return response;
        },
        resolveCurrentVersion: async (appId, options) => {
            calls.push({resolve: appId, options});
            return {latestVersionId: '123'};
        },
    });

    assert.equal(result, response);
    assert.deepEqual(calls, [
        {appId: '333206289', versionId: '', options: {listVersions: true}},
        {resolve: '333206289', options: {country: 'hk'}},
        {appId: '333206289', versionId: '123', options: {listVersions: true}},
    ]);
});

test('does not replace an explicitly requested historical version', async () => {
    const expected = Object.assign(new Error('empty'), {code: 'EMPTY_REDOWNLOAD_RESPONSE'});
    await assert.rejects(appInfoWithCurrentVersionFallback({
        appId: '333206289',
        appVerId: '999',
        appInfo: async () => { throw expected; },
        resolveCurrentVersion: async () => { throw new Error('must not resolve'); },
    }), error => error === expected);
});
