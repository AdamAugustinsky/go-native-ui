#!/bin/sh
# Build AppKitCorner (corner.m) and print the outline of continuous and
# circular rounded corners as Core Animation draws them, as JSON.
#
#	sh tools/appkitref/corner.sh [-machine]      # -machine: only where it ran
set -eu
here=$(cd "$(dirname "$0")" && pwd)
sdk=${SDKROOT:-/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk}
[ -d "$sdk" ] || sdk=$(xcrun --show-sdk-path)
mkdir -p "$here/bin"
clang -O2 -Wall -Wextra -fobjc-arc -mmacosx-version-min=14.0 -isysroot "$sdk" \
	-framework AppKit -framework QuartzCore -o "$here/bin/AppKitCorner" "$here/corner.m"
"$here/bin/AppKitCorner" "$@"
