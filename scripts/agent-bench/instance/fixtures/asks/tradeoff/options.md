# Product page cache

The product page reads prices and stock from Postgres on every request and its p95 latency is 420 ms. We have three strategies for a cache in front of it.

## Write-through Redis

Every price or stock write also updates a shared Redis. Reads take about 35 ms at p95. A write is visible on the next read, so nothing goes stale. Redis costs about $310 a month on our provider, and it is one more stateful service for the on-call rotation, with failover to rehearse.

## In-memory LRU on each API node

Each of the 12 API nodes keeps its own cache with a 60 s TTL. Reads take about 20 ms at p95 when cached. A price change can show stale for up to 60 s, and two nodes can disagree during that minute. It costs nothing extra and adds nothing to operate, but each node uses about 900 MB more memory.

## CDN edge cache

The CDN caches the rendered page for 5 minutes and we purge it on writes through the CDN's API. Reads take about 60 ms at p95 from the edge, and origin load drops by about 80%. Purges take up to 30 s to spread, and when a purge call fails the page stays stale for the full 5 minutes. It costs about $90 a month on our current plan, and the purge path needs its own alerting.
