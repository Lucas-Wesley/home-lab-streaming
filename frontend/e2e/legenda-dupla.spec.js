import { expect, test } from '@playwright/test'

// Legenda dupla (S46): na folha de assistir de um vídeo baixado, "Legenda dupla" + a tela pede a
// gravação do ponto e manda para a tela quando há 1 min gravado à frente. Respostas simuladas.
test('legenda dupla: pede, espera gravar à frente e manda para a tela', async ({ page }) => {
  let dupla = { chave: 'arquivo:7', estado: '' }
  let tocou = null
  await page.route('**/api/assinatura/downloads', (r) => r.fulfill({ json: [] }))
  await page.route('**/api/downloads/torrents', (r) =>
    r.fulfill({ json: [{ id: 7, hash: 'b'.repeat(40), titulo: 'Filme Inventado', torrent: 'Filme.Inventado.1080p', tamanho: 2e9, baixado: 2e9, estado: 'baixado', posicao: 600 }] }),
  )
  await page.route('**/api/telas', (r) => r.fulfill({ json: [{ id: 'tela-1', nome: 'Projetor', ligada: true, toca_4k: false }] }))
  await page.route('**/api/legenda-dupla/arquivo:7', (r) => {
    if (r.request().method() === 'POST') {
      expect(r.request().postDataJSON()).toEqual({ comeco: 600, esperar: false })
      dupla = { chave: 'arquivo:7', estado: 'sincronizando', comeco: 600, duracao: 6000, gravado: 0 }
      // Na leitura seguinte, já há 2 min gravados a partir do ponto.
      setTimeout(() => (dupla = { ...dupla, estado: 'gravando', gravado: 120, faixas: [[600, 720]] }), 500)
    }
    return r.fulfill({ json: dupla })
  })
  await page.route('**/api/telas/tela-1/tocar', (r) => {
    tocou = r.request().postDataJSON()
    return r.fulfill({ json: { chave: 'arquivo:7', titulo: 'Filme Inventado', dupla: true, crescendo: true } })
  })
  await page.route('**/api/telas/tela-1/estado', (r) => r.fulfill({ json: { estado: 'PLAYING', posicao: 600, duracao: 6000, carregado: [[600, 720]], idade_ms: 0 } }))

  await page.goto('/#/torrents')
  await page.getByRole('button', { name: 'Assistir' }).click()
  await page.getByRole('radio', { name: 'Legenda dupla' }).click()
  await expect(page.getByText('Inglês e português gravados na imagem')).toBeVisible()
  // No aparelho, só depois de pronta.
  await expect(page.getByRole('button', { name: /Continuar de 10:00/ })).toBeDisabled()

  await page.getByRole('button', { name: /Projetor/ }).click()
  await expect(page.getByText('Vai para Projetor sozinho.')).toBeVisible()
  await expect(page).toHaveURL(/#\/controle\/tela-1/, { timeout: 15000 })
  expect(tocou).toMatchObject({ chave: 'arquivo:7', posicao: 600, dupla: true })
})
