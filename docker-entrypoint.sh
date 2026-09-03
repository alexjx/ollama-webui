#!/bin/sh
set -eu

database_dir=$(dirname "${DATABASE_PATH}")
mkdir -p "${database_dir}"
chown webui:webui "${database_dir}"
mkdir -p "${AGENT_WORKSPACE}"

if ! su-exec webui:webui test -w "${database_dir}"; then
  echo "DATABASE_PATH directory is not writable by container UID 1000: ${database_dir}" >&2
  exit 1
fi
if [ -e "${DATABASE_PATH}" ] && ! su-exec webui:webui test -w "${DATABASE_PATH}"; then
  echo "Database file is not writable by container UID 1000: ${DATABASE_PATH}" >&2
  exit 1
fi
if ! su-exec webui:webui test -w "${AGENT_WORKSPACE}"; then
  echo "AGENT_WORKSPACE is not writable by container UID 1000: ${AGENT_WORKSPACE}" >&2
  echo "Ensure the mounted host directory is writable by UID 1000." >&2
  exit 1
fi

exec su-exec webui:webui /app/ollama-webui
