#!/bin/sh
set -eu

env_file="${1:-.env}"
compose_file="${2:-compose.release.yaml}"

if [ ! -r "${env_file}" ]; then
  echo "environment file not found: ${env_file}" >&2
  exit 1
fi
if [ ! -r "${compose_file}" ]; then
  echo "compose file not found: ${compose_file}" >&2
  exit 1
fi

docker compose --env-file "${env_file}" -f "${compose_file}" pull
docker compose --env-file "${env_file}" -f "${compose_file}" up -d --remove-orphans
docker compose --env-file "${env_file}" -f "${compose_file}" ps
