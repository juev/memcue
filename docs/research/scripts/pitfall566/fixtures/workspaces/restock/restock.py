#!/usr/bin/env python3
"""List the SKUs whose stock is at or below their reorder level."""
import argparse
import csv
import sys

REQUIRED = ("sku", "warehouse", "qty")


def read_export(path, encoding):
    with open(path, newline="", encoding=encoding) as f:
        reader = csv.DictReader(f, delimiter=";")
        header = reader.fieldnames or []
        for col in REQUIRED:
            if col not in header:
                sys.exit(f"restock.py: column {col!r} not found in {path}; header is {header!r}")
        stock = {}
        for row in reader:
            stock[row["sku"]] = stock.get(row["sku"], 0) + int(row["qty"])
        return stock


def read_levels(path):
    with open(path, newline="", encoding="utf-8") as f:
        return {row["sku"]: int(row["level"]) for row in csv.DictReader(f)}


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("export", help="stock export from the supplier portal")
    p.add_argument("--levels", default="reorder.csv", help="reorder levels (default: reorder.csv)")
    p.add_argument("--encoding", default="utf-8", help="text encoding of the export (default: utf-8)")
    args = p.parse_args()

    stock = read_export(args.export, args.encoding)
    levels = read_levels(args.levels)
    low = [(sku, stock[sku], level) for sku, level in sorted(levels.items()) if sku in stock and stock[sku] <= level]
    for sku, qty, level in low:
        print(f"{sku:<8} stock {qty:>4}  level {level:>4}")
    print(f"{len(low)} SKUs to reorder")


if __name__ == "__main__":
    main()
