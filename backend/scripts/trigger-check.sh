#!/usr/bin/env bash
# Force the worker to re-check a tracker on its next poll (every 30s) instead
# of waiting for its normal check_interval_minutes schedule.
#
# Targets the production server by default (over SSH, using its docker-compose
# `db` container's psql). Pass --local to hit the local docker-compose stack
# instead.
#
# Usage:
#   ./trigger-check.sh <tracker-id-or-prefix>                  # prod (default)
#   ./trigger-check.sh --local <tracker-id-or-prefix>           # local stack
set -euo pipefail

PROD_HOST="root@167.233.159.2"
PROD_DB_CONTAINER="deploy-db-1"
LOCAL_DB_CONTAINER="price-checker-bot-db-1"

TARGET="prod"
if [[ "${1:-}" == "--local" ]]; then
	TARGET="local"
	shift
fi

TRACKER_ID_PREFIX="${1:?usage: trigger-check.sh [--local] <tracker-id-or-prefix>}"

SQL="UPDATE trackers
SET next_check_at = now(),
    consecutive_errors = 0
WHERE id::text LIKE '${TRACKER_ID_PREFIX}%'
RETURNING id, url, status, current_price, next_check_at;"

if [[ "$TARGET" == "prod" ]]; then
	ssh "$PROD_HOST" "docker exec -i $PROD_DB_CONTAINER psql -U postgres -d price_tracker -c \"$SQL\""
else
	docker exec -i "$LOCAL_DB_CONTAINER" psql -U postgres -d price_tracker -c "$SQL"
fi
