# ingest-worker

Consumes uploaded batches from the intake queue, keeps partial results in a local key-value store until a batch is
complete, then hands the batch to the archiver.

## Requirements for the store

- Values: one partial result per key, up to 6 MiB each.
- Write rate: about 300 writes per second at peak.
- Deployment: two worker processes, one on each of the hosts ingest-a and ingest-b, write to the same data
  directory, `/mnt/shared/ingest`, an NFS export of the file server fs-01.
- Durability: a write acknowledged to the queue must survive a power loss.

The current store is a directory of JSON files (`worker/store.py`); we want to replace it.
