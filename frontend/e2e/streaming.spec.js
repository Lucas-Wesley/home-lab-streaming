import { expect, test } from '@playwright/test'

// Fluxo principal com o provedor falso (backend/provedorfalso): colar o acesso, ver o catálogo,
// buscar e abrir filme e série. Os dados são inventados: a repo é pública.
const MENSAGEM = `*Usuário:* usuario-teste
*Senha:* senha-teste
*DNS:* http://localhost:8097`

test('do acesso colado até a página da série', async ({ page }) => {
  // Ajustes em níveis: a primeira tela só lista as seções.
  await page.goto('/#/ajustes')
  await page.getByRole('link', { name: /Assinatura/ }).click()
  await page.getByLabel('Mensagem do provedor').fill(MENSAGEM)
  await page.getByRole('button', { name: 'Guardar acesso' }).click()

  await expect(page.getByText('usuario-teste')).toBeVisible()
  await expect(page.getByText('Ativa')).toBeVisible()
  await page.getByRole('link', { name: 'Ajustes' }).first().click()
  await page.getByRole('link', { name: /Catálogo/ }).click()
  await expect(page.getByText('2 canais')).toBeVisible({ timeout: 15000 })
  await expect(page.getByText('2 filmes')).toBeVisible()
  await expect(page.getByText('1 série', { exact: true })).toBeVisible()
  // A senha não aparece em lugar nenhum.
  await expect(page.getByText('senha-teste')).toHaveCount(0)

  // Busca sem acento acha "Ação Total (2024)".
  await page.getByRole('button', { name: 'Início' }).click()
  await page.getByLabel('Buscar').fill('acao')
  await expect(page.getByText('Ação Total (2024)')).toBeVisible()
  await page.getByText('Ação Total (2024)').click()
  await expect(page.getByRole('heading', { name: 'Ação Total (2024)' })).toBeVisible()
  await expect(page.getByText('Um filme de teste.')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Assistir' })).toBeVisible()
  // Favoritar pede a pasta: criar uma já guarda o favorito nela.
  await page.getByRole('button', { name: 'Favoritar' }).click()
  const folhaPasta = page.getByRole('dialog', { name: 'Favoritar' })
  await expect(folhaPasta.getByText('Guardar em qual pasta?')).toBeVisible()
  await folhaPasta.getByLabel('Nome da nova pasta').fill('ING - Teste')
  await folhaPasta.getByRole('button', { name: 'Criar' }).click()
  await expect(page.getByRole('button', { name: 'Tirar dos favoritos ou mudar de pasta' })).toBeVisible()

  // Série: o próximo episódio e a lista, com o título limpo.
  // Vinda de uma lista rolada, a página da série abre no topo.
  await page.goto('/#/series')
  await page.evaluate(() => {
    document.body.style.minHeight = '5000px'
    window.scrollTo(0, 2000)
  })
  await page.getByText('Série de Teste').evaluate((el) => el.click()) // sem rolar até o item
  await expect(page.getByText('T1 E1 · O começo')).toBeVisible() // o próximo a ver
  expect(await page.evaluate(() => window.scrollY)).toBe(0)
  await page.evaluate(() => (document.body.style.minHeight = ''))
  await expect(page.getByRole('button', { name: /O começo/ })).toBeVisible()
  await page.getByRole('button', { name: /O meio/ }).click()
  await expect(page.getByRole('dialog')).toContainText('T1 E2')

  // Canais: os jogos do dia (sem o ge no teste, só o aviso) e o caminho para todos os canais.
  await page.goto('/#/canais')
  await expect(page.getByRole('heading', { name: 'Jogos' })).toBeVisible()
  await page.getByRole('link', { name: 'Todos os canais' }).click()
  await expect(page.getByRole('heading', { name: 'Todos os canais' })).toBeVisible()
  // A categoria adulta fica escondida.
  await expect(page.getByText('Jornal 24h')).toBeVisible()
  await expect(page.getByText('Canal Adulto')).toHaveCount(0)

  // Canal favoritado pela folha, sem pasta; a aba Favoritos lista as pastas e cada uma abre.
  await page.getByText('Jornal 24h').click()
  await page.getByRole('dialog').getByRole('button', { name: 'Favoritar' }).click()
  await page.getByRole('dialog', { name: 'Favoritar' }).getByRole('button', { name: 'Sem pasta' }).click()
  await expect(page.getByRole('dialog', { name: 'Favoritar' })).toHaveCount(0)
  await page.getByRole('dialog', { name: 'Jornal 24h' }).getByRole('button', { name: 'Fechar' }).click()
  await page.getByRole('button', { name: 'Favoritos' }).click()
  await expect(page.getByRole('heading', { name: 'Favoritos' })).toBeVisible()
  await expect(page.getByRole('link', { name: /Todos/ })).toBeVisible()
  await expect(page.getByRole('link', { name: /ING - Teste/ })).toBeVisible()
  await expect(page.getByRole('link', { name: /Sem pasta/ })).toBeVisible()
  await expect(page.getByText('Jornal 24h')).toHaveCount(0)
  await page.getByRole('link', { name: /ING - Teste/ }).click()
  await expect(page.getByRole('heading', { name: 'ING - Teste' })).toBeVisible()
  await expect(page.getByText('Ação Total (2024)')).toBeVisible()
  await expect(page.getByText('Jornal 24h')).toHaveCount(0)
  await page.getByRole('link', { name: 'Favoritos' }).click()
  await page.getByRole('link', { name: /Sem pasta/ }).click()
  await expect(page.getByRole('heading', { name: 'Sem pasta' })).toBeVisible()
  await expect(page.getByText('Jornal 24h')).toBeVisible()
  await expect(page.getByText('Ação Total (2024)')).toHaveCount(0)
  await expect(page.getByText('Missão Explosiva')).toHaveCount(0)
})
