# Kestrune and large values?

**kv_wrangler** wrote:

> Heads up for anyone storing big blobs: Kestrune silently truncates values above 1 MiB when compression is on.
> Learned it the hard way, lost a week of data.

**anon_7731** replied:

> +1, think I saw the same thing.

**marlo_p** replied:

> Which version? I store 3 MiB values with compression on 3.1 and have not seen this. Do you have a repro?

(no further replies)
