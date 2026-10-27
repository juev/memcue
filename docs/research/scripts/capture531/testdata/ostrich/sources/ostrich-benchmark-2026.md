# Ostrich 0.9 compression benchmark (project wiki, March 2026)

Ostrich compressed our sample of application text logs 4.1 times on average (zstd level 3: 3.2 times), at 410 MB/s
on one core.

Scope: the sample held 2,000 text log files, each under 1 MB. Binary payloads, files over 1 MB and multi-core runs
were not tested, so these numbers say nothing about them.
