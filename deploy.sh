#!/usr/bin/env bash
# Publica o commit atual de origin/main no homelab.
# Uso: ./deploy.sh [--sem-testes]
set -euo pipefail

cd "$(dirname "$0")"

DEPLOY_HOST="${DEPLOY_HOST:-lucas@192.168.1.20}"
DEPLOY_KEY="${DEPLOY_KEY:-$HOME/.ssh/id_ed25519_homelab}"
APP_URL="${APP_URL:-https://192.168.1.20:8446}"
TELAS_URL="${TELAS_URL:-http://192.168.1.20:8447}"
CA_CERT="${CA_CERT:-../certs/homelab-ca.crt}"

falhar() { echo "ERRO: $*" >&2; exit 1; }

git fetch -q origin main
SHA=$(git rev-parse HEAD)
[ "$SHA" = "$(git rev-parse origin/main)" ] \
  || falhar "o commit atual não é origin/main. Faça o push para a main antes de publicar."
[ -z "$(git status --porcelain)" ] \
  || echo "AVISO: há alterações locais; elas não serão publicadas."

if [ "${1:-}" != "--sem-testes" ]; then
  echo "==> Rodando os testes"
  make test
fi

echo "==> Publicando ${SHA:0:7}"
ssh -i "$DEPLOY_KEY" -o IdentitiesOnly=yes -o BatchMode=yes "$DEPLOY_HOST" \
  'bash -s --' "$SHA" < scripts/remote-deploy.sh

echo "==> Conferindo a versão no ar"
HEALTH=$(curl -fsS --max-time 15 --cacert "$CA_CERT" "$APP_URL/api/health") \
  || falhar "a API não respondeu em $APP_URL."
case "$HEALTH" in
  *"\"version\":\"$SHA\""*) ;;
  *) falhar "a versão no ar não é a publicada: $HEALTH" ;;
esac

# Os testes de navegador usam um provedor falso; contra o servidor, trocariam o acesso de verdade.
# No ar, confere só que o PWA, a API e a porta das telas respondem.
echo "==> Conferindo o PWA"
curl -fsS --max-time 15 --cacert "$CA_CERT" "$APP_URL/" | grep -q '<div id="app">' \
  || falhar "o PWA não respondeu em $APP_URL."
curl -fsS --max-time 15 --cacert "$CA_CERT" "$APP_URL/api/catalogo" > /dev/null \
  || falhar "a API do catálogo não respondeu."
# A porta das telas só tem vídeo: um id que não existe responde 404, e não "conexão recusada".
[ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 "$TELAS_URL/tela/filme/0.mp4")" != "000" ] \
  || falhar "a porta das telas não respondeu em $TELAS_URL."

echo "==> Publicado: ${SHA:0:7} em $APP_URL"
