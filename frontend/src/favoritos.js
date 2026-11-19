import { ref } from 'vue'
import { api } from './api.js'

// As chaves favoritas ("filme:12") são as mesmas em todas as telas: uma lista só, lida uma vez.
// As pastas (S50) também: lidas quando a folha de escolha abre.
export const favoritos = ref(new Set())
export const pastas = ref([])
let lidas = null
let lidasPastas = null

export function carregarFavoritos() {
  lidas ||= api('/api/favoritos/chaves')
    .then((chaves) => (favoritos.value = new Set(chaves)))
    .catch(() => (lidas = null))
  return lidas
}

export function carregarPastas() {
  lidasPastas ||= api('/api/favoritos/pastas')
    .then((lista) => (pastas.value = lista))
    .catch(() => (lidasPastas = null))
  return lidasPastas
}

export async function criarPasta(nome) {
  const p = await api('/api/favoritos/pastas', { metodo: 'POST', corpo: { nome } })
  pastas.value = [...pastas.value, p].sort((a, b) => a.nome.localeCompare(b.nome, 'pt-BR'))
  return p
}

// Marca na hora e desfaz se o servidor recusar. pasta 0 é "sem pasta"; de novo no mesmo item só
// muda a pasta.
export async function favoritar(tipo, id, pasta = 0) {
  const chave = `${tipo}:${id}`
  const era = favoritos.value.has(chave)
  const novo = new Set(favoritos.value)
  novo.add(chave)
  favoritos.value = novo
  try {
    await api(`/api/favoritos/${tipo}/${id}`, { metodo: 'PUT', corpo: { pasta } })
  } catch (e) {
    const volta = new Set(favoritos.value)
    era ? volta.add(chave) : volta.delete(chave)
    favoritos.value = volta
    throw e
  }
}

export async function desfavoritar(tipo, id) {
  const chave = `${tipo}:${id}`
  const era = favoritos.value.has(chave)
  const novo = new Set(favoritos.value)
  novo.delete(chave)
  favoritos.value = novo
  try {
    await api(`/api/favoritos/${tipo}/${id}`, { metodo: 'DELETE' })
  } catch (e) {
    const volta = new Set(favoritos.value)
    era ? volta.add(chave) : volta.delete(chave)
    favoritos.value = volta
    throw e
  }
}
