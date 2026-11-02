#!/bin/sh
set -eu

identity="Pastel Release Signing"

if ! security find-identity -v -p codesigning | grep -F -q "\"${identity}\""; then
    echo "Missing the stable '${identity}' code-signing identity." >&2
    echo "Refusing to create an Ad-hoc release because that would invalidate Keychain access." >&2
    exit 1
fi

echo "Stable release signing identity found: ${identity}"
