# Getting Started

Welcome to the connection pool library. It keeps a small set of database
connections open and hands them out to request handlers.

## Installation

Install the package with your package manager and import it in your service.
The library has no runtime dependencies beyond the database driver.

## Configuration

Set `max_connections` to the number of connections the pool may open.
The default is 10, which suits most small services.

### Timeouts

The `idle_timeout` closes connections that sit unused. The `connect_timeout`
bounds how long a request waits for a free connection before it fails.

#### Retry policy

Failed connects are retried three times with exponential backoff.

## Configuration

A second Configuration section: environment variables override the file.
Set `POOL_MAX_CONNECTIONS` to change the pool size at deploy time.

## Next steps

Read the API reference and the troubleshooting guide.
