#!/bin/sh
# Compose the side-by-side sheets: AppKit (left) next to ours (right), per
# control group, light and dark.
#
#	sh tools/appkitref/sheets.sh REF_DIR OURS_DIR OUT_DIR
#
# REF_DIR is an AppKitRef -o directory, OURS_DIR a tools/appkitref/ours -o
# directory; both hold light/ and dark/ PNGs named after the samples.
set -eu
ref=$1 ours=$2 out=$3
here=$(cd "$(dirname "$0")" && pwd)
bin=$here/bin/AppKitRef.app/Contents/MacOS/AppKitRef
mkdir -p "$out"

group() { # group NAME TITLE sample...
	name=$1 title=$2
	shift 2
	for mode in light dark; do
		m=$out/$name-$mode.json
		dark=false
		[ $mode = dark ] && dark=true
		printf '{"title": "%s — %s", "dark": %s, "rows": [' "$title" "$mode" "$dark" >"$m"
		sep=
		for s in "$@"; do
			printf '%s{"label": "%s", "left": "%s", "right": "%s"}' "$sep" "$s" "$ref/$mode/$s.png" "$ours/$mode/$s.png" >>"$m"
			sep=,
		done
		printf ']}\n' >>"$m"
		"$bin" -sheet "$m" "$out/$name-$mode.png"
		rm "$m"
	done
}

group buttons "Push buttons" push-regular push-default push-pressed push-disabled push-default-disabled push-focused push-destructive icon-rounded
group choices "Checkbox, radio, switch" checkbox-off checkbox-on checkbox-mixed checkbox-on-disabled checkbox-on-focused radio-on radio-off switch-off switch-on switch-on-disabled switch-on-focused
group selects "Popup, segmented, tabs, slider, progress" popup popup-disabled popup-focused segmented tabview slider slider-disabled slider-focused progress-bar separator
group fields "Text fields" field-placeholder field-text field-focused field-disabled search-empty search-text
group tables "Tables" table-fullwidth-emphasized table-fullwidth-unemphasized
group type "Type" label-styles
