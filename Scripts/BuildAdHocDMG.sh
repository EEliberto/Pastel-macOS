#!/bin/sh
set -eu

project_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
developer_dir=${DEVELOPER_DIR:-/Applications/Xcode-beta.app/Contents/Developer}
if [ ! -d "$developer_dir" ]; then
    developer_dir=/Applications/Xcode.app/Contents/Developer
fi
# Keep products outside the iCloud-synced source tree. Finder/iCloud metadata on
# nested Sparkle bundles is rejected by codesign as resource-fork detritus.
derived_data=${PASTEL_DERIVED_DATA:-"${TMPDIR:-/tmp}/PastelDistributionDerivedData"}
output_dir=${PASTEL_OUTPUT_DIR:-"${project_root}/dist"}

export DEVELOPER_DIR="$developer_dir"

sh "${project_root}/Scripts/VerifyReleaseSigningIdentity.sh"

sh "${project_root}/Scripts/VerifyDeviceGUIDImplementation.sh" \
    "${project_root}/Pastel/PastelApp.swift"

xcodebuild \
    -project "${project_root}/Pastel.xcodeproj" \
    -scheme Pastel \
    -configuration Release \
    -derivedDataPath "$derived_data" \
    CODE_SIGN_STYLE=Manual \
    CODE_SIGN_IDENTITY="Pastel Release Signing" \
    DEVELOPMENT_TEAM= \
    CODE_SIGNING_ALLOWED=YES \
    build

app="${derived_data}/Build/Products/Release/Pastel.app"
node="${app}/Contents/Resources/node/bin/node"
signer="${app}/Contents/Resources/sap-signer"
store_helper="${app}/Contents/Resources/store-helper"
sparkle="${app}/Contents/Frameworks/Sparkle.framework"
release_identity="Pastel Release Signing"

test -d "$app"
# Re-sign outside Xcode's build subprocess so the complete self-signed
# certificate is embedded. Keep this order: leaf executables, framework, app.
codesign --force --sign "$release_identity" "$signer"
codesign --force --sign "$release_identity" --options runtime \
    --entitlements "${project_root}/Scripts/NodeJIT.entitlements" "$node"
codesign --force --sign "$release_identity" --options runtime \
    --entitlements "${project_root}/Scripts/StoreHelper.entitlements" "$store_helper"
codesign --force --sign "$release_identity" --options runtime "$sparkle"
codesign --force --sign "$release_identity" --options runtime "$app"

codesign --verify --strict "$signer"
codesign --verify --strict "$node"
codesign --verify --strict "$store_helper"
codesign --verify --deep --strict "$app"

# Validate Node after every enclosing signature has been applied. Merely checking
# the entitlement plist would not prove that V8 can create executable JIT pages.
"$node" -e '
function hot(value) { return (value * 3 + 7) ^ 0x55; }
let result = 0;
for (let index = 0; index < 1000000; index += 1) result = hot(result + index);
if (!process.versions.node.startsWith("24.") || !Number.isInteger(result)) process.exit(1);
process.stdout.write(`Node ${process.versions.node} JIT validation passed\n`);
'

version=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "${app}/Contents/Info.plist")
build=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' "${app}/Contents/Info.plist")
dmg="${output_dir}/Pastel-${version}-build-${build}.dmg"
staging=$(mktemp -d "${TMPDIR:-/tmp}/pastel-dmg.XXXXXX")
trap 'rm -rf "$staging"' EXIT HUP INT TERM

mkdir -p "$output_dir"
ditto "$app" "${staging}/Pastel.app"
ln -s /Applications "${staging}/Applications"
rm -f "$dmg"
hdiutil create -quiet -fs HFS+ -volname Pastel -srcfolder "$staging" -format UDZO "$dmg"

echo "Created ${dmg}"
