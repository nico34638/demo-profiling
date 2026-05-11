#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
INTERVAL="${INTERVAL:-1}"

echo "Envoi de trafic vers $BASE_URL (Ctrl+C pour arrêter)"
echo ""

i=0
while true; do
  i=$((i + 1))

  # Répartition : 40% fast, 40% slow, 20% leak
  r=$((RANDOM % 10))
  if [ $r -lt 4 ]; then
    endpoint="/fast"
  elif [ $r -lt 8 ]; then
    endpoint="/slow"
  else
    endpoint="/leak"
  fi

  status=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL$endpoint")
  echo "[#$i] $endpoint → $status"

  sleep "$INTERVAL"
done
