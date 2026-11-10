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

# O Node fica na imagem final: o Claude Code roda nele (S27), como no Acervo.
FROM node:24-alpine
# ffmpeg: ler duração e formatos dos vídeos e tirar quadros na verificação (S23).
RUN apk add --no-cache ca-certificates tzdata ffmpeg libgcc libstdc++ \
 && npm install -g @anthropic-ai/claude-code \
 && npm cache clean --force \
 && mkdir /data && chown 1000:1000 /data
COPY --from=api /app /app
USER 1000:1000
# O login do Claude vem de CLAUDE_CODE_OAUTH_TOKEN, no .env do servidor (fora do Git).
ENV DATA_DIR=/data \
    HOME=/data/home \
    DISABLE_AUTOUPDATER=1
ENTRYPOINT ["/app"]
