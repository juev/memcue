# Marrowlog 2.4 performance report, 2026 Q2

Published by the Marrowlog maintainers, 2026-06-18.

## Result

One Marrowlog 2.4 agent shipped 180,000 lines per second to a collector on one vCPU with 2 GiB of RAM, with CPU at
85%.

## Method

- Input: a file tailed from local SSD; every line 200 bytes or shorter.
- Output: the collector over plain TCP. TLS was not enabled in any run.
- Disk buffer off, `batch_lines: 500`.
- Three runs of 30 minutes each; the figure is the median.

## Not covered

TLS to the collector, lines longer than 200 bytes, and the disk buffer were not measured. We plan to measure TLS in
the Q4 report.
