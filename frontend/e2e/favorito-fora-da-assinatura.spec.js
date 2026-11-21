import { expect, test } from '@playwright/test'

// Favorito de título fora da assinatura (S58): o botão aparece na página do título, o favorito vai
// para a aba Favoritos e sai ao desmarcar. Título simulado (dados inventados).
test('favoritar um título que não está na assinatura', async ({ page }) => {
  await page.route('**/api/titulos/filme/900009', (r) =>
    r.fulfill({ json: { titulo: { tipo: 'filme', id: 900009, titulo: 'Filme Fora da Assinatura', ano: '1993', nota: 7.2 }, assinatura: [] } }),
  )
  await page.route('**/api/titulos/filme/900009/**', (r) =>
    r.fulfill(r.request().url().includes('aos-poucos') ? { body: '{"feitas":1,"total":1,"falharam":[],"torrents":{"versoes":[],"outros":[],"buscas":[]}}\n', contentType: 'application/x-ndjson' } : { json: [] }),
  )

  await page.goto('/#/titulo/filme/900009')
  await page.getByRole('button', { name: 'Favoritar' }).click()
  await page.getByRole('dialog', { name: 'Favoritar' }).getByRole('button', { name: 'Sem pasta' }).click()
  await expect(page.getByRole('button', { name: 'Tirar dos favoritos ou mudar de pasta' })).toBeVisible()

  await page.goto('/#/favoritos/todos')
  const link = page.getByRole('link', { name: /Filme Fora da Assinatura/ })
  await expect(link).toHaveAttribute('href', '#/titulo/filme/900009')

  await page.goto('/#/titulo/filme/900009')
  await page.getByRole('button', { name: 'Tirar dos favoritos ou mudar de pasta' }).click()
  await page.getByRole('dialog').getByRole('button', { name: /Tirar dos favoritos/ }).click()
  await page.goto('/#/favoritos/todos')
  await expect(page.getByRole('link', { name: /Filme Fora da Assinatura/ })).toHaveCount(0)
})
