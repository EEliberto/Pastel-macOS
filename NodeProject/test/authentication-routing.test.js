import test from 'node:test';
import assert from 'node:assert/strict';
import plist from 'plist';
import {
    normalizeAuthenticationEndpoint,
    parseLoginResponse,
    storePasswordAuthenticate,
} from '../src/gsa.js';

const path = '/WebObjects/MZFinance.woa/wa/authenticate';
const endpoint = `https://buy.itunes.apple.com${path}`;
const pod = `https://p7-buy.itunes.apple.com${path}`;
const response = (data) => ({status: 200, headers: '', body: Buffer.from(plist.build(data))});
const success = () => response({passwordToken: 'test-token', dsPersonId: '123'});

test('normalizes bag paths without changing the host or routing query', () => {
    for (const host of ['buy.itunes.apple.com', 'p7-buy.itunes.apple.com', 'buy.itunes.apple.com:443']) {
        for (const suffix of ['', '/']) {
            const normalized = normalizeAuthenticationEndpoint(`https://${host}${path}${suffix}?Pod=7&routing=a%2Fb+c`);
            assert.equal(normalized, `https://${host.replace(':443', '')}${path}/?Pod=7&routing=a%2Fb+c`);
        }
    }
});

test('rejects unsupported bag destinations before sending credentials', async () => {
    for (const value of [
        undefined, '', `${endpoint}//`, `${endpoint}/extra`, `${endpoint}%2f`,
        'https://auth.itunes.apple.com/auth/v1/native/fast/',
        `http://buy.itunes.apple.com${path}`, `https://buy.itunes.apple.com:8443${path}`,
        `https://buy.itunes.apple.com.evil.test${path}`, `https://evil-buy.itunes.apple.com${path}`,
        `https://user:secret@buy.itunes.apple.com${path}`, `${endpoint}#fragment`,
        `https://buy.itunes.apple.com\\@evil.test${path}`,
    ]) {
        let requests = 0;
        await assert.rejects(storePasswordAuthenticate('user', 'password', '', 'guid', 'jar', value, {}, () => {
            requests++;
            return success();
        }), /Invalid Apple Store authentication endpoint/);
        assert.equal(requests, 0);
    }
});

for (const status of [301, 302, 307, 308]) {
    test(`${status} preserves the retry payload, verification code, cookie jar and signer`, async () => {
        const signer = {};
        const requests = [];
        const result = await storePasswordAuthenticate('user@example.test', ' p a s s ', '12 34 56', 'guid', 'session-jar', endpoint, signer,
            async (url, body, jar, session) => {
                requests.push({url, body});
                assert.equal(jar, 'session-jar');
                assert.equal(session, signer);
                if (requests.length === 1) return response({failureType: '-5000'});
                if (requests.length === 2) return {status, headers: `Location: ${pod}?routing=a%2Fb+c\r\n`, body: Buffer.alloc(0)};
                return success();
            });
        assert.equal(result.parsed.passwordToken, 'test-token');
        assert.equal(requests.length, 3);
        assert.equal(requests[0].url, `${endpoint}/`);
        assert.equal(requests[1].url, `${endpoint}/`);
        // Preserve server-provided path exactly, including the absence of a slash.
        assert.equal(requests[2].url, `${pod}?routing=a%2Fb+c`);
        assert.equal(requests[1].body, requests[2].body);
        assert.equal(plist.parse(requests[2].body).attempt, '2');
        assert.equal(plist.parse(requests[2].body).password, ' p a s s 123456');
    });
}

test('resolves relative and pod redirects without consuming credential attempts', async () => {
    const urls = [`${endpoint}/`, endpoint, pod, `${pod}/`, `${pod}/?routing=opaque`];
    const locations = [path, `//p7-buy.itunes.apple.com${path}`, 'authenticate/', '?routing=opaque'];
    let calls = 0;
    let originalBody;
    await storePasswordAuthenticate('user', 'password', '', 'guid', 'jar', endpoint, {}, async (url, body) => {
        assert.equal(url, urls[calls]);
        originalBody ??= body;
        assert.equal(body, originalBody);
        assert.equal(plist.parse(body).attempt, '1');
        const location = locations[calls++];
        return location ? {status: 307, headers: `location: ${location}\r\n`, body: Buffer.alloc(0)} : success();
    });
    assert.equal(calls, 5);
});

