#!/usr/bin/env bash
# Roda na VM, chamado pelo deploy.sh. Se o app não ficar saudável, volta ao commit anterior.
set -euo pipefail

SHA="$1"
REPO=https://github.com/Lucas-Wesley/home-lab-streaming.git
APP_DIR=/opt/apps/streaming
STATE=/opt/apps/streaming.deployed
COMPOSE=(docker compose -f compose.yaml -f compose.prod.yaml)

# A pasta pode já existir só com o .env (o token do Claude entra antes do primeiro deploy).
if [ ! -d "$APP_DIR/.git" ]; then
  mkdir -p "$APP_DIR"
  git -C "$APP_DIR" init -q
  git -C "$APP_DIR" remote add origin "$REPO"
fi
cd "$APP_DIR"
git fetch -q origin
git checkout -q --detach "$SHA"

PREV=$(cat "$STATE" 2>/dev/null || true)

# O acesso ao provedor e as chaves (Prowlarr, TMDB, OpenSubtitles) ficam no banco, em
# /srv/data/streaming. O .env (fora do Git) só tem o token do Claude, se houver (S27).
mkdir -p /srv/data/streaming

export VERSION="$SHA"
"${COMPOSE[@]}" build --quiet

if ! "${COMPOSE[@]}" up -d --wait --wait-timeout 60; then
  echo "ERRO: ${SHA:0:7} não ficou saudável." >&2
  "${COMPOSE[@]}" logs --tail 20 >&2 || true
  if [ -n "$PREV" ]; then
    echo "==> Voltando para ${PREV:0:7}" >&2
    git checkout -q --detach "$PREV"
    VERSION="$PREV" "${COMPOSE[@]}" up -d --wait --wait-timeout 60
  fi
  exit 1
fi

echo "$SHA" > "$STATE"

# Mantém só as imagens do commit atual e do anterior
docker images streaming --format '{{.Tag}}' \
  | { grep -vxF -e "$SHA" -e "${PREV:-$SHA}" || true; } \
  | xargs -r -I{} docker rmi -f "streaming:{}" >/dev/null
docker image prune -f >/dev/null
