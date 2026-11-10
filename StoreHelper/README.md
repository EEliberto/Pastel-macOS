# Pastel Store Helper

The helper pins ipatool to `v2.8.0` (`72c47d538b72`, 2026-11-08), including
native Store AMD/Anisette provisioning and commerce request proofs. This version
also sends the current Store User-Agent when acquiring a free App license; the
older default User-Agent can incorrectly produce a password-token-expired error.

Rebuild the executable shipped with Xcode after changing this directory:

```sh
PASTEL_GO=/path/to/go sh Scripts/BuildStoreHelper.sh
```

Run that command from the repository root. It updates `node/bin/store-helper.gz`
for macOS arm64. Xcode's existing bundle phase decompresses and signs this
archive; changing Go sources alone does not update the shipped executable.

The stdin JSON request carries `sessionKey` (base64, 32 bytes), supplied by
Pastel's existing Keychain-backed encryption vault. Device state and
account-scoped cookies are AES-GCM encrypted under
`$IPA_SESSION_DIR/store-auth/`. Without that override, the path is
`~/Library/Application Support/IPA Download/sessions/store-auth/` on macOS.
Passwords are never persisted by the helper. Cookie state is saved even when
login returns a 2FA challenge, so the next process can continue verification.
State files use atomic replacement and mode 0600 inside a mode 0700 directory.
A lock per device protects login and commerce authorization. Native downloads
use `prepareNative`: the helper returns a signed URL and private authorization
payload, saves state and exits before any file transfer. `finalizeNative` only
rewrites the local IPA, does not acquire the authentication lock, and exits if
its owning app disappears. Legacy CLI `download` remains available and releases
the lock before transfer. The `kbsync-v1` proof cache remains encrypted.

Swift owns a cancellation-aware authentication queue (one), file-transfer queue
(three) and packaging queue (one). `URLSession` streams to a durable raw archive,
using Range/If-Range, bounded network retries and a single signed-URL refresh.
It verifies the checksum, packages locally, then atomically publishes the IPA.
No Node or Go process stays alive for the main file transfer. Node remains a
compatibility adapter for Apple sessions, free-license confirmation and catalog
queries; its final preparation response is private IPC, never a task log.

Only structured `-5000` credential rejection without a submitted code is labeled
`INVALID_CREDENTIALS`. When a code was submitted, the same response cannot tell
which part was rejected and is labeled `AUTH_VERIFICATION_FAILED`. An explicit
upstream challenge is `AUTH_CODE_REQUIRED`; protocol/transport/storage errors
are not treated as 2FA. The upstream Configurator challenge itself still does
not prove that the password is correct.

Checks:

```sh
cd StoreHelper
go test -race ./...
```

Real-account login, code delivery, and an authorized IPA download still require
manual verification against Apple's service. Unit tests use synthetic responses
and never read the user's saved Apple credentials.


Managed downloads use a stable, account-scoped `.pastel-partials/<task hash>`
workspace beneath the download folder. Preparation pins the external version.
Pause retains downloaded bytes; cancellation removes the workspace only after
its writer stops. Existing helper partial files are migrated on first resume.
The journal stores credential-free configuration and progress. Running tasks
resume on relaunch; manually paused tasks remain paused. Generation IDs reject
late events, and an immediate resume waits until the cancelled writer closes.

Checks (repository root):

```sh
python3 Scripts/Tests/NativeDownloadTests.py
python3 Scripts/Tests/DownloadResumeTests.py
```

## Upstream monitoring

`upstream.json` records the exact base, applied patches and reviewed head. See
`ipatool/PASTEL-PATCH.md` for the deferred storefront change and validation result.
`.github/workflows/store-upstream.yml` checks the latest release and main branch
every twelve hours (or manually). A changed head is staged in a disposable CI
checkout, then the helper and native transfer tests run. One issue per revision
links the diff and test result. GitHub Actions must be enabled and the workflow
must be pushed to the default branch before scheduling takes effect. Notification
delivery follows the maintainer's GitHub notification preferences.

The monitor never changes installed components, merges changes, or publishes a
release. Passing fixture tests cannot verify real Apple login, 2FA delivery or
account licensing. After review and an authorized real-account smoke test,
update the manifest/patches, rebuild `node/bin/store-helper.gz`, build Pastel,
and publish through the existing Sparkle feed and EdDSA release-signing process.
An Apple Developer account is not needed for the existing ad-hoc packaging flow.

Read-only local check:

```sh
python3 Scripts/CheckStoreUpstream.py
```

Use `GH_TOKEN` when API rate limiting requires authentication. `--stage` replaces
the vendored source with a candidate and is intended for disposable CI checkouts.
