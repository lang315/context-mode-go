# Changelog

## v2.1.0

- Added `Pool.Stats` with idle, busy and wait counters.
- The rate limiter middleware now sets `Retry-After` on 429 responses.
- Fixed a race in `Pool.Put` when the context was cancelled during return.

## v2.0.0

Breaking: `NewPool` takes an options struct instead of positional arguments.
The `max_connections` option replaces `size`, and `idle_timeout` replaces
`ttl`. Configuration files using the old keys fail to load with a clear error
that names the new key. The migration guide lists every renamed option.

Connections are now validated with a ping before they are handed out when
they have been idle for longer than `validate_after`. This removes most of the
broken pipe errors seen after network maintenance, at the cost of one round
trip on the first request after a quiet period.

## v1.4.2

- Security: redact passwords from connection strings in error messages.
- Fixed `connect_timeout` being ignored when the DNS lookup itself hung.

## v1.4.1

- Fixed a goroutine leak in the health checker when the pool was closed twice.
