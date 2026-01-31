---
title: "Service dependencies"
description: "Run ephemeral databases and caches alongside your agent containers."
keywords: ["moat", "postgres", "mysql", "redis", "database", "service", "sidecar"]
---

# Service dependencies

This guide covers running ephemeral databases and caches alongside agent containers. Moat provisions service containers automatically, generates credentials, and injects connection info as environment variables.

You will learn how to declare service dependencies, configure them with custom settings, and use the injected environment variables to connect from your agent code.

## Prerequisites

Docker runtime is required. Apple containers do not support service dependencies.

## Quick start

Add a service to your dependencies and use the injected environment variables:

```yaml
# agent.yaml
name: my-app

dependencies:
  - node@20
  - postgres@17
  - psql           # Client tools for the demo

command: ["sh", "-c", "psql $MOAT_POSTGRES_URL -c 'SELECT 1'"]
```

```bash
$ moat run .
```

Moat starts a PostgreSQL 17 container, waits for it to accept connections, and injects `MOAT_POSTGRES_URL` (and other `MOAT_POSTGRES_*` variables) into the agent container.

## Connection patterns

### Using the URL

Most database libraries accept a connection URL:

```python
# Python
import psycopg2
import os

conn = psycopg2.connect(os.environ["MOAT_POSTGRES_URL"])
```

```javascript
// Node.js
const { Pool } = require("pg");

const pool = new Pool({
  connectionString: process.env.MOAT_POSTGRES_URL,
});
```

```go
// Go
db, err := sql.Open("postgres", os.Getenv("MOAT_POSTGRES_URL"))
```

### Using individual variables

Some libraries require separate connection parameters:

```python
conn = psycopg2.connect(
    host=os.environ["MOAT_POSTGRES_HOST"],
    port=os.environ["MOAT_POSTGRES_PORT"],
    user=os.environ["MOAT_POSTGRES_USER"],
    password=os.environ["MOAT_POSTGRES_PASSWORD"],
    dbname=os.environ["MOAT_POSTGRES_DB"],
)
```

## Multiple services

Declare multiple services in the same `dependencies:` list:

```yaml
dependencies:
  - python@3.11
  - postgres@17
  - redis@7
```

Both services start in parallel on the same network. The agent container receives both sets of environment variables:

```bash
MOAT_POSTGRES_URL=postgresql://postgres:<pw>@postgres:5432/postgres
MOAT_REDIS_URL=redis://:<pw>@redis:6379
```

## Custom configuration

### Custom database name

```yaml
dependencies:
  - postgres@17

services:
  postgres:
    env:
      POSTGRES_DB: myapp
```

The custom database name propagates to the injected environment variables:

```bash
MOAT_POSTGRES_DB=myapp
MOAT_POSTGRES_URL=postgresql://postgres:<pw>@postgres:5432/myapp
```

### External passwords

Use secret references instead of auto-generated passwords:

```yaml
dependencies:
  - postgres@17

services:
  postgres:
    env:
      POSTGRES_PASSWORD: op://Dev/Database/password
```

Secret references support 1Password (`op://`) and AWS SSM (`ssm://`).

### Background startup

By default, Moat blocks the agent container until all services pass readiness checks. To start the agent immediately:

```yaml
dependencies:
  - postgres@17

services:
  postgres:
    wait: false
```

The agent must retry connections until the service is ready. This is useful when the agent has setup work to do before accessing the database.

## Lifecycle

### Startup sequence

1. Parse dependencies, identify services
2. Create a Docker network (`moat-<run-id>`)
3. Pull service images (if not cached)
4. Start service containers in parallel on the network
5. Run readiness checks (poll every 1 second, timeout after 30 seconds)
6. Inject `MOAT_*` environment variables
7. Start the agent container on the same network

### Shutdown sequence

1. Stop the agent container
2. Force-remove all service containers
3. Remove the network

Service data is ephemeral. It does not persist between runs.

### Cleanup

Service containers are labeled with `moat.run-id` and `moat.role=service`. If moat crashes, `moat clean` removes orphaned service containers by these labels.

Container naming follows the pattern `moat-<service>-<run-id>`:

```text
moat-postgres-run_abc123
moat-redis-run_abc123
```

## Readiness checks

Each service has a built-in readiness command:

| Service | Readiness check |
|---------|----------------|
| `postgres` | `pg_isready -h localhost -U postgres` |
| `mysql` | `mysqladmin ping -h localhost -u root --password=<pw>` |
| `redis` | `redis-cli -a <pw> PING` |

Readiness checks run inside the service container via `docker exec`. They verify that the service accepts connections with the generated credentials.

If a service fails to become ready within 30 seconds:

```text
Error: postgres service failed to become ready: timed out after 30s

Disable wait:
  services:
    postgres:
      wait: false
```

## Network architecture

```text
Docker network: moat-<run-id>
  ├── agent container (hostname: <run-id>)
  ├── postgres container (hostname: postgres)
  ├── redis container (hostname: redis)
  └── buildkit container (hostname: buildkit, if docker:dind)
```

Services are reachable from the agent container by hostname. All containers on the network can communicate. No ports are exposed to the host.

If `docker:dind` is also declared, the BuildKit sidecar shares the same network.

## Troubleshooting

### Connection refused

The service container is running but not ready. Possible causes:

- **Slow startup**: MySQL can take 10-20 seconds on first run. The 30-second timeout should cover this, but if it doesn't, check `moat logs <run-id>`.
- **wait: false**: If you disabled readiness waiting, add retry logic to your agent.
- **Custom image**: If you override the image, ensure it's compatible with the readiness check command.

### Service not found in dependencies

```text
Error: services.postgres configured but postgres not declared in dependencies

Add to dependencies:
  dependencies:
    - postgres@17
```

The `services:` block only customizes services declared in `dependencies:`. Add the service dependency first.

### Apple containers

Service dependencies require Docker runtime. Apple containers do not support sidecar containers.

```text
Error: service dependencies require Docker runtime
Apple containers don't support service dependencies
```

## Example: Full-stack test runner

```yaml
name: test-runner

dependencies:
  - node@20
  - postgres@17
  - redis@7

services:
  postgres:
    env:
      POSTGRES_DB: testdb

command:
  - sh
  - -c
  - |
    cd /workspace
    npm ci
    DATABASE_URL=$MOAT_POSTGRES_URL REDIS_URL=$MOAT_REDIS_URL npm test
```

## Related guides

- [Dependencies concept](../concepts/06-dependencies.md) — Dependency types and registry
- [agent.yaml reference](../reference/02-agent-yaml.md) — Full configuration options
- [Secrets management](04-secrets-management.md) — Using secret references in service config
