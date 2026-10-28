# Kestrune 3.2 documentation: Limits

Last updated 2026-08-30. Applies to Kestrune 3.2.

## Sizes

| Item | Limit |
|---|---|
| Key | 1 KiB |
| Value | 8 MiB |
| Database file | 2 TiB |

## Processes

A database may be opened for writing by one process at a time; Kestrune takes an exclusive file lock on open. Other
processes may open the same database read-only.

## Filesystems

The data directory must be on a local filesystem. Network filesystems (NFS, SMB/CIFS) are not supported: their file
locking is not reliable enough for Kestrune's write lock, and a database on one may be corrupted.

## Durability

With `sync = "full"` (the default) a write returns after the data reaches the disk. `sync = "off"` returns before
that and may lose the last writes on a power failure.
