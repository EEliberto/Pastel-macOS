Base: https://github.com/majd/ipatool/tree/3b02b4c55576562f2a55284cda9db9a4411d3067 (v2.8.1 plus subsequent upstream fixes).
Retained: pkg/, internal/, module files and LICENSE.

Synced on 2026-11-19. No upstream commits are deferred. The previously deferred
dynamic storefront discovery is superseded by 3b02b4c55576, which resolves
storefronts using scoped Apple bags instead of the failing fixed list endpoint.
See ../upstream.json for the exact source and reviewed revisions.

Pastel also synchronizes the parallel download test fixture handler assignment
with request handling to eliminate a race detected by go test -race. This is a
test-only patch.

Pastel adds pkg/appstore/pastel_native.go. It separates preparation from
local finalization, keeping original Apple authentication and packaging logic.
Metadata travels as a binary plist inside the opaque JSON payload, preserving
integer/date/data types. Local finalization clears a stale SINF staging ZIP
before retrying, while retaining the downloaded raw archive.

NativeDownloadPlan contains account-specific authorization data. Pass it through
private pipes/in memory; never persist it unencrypted or include it in logs.
Review this adapter and deferred changes when updating the upstream revision.
