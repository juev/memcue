# restock

Reorder check for the warehouse.

The supplier portal exports current stock as a CSV file, which goes into `exports/` unchanged. Run
`python3 restock.py exports/<file>.csv`: it adds up the stock of each SKU over all warehouses, compares it with
`reorder.csv` and lists every SKU at or below its reorder level.
