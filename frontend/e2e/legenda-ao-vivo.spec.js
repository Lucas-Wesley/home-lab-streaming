import { expect, test } from '@playwright/test'

// Legenda no celular (S31): a fala que está na tela fica em destaque e no meio da lista; no intervalo
// entre duas falas, nenhuma fica em destaque. A tela é simulada (sem TV de verdade nos testes).
const TELA = 'uuid:tela-de-teste'
const VTT = `WEBVTT

${Array.from({ length: 30 }, (_, i) => {
  const s = (n) => `00:00:${String(n).padStart(2, '0')}.000`
  return `${s(i * 2)} --> ${s(i * 2 + 1)}\nFala número ${i + 1}`
}).join('\n\n')}
`

async function abrir(page, posicao, { flashcards = false, audio = false, largura = 390 } = {}) {
  await page.route('**/api/telas', (r) => r.fulfill({ json: [{ id: TELA, nome: 'TV de teste', ligada: true }] }))
  await page.route('**/api/telas/*/estado', (r) =>
    r.fulfill({ json: { estado: 'PAUSED_PLAYBACK', posicao, duracao: 600, volume: 10, idade_ms: 0, sessao: { chave: 'filme:1', titulo: 'Filme de teste' } } }),
  )
  await page.route('**/api/legendas/filme:1', (r) => r.fulfill({ json: { escolhida: { id: 'l1', idioma: 'en', atraso: 0, escala: 1 } } }))
  await page.route('**/api/legenda/l1.vtt*', (r) => r.fulfill({ body: VTT, contentType: 'text/vtt' }))
  await page.route(/\/api\/flashcards\?/, (r) => r.fulfill({ json: { disponivel: flashcards, audio } }))
  await page.setViewportSize({ width: largura, height: 844 })
  await page.goto(`/#/controle/${encodeURIComponent(TELA)}`)
}

// A fala mais perto do meio da lista, pela posição na tela.
const noMeio = (page) =>
  page.locator('.falas').evaluate((lista) => {
    const meio = lista.getBoundingClientRect().top + lista.clientHeight / 2
    let melhor = null
    for (const li of lista.querySelectorAll('.fala')) {
      const r = li.getBoundingClientRect()
      const d = Math.abs(r.top + r.height / 2 - meio)
      if (!melhor || d < melhor.d) melhor = { d, texto: li.textContent.trim() }
    }
    return melhor.texto
  })

test('a fala que está na tela fica em destaque, no meio da lista', async ({ page }) => {
  await abrir(page, 40.5) // a 21ª fala vai de 40 a 41 s
  await expect(page.locator('.fala.atual')).toHaveText('Fala número 21')
  await expect.poll(() => noMeio(page)).toBe('Fala número 21')
})

test('entre duas falas, nenhuma em destaque e a próxima espera no meio', async ({ page }) => {
  await abrir(page, 41.5)
  await expect(page.locator('.fala').first()).toBeVisible()
  await expect(page.locator('.fala.atual')).toHaveCount(0)
  await expect.poll(() => noMeio(page)).toBe('Fala número 22')
})

test('com uma fala escolhida, as ações ficam em duas linhas organizadas, mesmo no celular estreito', async ({ page }) => {
  await abrir(page, 40.5, { flashcards: true, audio: true, largura: 360 })
  await page.locator('.fala', { hasText: 'Fala número 21' }).click()
  const linhas = page.locator('.acoes-da-legenda .linha')
  await expect(linhas.nth(0).locator('.botao')).toHaveText(['Tocar desta fala', 'Voltar ao ponto'])
  await expect(linhas.nth(1).locator('.botao')).toHaveText(['Gerar flashcard', 'Com áudio', '✕'])
  for (const linha of await linhas.all()) {
    const topos = await linha.locator('.botao').evaluateAll((bs) => bs.map((b) => Math.round(b.getBoundingClientRect().top)))
    expect(new Set(topos).size).toBe(1)
  }
  await page.locator('.legenda-ao-vivo').screenshot({ path: process.env.FOTO || 'test-results/acoes.png' })
})

test('"Voltar ao ponto" solta a fala escolhida e as ações do cartão somem', async ({ page }) => {
  await abrir(page, 40.5, { flashcards: true, audio: true })
  await page.locator('.fala', { hasText: 'Fala número 5' }).click()
  await page.getByRole('button', { name: 'Voltar ao ponto' }).click()
  await expect(page.locator('.fala.escolhida')).toHaveCount(0)
  await expect(page.locator('.acoes-da-legenda .botao')).toHaveCount(0)
  await expect.poll(() => noMeio(page)).toBe('Fala número 21')
})

test('"Tocar desta fala" leva o vídeo à fala escolhida e toca', async ({ page }) => {
  await abrir(page, 40.5)
  const comandos = []
  await page.route('**/api/telas/*/comando', (r) => {
    comandos.push(r.request().postDataJSON())
    r.fulfill({ json: {} })
  })
  await page.locator('.fala', { hasText: 'Fala número 5' }).click() // de 8 a 9 s
  await page.getByRole('button', { name: 'Tocar desta fala' }).click()
  await expect.poll(() => comandos).toEqual([
    { comando: 'ir', valor: 7.5 },
    { comando: 'continuar', valor: 0 },
  ])
})

test('"Ampliar" abre a legenda na tela inteira, com as ações embaixo', async ({ page }) => {
  await abrir(page, 40.5, { flashcards: true, largura: 360 })
  await page.getByRole('button', { name: 'Ampliar a legenda' }).click()
  const caixa = await page.locator('.legenda-ao-vivo.ampliada').boundingBox()
  expect(caixa.height).toBeGreaterThan(800)
  await expect(page.locator('.fala.atual')).toHaveText('Fala número 21')
  await page.locator('.fala', { hasText: 'Fala número 22' }).click()
  await expect(page.getByRole('button', { name: 'Gerar flashcard' })).toBeInViewport()
  await page.screenshot({ path: process.env.FOTO2 || 'test-results/ampliada.png' })
  await page.getByRole('button', { name: 'Fechar a legenda ampliada' }).click()
  await expect(page.locator('.legenda-ao-vivo.ampliada')).toHaveCount(0)
})

test('"Com áudio" manda o trecho da fala junto', async ({ page }) => {
  await abrir(page, 40.5, { flashcards: true, audio: true })
  let pedido
  await page.route('**/api/flashcards', (r) => {
    pedido = r.request().postDataJSON()
    r.fulfill({ status: 202, json: { estado: 'na fila' } })
  })
  await page.locator('.fala', { hasText: 'Fala número 21' }).click()
  await page.locator('.fala', { hasText: 'Fala número 22' }).click()
  await page.getByRole('button', { name: 'Com áudio' }).click()
  await expect(page.getByText('com o áudio da fala')).toBeVisible()
  expect(pedido).toMatchObject({ chave: 'filme:1', audio: true, inicio: 40, fim: 43, falas: ['Fala número 21', 'Fala número 22'] })
})

test('sem como cortar o áudio, só "Gerar flashcard"', async ({ page }) => {
  await abrir(page, 40.5, { flashcards: true })
  await page.locator('.fala', { hasText: 'Fala número 21' }).click()
  await expect(page.locator('.acoes-da-legenda .botao')).toHaveText(['Tocar desta fala', 'Voltar ao ponto', 'Gerar flashcard', '✕'])
})
