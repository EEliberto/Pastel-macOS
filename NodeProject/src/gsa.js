// Apple StoreServices 登录：通过持久 SAP 会话签名，使用当前 Apple bag 的认证地址。
// 地址规范化和重定向处理参考 majd/ipatool PR #603、#609：
// https://github.com/majd/ipatool/pull/603
// https://github.com/majd/ipatool/pull/609
// 重定向保留同一份 plist、验证码和 credential attempt；仅首次 -5000 才递增 attempt。
//
// HTTP 走系统 curl（避免 Node 自带 CA 在 TLS 解密代理下失败）。
import crypto from 'crypto';
import os from 'os';
import path from 'path';
import {execFileSync, spawn} from 'child_process';
import {writeFileSync, readFileSync, existsSync, mkdtempSync, rmSync} from 'fs';
import {fileURLToPath} from 'url';
import readline from 'readline';
import plist from 'plist';
import {t} from './i18n.js';

// ---- 错误工厂 ----
function ambiguousAuthError() {
    const e = new Error(t('auth_or_2fa'));
    e.code = 'AUTH_OR_2FA';
    return e;
}

function transientAuthError(message = t('store_token_failed')) {
    const e = new Error(message);
    e.code = 'AUTH_TRANSIENT';
    return e;
}

function sapUnavailableError(message) {
    const e = new Error(message);
    e.code = 'SAP_UNAVAILABLE';
    return e;
}

// ---- 常量 ----
const CURL = '/usr/bin/curl';
const SCUTIL = '/usr/sbin/scutil';
const STORE_UA = 'Configurator/2.17 (Macintosh; OS X 15.2; 24C5089c) AppleWebKit/0620.1.16.11.6';

// 对齐 ipatool-sapfix/pkg/appstore/constants.go
const FAILURE_TYPE_INVALID_CREDENTIALS = '-5000'; // 对应 FailureTypeInvalidCredentials
const CUSTOMER_MESSAGE_BAD_LOGIN       = 'MZFinance.BadLogin.Configurator_message';
const CUSTOMER_MESSAGE_ACCOUNT_DISABLED = 'Your account is disabled.';

// 对齐 ipatool-sapfix/pkg/http/constants.go
const HEADER_SAP_SIGNATURE = 'X-Apple-ActionSignature';

const AUTH_PATH = '/WebObjects/MZFinance.woa/wa/authenticate';
const MAX_AUTH_REDIRECTS = 4;
// Authentication runs behind an interactive loading state. Long upstream
// backoffs leave the UI spinning before it can ask the user to sign in again.
const AUTH_RETRY_DELAYS_MS = [2_000, 5_000];

// SAP signer 路径
const MODULE_DIR = path.dirname(fileURLToPath(import.meta.url));
const DEFAULT_SAP_SIGNER = path.resolve(MODULE_DIR, '..', '..', 'sap-signer');

// ---- 临时目录 ----
let _tmpDir = null;
function tmpDir() {
    if (!_tmpDir) _tmpDir = mkdtempSync(path.join(os.tmpdir(), 'ipa-gsa-'));
    return _tmpDir;
}
export function cleanup() {
    if (_tmpDir) { rmSync(_tmpDir, {recursive: true, force: true}); _tmpDir = null; }
}

// ---- 系统代理 ----
function systemProxy() {
    try {
        const out = execFileSync(SCUTIL, ['--proxy'], {timeout: 5000}).toString();
        const httpsOn = /HTTPSEnable\s*:\s*1/.test(out);
        const host = (out.match(/HTTPSProxy\s*:\s*(\S+)/) || [])[1];
        const port = (out.match(/HTTPSPort\s*:\s*(\d+)/) || [])[1];
        if (httpsOn && host && port) return `http://${host}:${port}`;
    } catch { /* ignore */ }
    return process.env.HTTPS_PROXY || process.env.https_proxy || '';
}

export const STORE_USER_AGENT = STORE_UA;

