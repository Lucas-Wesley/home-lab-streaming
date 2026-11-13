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

test('rastreia o grupo, mostra o peso de cada vídeo e baixa só os marcados', async ({ page }) => {
  // No celular: nada passa da largura da tela.
  await page.setViewportSize({ width: 360, height: 760 })
  await page.goto('/#/meus-videos')
  await page.getByRole('button', { name: 'Baixar do Telegram' }).click()
  await page.getByPlaceholder('https://t.me/c/…/4').fill('https://t.me/c/1234567890/5')
  await page.getByRole('radio', { name: 'O grupo todo' }).click()
  await page.getByRole('button', { name: 'Rastrear o grupo' }).click()

  // O nome do grupo vira a pasta; o .rar fica de fora; os nomes saem do texto ou da data.
  await expect(page.locator('.resumo')).toContainText('Grupo Inventado 2026')
  const itens = page.locator('.opcao-de-video')
  await expect(itens.locator('.nome-do-video')).toHaveText(['Aula 1 - Começo', 'Vídeo de 01-01-2026 às 09h00', 'Praia do Forte'])
  await expect(itens.locator('.peso')).toHaveText(['tamanho desconhecido', '50 MB', '2 min · 300 MB'])
  await expect(page.getByPlaceholder('Viagens, Trabalho…')).toHaveValue('Grupo Inventado 2026')
  await expect(page.locator('.total')).toContainText('3 vídeos · 350 MB')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)

  // Tira a aula da escolha: o total muda.
  await itens.first().locator('input').uncheck()
  await expect(page.locator('.total')).toContainText('2 vídeos · 350 MB')
  await page.getByRole('button', { name: 'Baixar 2 vídeos' }).click()
  // Baixou só parte do grupo: ele vai para os Salvos sozinho.
  await expect(page.getByText('O grupo ficou em Salvos')).toBeVisible()

  await expect(page.locator('.rotulo', { hasText: 'Grupo Inventado 2026' })).toBeVisible({ timeout: 15000 })
  await expect(page.locator('.cartaz')).toHaveText(['Vídeo de 01-01-2026 às 09h00', 'Praia do Forte'])

  // De novo: os que já vieram aparecem, mas não dá para marcar.
  await page.getByRole('button', { name: 'Baixar do Telegram' }).click()
  await page.getByPlaceholder('https://t.me/c/…/4').fill('https://t.me/c/1234567890/5')
  await page.getByRole('radio', { name: 'O grupo todo' }).click()
  await page.getByRole('button', { name: 'Rastrear o grupo' }).click()
  await expect(itens.locator('.peso')).toHaveText(['tamanho desconhecido', 'já baixado', 'já baixado'])
  await expect(itens.nth(1).locator('input')).toBeDisabled()
  await expect(page.getByRole('button', { name: 'Baixar 1 vídeo' })).toBeVisible()
})

test('salvos: reabre o grupo sem o link e guarda um vídeo para depois', async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 760 })
  await page.goto('/#/meus-videos')
  // O grupo do teste anterior ficou salvo: dois baixados, a aula não.
  const salvos = page.locator('.salvos li')
  await expect(salvos).toHaveCount(1)
  await expect(salvos.first()).toContainText('Grupo Inventado 2026')
  await expect(salvos.first()).toContainText('Grupo · 3 vídeos · 2 baixados')

  // Abre com a lista guardada, sem nada marcado.
  await salvos.first().getByRole('button', { name: /Grupo Inventado 2026/ }).click()
  await expect(page.locator('.resumo')).toContainText('Lista de')
  const itens = page.locator('.opcao-de-video')
  await expect(itens.locator('.peso')).toHaveText(['tamanho desconhecido', 'já baixado', 'já baixado'])
  await expect(itens.first().locator('input')).not.toBeChecked()
  await expect(page.getByRole('button', { name: 'Marque os vídeos' })).toBeDisabled()
  await expect(page.getByRole('button', { name: 'Salvo', exact: true })).toHaveAttribute('aria-pressed', 'true')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)

  // Buscar de novo lê o grupo outra vez (e marca o que é novo).
  await page.getByRole('button', { name: 'buscar de novo' }).click()
  await expect(page.getByRole('button', { name: 'Baixar 1 vídeo' })).toBeVisible()

  // Um vídeo só: o servidor olha a mensagem e guarda com o nome e o peso.
  await page.getByRole('radio', { name: 'Só este vídeo' }).click()
  await page.getByPlaceholder('https://t.me/c/…/4').fill('https://t.me/c/1234567890/4')
  await page.getByRole('button', { name: 'Salvar para depois' }).click()
  await expect(page.locator('.video-salvo')).toContainText('Aula 1 - Começo')
  await page.getByRole('button', { name: 'Fechar' }).click()
  await expect(salvos.locator('.nome-do-pedido')).toHaveText(['Aula 1 - Começo', 'Grupo Inventado 2026'])

  page.on('dialog', (d) => d.accept())
  await salvos.first().getByRole('button', { name: 'Tirar dos Salvos' }).click()
  await salvos.first().getByRole('button', { name: 'Tirar dos Salvos' }).click()
  await expect(salvos).toHaveCount(0)
})

test('no celular, a barra tem Início, Favoritos, Canais e Outros', async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 760 })
  await page.goto('/#/inicio')
  const barra = page.locator('.menu .item:visible')
  await expect(barra).toHaveText(['Início', 'Favoritos', 'Canais', 'Outros'])
  await page.getByRole('button', { name: 'Outros' }).click()
  await expect(page.getByRole('heading', { name: 'Outros' })).toBeVisible()
  await page.getByRole('link', { name: /Meus vídeos/ }).click()
  await expect(page.getByRole('heading', { name: 'Meus vídeos' })).toBeVisible()
  await expect(page.locator('.item.ativo-na-barra')).toHaveText('Outros')
})
