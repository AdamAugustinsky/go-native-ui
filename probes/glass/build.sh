#!/bin/sh
# Build probes/glass/bin/GlassProbe.app (ad-hoc signed), issue #25's probe.
#
#	sh probes/glass/build.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=$here/bin/GlassProbe.app
sdk=${SDKROOT:-/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk}
[ -d "$sdk" ] || sdk=$(xcrun --show-sdk-path)

rm -rf "$out"
mkdir -p "$out/Contents/MacOS"
clang -O2 -Wall -Wextra -Wno-unused-parameter -Wno-arc-retain-cycles -fobjc-arc -mmacosx-version-min=26.0 \
	-isysroot "$sdk" \
	-framework AppKit -framework QuartzCore -framework Metal -framework IOKit \
	-o "$out/Contents/MacOS/GlassProbe" "$here/glassprobe.m"
cp "$here/Info.plist" "$out/Contents/Info.plist"
codesign -s "${SIGN_IDENTITY:--}" -f --identifier dev.gonativeui.glassprobe "$out"
echo "built $out"
