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
A lock per device protects login and commerce authorization, with a 20-second
maximum wait. `transferOperatingSystem` releases it before the pinned upstream
opens the package destination and sends its CDN request. File transfers and IPA
processing therefore run concurrently. Authentication state/cookies are committed
before release; a completed older transfer cannot overwrite a newer login's state.
The upstream's `kbsync-v1` proof cache is also encrypted and reused.

Helpers emit credential-free phase markers. Node enforces phase deadlines and a
120-second transfer inactivity limit, forwards cancellation, and force-kills a
helper that ignores termination. The Swift UI displays preparation/authorization
instead of 0% before actual bytes arrive. Initial upstream asset preparation may
still take several minutes on a slow connection; its deadline is ten minutes.

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
