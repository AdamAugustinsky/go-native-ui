#!/bin/sh
# Build tools/bouncerec/bin/BounceRec.app (ad-hoc signed, stable identifier).
#
#	bash tools/bouncerec/build.sh
#
# Caveat: TCC (Screen Recording) keys an ad-hoc signed app on its code
# directory hash, not only on the bundle id. Every rebuild that changes the
# binary changes that hash, so macOS may drop the grant: re-enable BounceRec
# in System Settings > Privacy & Security > Screen & System Audio Recording
# (remove it with "-" and add/enable again if the toggle looks on but fails).
# For a grant that survives rebuilds, sign with a real identity instead:
#	SIGN_IDENTITY="Apple Development: Name (TEAMID)" bash tools/bouncerec/build.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=$here/bin/BounceRec.app
sdk=${SDKROOT:-/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk}
[ -d "$sdk" ] || sdk=$(xcrun --show-sdk-path)

rm -rf "$out"
mkdir -p "$out/Contents/MacOS"
clang -O2 -Wall -Wextra -Wno-unused-parameter -fobjc-arc -mmacosx-version-min=14.0 \
	-isysroot "$sdk" \
	-framework ScreenCaptureKit -framework CoreMedia -framework CoreVideo \
	-framework AppKit -framework CoreGraphics -framework ImageIO \
	-o "$out/Contents/MacOS/BounceRec" "$here/main.m"
cp "$here/Info.plist" "$out/Contents/Info.plist"
codesign -s "${SIGN_IDENTITY:--}" -f --identifier dev.gonativeui.bouncerec "$out"
echo "built $out"
