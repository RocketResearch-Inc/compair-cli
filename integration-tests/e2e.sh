#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." &> /dev/null && pwd)
export COMPOSE_PROJECT_NAME=compair_core_e2e

pushd "$ROOT_DIR/core/compose" >/dev/null
cp -n .env.example .env || true
docker compose up -d --build
cleanup() {
  docker compose down -v
  popd >/dev/null
}
trap cleanup EXIT

echo "Waiting for API to be healthy..."
for i in {1..60}; do
  if curl -fsS http://localhost:4000/_operator/healthz >/dev/null; then echo "API healthy"; break; fi
  sleep 2
done

export COMPAIR_API_BASE=http://localhost:4000

echo "Testing CLI login:"
"${ROOT_DIR}/compair" login

echo "Create/list groups:"
"${ROOT_DIR}/compair" group create "e2e-smoke"
"${ROOT_DIR}/compair" group ls
