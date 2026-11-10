import { ref } from 'vue'
import { api } from './api.js'

// As telas (TV, projetor) são as mesmas em todas as páginas: uma lista só, lida de tempos em tempos.
export const telas = ref([])
let lidaEm = 0
let lendo = null

export function carregarTelas(forcar = false) {
  if (!forcar && Date.now() - lidaEm < 30000) return Promise.resolve(telas.value)
  if (lendo) return lendo
  lendo = api('/api/telas', { tempo: 8000 })
    .then((lista) => {
      telas.value = lista
      lidaEm = Date.now()
      return lista
    })
    .catch(() => telas.value)
    .finally(() => (lendo = null))
  return lendo
}

// Manda o vídeo para a tela; o servidor para o que tocava em outro lugar (uma conexão só).
export async function tocarNaTela(tela, chave, { posicao = 0, capa = '' } = {}) {
  return api(`/api/telas/${encodeURIComponent(tela.id)}/tocar`, { metodo: 'POST', corpo: { chave, posicao, capa }, tempo: 20000 })
}

export function comandar(idDaTela, comando, valor = 0) {
  return api(`/api/telas/${encodeURIComponent(idDaTela)}/comando`, { metodo: 'POST', corpo: { comando, valor } })
}
