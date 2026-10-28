# Ostrel 5.3: limits and retries

Copied from the Ostrel documentation, section "Operating limits", on 2026-05-14.

## Payload

- A job payload is at most 256 KiB after serialization. A larger payload is rejected at enqueue time with
  `PayloadTooLarge`.
- Store larger data elsewhere and pass a reference in the payload.

## Retries

- A failed job is retried with exponential backoff: the first retry after 2 seconds, each next delay doubled, capped at
  10 minutes.
- After 25 attempts the job moves to the dead-letter queue.
- `retry_limit` per job type overrides the 25 attempts; the backoff curve cannot be changed in 5.3.
