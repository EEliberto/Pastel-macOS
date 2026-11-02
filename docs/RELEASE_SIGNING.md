# Pastel release signing

Pastel releases must always be signed with the long-lived self-signed identity
named `Pastel Release Signing`. macOS uses this identity as part of Pastel's
designated requirement, which lets an updated build continue accessing the
same Keychain vault without repeatedly asking the user for their Mac password.

The encrypted PKCS#12 backup is stored outside this repository at:

`~/Documents/Pastel Release Signing Backup/Pastel Release Signing.p12`

Its password is stored in the login Keychain with service
`com.eeliberto.pastel.release-signing-backup-password` and account
`Pastel Release Signing`.

Never regenerate or replace this identity for an ordinary release. Losing it
means that existing users must authorize Keychain access again. Restore the
PKCS#12 backup into the login Keychain on a replacement build Mac.

`Scripts/BuildAdHocDMG.sh` refuses to build a distribution package when the
stable identity is missing. The script signs nested executables first and the
app last, then performs strict deep signature verification before creating the
DMG. Sparkle's EdDSA update signature remains a separate requirement.

The identity is self-signed, so it does not provide Developer ID notarization.
Users still need to approve the app through macOS when opening it for the first
time. The private key and backup must never be committed or shipped in the app.
