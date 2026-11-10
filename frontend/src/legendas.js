import { api } from './api.js'

// Sincronia da legenda escolhida (S25): o ajuste automático roda no servidor depois da escolha;
// esperarAjuste espera ele terminar (até 4 min) e devolve a legenda com o atraso final.
export async function esperarAjuste(chave, legenda) {
  let l = legenda
  for (let i = 0; l?.alinhando && i < 120; i++) {
    await new Promise((ok) => setTimeout(ok, 2000))
    try {
      l = (await api(`/api/legendas/${chave}`)).escolhida
    } catch {
      // tenta de novo
    }
  }
  return l
}

// enderecoDaLegenda: o WebVTT com a sincronia guardada (cada ajuste é um endereço novo).
// A gerada pelo áudio (S29) cresce: cada minuto novo (ou revisado) é um endereço novo.
export function enderecoDaLegenda(l) {
  const versao = l.geracao ? `&v=${l.geracao.feitos}-${l.geracao.revisados}` : ''
  return `/api/legenda/${l.id}.vtt?atraso=${l.atraso || 0}&escala=${l.escala || 1}${versao}`
}

// acompanharGeracao acompanha a geração e chama aoMudar a cada minuto novo; para quando fica
// completa, para ou dá erro, ou quando parar() for chamada.
export function acompanharGeracao(chave, aoMudar) {
  let ativo = true
  ;(async () => {
    let anterior = ''
    while (ativo) {
      await new Promise((ok) => setTimeout(ok, 5000))
      if (!ativo) break
      try {
        const l = (await api(`/api/legendas/${chave}`)).escolhida
        const g = l?.geracao
        if (!g) break
        const marca = `${g.estado}-${g.feitos}-${g.revisados}-${g.puxar}`
        if (marca !== anterior) {
          anterior = marca
          aoMudar(l)
        }
        if (['pronta', 'parada', 'erro'].includes(g.estado)) break
      } catch {
        // tenta de novo
      }
    }
  })()
  return () => (ativo = false)
}

export function sincroniaLegivel(l) {
  if (!l) return ''
  const s = Math.abs(l.atraso || 0).toLocaleString('pt-BR', { minimumFractionDigits: 1, maximumFractionDigits: 1 })
  if (!l.atraso) return 'sem atraso'
  return l.atraso > 0 ? `atrasada ${s} s` : `adiantada ${s} s`
}

// lerVTT transforma o WebVTT em falas { inicio, fim, texto }, em segundos e sem marcas (<i>, {\an8}).
export function lerVTT(texto) {
  const segundos = (t) => {
    const p = t.trim().replace(',', '.').split(':').map(Number)
    return p.length === 3 ? p[0] * 3600 + p[1] * 60 + p[2] : p[0] * 60 + p[1]
  }
  const falas = []
  for (const bloco of texto.replace(/\r/g, '').split(/\n{2,}/)) {
    const linhas = bloco.split('\n')
    const i = linhas.findIndex((l) => l.includes('-->'))
    if (i < 0) continue
    const [ini, fim] = linhas[i].split('-->')
    const fala = linhas
      .slice(i + 1)
      .join('\n')
      .replace(/<[^>]+>|\{[^}]*\}/g, '')
      .trim()
    if (fala) falas.push({ inicio: segundos(ini), fim: segundos(fim.trim().split(/\s+/)[0]), texto: fala })
  }
  return falas.sort((a, b) => a.inicio - b.inicio)
}

// falaAtual: o índice da última fala que já começou (-1 antes da primeira).
export function falaAtual(falas, posicao) {
  let lo = 0
  let hi = falas.length - 1
  let achada = -1
  while (lo <= hi) {
    const meio = (lo + hi) >> 1
    if (falas[meio].inicio <= posicao) {
      achada = meio
      lo = meio + 1
    } else hi = meio - 1
  }
  return achada
}
