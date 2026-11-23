# Worker service

`arch.svg` shows the worker service as it runs today. One deployment takes jobs from one queue and does two kinds of work:

- image thumbnails: CPU-heavy, 2 to 8 s a job
- transactional email: I/O-bound, about 50 ms a job

During upload spikes, email jobs wait behind thumbnail jobs for up to 6 minutes, and password-reset emails arrive late.

Splitting it means a thumbnail worker on CPU-optimized nodes and an email worker that stays small, each with its own queue. That is one more deployment and one more queue to operate and alert on. Keeping it means adding a priority lane to the one queue, which the queue library supports but nobody here has used.
