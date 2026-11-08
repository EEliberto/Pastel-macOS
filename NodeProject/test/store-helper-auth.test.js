import assert from 'node:assert/strict';
import test from 'node:test';
import {mkdtemp, writeFile, readFile, rm} from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import {runStoreHelper, storeAuthenticationError} from '../src/store-helper.js';
import {Store} from '../src/client.js';
import {Ipa} from '../src/ipa.js';

for (const code of ['AUTH_FAILED', 'AUTH_SERVICE_ERROR', 'AUTH_STATE_FAILED', 'ACCOUNT_DISABLED', 'INVALID_CREDENTIALS']) {
    test(`${code} never becomes a 2FA challenge`, () => {
        const error = storeAuthenticationError(Object.assign(new Error('specific upstream failure'), {code}));
        assert.equal(error.code, code);
        assert.doesNotMatch(error.message, /\[2FA\]|登录与安全性|Sign-In & Security/);
        if (code !== 'INVALID_CREDENTIALS') assert.match(error.message, /specific upstream failure/);
    });
}
for (const code of ['AUTH_CODE_REQUIRED', 'AUTH_CODE_INVALID', 'AUTH_VERIFICATION_FAILED']) {
    test(`${code} supports entering or retrying the verification code`, () => {
        const error = storeAuthenticationError(Object.assign(new Error('challenge'), {code}));
        assert.equal(error.code, 'NEEDS_2FA');
        assert.match(error.message, /\[2FA\]/);
        assert.doesNotMatch(error.message, /登录与安全性|Sign-In & Security/);
    });
}

test('login and a later helper reuse encrypted account/cookies and pass the key only on stdin', async () => {
    const directory = await mkdtemp(path.join(os.tmpdir(), 'pastel-helper-test-'));
    const helper = path.join(directory, 'helper');
    const trace = path.join(directory, 'trace.jsonl');
    const previous = {helper: process.env.PASTEL_STORE_HELPER, session: process.env.IPA_SESSION_DIR};
    const key = Buffer.alloc(32, 7).toString('base64');
    await writeFile(helper, `#!${process.execPath}
import fs from 'node:fs';
let input='';
for await (const chunk of process.stdin) input+=chunk;
const req=JSON.parse(input);
fs.appendFileSync(${JSON.stringify(trace)}, JSON.stringify({req, argv:process.argv.slice(2)})+'\\n');
if (req.password==='wrong-password') {
 console.log(JSON.stringify({ok:false,code:'INVALID_CREDENTIALS',message:'credential rejection'}));
 process.exit(1);
}
const account=req.account || {email:req.email,passwordToken:'test-token',directoryServicesIdentifier:'123',storeFront:'143465-19,32',pod:'7'};
const cookieState=req.cookieState || [{origin:'https://auth.itunes.apple.com/',cookie:{Name:'session',Value:'cookie-secret',Path:'/',Secure:true}}];
console.log(JSON.stringify({ok:true,account,cookieState,versions:{ExternalVersionIdentifiers:[123]}}));
`, {mode: 0o700});
    try {
        process.env.PASTEL_STORE_HELPER = helper;
        process.env.IPA_SESSION_DIR = directory;
        await assert.rejects(Store.login('user@example.test', 'wrong-password', '', null, key), {code:'INVALID_CREDENTIALS'});
        const credentials = {APPLE_ID:'user@example.test', PASSWORD:'test-password', SESSION_KEY:key};
        const first = new Ipa(credentials);
        await first.login({force:true});
        const encrypted = await readFile(first.sessionFile, 'utf8');
        assert.doesNotMatch(encrypted, /test-token|cookie-secret|test-password/);
        const second = new Ipa(credentials);
        await second.helperRequest('listVersions', {app:{trackId:123},platform:'iphone'});
        const calls=(await readFile(trace,'utf8')).trim().split('\n').map(JSON.parse);
        assert.deepEqual(calls.map(x=>x.req.command), ['login','login','listVersions']);
        const request=calls.at(-1).req;
        assert.equal(request.account.passwordToken,'test-token');
        assert.equal(request.cookieState[0].cookie.Value,'cookie-secret');
        assert.equal(request.sessionKey,key);
        assert.deepEqual(calls.at(-1).argv, []);
        assert.equal((await second.loadSession()).frameworkAccount.password, undefined);
    } finally {
        for (const [name,value] of [['PASTEL_STORE_HELPER',previous.helper],['IPA_SESSION_DIR',previous.session]]) {
            if(value === undefined) delete process.env[name]; else process.env[name]=value;
        }
        await rm(directory,{recursive:true,force:true});
    }
});


test('stalled helpers time out and are killed even if they ignore cancellation', async () => {
    const directory = await mkdtemp(path.join(os.tmpdir(), 'pastel-timeout-test-'));
    const helper = path.join(directory, 'helper');
    const pidFile = path.join(directory, 'pid');
    const previous = process.env.PASTEL_STORE_HELPER;
    await writeFile(helper, `#!${process.execPath}
import fs from 'node:fs';
fs.writeFileSync(${JSON.stringify(pidFile)}, String(process.pid));
process.on('SIGTERM',()=>{});
process.stderr.write('@@PASTEL:pha');
setTimeout(()=>process.stderr.write('se=authenticating\\n'),5);
setInterval(()=>{},1000);
`, {mode:0o700});
    try {
        process.env.PASTEL_STORE_HELPER = helper;
        const phases=[];
        await assert.rejects(runStoreHelper({}, {
            onPhase: phase => phases.push(phase), timeouts:{waiting:2000,authenticating:50},
        }), {code:'STORE_TIMEOUT'});
        assert.deepEqual(phases,['authenticating']);
        const pid=Number(await readFile(pidFile,'utf8'));
        assert.throws(()=>process.kill(pid,0), {code:'ESRCH'});
    } finally {
        if (previous===undefined) delete process.env.PASTEL_STORE_HELPER; else process.env.PASTEL_STORE_HELPER=previous;
        await rm(directory,{recursive:true,force:true});
    }
});
