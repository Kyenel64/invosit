#!/usr/bin/env bash
# Dev stack runner: base compose + dev overlay, with Infisical-injected env.
# Usage:
#   ./dev.sh up --build
#   ./dev.sh down
#   ./dev.sh logs -f kratos
#   ./dev.sh ps
set -euo pipefail

infisical run --env=local -- \
  docker compose \
    -f docker-compose.yml \
    -f docker-compose.dev.yml \
    "$@"
