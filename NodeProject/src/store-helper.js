import {t} from './i18n.js';
import path from 'path';
import {fileURLToPath} from 'url';
import {existsSync} from 'fs';
import {spawn} from 'child_process';

const MODULE_DIR = path.dirname(fileURLToPath(import.meta.url));
const DEFAULT_HELPER = path.resolve(MODULE_DIR, '..', '..', 'store-helper');

export function storeHelperPath() {
    return process.env.PASTEL_STORE_HELPER || DEFAULT_HELPER;
}

function helperError(result, fallback) {
    const error = new Error(result?.message || fallback || 'Apple Store helper failed');
    error.code = result?.code || 'STORE_FAILED';
    return error;
}

const PHASE_TIMEOUTS = {
    waiting: 25_000, preparing: 600_000, authenticating: 180_000,
    authorizing: 180_000, transferring: 120_000, packaging: 600_000,
};

export function reportStorePhase(phase) {
    process.stdout.write(`@@IPA:phase=${phase}\n`);
}

export async function runStoreHelper(input, {onProgress, onPhase = reportStorePhase, timeouts = PHASE_TIMEOUTS} = {}) {
    const helper = storeHelperPath();
    if (!existsSync(helper)) {
        const error = new Error(`缺少 Apple Store 组件：${helper}`);
        error.code = 'STORE_HELPER_UNAVAILABLE';
        throw error;
    }

    return await new Promise((resolve, reject) => {
        const child = spawn(helper, [], {stdio: ['pipe', 'pipe', 'pipe']});
        const stdout = [];
        let stderr = '';
        let lastPercent = -1;
        let phase = 'waiting';
        let deadline;
        let killDeadline;
        let stopped;
        const stop = (error = helperError({code: 'CANCELLED', message: t('store_cancelled')})) => {
            if (stopped) return;
            stopped = error;
            clearTimeout(deadline);
            child.kill('SIGTERM');
            killDeadline = setTimeout(() => child.kill('SIGKILL'), 3_000);
        };
        const cancel = () => stop();
        const killOnExit = () => child.kill('SIGKILL');
        const armDeadline = () => {
            clearTimeout(deadline);
            deadline = setTimeout(() => stop(helperError({
                code: 'STORE_TIMEOUT', message: t('store_timeout'),
            })), timeouts[phase] ?? PHASE_TIMEOUTS[phase]);
        };
        const detach = () => {
            clearTimeout(deadline);
            clearTimeout(killDeadline);
            process.removeListener('SIGTERM', cancel);
            process.removeListener('SIGINT', cancel);
            process.removeListener('exit', killOnExit);
        };
        process.once('SIGTERM', cancel);
        process.once('SIGINT', cancel);
        process.once('exit', killOnExit);
        armDeadline();

        child.stdout.on('data', chunk => stdout.push(Buffer.from(chunk)));
        child.stderr.setEncoding('utf8');
        child.stderr.on('data', chunk => {
            stderr = (stderr + chunk).slice(-8192);
            // Keep the tail so a marker or percentage split across pipe reads
            // still parses. Unknown output cannot keep a stalled task alive.
            const stages = [...stderr.matchAll(/@@PASTEL:phase=([a-z]+)\r?\n/g)];
            const nextPhase = stages.at(-1)?.[1];
            if (nextPhase && nextPhase in PHASE_TIMEOUTS && nextPhase !== phase) {
                phase = nextPhase;
                onPhase?.(phase);
                if (!stopped) armDeadline();
            } else if (nextPhase === 'waiting' && !stopped) {
                onPhase?.('waiting');
            }
            const matches = [...stderr.matchAll(/(\d{1,3})\s*%/g)];
            const percent = matches.length ? Number(matches.at(-1)[1]) : NaN;
            if (phase === 'transferring' && /%/.test(chunk) && !stopped) armDeadline();
            if (Number.isFinite(percent) && percent !== lastPercent) {
                lastPercent = percent;
                onProgress?.(Math.min(Math.max(percent, 0), 100));
            }
        });
        child.on('error', error => {
            detach();
            reject(helperError(null, error.message));
        });
        child.on('close', code => {
            detach();
            if (stopped) { reject(stopped); return; }
            let result;
            try {
                result = JSON.parse(Buffer.concat(stdout).toString('utf8').trim());
            } catch {
                reject(helperError(null, `Apple Store helper exited with code ${code}`));
                return;
            }
            if (code !== 0 || !result?.ok) {
                reject(helperError(result));
                return;
            }
            resolve(result);
        });
        child.stdin.on('error', error => stop(helperError(null, error.message)));
        child.stdin.end(JSON.stringify(input));
    });
}

export function helperUser(result, fallbackEmail = '') {
    const account = result?.account || {};
    const names = String(account.name || '').trim().split(/\s+/);
    return {
        accountInfo: {
            appleId: account.email || fallbackEmail,
            address: {
                firstName: names[0] || '',
                lastName: names.slice(1).join(' '),
            },
        },
        dsPersonId: account.directoryServicesIdentifier || '',
        passwordToken: account.passwordToken || '',
        pod: account.pod || '',
        authHeaders: {
            'X-Dsid': account.directoryServicesIdentifier || '',
            'iCloud-DSID': account.directoryServicesIdentifier || '',
            'X-Token': account.passwordToken || '',
            'X-Apple-Store-Front': account.storeFront || '',
        },
        frameworkAccount: account,
        frameworkCookieState: result?.cookieState || [],
        cookieText: '',
    };
}

// Both account validation and downloads use the same stable error mapping.
// Only an explicit upstream challenge (or a failed code submission) opens 2FA.
export function storeAuthenticationError(error) {
    const messages = {
        AUTH_CODE_REQUIRED: 'login_2fa',
        AUTH_CODE_INVALID: 'login_code_invalid',
        AUTH_VERIFICATION_FAILED: 'login_verification_failed',
        INVALID_CREDENTIALS: 'login_invalid_credentials',
        AUTH_STATE_BUSY: 'store_auth_busy',
    };
    const key = messages[error.code];
    const challenge = ['AUTH_CODE_REQUIRED', 'AUTH_CODE_INVALID', 'AUTH_VERIFICATION_FAILED'].includes(error.code);
    const result = new Error(key ? t(key) : t('login_auth_failed', {msg: error.message || String(error)}));
    result.code = challenge ? 'NEEDS_2FA' : error.code;
    return result;
}
