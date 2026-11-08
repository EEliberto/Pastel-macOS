# Local builds and optional release identity

`Scripts/BuildAdHocDMG.sh` now builds locally without an Apple Developer account
or certificate. It applies ad-hoc signatures (`codesign --sign -`) required to
run the bundled arm64 executables and their JIT entitlements. The script signs
nested executables first and the app last, verifies the bundle, and creates a
DMG. These signatures are local runtime signatures, not Developer ID notarization.

Run from the repository root:

```sh
sh Scripts/BuildAdHocDMG.sh
```

A configured stable self-signed identity remains optional:

```sh
PASTEL_SIGNING_IDENTITY="Pastel Release Signing" sh Scripts/BuildAdHocDMG.sh
```

A stable identity can retain the same Keychain designated requirement across
updates. Switching signing identities may require authorizing Keychain access
again. The build never deletes the vault or replaces its encryption key.

For a Mac which previously used that optional identity, its encrypted PKCS#12
backup is outside the repository at
`~/Documents/Pastel Release Signing Backup/Pastel Release Signing.p12`.
The backup password is in the login Keychain under service
`com.eeliberto.pastel.release-signing-backup-password` and account
`Pastel Release Signing`. Do not commit or bundle the private key or backup.

Sparkle's EdDSA update signature is separate from local code signing. This local
build script does not publish an update or change the appcast.
