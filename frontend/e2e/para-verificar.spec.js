import { expect, test } from '@playwright/test'

// Mandar para verificação (S37): o menu ⋯ da página do título guarda o título e o que a página via;
// Ajustes → Títulos para verificar lista. O título vem de um TMDB simulado (dados inventados).
test('o título mandado para verificação aparece em Ajustes', async ({ page }) => {
  await page.route('**/api/titulos/serie/900001', (r) =>
    r.fulfill({ json: { titulo: { tipo: 'serie', id: 900001, titulo: 'Série Inventada', ano: '2018', temporadas: [{ numero: 1, episodios: 2 }] }, assinatura: [] } }),
  )
  await page.route('**/api/titulos/serie/900001/onde', (r) => r.fulfill({ json: [] }))
  await page.route('**/api/titulos/serie/900001/temporada/1', (r) =>
    r.fulfill({ json: [{ numero: 1, nome: 'Começo' }, { numero: 2, nome: 'Fim' }] }),
  )
  await page.route('**/api/titulos/serie/900001/torrents/aos-poucos*', (r) =>
    r.fulfill({ body: '{"feitas":1,"total":1,"falharam":[],"torrents":{"temporadas":[],"outros":[],"total":0,"buscas":["Serie Inventada S01E02"]}}\n', contentType: 'application/x-ndjson' }),
  )

  await page.goto('/#/titulo/serie/900001')
  await page.getByRole('button', { name: /Fim/ }).click()
  await page.getByRole('button', { name: 'Fechar' }).click()
  await page.getByRole('button', { name: 'Mais opções' }).click()
  await page.getByRole('menuitem', { name: 'Mandar para verificação' }).click()
  await expect(page.getByRole('dialog')).toContainText('T1 E2')
  await page.getByLabel('O que pareceu errado').fill('Não achou torrent.')
  await page.getByRole('button', { name: 'Guardar para verificação' }).click()
  await expect(page.getByText('Guardado para verificação.')).toBeVisible()

  await page.goto('/#/ajustes/verificar')
  await expect(page.getByRole('link', { name: 'Série Inventada (2018) · T1 E2' })).toBeVisible()
  await expect(page.getByText('Não achou torrent.')).toBeVisible()
  await expect(page.locator('.chip', { hasText: 'Para verificar' })).toBeVisible()
})
