#!/usr/bin/env bash

trap 'exit 1' INT

echo "Running test $1 for $2 iters"
for i in $(seq 1 $2); do
	# -n do not output the trailing newline
	# -e enable interpretation of backslash escapes
    echo -ne "\r$i / $2\r"
    LOG="$1_$i.txt"
    # Failed go test return nonzero exit codes
    go test -run $1 &> $LOG
    if [[ $? -eq 0 ]]; then
        rm $LOG
    else
        echo "Failed at iter $i, saving log at $LOG"
    fi
done
