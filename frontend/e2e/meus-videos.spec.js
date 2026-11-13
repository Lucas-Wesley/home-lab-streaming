import { expect, test } from '@playwright/test'

// Meus vídeos (S42): o link de uma mensagem do Telegram vira um vídeo na pasta escolhida. O tdl é o
// falso (backend/testdata/tdl-falso): "baixa" um vídeo inventado e uma foto, que fica de fora.
test('baixa do Telegram para a pasta, renomeia e apaga', async ({ page }) => {
  // Chega pelo compartilhar do Android, com o link no meio do texto.
  await page.goto('/compartilhar?text=' + encodeURIComponent('olha https://t.me/c/1234567890/4 aí'))
  await expect(page.getByRole('heading', { name: 'Meus vídeos' })).toBeVisible()
  await expect(page.getByPlaceholder('https://t.me/c/…/4')).toHaveValue('https://t.me/c/1234567890/4')
  await page.getByPlaceholder('Praia do Forte, dia 2').fill('Praia do Forte')
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
