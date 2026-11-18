import { relogio } from './tempo.js'

// Legenda dupla (S46): inglês e português gravados na imagem, para a TV e o projetor.

export const GRAVANDO = ['na fila', 'sincronizando', 'gravando', 'juntando']

// Folga gravada à frente do ponto antes de mandar à tela: a gravação anda ~3× mais rápido que o
// vídeo (1080p), então com 1 min à frente ela não é alcançada.
export const FOLGA = 60

// gravadoAte: até onde há vídeo gravado sem buraco a partir de pos (pos, se nada).
export function gravadoAte(faixas, pos) {
  for (const [ini, fim] of faixas || []) {
    if (pos >= ini - 0.5 && pos < fim) return fim
  }
  return pos
}

// podeTocar: a tela já pode receber a dupla a partir de pos.
export function podeTocar(d, pos) {
  if (!d) return false
  if (d.estado === 'pronta') return true
  const ate = gravadoAte(d.faixas, pos)
  return ate - pos >= FOLGA || (d.duracao > 0 && ate >= d.duracao - 1 && ate > pos)
}

// textoDaDupla: o que mostrar sobre a gravação.
export function textoDaDupla(d) {
  if (!d?.estado) return ''
  const parte = d.duracao > 0 ? Math.floor((d.gravado / d.duracao) * 100) : 0
  switch (d.estado) {
    case 'na fila':
      return d.esperar ? 'Grava quando o download terminar.' : 'Na fila para gravar.'
    case 'sincronizando':
      return 'Escolhendo e sincronizando as legendas em inglês e português…'
    case 'gravando':
      return `Gravando: ${parte}% (${relogio(d.gravado)} de ${relogio(d.duracao)}).`
    case 'juntando':
      return 'Quase pronta: juntando as partes.'
    case 'pronta':
      return 'Pronta: inglês e português na imagem.'
    case 'erro':
      return `Não deu certo: ${d.mensagem || 'erro ao gravar'}.`
  }
  return ''
}

// fundoDaBarra: a trilha da barra de posição como um degradê: visto (marca), carregado (marca
// clara) e o resto. carregado: trechos [início, fim] em segundos.
export function fundoDaBarra(posicao, duracao, carregado) {
  const resto = 'var(--linha-forte)'
  const visto = 'var(--marca)'
  const claro = 'color-mix(in srgb, var(--marca) 45%, var(--linha-forte))'
  if (!duracao) return resto
  const pct = (s) => `${Math.max(0, Math.min(100, (s / duracao) * 100)).toFixed(2)}%`
  const partes = [`${visto} 0`, `${visto} ${pct(posicao)}`]
  let cursor = posicao
  for (const [ini, fim] of [...(carregado || [])].sort((a, b) => a[0] - b[0])) {
    if (fim <= cursor) continue
    const a = Math.max(ini, cursor)
    if (a > cursor) partes.push(`${resto} ${pct(cursor)}`, `${resto} ${pct(a)}`)
    partes.push(`${claro} ${pct(a)}`, `${claro} ${pct(fim)}`)
    cursor = fim
  }
  partes.push(`${resto} ${pct(cursor)}`, `${resto} 100%`)
  return `linear-gradient(to right, ${partes.join(', ')})`
}
