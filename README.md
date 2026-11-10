# Streaming

Central de streaming do homelab: canais ao vivo, filmes e séries de uma assinatura IPTV (padrão Xtream Codes), num
PWA para o celular e o computador, com o celular como controle remoto da TV e do projetor (DLNA). Roda em
`https://192.168.1.20:8446`; a TV e o projetor buscam o vídeo em `http://192.168.1.20:8447`.

O acesso à assinatura é colado no próprio app (Ajustes) e fica no banco do servidor; **nenhum dado do provedor vai
para esta repo.**

| Camada | Escolha |
|---|---|
| Frontend | Vue 3 + Vite, PWA; hls.js para o ao vivo |
| Backend | Go (biblioteca padrão), repassa o vídeo do provedor |
| Banco | SQLite (`modernc.org/sqlite`, sem CGO) |
| Telas | DLNA/UPnP (SSDP para achar, AVTransport para tocar) |
| Produção | Um binário Go com o PWA embutido, em container, atrás do Caddy |

## Como rodar

```sh
make dev-api   # API em :8080 e porta das telas em :8081, dados em ./data
make dev-web   # PWA com hot reload, proxy de /api para :8080
make test      # Go, Vitest e Playwright (com um provedor falso)
./deploy.sh    # publica o commit de origin/main no homelab
```

Decisões, estado atual e próximos passos: [`docs/DECISOES.md`](docs/DECISOES.md).
Design system comum às apps: [`docs/DESIGN-SYSTEM.md`](docs/DESIGN-SYSTEM.md).