// ---- 通用 curl 请求 ----
// jar：cookie 文件路径，传入则读写 cookie（authenticate 与后续下载/购买共享会话）。
export function curlRequest(method, url, {headers = {}, body = null, follow = false, timeout = 30, jar = null} = {}) {
    const dir = tmpDir();
    const outFile = path.join(dir, `out-${crypto.randomBytes(4).toString('hex')}.bin`);
    const hdrFile = path.join(dir, `hdr-${crypto.randomBytes(4).toString('hex')}.txt`);
    const args = ['-s', '-m', String(timeout), '-X', method, url,
        '-o', outFile, '-D', hdrFile, '-w', '%{http_code}'];
    if (jar) args.push('-b', jar, '-c', jar);
    if (follow) args.push('-L', '--post302');
    const proxy = systemProxy();
    if (proxy) args.push('--proxy', proxy);
    for (const [k, v] of Object.entries(headers)) args.push('-H', `${k}: ${v}`);
    if (body !== null) {
        const bodyFile = path.join(dir, `body-${crypto.randomBytes(4).toString('hex')}.bin`);
        writeFileSync(bodyFile, body);
        args.push('--data-binary', `@${bodyFile}`);
    }
    let status = '000';
    try { status = execFileSync(CURL, args, {maxBuffer: 64 * 1024 * 1024, timeout: (timeout + 5) * 1000}).toString().trim(); }
    catch { status = '000'; }
    const respBody = existsSync(outFile) ? readFileSync(outFile) : Buffer.alloc(0);
    const respHdrs = existsSync(hdrFile) ? readFileSync(hdrFile, 'utf8') : '';
    return {status: Number(status), headers: respHdrs, body: respBody};
}

function headerValue(rawHeaders, name) {
    const m = rawHeaders.match(new RegExp(`^${name}:[^\\S\\r\\n]*([^\\r\\n]*)`, 'im'));
    return m ? m[1].trim() : '';
}

// ---- SAP 签名（对齐 mescal.Sign） ----
function sapSignerPath() {
    return process.env.IPA_SAP_SIGNER || DEFAULT_SAP_SIGNER;
}

function signAppleAction(bodyBytes) {
    const signer = sapSignerPath();
    if (!existsSync(signer)) {
        throw sapUnavailableError(`缺少 Apple SAP 签名组件：${signer}`);
    }
    try {
        const output = execFileSync(signer, [], {
            input: bodyBytes,
            encoding: 'utf8',
            maxBuffer: 1024 * 1024,
            timeout: 35_000,
        }).trim();
        if (!/^[A-Za-z0-9+/]+=*$/.test(output)) {
            throw new Error('签名组件返回了无效数据');
        }
        return Buffer.from(output, 'base64');
    } catch (error) {
        const stderr = Buffer.isBuffer(error?.stderr)
            ? error.stderr.toString('utf8').trim()
            : String(error?.stderr || '').trim();
        const detail = stderr || error.message || String(error);
        throw sapUnavailableError(`Apple SAP 签名失败：${detail}`);
    }
}

class PersistentSAPSigner {
    constructor(child, lines, stderr) {
        this.child = child;
        this.lines = lines;
        this.stderr = stderr;
    }

    static async open(config) {
        const signer = sapSignerPath();
        if (!existsSync(signer)) throw sapUnavailableError(`缺少 Apple SAP 签名组件：${signer}`);
        const child = spawn(signer, ['--server', config.setupURL, config.certificateURL], {stdio: ['pipe', 'pipe', 'pipe']});
        let stderr = '';
        child.stderr.setEncoding('utf8');
        child.stderr.on('data', chunk => { stderr += chunk; });
        const lines = readline.createInterface({input: child.stdout, crlfDelay: Infinity})[Symbol.asyncIterator]();
        const ready = await Promise.race([
            lines.next(),
            new Promise((_, reject) => setTimeout(() => reject(sapUnavailableError('Apple SAP 签名会话启动超时')), 35_000)),
        ]);
        if (ready.done || ready.value !== 'READY') {
            child.kill();
            throw sapUnavailableError(`Apple SAP 签名会话启动失败：${stderr.trim() || ready.value || 'no response'}`);
        }
        return new PersistentSAPSigner(child, lines, () => stderr);
    }

    async sign(body) {
        this.child.stdin.write(`${Buffer.from(body).toString('base64')}\n`);
        const result = await Promise.race([
            this.lines.next(),
            new Promise((_, reject) => setTimeout(() => reject(sapUnavailableError('Apple SAP 签名超时')), 15_000)),
        ]);
        const value = String(result.value || '');
        if (result.done || value.startsWith('ERROR ')) {
            throw sapUnavailableError(value.slice(6) || this.stderr().trim() || 'Apple SAP 签名失败');
        }
        const signature = Buffer.from(value, 'base64');
        if (!signature.length) throw sapUnavailableError('Apple SAP 签名为空');
        return signature;
    }

    close() {
        try { this.child.stdin.end('CLOSE\n'); } catch { /* ignore */ }
    }
}

