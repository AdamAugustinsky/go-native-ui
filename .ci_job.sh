#!/bin/bash
# usage: .ci_job.sh <jobid> -> $T/job<id>.log
T=/private/var/folders/2p/gmsgpspx4xlg9gjphgh84c540000gn/T/opencode
/opt/homebrew/bin/gh api --allow-escape-sequences repos/AdamAugustinsky/go-native-ui/actions/jobs/$1/logs 2>&1 | sed 's/\x1b\[[0-9;]*m//g' > $T/job$1.log
