import { expect, test } from '@playwright/test'

// Buscas salvas (S44): "Salvar busca" no pé dos resultados do Início e no menu ⋯ do título; a lista
// fica no Início sem busca. Busca e título simulados (dados inventados).
test('salvar uma busca sem resultado e voltar a ela pelo Início', async ({ page }) => {
  await page.route('**/api/buscar?*', (r) => r.fulfill({ json: { filme: [], serie: [], canal: [] } }))
  await page.route('**/api/titulos/filme/900002', (r) =>
    r.fulfill({ json: { titulo: { tipo: 'filme', id: 900002, titulo: 'Filme Inventado', ano: '2001' }, assinatura: [] } }),
  )
  await page.route('**/api/titulos/filme/900002/**', (r) =>
    r.fulfill(r.request().url().includes('aos-poucos') ? { body: '{"feitas":1,"total":1,"falharam":[],"torrents":{"versoes":[],"outros":[],"buscas":[]}}\n', contentType: 'application/x-ndjson' } : { json: [] }),
  )

  await page.goto('/#/inicio')
  await page.getByLabel('Buscar').fill('Coisa Que Não Existe')
  await expect(page.getByText('Nada com “Coisa Que Não Existe”.')).toBeVisible()
  await page.getByRole('button', { name: 'Salvar busca' }).click()
  await expect(page.getByRole('button', { name: 'Busca salva' })).toBeDisabled()

  await page.goto('/#/titulo/filme/900002')
  await page.getByRole('button', { name: 'Mais opções' }).click()
  await page.getByRole('menuitem', { name: 'Salvar busca' }).click()
  await expect(page.getByText('Busca salva. Ela fica no Início')).toBeVisible()

  await page.goto('/#/inicio')
  const salvas = page.locator('.salvas')
  await expect(salvas.getByRole('link')).toHaveText([/Filme Inventado \(2001\)\s*Título/, /Coisa Que Não Existe\s*Início/])
  await salvas.getByRole('link', { name: /Coisa Que Não Existe/ }).click()
  await expect(page.getByLabel('Buscar')).toHaveValue('Coisa Que Não Existe')

  await page.getByLabel('Buscar').fill('')
  await salvas.getByRole('button', { name: 'Apagar a busca Coisa Que Não Existe' }).click()
  await expect(salvas.getByRole('link')).toHaveCount(1)
  await page.reload()
  await expect(page.locator('.salvas').getByRole('link')).toHaveCount(1)
})
