#!/bin/sh
# Build tools/appkitref/bin/AppKitRef.app (ad-hoc signed, stable identifier).
#
#	bash tools/appkitref/build.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=$here/bin/AppKitRef.app
sdk=${SDKROOT:-/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk}
[ -d "$sdk" ] || sdk=$(xcrun --show-sdk-path)

rm -rf "$out"
mkdir -p "$out/Contents/MacOS"
clang -O2 -Wall -Wextra -Wno-unused-parameter -fobjc-arc -mmacosx-version-min=14.0 \
	-isysroot "$sdk" \
	-framework AppKit -framework CoreText \
	-o "$out/Contents/MacOS/AppKitRef" "$here/main.m"
cp "$here/Info.plist" "$out/Contents/Info.plist"
codesign -s "${SIGN_IDENTITY:--}" -f --identifier dev.gonativeui.appkitref "$out"
echo "built $out"
