#!/usr/bin/env bash
set -euo pipefail
# Private, durable lab database. No TCP listener; socket directory is owner-only.
data="${1:?absolute database directory required}"
mkdir -p "$data"
chmod 700 "$data"
if [[ "$EUID" == 0 ]]; then
  chown postgres:postgres "$data"
  exec runuser -u postgres -- bash "$0" "$data"
fi
bin=/usr/lib/postgresql/17/bin
if [[ ! -f "$data/PG_VERSION" ]]; then
  "$bin/initdb" -D "$data" -U plantlab --auth-local=trust --auth-host=reject --no-instructions
fi
exec "$bin/postgres" -D "$data" -k "$data" -h '' -p 15432 -c max_connections=30 -c shared_buffers=32MB
