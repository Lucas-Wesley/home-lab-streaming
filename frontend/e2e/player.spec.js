import { execFileSync } from 'node:child_process'
import { mkdtempSync, readFileSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { expect, test } from '@playwright/test'

// Player do aparelho (S51): controles próprios, aviso de outro aparelho e versões do canal. O vídeo é
// um WebM feito aqui pelo ffmpeg (o Chromium do Playwright não toca H.264), entregue no lugar do
// provedor falso.
let webm
test.beforeAll(() => {
  const pasta = mkdtempSync(path.join(os.tmpdir(), 'player-'))
  const arquivo = path.join(pasta, 'teste.webm')
  execFileSync('ffmpeg', ['-v', 'error', '-f', 'lavfi', '-i', 'testsrc=size=320x180:rate=15:duration=40', '-c:v', 'libvpx-vp9', '-b:v', '150k', '-deadline', 'realtime', arquivo])
  webm = readFileSync(arquivo)
})

// Com Range, como o servidor de verdade: sem ele o navegador não deixa avançar.
async function entregarVideo(page, padrao) {
  await page.route(padrao, (r) => {
    const m = /bytes=(\d+)-(\d*)/.exec(r.request().headers().range || '')
    if (!m) return r.fulfill({ status: 200, contentType: 'video/webm', headers: { 'Accept-Ranges': 'bytes' }, body: webm })
    const de = Number(m[1])
    const ate = m[2] ? Number(m[2]) : webm.length - 1
    r.fulfill({
      status: 206,
      contentType: 'video/webm',
      headers: { 'Accept-Ranges': 'bytes', 'Content-Range': `bytes ${de}-${ate}/${webm.length}` },
      body: webm.subarray(de, ate + 1),
    })
  })
}

test('controles: tempo, pular 10 s, velocidade e teclado', async ({ page }) => {
  await entregarVideo(page, '**/api/video/filme/200.mp4')
  await page.goto('/#/assistir/filme:200?titulo=Filme%20de%20teste')
  await expect(page.locator('.titulo')).toHaveText('Filme de teste')
  await expect(page.locator('.tempos')).toContainText('/ 0:40')
  const posicao = () => page.locator('video').evaluate((v) => v.currentTime)

  await page.getByRole('button', { name: 'Avançar 10 segundos' }).click()
  await expect.poll(posicao).toBeGreaterThan(9)
  await page.keyboard.press('ArrowLeft')
  await expect.poll(posicao).toBeLessThan(9)
  await expect(page.locator('.pulo')).toHaveText('−10 s')

  await page.getByRole('button', { name: 'Opções' }).click()
  await page.getByRole('button', { name: '1,5×' }).click()
  await expect.poll(() => page.locator('video').evaluate((v) => v.playbackRate)).toBe(1.5)
  await page.getByRole('button', { name: 'Fechar' }).click()
  await expect(page.locator('.botao.fino', { hasText: '1,5×' })).toBeVisible()

  // Barra: clicar no meio vai para o meio.
  const barra = page.getByRole('slider', { name: 'Posição no vídeo' })
  const caixa = await barra.boundingBox()
  await page.mouse.click(caixa.x + caixa.width / 2, caixa.y + caixa.height / 2)
  await expect.poll(posicao).toBeGreaterThan(17)
})

test('outro aparelho pega a assinatura: o player para e diz quem foi', async ({ page }) => {
  await entregarVideo(page, '**/api/video/filme/200.mp4')
  await page.goto('/#/assistir/filme:200?titulo=Filme%20de%20teste')
  await expect(page.locator('.tempos')).toContainText('/ 0:40')
  await page.waitForTimeout(500) // o player confere que é o dono logo depois de assumir

  const r = await page.request.post('/api/reproducao', { data: { chave: 'filme:200', aparelho: 'computador-teste-1', nome: 'o computador Windows', titulo: 'Filme de teste' } })
  expect(r.status()).toBe(204)
  await expect(page.getByText('Outro aparelho pegou a assinatura')).toBeVisible({ timeout: 10000 })
  await expect(page.getByText('O computador Windows começou a assistir este mesmo vídeo')).toBeVisible()
  expect(await page.locator('video').evaluate((v) => v.paused)).toBe(true)

  // "Assistir aqui" toma de volta.
  await page.getByRole('button', { name: 'Assistir aqui' }).click()
  await expect(page.getByText('Outro aparelho pegou a assinatura')).toHaveCount(0)
  await expect.poll(async () => (await (await page.request.get('/api/reproducao')).json()).nome).toMatch(/^o /)
})

test('ao vivo: versões do canal, a melhor indicada, e trocar sem sair', async ({ page }) => {
  await page.route('**/api/canais/*/alternativas', (r) =>
    r.fulfill({
      json: {
        nome: 'Canal Esporte 3',
        grupo: 'Canal Esporte',
        versoes: [
          { id: 101, nome: 'Canal Esporte 3 FHD', qualidade: 'FHD' },
          { id: 100, nome: 'Canal Esporte 3 HD', qualidade: 'HD' },
          { id: 102, nome: 'Canal Esporte 3 SD²', qualidade: 'SD', reserva: true },
        ],
        irmaos: [{ id: 110, nome: 'Canal Esporte 4', versoes: [], agora: { titulo: 'Time A x Time B', fim: '2030-01-01T00:00:00Z' } }],
      },
    }),
  )
  await page.goto('/#/assistir/canal:100')
  await expect(page.locator('.titulo')).toHaveText('Canal Esporte 3')
  await expect(page.locator('.subtitulo')).toContainText('HD')
  await expect(page.getByRole('button', { name: 'FHD disponível' })).toBeVisible()

  await page.getByRole('button', { name: 'Qualidade e outros canais' }).click()
  const gaveta = page.getByRole('dialog', { name: 'Opções' })
  await expect(gaveta.getByText('assistindo')).toBeVisible()
  await expect(gaveta.getByText('Outros canais Canal Esporte')).toBeVisible()
  await gaveta.getByRole('button', { name: /SD · reserva/ }).click()
  await expect(page).toHaveURL(/canal:102/)
  await expect(page.locator('.subtitulo')).toContainText('SD · reserva')
})

test.describe('no celular', () => {
  test.use({ hasTouch: true, viewport: { width: 390, height: 844 } })

  // Um toque mostra os controles e eles ficam uns 5 s (antes, o mesmo toque mostrava e escondia).
  test('um toque mostra os controles, e eles ficam tempo para usar', async ({ page }) => {
    await entregarVideo(page, '**/api/video/filme/200.mp4')
    await page.goto('/#/assistir/filme:200?titulo=Filme%20de%20teste')
    const player = page.locator('.player')
    await expect(player).toHaveClass(/sem-controles/, { timeout: 8000 }) // tocando, somem sozinhos
    // O Chrome do Android solta um pointermove de toque junto com o toque.
    await player.dispatchEvent('pointermove', { pointerType: 'touch', bubbles: true })
    await page.locator('video').tap()
    await expect(player).not.toHaveClass(/sem-controles/)
    await page.waitForTimeout(3500)
    await expect(player).not.toHaveClass(/sem-controles/)
    await expect(page.getByRole('button', { name: 'Tela cheia' })).toBeVisible()
    await expect(player).toHaveClass(/sem-controles/, { timeout: 4000 })
    // Outro toque, com os controles à vista, esconde.
    await page.locator('video').tap()
    await expect(player).not.toHaveClass(/sem-controles/)
    await page.locator('video').tap()
    await expect(player).toHaveClass(/sem-controles/)
  })
})

test('próximo episódio: aparece nos créditos e abre o seguinte', async ({ page }) => {
  await entregarVideo(page, '**/api/video/episodio/*.mp4')
  await page.route('**/api/episodios/301/proximo', (r) =>
    r.fulfill({ json: { serie: 'Série de Teste', proximo: { id: 302, temporada: 1, numero: 2, titulo: 'O meio', posicao: 0 } } }),
  )
  await page.route('**/api/episodios/302/proximo', (r) => r.fulfill({ json: { serie: 'Série de Teste', proximo: null } }))
  await page.goto('/#/assistir/episodio:301?titulo=S%C3%A9rie%20de%20Teste%20T1%20E1')
  await expect(page.getByRole('button', { name: /Próximo episódio: T1 E2 · O meio/ })).toBeVisible()
  // O vídeo tem 40 s: já está "nos créditos".
  const cartao = page.locator('.proximo-episodio')
  await expect(cartao).toContainText('T1 E2 · O meio')
  await cartao.getByRole('button', { name: 'Assistir agora' }).click()
  await expect(page).toHaveURL(/episodio:302/)
  await expect(page.locator('.titulo')).toHaveText('Série de Teste T1 E2')
  await expect(page.locator('.proximo-episodio')).toHaveCount(0)
})
