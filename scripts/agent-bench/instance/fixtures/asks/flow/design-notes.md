# Webhook retries

Three services handle a payment provider's webhook: `gateway` receives it, `ledger` writes the payment to Postgres, and `notifier` emails the customer.

When `ledger` fails, the provider redelivers the webhook after 30 s while our own retry is still running. Two deliveries of one event then race to write the same ledger row. Last week that race charged 14 customers twice.

## Design A: retry inline under a row lock

`gateway` takes `SELECT ... FOR UPDATE` on the event's row, retries `ledger` inline up to three times (after 1 s, 4 s and 16 s), then calls `notifier`. The lock makes the second delivery wait for the first, so the race goes away without a new service.

The lock holds a Postgres connection for up to 21 s while it retries. At last year's peak of 900 webhooks a second, the pool of 200 connections would run dry and `gateway` would start refusing webhooks.

## Design B: a job queue with an idempotency key

`gateway` stores the event under a unique idempotency key, enqueues a job and answers 200 at once. A new `retry-worker` takes jobs from the queue (Postgres with `SKIP LOCKED`, no new database), calls `ledger`, then `notifier`. The unique key drops the second delivery.

Bursts wait in the queue instead of holding connections. It adds a worker deployment to operate, and a retried payment can land up to a minute later.

The team leans toward Design B.
