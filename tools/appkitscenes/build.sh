#!/bin/sh
# Build tools/appkitscenes/bin/AppKitScenes.app (ad-hoc signed, stable identifier).
#
#	bash tools/appkitscenes/build.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=$here/bin/AppKitScenes.app
sdk=${SDKROOT:-/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk}
[ -d "$sdk" ] || sdk=$(xcrun --show-sdk-path)

rm -rf "$out"
mkdir -p "$out/Contents/MacOS"
clang -O2 -Wall -Wextra -Wno-unused-parameter -fobjc-arc -mmacosx-version-min=26.0 \
	-isysroot "$sdk" \
	-framework AppKit -framework QuartzCore -framework UniformTypeIdentifiers \
	-o "$out/Contents/MacOS/AppKitScenes" "$here/main.m" "$here/measure.m"
cp "$here/Info.plist" "$out/Contents/Info.plist"
codesign -s "${SIGN_IDENTITY:--}" -f --identifier dev.gonativeui.appkitscenes "$out"
echo "built $out"
