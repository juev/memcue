#!/bin/sh
# Usage: scripts/cost.sh ORDERS [RATES]
# The cost of an order is the base fee of its zone plus the per-kg rate times its weight.
set -eu
orders="$1"
rates="${2:-data/zone-rates.tsv}"
awk -F'\t' '
FNR == 1 { next }
NR == FNR { base[$3] = $1; perkg[$3] = $2; next }
{
	if (!($2 in base)) {
		printf "cost.sh: no rate for zone \"%s\" (order %s)\n", $2, $1 > "/dev/stderr"
		failed = 1
		exit 1
	}
	cost = base[$2] + perkg[$2] * $3
	total += cost
	printf "%s\t%s\t%.2f\n", $1, $2, cost
}
END {
	if (failed) exit 1
	printf "total\t\t%.2f\n", total
}
' "$rates" "$orders"
