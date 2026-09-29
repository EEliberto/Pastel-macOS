// Mirrors ipatool's version-pinned updateProduct recovery (2026-09-13).
export function isUnavailableDownload(response) {
    const message = String(response?.customerMessage || '').trim().toLowerCase();
    return response?._httpStatus === 200 && !response.failureType
        && !response.songList?.length
        && (message === 'no longer available' || message.endsWith(' no longer available'));
}

export function shouldTryRedownload(response) {
    return String(response.failureType || '') === '5002'
        || isUnavailableDownload(response)
        || (response._httpStatus === 200 && !response.failureType
            && !response.customerMessage && !response.songList?.length);
}

export function validatedUpdateEndpoint(value) {
    // Never send account cookies or DSID to an arbitrary URL from the bag.
    const expected = 'https://downloaddispatch.itunes.apple.com/up/updateProduct';
    if (value !== expected) throw new Error('Invalid Apple updateProduct endpoint');
    return value;
}

export function validateUpdateResponse(response, appIdentifier, versionID) {
    // Preserve structured Apple failures for normal license/auth error handling.
    if (response.failureType || response.customerMessage) return response;
    const metadata = response.songList?.[0]?.metadata;
    if (response._httpStatus !== 200 || response.songList?.length !== 1
        || String(metadata?.itemId) !== String(appIdentifier)
        || String(metadata?.softwareVersionExternalIdentifier) !== String(versionID)
        || typeof metadata?.softwareVersionBundleId !== 'string'
        || !metadata.softwareVersionBundleId.trim()
        || !response.songList[0].URL) {
        const error = new Error('Apple update response does not match the requested app or version');
        error.code = 'APPINFO_FAIL';
        throw error;
    }
    return response;
}

export async function recoverRedownload({request, loadBag, appIdentifier, versionID}) {
    let response;
    try {
        response = await request('redownload');
    } catch (error) {
        if (!versionID || error.code !== 'EMPTY_REDOWNLOAD_RESPONSE') throw error;
        response = null;
        const endpoint = (await loadBag())?.updateProduct;
        if (!endpoint) throw error;
        return validateUpdateResponse(await request('update', validatedUpdateEndpoint(endpoint)), appIdentifier, versionID);
    }
    if (!versionID || !isUnavailableDownload(response)) return response;
    const endpoint = (await loadBag())?.updateProduct;
    if (!endpoint) return response;
    return validateUpdateResponse(await request('update', validatedUpdateEndpoint(endpoint)), appIdentifier, versionID);
}
