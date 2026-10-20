# Troubleshooting

## Connection refused

If clients log `ECONNREFUSED` or "connection refused", the database is not
listening on the configured port. Check the host, the port and the firewall.

## Timeout errors

A `context deadline exceeded` error means the pool waited longer than
`connect_timeout` for a free connection. Raise `max_connections` or find the
handler that leaks connections without calling `Put`.

## Authentication

Clients see `password authentication failed` when credentials rotate.
Reload the secret and restart the workers.

## Slow queries

Enable the slow query log and look for missing indexes. Queries that scan
large tables hold a connection for the whole scan, which starves the pool.

```sql
EXPLAIN ANALYZE SELECT * FROM orders WHERE customer_id = 42;
```
