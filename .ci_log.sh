#!/bin/bash
# usage: .ci_log.sh <run-id> <step-substring> [job-substring]
/opt/homebrew/bin/gh run view "$1" --log 2>&1 | awk -F'\t' -v s="$2" -v j="${3:-}" 'index($2,s) && index($1,j) {sub(/^[^ ]* /,"",$3); print $1 "|" $3}'
