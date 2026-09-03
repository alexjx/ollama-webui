#!/bin/sh
set -eu

database_dir=$(dirname "${DATABASE_PATH}")
mkdir -p "${database_dir}"
chown webui:webui "${database_dir}"

exec su-exec webui:webui /app/ollama-webui
