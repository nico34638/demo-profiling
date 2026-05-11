#!/usr/bin/env bash
set -euo pipefail

BASE_GO="${BASE_GO:-http://localhost:8080}"
BASE_NODE="${BASE_NODE:-http://localhost:8081}"
INTERVAL="${INTERVAL:-1}"

echo "Envoi de trafic vers Go ($BASE_GO) et Node ($BASE_NODE) (Ctrl+C pour arrêter)"
echo ""

i=0
while true; do
  i=$((i + 1))

  r=$((RANDOM % 10))
  if [ $r -lt 4 ]; then
    endpoint="/fast"
  elif [ $r -lt 8 ]; then
    endpoint="/slow"
  else
    endpoint="/leak"
  fi

  # Alterner entre Go et Node
  if [ $((i % 2)) -eq 0 ]; then
    target="$BASE_GO"
    lang="go  "
  else
    target="$BASE_NODE"
    lang="node"
  fi

  status=$(curl -s -o /dev/null -w "%{http_code}" "$target$endpoint")
  echo "[#$i] [$lang] $endpoint → $status"

  sleep "$INTERVAL"
done
