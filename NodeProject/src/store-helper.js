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

export async function runStoreHelper(input, {onProgress} = {}) {
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

        child.stdout.on('data', chunk => stdout.push(Buffer.from(chunk)));
        child.stderr.setEncoding('utf8');
        child.stderr.on('data', chunk => {
            stderr += chunk;
            const matches = [...chunk.matchAll(/(\d{1,3})\s*%/g)];
            const percent = matches.length ? Number(matches[matches.length - 1][1]) : NaN;
            if (Number.isFinite(percent) && percent !== lastPercent) {
                lastPercent = percent;
                onProgress?.(Math.min(Math.max(percent, 0), 100));
            }
        });
        child.on('error', error => reject(helperError(null, error.message)));
        child.on('close', code => {
            let result;
            try {
                result = JSON.parse(Buffer.concat(stdout).toString('utf8').trim());
            } catch {
                reject(helperError(null, stderr.trim() || `Apple Store helper exited with code ${code}`));
                return;
            }
            if (code !== 0 || !result?.ok) {
                reject(helperError(result, stderr.trim()));
                return;
            }
            resolve(result);
        });

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
        frameworkCookies: result?.cookies || [],
        cookieText: '',
    };
}
