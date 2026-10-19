#!/bin/sh
# Build tools/gnuishot/bin/GnuiShot.app (ad-hoc signed, stable identifier).
#
#	bash tools/gnuishot/build.sh
#
# Caveat: TCC (Screen Recording) keys an ad-hoc signed app on its code
# directory hash, not only on the bundle id. Every rebuild that changes the
# binary changes that hash, so the grant is void and must be redone by hand at
# the keyboard of the test Mac: System Settings > Privacy & Security > Screen &
# System Audio Recording (remove GnuiShot with "-" and add/enable it again if
# the toggle looks on but capture fails). Build once, grant once, copy the same
# bundle to the test Mac, and compare the CDHash (README) to detect rebuilds.
# For a grant that survives rebuilds sign with a real identity instead:
#	SIGN_IDENTITY="Apple Development: Name (TEAMID)" bash tools/gnuishot/build.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=$here/bin/GnuiShot.app
sdk=${SDKROOT:-/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk}
[ -d "$sdk" ] || sdk=$(xcrun --show-sdk-path)

rm -rf "$out"
mkdir -p "$out/Contents/MacOS"
clang -O2 -Wall -Wextra -fobjc-arc -mmacosx-version-min=14.0 \
	-isysroot "$sdk" \
	-framework ScreenCaptureKit -framework CoreMedia -framework CoreVideo \
	-framework AppKit -framework CoreGraphics -framework ImageIO \
	-o "$out/Contents/MacOS/GnuiShot" "$here/main.m"
cp "$here/Info.plist" "$out/Contents/Info.plist"
codesign -s "${SIGN_IDENTITY:--}" -f --identifier dev.gonativeui.gnuishot "$out"
echo "built $out"
codesign -dvvv "$out" 2>&1 | grep -E 'Identifier|CDHash' || true
