#!/bin/sh
set -eu

# Rebuild the bundled helper after changes to StoreHelper or its pinned ipatool.
# Xcode consumes this archive; it does not invoke Go during normal app builds.
project_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
go_bin=${PASTEL_GO:-go}
staging=$(mktemp -d "${TMPDIR:-/tmp}/pastel-store-helper.XXXXXX")
trap 'rm -rf "$staging"' EXIT HUP INT TERM
cd "${project_root}/StoreHelper"
GOOS=darwin GOARCH=arm64 "$go_bin" build -mod=readonly -trimpath -ldflags='-s -w' -o "$staging/store-helper" .
"$go_bin" version -m "$staging/store-helper"
gzip -n -c "$staging/store-helper" > "$staging/store-helper.gz"
cp "$staging/store-helper.gz" "${project_root}/node/bin/store-helper.gz"
