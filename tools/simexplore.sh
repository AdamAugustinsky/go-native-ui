#!/bin/sh
# Explore random seeds with the simulated user (app.SimUser) and the
# invariant assertions compiled in (-tags gnuiassert).
#
#	tools/simexplore.sh            # 10 minutes per suite, 1,000 steps per seed
#	tools/simexplore.sh 2h 2000    # two hours per suite, 2,000 steps per seed
#
# The suites are app/simtests/ui (scene, notes, gallery and rexlook, sharing
# the time) and Pilot. Every seed is logged as it starts; a failure prints its
# seed and the command that replays it, and the script stops there with the
# test's exit code.
set -u
cd "$(dirname "$0")/.." || exit 1
export CGO_ENABLED=0 GNUI_SIM_EXPLORE="${1:-10m}" GNUI_SIM_STEPS="${2:-1000}"
go test -tags gnuiassert -count=1 -timeout 0 -v -run '^TestSimUser$' ./app/simtests/ui || exit
go test -tags gnuiassert -count=1 -timeout 0 -v -run '^TestPilotSimUser$' ./cmd/pilot/pilot
