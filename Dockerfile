FROM node:24-alpine AS web
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.26-alpine AS api
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=web /src/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /app .

# tdl (S42): baixa do Telegram com a conta do Lucas (Meus vídeos). Versão e checksum fixos.
FROM alpine:3.22 AS tdl
ARG TDL_VERSION=0.20.4
ARG TDL_SHA256=be251adca3ff96c14fd0417f179e9ff119b8073ed3b3c0ac6fe43ec05ebbda77
RUN wget -qO /tmp/tdl.tgz "https://github.com/iyear/tdl/releases/download/v${TDL_VERSION}/tdl_Linux_64bit.tar.gz" \
 && echo "${TDL_SHA256}  /tmp/tdl.tgz" | sha256sum -c - \
 && tar xzf /tmp/tdl.tgz -C /usr/local/bin tdl

# O Node fica na imagem final: o Claude Code roda nele (S27), como no Acervo.
FROM node:24-alpine
# ffmpeg: ler duração e formatos dos vídeos e tirar quadros na verificação (S23).
RUN apk add --no-cache ca-certificates tzdata ffmpeg libgcc libstdc++ \
 && npm install -g @anthropic-ai/claude-code \
 && npm cache clean --force \
 && mkdir /data && chown 1000:1000 /data
COPY --from=api /app /app
COPY --from=tdl /usr/local/bin/tdl /usr/local/bin/tdl
USER 1000:1000
# O login do Claude vem de CLAUDE_CODE_OAUTH_TOKEN, no .env do servidor (fora do Git).
ENV DATA_DIR=/data \
    HOME=/data/home \
    DISABLE_AUTOUPDATER=1
ENTRYPOINT ["/app"]
