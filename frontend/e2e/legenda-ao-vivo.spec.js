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

async function abrir(page, posicao, { flashcards = false, largura = 390 } = {}) {
  await page.route('**/api/telas', (r) => r.fulfill({ json: [{ id: TELA, nome: 'TV de teste', ligada: true }] }))
  await page.route('**/api/telas/*/estado', (r) =>
    r.fulfill({ json: { estado: 'PAUSED_PLAYBACK', posicao, duracao: 600, volume: 10, idade_ms: 0, sessao: { chave: 'filme:1', titulo: 'Filme de teste' } } }),
  )
  await page.route('**/api/legendas/filme:1', (r) => r.fulfill({ json: { escolhida: { id: 'l1', idioma: 'en', atraso: 0, escala: 1 } } }))
  await page.route('**/api/legenda/l1.vtt*', (r) => r.fulfill({ body: VTT, contentType: 'text/vtt' }))
  await page.route('**/api/flashcards', (r) => r.fulfill({ json: { disponivel: flashcards } }))
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

test('com uma fala escolhida, as três ações ficam numa linha só, mesmo no celular estreito', async ({ page }) => {
  await abrir(page, 40.5, { flashcards: true, largura: 360 })
  await page.locator('.fala', { hasText: 'Fala número 21' }).click()
  const botoes = page.locator('.acoes-da-legenda .botao')
  await expect(botoes).toHaveText(['Voltar ao ponto', 'Gerar flashcard', 'Cancelar'])
  const topos = await botoes.evaluateAll((bs) => bs.map((b) => Math.round(b.getBoundingClientRect().top)))
  expect(new Set(topos).size).toBe(1)
  await page.locator('.acoes-da-legenda').screenshot({ path: process.env.FOTO || 'test-results/acoes.png' })
})