// 对齐 client.go Send() 中的 SignAction 处理
export function buildSignedAuthenticationHeaders(baseHeaders, body, signer = signAppleAction) {
    const bodyBytes = Buffer.isBuffer(body) ? body : Buffer.from(body);
    const signature = signer(bodyBytes);
    if (!Buffer.isBuffer(signature) || signature.length === 0) {
        throw new Error('Apple SAP 签名为空');
    }
    return {...baseHeaders, [HEADER_SAP_SIGNATURE]: signature.toString('base64')};
}

function validatedAuthenticationURL(endpoint) {
    const invalid = () => new Error('Invalid Apple Store authentication endpoint');
    if (typeof endpoint !== 'string' || !endpoint || /[\s\\#]/.test(endpoint)) throw invalid();
    let url;
    try { url = new URL(endpoint); } catch { throw invalid(); }
    if (url.protocol !== 'https:' || url.username || url.password
        || /\/\/[^/?#]*@/.test(endpoint) || url.port
        || !/^(?:p\d+-)?buy\.itunes\.apple\.com$/.test(url.hostname)
        || (url.pathname !== AUTH_PATH && url.pathname !== `${AUTH_PATH}/`)) {
        throw invalid();
    }
    return url;
}

// Only normalize the initial bag URL; redirect destinations retain their path/query.
export function normalizeAuthenticationEndpoint(endpoint) {
    const url = validatedAuthenticationURL(endpoint);
    url.pathname = `${AUTH_PATH}/`;
    return url.toString();
}

function resolveAuthenticationRedirect(endpoint, location) {
    try {
        if (!location || /[\s\\#]/.test(location)) throw new Error();
        const destination = new URL(location, endpoint).toString();
        return validatedAuthenticationURL(destination).toString();
    } catch {
        // Do not include routing queries, response headers, or untrusted URLs in errors.
        throw new Error('Invalid Apple Store authentication redirect');
    }
}

function authenticationResponseError(status, detail) {
    const message = `${t('store_token_failed')} (HTTP ${status}: ${detail})`;
    if (status === 0 || status === 204 || status === 404 || status === 429 || status >= 500) {
        return transientAuthError(message);
    }
    return new Error(message);
}

// ---- bag.xml 获取 Apple 当前的 StoreServices 端点 ----
function extractPlistText(text) {
    const start = text.indexOf('<plist');
    const end = text.indexOf('</plist>');
    if (start >= 0 && end >= start) return text.slice(start, end + '</plist>'.length);
    return text;
}

export function parseStoreURLBag(text) {
    const parsed = plist.parse(extractPlistText(text));
    const root = parsed && typeof parsed === 'object' ? parsed : {};
    const nested = root.urlBag && typeof root.urlBag === 'object' ? root.urlBag : {};
    // Apple has advertised StoreServices endpoints both at the plist root and
    // inside urlBag. Prefer the root while retaining every nested endpoint.
    return {...nested, ...root};
}

export function sapConfigurationFromBag(bag) {
    const setupURL = String(bag?.['sign-sap-setup'] || '');
    const certificateURL = String(bag?.['sign-sap-setup-cert'] || '');
    const version = String(bag?.['sign-sap-version'] || '');
    let setup;
    let certificate;
    try {
        setup = new URL(setupURL);
        certificate = new URL(certificateURL);
    } catch {
        throw sapUnavailableError('Apple Store 配置未提供有效的 SAP 签名地址');
    }
    const validSetup = setup.protocol === 'https:' && !setup.username && !setup.password && !setup.port
        && ['fpinit.itunes.apple.com', 'play.itunes.apple.com'].includes(setup.hostname)
        && (setup.pathname.startsWith('/v1/signSapSetup/')
            || setup.pathname === '/WebObjects/MZPlay.woa/wa/signSapSetup');
    const validCertificate = certificate.protocol === 'https:' && !certificate.username && !certificate.password
        && !certificate.port && certificate.hostname === 's.mzstatic.com'
        && certificate.pathname === '/sap/setupCert.plist';
    if (!validSetup || !validCertificate || version !== '200') {
        throw sapUnavailableError('Apple Store 配置包含不受支持的 SAP 签名参数');
    }
    return {setupURL: setup.toString(), certificateURL: certificate.toString(), version};
}

export function fetchStoreURLBag(guid) {
    const url = `https://init.itunes.apple.com/bag.xml?guid=${encodeURIComponent(guid)}`;
    const {status, body} = curlRequest('GET', url, {
        headers: {'User-Agent': STORE_UA, Accept: 'application/xml'},
        follow: true,
        timeout: 20,
    });
    if (status < 200 || status >= 300) throw new Error('Unable to fetch Apple URL bag');
    return parseStoreURLBag(body.toString('utf8'));
}

// ---- 构建登录请求参数（对齐 loginRequest().Payload.Content） ----
// 注意：plist.build() 序列化为 XML plist，但 Content-Type 为 application/x-www-form-urlencoded
// 这是 ipatool-sapfix 的 XMLPayload 行为（参见 payload.go）。
export function buildLoginBody(email, password, code, guid, attempt) {
    return plist.build({
        appleId: email,
        attempt: String(attempt),
        guid,
        password: `${password}${String(code || '').replace(/\s+/g, '')}`,
        rmp: '0',
        why: 'signIn',
    });
}

// ---- 发送单次带 SAP 签名的 POST（不自动跟随 redirect） ----
// 对齐 client.go NewClient() 中的 CheckRedirect: ErrUseLastResponse（auth URL 不自动跟随）
async function postWithSAP(url, body, jar, signerSession) {
    const baseHeaders = {
        'User-Agent': STORE_UA,
        // 对齐 loginRequest() Headers: {"Content-Type": "application/x-www-form-urlencoded"}
        // body 是 plist XML，但 Content-Type 是 form-urlencoded（ipatool-sapfix 的准确行为）
        'Content-Type': 'application/x-www-form-urlencoded',
    };
    const signature = await signerSession.sign(body);
    const headers = {...baseHeaders, [HEADER_SAP_SIGNATURE]: signature.toString('base64')};
    return curlRequest('POST', url, {headers, body, follow: false, timeout: 30, jar});
}

function sleepSync(milliseconds) {
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, milliseconds);
}

async function postAuthenticationWithRetry(url, body, jar, signerSession) {
    let response;
    for (let requestAttempt = 0; requestAttempt <= AUTH_RETRY_DELAYS_MS.length; requestAttempt++) {
        response = await postWithSAP(url, body, jar, signerSession);
        const retryable = response.status === 204
            || response.status === 404
            || response.status === 429
            || response.status >= 500;
        if (!retryable || requestAttempt === AUTH_RETRY_DELAYS_MS.length) return response;
        sleepSync(AUTH_RETRY_DELAYS_MS[requestAttempt]);
    }
    return response;
}

// Redirects change only the destination. Credential retries stay at the assigned pod.
export async function storePasswordAuthenticate(email, password, code, guid, jar, endpoint, signerSession,
    sendRequest = postAuthenticationWithRetry) {
    let targetURL = normalizeAuthenticationEndpoint(endpoint);
    let attempt = 1;
    let redirects = 0;
    let body = buildLoginBody(email, password, code, guid, attempt);

    for (;;) {
        const res = await sendRequest(targetURL, body, jar, signerSession);
        const parsed = parseLoginResponse(res, attempt, code, targetURL);
        if (parsed.error) throw parsed.error;
        if (!parsed.retry) {
            return {res, parsed: parsed.data};
        }
        if (parsed.redirect) {
            if (redirects === MAX_AUTH_REDIRECTS) {
                throw authenticationResponseError(res.status, 'too many authentication redirects');
            }
            redirects++;
            targetURL = parsed.redirect;
        } else {
            attempt++;
            body = buildLoginBody(email, password, code, guid, attempt);
        }
    }
}

// 对齐 parseLoginResponse()
export function parseLoginResponse(res, attempt, authCode, endpoint) {
    const status = res.status;

    if (status >= 300 && status < 400) {
        let error;
        const location = headerValue(res.headers, 'location');
        if (![301, 302, 307, 308].includes(status)) {
            error = authenticationResponseError(status, 'unsupported authentication redirect');
        } else if (!location) {
            error = authenticationResponseError(status, 'authentication redirect is missing Location');
        } else {
            try {
                const redirect = resolveAuthenticationRedirect(endpoint, location);
                return {retry: true, redirect, error: null, data: null};
            } catch {
                error = authenticationResponseError(status, 'invalid authentication redirect');
            }
        }
        return {retry: false, redirect: '', error, data: null};
    }

    // 非重定向但也不是成功响应，尝试解析 plist
    let parsed = null;
    if (res.body && res.body.length > 0) {
        try { parsed = parsePlistLoose(res.body, t('ctx_store_login_resp')); } catch { /* ignore */ }
    }

    if (!parsed) {
        // 无法解析 plist，视为服务端错误
        return {retry: false, redirect: '', error: authenticationResponseError(status, 'empty or non-plist authentication response'), data: null};
    }

    const failureType = String(parsed.failureType || '');
    const customerMessage = String(parsed.customerMessage || '');

    // attempt==1 且 failureType==-5000（FailureTypeInvalidCredentials）→ 重试
    if (attempt === 1 && failureType === FAILURE_TYPE_INVALID_CREDENTIALS) {
        return {retry: true, redirect: '', error: null, data: null};
    }

    // Apple 不会通过这个响应触发受信任设备验证码，而且错误密码也可能返回
    // 相同的 Configurator_message，因此不能把它当成已确认的 2FA 挑战。
    if (failureType === '' && !authCode && customerMessage === CUSTOMER_MESSAGE_BAD_LOGIN) {
        return {retry: false, redirect: '', error: ambiguousAuthError(), data: null};
    }

    // failureType=="" && customerMessage=="Your account is disabled."
    if (failureType === '' && customerMessage === CUSTOMER_MESSAGE_ACCOUNT_DISABLED) {
        return {retry: false, redirect: '', error: new Error(t('wrong_password')), data: null};
    }

    // failureType != "" → 错误
    if (failureType !== '') {
        const msg = customerMessage || t('store_token_failed');
        return {retry: false, redirect: '', error: new Error(msg), data: null};
    }

    // 成功条件：有 passwordToken 和 dsPersonId
    if (status !== 200 || !parsed.passwordToken || !parsed.dsPersonId) {
        return {retry: false, redirect: '', error: transientAuthError(), data: null};
    }

    return {retry: false, redirect: '', error: null, data: parsed};
}

// ---- plist 解析（宽松）----
export function parsePlistLoose(buf, context = t('ctx_apple_resp')) {
    let xml = buf.toString('utf8').trim();
    if (!xml) throw new Error(t('empty_resp', {context}));
    if (!/^<\?xml/i.test(xml) && !/^<plist/i.test(xml)) {
        xml = `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0">${xml}</plist>`;
    }
    return plist.parse(xml);
}

// ---- 对外暴露的登录入口（对齐 Login() → login()） ----
export async function storeLogin(email, password, code, guid, cookieText = '', pod = '') {
    const jar = path.join(tmpDir(), `store-cookies-${crypto.createHash('sha256').update(String(email || '')).digest('hex').slice(0, 12)}.txt`);
    if (cookieText) writeFileSync(jar, cookieText);

    // Use the validated current bag URL instead of probing native/legacy alternatives.
    const bag = fetchStoreURLBag(guid);
    const endpoint = normalizeAuthenticationEndpoint(bag?.authenticateAccount);
    const sapConfig = sapConfigurationFromBag(bag);

    // 同一次登录和 2FA 验证必须保持同一个 SAP 会话；签完即关闭会使
    // Apple 在认证前返回 204/301/403 空响应。
    const signerSession = await PersistentSAPSigner.open(sapConfig);
    let res;
    let parsed;
    try {
        ({res, parsed} = await storePasswordAuthenticate(email, password, code, guid, jar, endpoint, signerSession));
    } finally {
        signerSession.close();
    }

    // 构建用户信息（对齐 login() 返回 Account）
    const storeFront = headerValue(res.headers, 'x-set-apple-store-front');
    const newPod = headerValue(res.headers, 'pod') || (res.headers.match(/Pod=(\d+)/) || [])[1] || '';
    const dsid = parsed.dsPersonId;

    const authHeaders = {
        'X-Dsid': dsid,
        'iCloud-DSID': dsid,
        'X-Token': parsed.passwordToken,
    };
    if (storeFront) authHeaders['X-Apple-Store-Front'] = storeFront;

    const cookieOut = existsSync(jar) ? readFileSync(jar, 'utf8') : '';

    return {
        accountInfo: parsed.accountInfo || {appleId: email, address: {firstName: '', lastName: ''}},
        dsPersonId: dsid,
        passwordToken: parsed.passwordToken,
        pod: newPod || pod || '',
        authHeaders,
        cookieText: cookieOut,
    };
}

// ---- Cookie jar 工具 ----
// 把缓存的 cookie 文本写回一个临时 jar 文件，返回路径（供复用会话时使用）。
export function restoreCookieJar(cookieText, seed = 'default') {
    if (!cookieText) return null;
    const digest = crypto.createHash('sha256').update(String(seed || 'default')).digest('hex').slice(0, 12);
    const jar = path.join(tmpDir(), `store-cookies-${digest}.txt`);
    writeFileSync(jar, cookieText);
    return jar;
}

export function readCookieJar(jar) {
    return jar && existsSync(jar) ? readFileSync(jar, 'utf8') : '';
}
