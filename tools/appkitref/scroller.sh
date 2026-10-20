#!/bin/sh
# Build AppKitScroller (scroller.m) and print NSScroller's widths, knob slots
# and knobs per control size and style as JSON.
#
#	sh tools/appkitref/scroller.sh [-machine]      # -machine: only where it ran
set -eu
here=$(cd "$(dirname "$0")" && pwd)
sdk=${SDKROOT:-/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk}
[ -d "$sdk" ] || sdk=$(xcrun --show-sdk-path)
mkdir -p "$here/bin"
clang -O2 -Wall -Wextra -fobjc-arc -mmacosx-version-min=14.0 -isysroot "$sdk" \
	-framework AppKit -o "$here/bin/AppKitScroller" "$here/scroller.m"
"$here/bin/AppKitScroller" "$@"
