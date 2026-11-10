// Jogos do dia (S33): rótulos e ordem da lista de Canais.

const SEMANA = ['Dom', 'Seg', 'Ter', 'Qua', 'Qui', 'Sex', 'Sáb']

// "2026-11-14" → "Hoje", "Amanhã" ou "Sáb 14/11", contando a partir de hoje (também "AAAA-MM-DD").
export function diaDoJogo(dia, hoje) {
  const [a, m, d] = dia.split('-').map(Number)
  const data = new Date(Date.UTC(a, m - 1, d))
  const [ha, hm, hd] = (hoje || dia).split('-').map(Number)
  const dif = Math.round((data - Date.UTC(ha, hm - 1, hd)) / 86400000)
  if (dif === 0) return 'Hoje'
  if (dif === 1) return 'Amanhã'
  return `${SEMANA[data.getUTCDay()]} ${String(d).padStart(2, '0')}/${String(m).padStart(2, '0')}`
}

// Hora local de um horário RFC 3339: "18:30".
export function hora(iso) {
  return new Date(iso).toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit' })
}

// O nome do canal no botão: "Globo SP: São Paulo" fica "Globo SP".
export function nomeDoCanal(nome) {
  return nome.replace(/^(Globo [A-Z]{2}):.*$/, '$1')
}

// Ao vivo primeiro, depois os que vão começar (pela hora) e por último os encerrados.
const PESO = { ao_vivo: 0, antes: 1, encerrado: 2 }
export function ordenarJogos(jogos) {
  return [...jogos].sort((a, b) => PESO[a.momento] - PESO[b.momento] || (a.hora || '99').localeCompare(b.hora || '99'))
}

// A fase só quando diz algo: "Fase única" não diz, e "Amistoso" já está em "Amistosos da Seleção".
export function faseDoJogo(j) {
  const f = (j.fase || '').trim()
  if (!f || /^fase única$/i.test(f)) return ''
  return j.campeonato.toLowerCase().includes(f.toLowerCase().slice(0, 7)) ? '' : f
}
