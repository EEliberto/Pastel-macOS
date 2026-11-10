Base: https://github.com/majd/ipatool/tree/72c47d538b726df397fbf17bb3af809d69ef51b7 (v2.8.0).
Retained: pkg/, internal/, module files and LICENSE.

Applied upstream commits:
- 856323251ab1f45455ccf32d2ae77b17c8b4cca9: authentication server dialogs.
- 536d7bacc1c579d4420d0f4d2045368dcbf68732: percent-encoded URL queries.

Deferred 0d99fc2d967b3f5b7696b7327adb277c7e3b5525 (dynamic storefronts):
on 2026-11-09, real download preparation failed with “available storefronts
request failed”. The same account and app succeeded with the v2.8.0 mapping.
See ../upstream.json for the base, patches and last reviewed upstream revision.

Pastel adds only pkg/appstore/pastel_native.go. It separates preparation from
local finalization, keeping original Apple authentication and packaging logic.
Metadata travels as a binary plist inside the opaque JSON payload, preserving
integer/date/data types. Local finalization clears a stale SINF staging ZIP
before retrying, while retaining the downloaded raw archive.

NativeDownloadPlan contains account-specific authorization data. Pass it through
private pipes/in memory; never persist it unencrypted or include it in logs.
Review this adapter and deferred changes when updating the upstream revision.
