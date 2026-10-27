#!/bin/sh
# Runs the Vellum exporter over today's rows (stand-in for the real exporter).
conf=$(dirname "$0")/vellum.conf
batch=$(sed -n 's/^batch_size *= *//p' "$conf")
if [ "$batch" -gt 4096 ]; then
	echo "vellum-export: fatal: segment table full (batch_size=$batch)" >&2
	exit 3
fi
echo "vellum-export: exported 18234 rows in batches of $batch"
