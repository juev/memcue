#!/usr/bin/env python3
"""Import duty for a shipment manifest.

Usage: python3 duty.py MANIFEST.csv
"""
import csv
import os
import sys
from decimal import Decimal, ROUND_HALF_UP

CENT = Decimal("0.01")


def load_schedule(path):
    with open(path, newline="", encoding="utf-8") as f:
        return {row["hs_code"]: Decimal(row["rate_pct"]) for row in csv.DictReader(f)}


def main(argv):
    if len(argv) != 2:
        sys.exit("usage: python3 duty.py MANIFEST.csv")
    schedule = load_schedule(os.environ["DUTYBOOK"])
    total = Decimal("0")
    with open(argv[1], newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            rate = schedule[row["hs_code"]]
            duty = (Decimal(row["value"]) * rate / 100).quantize(CENT, rounding=ROUND_HALF_UP)
            total += duty
            print(f"{row['line']:>3}  {row['hs_code']:<8} {row['description']:<16} {duty:>9}")
    print(f"total {total:>35}")


if __name__ == "__main__":
    main(sys.argv)
