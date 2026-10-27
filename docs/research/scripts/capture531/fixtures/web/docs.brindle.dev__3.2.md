# Brindle 3.2 documentation

Brindle is an embeddable message-queue library for Go and Rust services.

## Ordering

Messages are delivered in the order they were published within one queue. The guarantee holds per queue, across all
consumers of that queue, as long as the queue has a single writer.

## Flow control

Each consumer may hold at most 10,000 unacknowledged messages. When a consumer falls further behind, Brindle drops
the oldest unacknowledged message to make room for the new one. The drop is not reported to the publisher or the
consumer; the only trace is the counter `brindle_dropped_total`. Raise `max_unacked` (up to 50,000) or add consumers
if a drop is not acceptable.

## Persistence

Queues are kept in memory and written to a write-ahead log every 200 ms by default (`wal_interval`).