test('retries the first credential failure at the assigned pod', async () => {
    const requests = [];
    await storePasswordAuthenticate('user', 'password', '', 'guid', 'jar', endpoint, {}, async (url, body) => {
        requests.push({url, body});
        if (requests.length === 1) return {status: 302, headers: `Location: ${pod}/\r\n`, body: Buffer.alloc(0)};
        if (requests.length === 2) return response({failureType: '-5000'});
        return success();
    });
    assert.deepEqual(requests.map(x => x.url), [`${endpoint}/`, `${pod}/`, `${pod}/`]);
    assert.deepEqual(requests.map(x => plist.parse(x.body).attempt), ['1', '1', '2']);
});

test('stops redirect loops after four redirects without exposing response secrets', async () => {
    let requests = 0;
    await assert.rejects(storePasswordAuthenticate('user', 'password', '', 'guid', 'jar', endpoint, {}, async () => {
        requests++;
        return {status: 301, headers: `Location: ${pod}?token=secret-query\r\nSet-Cookie: secret-cookie\r\n`, body: Buffer.from('secret-body')};
    }), error => {
        assert.match(error.message, /too many authentication redirects/);
        assert.doesNotMatch(error.message, /secret/);
        return true;
    });
    assert.equal(requests, 5);
});

test('rejects invalid redirect destinations before sending another signed request', async () => {
    for (const location of [
        `http://p7-buy.itunes.apple.com${path}`,
        `https://evil.test${path}?token=secret`,
        `${pod}/other`, `${pod}%2f`, `${pod}#secret`,
        `https://user:secret@p7-buy.itunes.apple.com${path}`,
    ]) {
        let requests = 0;
        await assert.rejects(storePasswordAuthenticate('user', 'password', '', 'guid', 'jar', endpoint, {}, async () => {
            requests++;
            return {status: 302, headers: `Location: ${location}\r\n`, body: Buffer.alloc(0)};
        }), error => {
            assert.match(error.message, /invalid authentication redirect/);
            assert.doesNotMatch(error.message, /secret|evil\.test/);
            return true;
        });
        assert.equal(requests, 1);
    }
});

test('reports unsupported and missing redirect headers without leaking response content', () => {
    for (const status of [301, 302, 303, 307, 308]) {
        const result = parseLoginResponse({status, headers: 'Location:\r\nSet-Cookie: secret-cookie\r\n', body: Buffer.from('secret-body')}, 1, '', endpoint);
        assert.equal(result.retry, false);
        assert.match(result.error.message, new RegExp(`HTTP ${status}`));
        assert.match(result.error.message, /unsupported authentication redirect|missing Location/);
        assert.doesNotMatch(result.error.message, /secret/);
    }
});

test('stops after a second credential rejection instead of probing other endpoints', async () => {
    let requests = 0;
    await assert.rejects(storePasswordAuthenticate('user', 'password', '', 'guid', 'jar', endpoint, {}, async () => {
        requests++;
        return response({failureType: '-5000', customerMessage: 'Invalid credentials'});
    }), /Invalid credentials/);
    assert.equal(requests, 2);
});

test('keeps the ambiguous password/2FA challenge after a redirect', async () => {
    let requests = 0;
    await assert.rejects(storePasswordAuthenticate('user', 'password', '', 'guid', 'jar', endpoint, {}, async () => {
        if (++requests === 1) return {status: 308, headers: `Location: ${pod}/\r\n`, body: Buffer.alloc(0)};
        return response({customerMessage: 'MZFinance.BadLogin.Configurator_message'});
    }), {code: 'AUTH_OR_2FA'});
    assert.equal(requests, 2);
});

test('reports HTTP failures without treating empty or HTML responses as login success', () => {
    for (const [status, body] of [[204, ''], [404, '<html>secret-body</html>']]) {
        const result = parseLoginResponse({status, headers: '', body: Buffer.from(body)}, 1, '', endpoint);
        assert.match(result.error.message, new RegExp(`HTTP ${status}`));
        assert.doesNotMatch(result.error.message, /secret/);
        assert.equal(result.retry, false);
    }
});
