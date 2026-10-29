# ship-cost

Shipping cost of each order, from the zone rate card.

Run `scripts/cost.sh ORDERS [RATES]`. ORDERS is a tab-separated order list from `data/`. RATES defaults to
`data/zone-rates.tsv`, the rate card the logistics team exports from its spreadsheet. The script prints the cost of
every order and the total.
