#!/bin/sh
# PostgreSQL Service Dependency Demo
#
# Uses the auto-injected MOAT_POSTGRES_* environment variables
# to connect and run queries against the sidecar database.

set -e

echo "=================================================="
echo "PostgreSQL Service Dependency Demo"
echo "=================================================="
echo

echo "--- Connection Info (from MOAT_POSTGRES_* env) ---"
echo "  Host:     $MOAT_POSTGRES_HOST"
echo "  Port:     $MOAT_POSTGRES_PORT"
echo "  User:     $MOAT_POSTGRES_USER"
echo "  Database: $MOAT_POSTGRES_DB"
echo "  URL:      ${MOAT_POSTGRES_URL%%:*}://...@$MOAT_POSTGRES_HOST:$MOAT_POSTGRES_PORT/$MOAT_POSTGRES_DB"
echo

# Use PGPASSWORD so psql doesn't prompt
export PGPASSWORD="$MOAT_POSTGRES_PASSWORD"

echo "--- Server Version ---"
psql -h "$MOAT_POSTGRES_HOST" -p "$MOAT_POSTGRES_PORT" -U "$MOAT_POSTGRES_USER" -d "$MOAT_POSTGRES_DB" -t -c "SELECT version();"
echo

echo "--- Create Table ---"
psql -h "$MOAT_POSTGRES_HOST" -p "$MOAT_POSTGRES_PORT" -U "$MOAT_POSTGRES_USER" -d "$MOAT_POSTGRES_DB" <<'SQL'
CREATE TABLE tasks (
    id    SERIAL PRIMARY KEY,
    title TEXT NOT NULL,
    done  BOOLEAN DEFAULT false
);
SQL
echo "  Created 'tasks' table"
echo

echo "--- Insert Rows ---"
psql -h "$MOAT_POSTGRES_HOST" -p "$MOAT_POSTGRES_PORT" -U "$MOAT_POSTGRES_USER" -d "$MOAT_POSTGRES_DB" <<'SQL'
INSERT INTO tasks (title, done) VALUES
    ('Set up database', true),
    ('Write queries', true),
    ('Ship feature', false);
SQL
echo "  Inserted 3 rows"
echo

echo "--- Query Results ---"
psql -h "$MOAT_POSTGRES_HOST" -p "$MOAT_POSTGRES_PORT" -U "$MOAT_POSTGRES_USER" -d "$MOAT_POSTGRES_DB" -c \
    "SELECT id, title, CASE WHEN done THEN 'done' ELSE 'todo' END AS status FROM tasks ORDER BY id;"
echo

echo "--- Aggregate ---"
psql -h "$MOAT_POSTGRES_HOST" -p "$MOAT_POSTGRES_PORT" -U "$MOAT_POSTGRES_USER" -d "$MOAT_POSTGRES_DB" -t -c \
    "SELECT count(*) || ' total, ' || count(*) FILTER (WHERE done) || ' done, ' || count(*) FILTER (WHERE NOT done) || ' remaining' FROM tasks;"
echo

echo "=================================================="
echo "Demo complete. Database will be cleaned up with the run."
echo "=================================================="
