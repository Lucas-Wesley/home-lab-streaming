import os from 'node:os'
import path from 'node:path'
import { defineConfig } from '@playwright/test'

const remoto = process.env.E2E_BASE_URL
// O teste local roda num servidor e num banco só dele, com um provedor falso (a repo é pública).
const dados = path.join(os.tmpdir(), 'streaming-e2e')

export default defineConfig({
  testDir: './e2e',
  workers: 1,
  timeout: 60000,
  use: {
    baseURL: remoto || 'http://localhost:5181',
    ignoreHTTPSErrors: true,
  },
  webServer: remoto
    ? undefined
    : [
        {
          command: 'go run ./provedorfalso/cmd :8097',
          cwd: '../backend',
          url: 'http://localhost:8097/player_api.php',
        },
        {
          // Sem procurar telas na rede de casa (REDE_LOCAL aponta para uma rede vazia) nem ir ao ge.
          command: `rm -rf "${dados}" && go run .`,
          cwd: '../backend',
          env: { DATA_DIR: dados, ADDR: ':8098', ADDR_TELAS: ':8096', REDE_LOCAL: '192.0.2.0/30', CATALOGO_A_CADA: '0', AGENDA_URL: 'http://127.0.0.1:1/' },
          url: 'http://localhost:8098/api/health',
        },
        {
          // A versão de produção, com service worker, como no servidor.
          command: 'npm run build && npx vite preview --port 5181 --strictPort',
          env: { API_URL: 'http://localhost:8098' },
          url: 'http://localhost:5181',
          timeout: 180000,
        },
      ],
})
