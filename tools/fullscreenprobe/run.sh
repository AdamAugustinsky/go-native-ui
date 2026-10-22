#!/bin/bash
set -euo pipefail
control=$1
candidate=$2
build=$3
evidence=$4
here=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$build" "$evidence"
export CGO_ENABLED=0 GOTOOLCHAIN=local
{
  date -u
  sw_vers
  uname -a
  go version
  echo "control=$(git -C "$control" rev-parse HEAD)"
  echo "candidate=$(git -C "$candidate" rev-parse HEAD)"
  echo 'tags=gnui_publicapi; repetitions=3; retries=0; one GUI process per hold'
  echo "ImageVersion=${ImageVersion:-local}"
  git -C "$control" diff --name-only HEAD "$(git -C "$candidate" rev-parse HEAD)"
} > "$evidence/environment.txt"
# This pair differs ONLY in tests. Fail setup if that premise is false.
git -C "$control" diff --exit-code HEAD "$(git -C "$candidate" rev-parse HEAD)" -- . ':(exclude)**/*_test.go' > "$evidence/production-diff.txt"
for cell in control-original control-delayed candidate-delayed; do
  root=$control
  harness=$candidate/app/fullscreen_darwin_test.go
  [ "$cell" = control-original ] && harness=$control/app/fullscreen_darwin_test.go
  [ "$cell" = candidate-delayed ] && root=$candidate
  python3 "$here/instrument.py" "$root" "$harness" "$build/$cell"
  go -C "$root" test -c -tags gnui_publicapi -overlay "$build/$cell/overlay.json" -o "$build/$cell.test" ./app
  cp "$build/$cell/fullscreen_darwin_test.go" "$evidence/$cell.go.txt"
done
clang -fobjc-arc -framework Cocoa -framework QuartzCore "$here/native.m" -o "$build/native-control"
shasum -a 256 "$build/"*.test "$build/native-control" "$evidence/"*.go.txt > "$evidence/sha256.txt"
cp "$here/testdata/probe_darwin_test.go" "$evidence/probe.go.txt"
cp "$here/native.m" "$evidence/native.m.txt"
printf 'repetition\tcell\texit\tverdict\n' > "$evidence/results.tsv"
lock=${FS_LIVE_LOCK:-/private/var/folders/2p/gmsgpspx4xlg9gjphgh84c540000gn/T/opencode/perf/live.lock}
failed=0
for rep in 1 2 3; do
  case "$rep" in
    1) order='control-original control-delayed candidate-delayed native-control';;
    2) order='native-control candidate-delayed control-delayed control-original';;
    3) order='candidate-delayed control-original native-control control-delayed';;
  esac
  for cell in $order; do
    log=$evidence/$rep-$cell.log
    status=0
    if [ "$cell" = native-control ]; then
      cmd=("$build/native-control")
    else
      delayed=1; [ "$cell" = control-original ] && delayed=0
      cmd=(env "FS_PROBE_DELAYED=$delayed" "$build/$cell.test" -test.run '^TestFullscreenProbeOnce$' -test.v -test.count=1 -test.timeout=150s)
    fi
    caffeinate -d /usr/bin/lockf -k "$lock" /bin/sh -c '
      ioreg -n Root -d1 | grep "IOConsoleLocked.*No" || exit 125
      exec "$@"
    ' sh python3 "$here/run-one.py" "${cmd[@]}" > "$log" 2>&1 || status=$?
    verdict=pass
    if [ "$status" != 0 ]; then verdict=fail; failed=1; fi
    if grep -q -- '--- SKIP:' "$log"; then verdict=skip; failed=1; fi
    printf '%s\t%s\t%s\t%s\n' "$rep" "$cell" "$status" "$verdict" | tee -a "$evidence/results.tsv"
    sleep 2 # fixed inter-case separation, not a retry or an assertion budget
  done
done
cat "$evidence/results.tsv"
exit "$failed"
