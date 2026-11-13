import { expect, test } from '@playwright/test'

// Meus vídeos (S42): o link de uma mensagem do Telegram vira um vídeo na pasta escolhida. O tdl é o
// falso (backend/testdata/tdl-falso): "baixa" um vídeo inventado e uma foto, que fica de fora.
test('baixa do Telegram para a pasta, renomeia e apaga', async ({ page }) => {
  // Chega pelo compartilhar do Android, com o link no meio do texto.
  await page.goto('/compartilhar?text=' + encodeURIComponent('olha https://t.me/c/1234567890/4 aí'))
  await expect(page.getByRole('heading', { name: 'Meus vídeos' })).toBeVisible()
  await expect(page.getByPlaceholder('https://t.me/c/…/4')).toHaveValue('https://t.me/c/1234567890/4')
  await page.getByPlaceholder('Vazio: o texto da mensagem ou o nome do arquivo').fill('Praia do Forte')
  await page.getByPlaceholder('Viagens, Trabalho…').fill('Viagens')
  await page.getByRole('button', { name: 'Baixar', exact: true }).click()

  const cartaz = page.locator('.cartaz', { hasText: 'Praia do Forte' })
  await expect(cartaz).toBeVisible({ timeout: 15000 })
  await expect(page.locator('.rotulo', { hasText: 'Viagens' })).toBeVisible()
  await expect(page.locator('.chip', { hasText: 'pronto' })).toBeVisible()

  await cartaz.click()
  await expect(page.getByRole('dialog')).toContainText('Viagens')
  await expect(page.getByRole('button', { name: 'Assistir' })).toBeVisible()
  await page.getByRole('button', { name: 'Renomear ou mudar de pasta' }).click()
  await page.getByRole('dialog').getByRole('textbox').first().fill('Praia do Forte, dia 1')
  await page.getByRole('button', { name: 'Guardar' }).click()
  await expect(page.getByRole('dialog')).toHaveAttribute('aria-label', 'Praia do Forte, dia 1')

  page.once('dialog', (d) => d.accept())
  await page.getByRole('button', { name: 'Apagar' }).click()
  await expect(page.locator('.cartaz')).toHaveCount(0)
})

test('rastreia o grupo e baixa todos, com o nome de cada mensagem', async ({ page }) => {
  await page.goto('/#/meus-videos')
  await page.getByRole('button', { name: 'Baixar do Telegram' }).click()
  await page.getByPlaceholder('https://t.me/c/…/4').fill('https://t.me/c/1234567890/5')
  await page.getByRole('radio', { name: 'O grupo todo' }).click()
  await page.getByRole('button', { name: 'Rastrear o grupo' }).click()

  // O nome do grupo vira a pasta; o .rar fica de fora; os nomes saem do texto ou da data.
  await expect(page.locator('.resumo')).toContainText('Grupo Inventado 2026')
  await expect(page.locator('.nomes li')).toHaveText(['Aula 1 - Começo', 'Vídeo de 01-01-2026 às 09h00', 'Praia do Forte'])
  await expect(page.getByPlaceholder('Viagens, Trabalho…')).toHaveValue('Grupo Inventado 2026')
  await page.getByRole('button', { name: 'Baixar os 3 vídeos novos' }).click()

  await expect(page.locator('.rotulo', { hasText: 'Grupo Inventado 2026' })).toBeVisible({ timeout: 15000 })
  // Na ordem das mensagens (a 4 e a 5 têm a mesma data).
  await expect(page.locator('.cartaz')).toHaveText(['Aula 1 - Começo', 'Vídeo de 01-01-2026 às 09h00', 'Praia do Forte'])
})
