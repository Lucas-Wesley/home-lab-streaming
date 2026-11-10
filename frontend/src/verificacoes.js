import { reactive } from 'vue'
import { api } from './api.js'

// Verificações das opções da busca (S23), por chave da opção. A página do título pede as três
// primeiras sozinha; as demais, pelo botão "Verificar". Enquanto alguma roda, a lista se atualiza.
export const verificacoes = reactive({})

export async function carregarVerificacoes(chaves) {
  const faltam = chaves.filter(Boolean)
  for (let i = 0; i < faltam.length; i += 100) {
    try {
      Object.assign(verificacoes, await api(`/api/verificacoes?chaves=${encodeURIComponent(faltam.slice(i, i + 100).join(','))}`))
    } catch {
      // Sem rede agora: fica para a próxima.
    }
  }
  acompanhar()
}

// verificar pede a verificação de uma opção. contexto: { tmdb: 'filme/1', temporada, episodio }.
export async function verificar(r, contexto, deNovo = false) {
  verificacoes[r.chave] = { chave: r.chave, estado: 'verificando', resumo: 'Verificando…' }
  try {
    const v = await api(`/api/verificacoes${deNovo ? '?de-novo' : ''}`, {
      metodo: 'POST',
      corpo: { chave: r.chave, magnet: r.magnet, link: r.link, nome: r.titulo, ...contexto },
    })
    verificacoes[r.chave] = v
  } catch (e) {
    verificacoes[r.chave] = { chave: r.chave, estado: 'falhou', resumo: e.message }
  }
  acompanhar()
}

let relogio
function acompanhar() {
  if (relogio) return
  relogio = setInterval(async () => {
    const pendentes = Object.keys(verificacoes).filter((k) => verificacoes[k]?.estado === 'verificando')
    if (!pendentes.length) {
      clearInterval(relogio)
      relogio = null
      return
    }
    try {
      Object.assign(verificacoes, await api(`/api/verificacoes?chaves=${encodeURIComponent(pendentes.join(','))}`))
    } catch {
      // tenta de novo no próximo
    }
  }, 3000)
}

// Quanto a verificação muda a nota de uma opção na ordem da lista.
export function ajusteDaVerificacao(v) {
  return { ok: 20, suspeito: -60, errado: -300 }[v?.estado] || 0
}

export function recomendavel(o, v) {
  return !o.pesado && !o.suspeito && o.semeadores > 0 && !['errado', 'suspeito'].includes(v?.estado)
}
