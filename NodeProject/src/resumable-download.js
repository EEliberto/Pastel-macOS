import {promises as fs} from 'node:fs';
import path from 'node:path';
import {createHash} from 'node:crypto';

export async function resumeWorkspace({directory, taskID, email, appID, versionID, resolveVersion}) {
    if (!taskID) return {directory, versionID, finish: async file => file};
    const key = createHash('sha256').update(`${taskID}\n${email.toLowerCase()}`).digest('hex');
    const workspace = path.join(directory, '.pastel-partials', key);
    await fs.mkdir(workspace, {recursive: true, mode: 0o700});
    const manifestPath = path.join(workspace, 'resume.json');
    let manifest;
    try { manifest = JSON.parse(await fs.readFile(manifestPath, 'utf8')); }
    catch (error) { if (error.code !== 'ENOENT') throw error; }
    if (manifest && (manifest.appID !== String(appID) || manifest.requestedVersion !== versionID)) {
        throw new Error('Download resume metadata does not match this task');
    }
    if (!manifest) {
        const pinned = versionID || await resolveVersion();
        if (!/^\d+$/.test(pinned)) throw new Error('Unable to resolve a stable App version for download');
        manifest = {appID: String(appID), requestedVersion: versionID, pinnedVersion: pinned};
        const staging = `${manifestPath}.tmp`;
        await fs.writeFile(staging, JSON.stringify(manifest), {mode: 0o600});
        await fs.rename(staging, manifestPath);
    }
    return {
        directory: workspace,
        versionID: manifest.pinnedVersion,
        async finish(file) {
            const relative = path.relative(workspace, file);
            if (relative.startsWith('..') || path.isAbsolute(relative)) throw new Error('Unexpected downloaded file path');
            const target = path.join(directory, path.basename(file));
            await fs.rename(file, target);
            await fs.rm(workspace, {recursive: true, force: true});
            return target;
        },
    };
}
