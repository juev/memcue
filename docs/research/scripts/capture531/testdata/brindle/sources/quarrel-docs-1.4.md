# Quarrel 1.4 reference

Quarrel is a partitioned message-queue library.

## Delivery

Delivery is at least once. A message that is not acknowledged within `ack_timeout` (30 s by default) is delivered
again, so consumers must be idempotent.

## Ordering

Order is kept within a partition only. A queue is split into 64 partitions by default, and messages of one queue in
different partitions may be delivered in any order. Pin a key to a partition (`partition_key`) to keep the order of
the messages that share it.

## Back-pressure

A publisher blocks when the partition buffer is full; no message is dropped.
