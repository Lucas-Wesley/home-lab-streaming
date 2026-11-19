import { expect, test } from '@playwright/test'

// Flashcards de um título (S49): no menu ⋯ de um filme em inglês, "Gerar flashcards" com o limite;
// a folha acompanha o pedido (escolhendo, enviado, áudios). Respostas simuladas, dados inventados.
test('gera os flashcards de um filme e acompanha os áudios', async ({ page }) => {
  let pedido = null
  let leituras = 0
  await page.route('**/api/titulos/filme/900002', (r) =>
    r.fulfill({ json: { titulo: { tipo: 'filme', id: 900002, titulo: 'Filme Inventado', ano: '2026', idioma: 'en' }, assinatura: [] } }),
  )
  await page.route('**/api/titulos/filme/900002/onde', (r) => r.fulfill({ json: [] }))
  await page.route('**/api/titulos/filme/900002/torrents/aos-poucos*', (r) =>
    r.fulfill({ body: '{"feitas":1,"total":1,"falharam":[],"torrents":{"temporadas":[],"outros":[],"total":0,"buscas":[]}}\n', contentType: 'application/x-ndjson' }),
  )
  await page.route('**/api/titulos/filme/900002/flashcards', (r) => {
    if (r.request().method() === 'POST') {
      expect(r.request().postDataJSON()).toMatchObject({ limite: 25 })
      leituras = 0
      pedido = { id: 1, estado: 'escolhendo', etapa: 'O Claude está escolhendo entre 150 frases…', frases: 0, com_audio: 0, limite: 25 }
      return r.fulfill({ json: pedido })
    }
    leituras++
    if (pedido && leituras > 3) {
      pedido = { ...pedido, estado: 'enviado', etapa: '', frases: 25, com_audio: 0, audio: 'sem_video', audio_msg: 'Sem vídeo deste título para cortar o áudio.', geracao: 41, link: 'https://flashcards.internal/#/geracao/41' }
    }
    return r.fulfill({ json: { disponivel: true, limite: 40, pedido } })
  })

  await page.goto('/#/titulo/filme/900002')
  await page.getByRole('button', { name: 'Mais opções' }).click()
  await page.getByRole('menuitem', { name: 'Gerar flashcards' }).click()
  await expect(page.getByRole('dialog')).toContainText('Inglês · Filmes e séries')
  await page.getByLabel('Até quantas frases').fill('25')
  await page.getByRole('button', { name: 'Gerar flashcards' }).click()
  await expect(page.getByText('Escolhendo as frases')).toBeVisible()
  await expect(page.getByText('Sem vídeo para o áudio')).toBeVisible({ timeout: 15000 })
  await expect(page.getByText('25 frases no Flashcards · 0 com áudio.')).toBeVisible()
  await expect(page.getByRole('link', { name: 'Abrir no Flashcards' })).toHaveAttribute('href', 'https://flashcards.internal/#/geracao/41')
  await expect(page.getByRole('button', { name: 'Gerar os áudios que faltam' })).toBeVisible()
})
