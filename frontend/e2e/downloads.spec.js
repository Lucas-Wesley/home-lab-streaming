import { expect, test } from '@playwright/test'

// Downloads (S45): uma lista só, vídeo por vídeo, com o nome do título; sem o Internet Archive.
// Respostas simuladas (dados inventados).
test('downloads mostra os vídeos no servidor pelo nome do título', async ({ page }) => {
  let apagou = ''
  await page.route('**/api/assinatura/downloads', (r) => r.fulfill({ json: [] }))
  await page.route('**/api/downloads/torrents', (r) =>
    r.fulfill({
      json: apagou
        ? []
        : [
            { id: 7, hash: 'a'.repeat(40), titulo: 'Filme Inventado', torrent: 'Filme.Inventado.2026.1080p.WEBRip', tmdb: 'filme/900003', tamanho: 2e9, baixado: 5e8, estado: 'baixando' },
          ],
    }),
  )
  await page.route('**/api/arquivos/7/baixar', (r) => {
    apagou = r.request().method()
    return r.fulfill({ status: 204 })
  })

  await page.goto('/#/torrents')
  const item = page.locator('.download')
  await expect(item.getByRole('link', { name: 'Filme Inventado' })).toHaveAttribute('href', '#/titulo/filme/900003')
  await expect(item).toContainText('Torrent · 1,9 GB · Baixando 25%')
  await expect(item).toContainText('Filme.Inventado.2026.1080p.WEBRip')
  await expect(page.getByText('Explorar o Internet Archive')).toHaveCount(0)

  page.once('dialog', (d) => d.accept())
  await item.getByRole('button', { name: 'Apagar Filme Inventado' }).click()
  await expect(page.getByText('Nada no servidor.')).toBeVisible()
  expect(apagou).toBe('DELETE')
})
