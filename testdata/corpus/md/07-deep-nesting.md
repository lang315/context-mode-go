# Architecture

Top level overview of the scheduler service.

## Components

### Scheduler

#### Queue

The queue stores pending jobs in priority order.

##### Level five is body text

Still inside the Queue section.

#### Workers

Workers pull jobs from the queue and report heartbeats.

### Storage

The storage layer persists job state to SQLite.

## Deployment

Deployment uses a rolling restart of workers.

# Appendix

## Glossary

A heartbeat is a periodic liveness signal from a worker.
