# 1.2 million writes per second with Kestrune 3.2

Posted by the Kestrune Labs team, 2026-07-02.

Kestrune 3.2 sustained 1.2 million writes per second in our lab, the fastest embedded store we have measured.

## Setup

- One machine: 32 cores, 128 GiB RAM, a single local NVMe drive.
- One writer process, writes in batches of 1,000.
- 16-byte keys, 64-byte values.
- `sync = "off"`.

We did not test values larger than 4 KiB or `sync = "full"` in this round. Results on other hardware will differ.
