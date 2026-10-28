#!/bin/sh
# Usage: scripts/monthly.sh YYYY-MM
# Sums the amount column of data/YYYY-MM.csv and prints the total.
set -eu
month="$1"
src="data/$month.csv"
dest="out/total-$month.txt"
awk -F';' 'NR > 1 { sum += $3 } END { printf "%.2f\n", sum }' "$src" > "$dest"
cat "$dest"
