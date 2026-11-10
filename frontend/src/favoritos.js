import { ref } from 'vue'
import { api } from './api.js'

// As chaves favoritas ("filme:12") são as mesmas em todas as telas: uma lista só, lida uma vez.
export const favoritos = ref(new Set())
let lidas = null

export function carregarFavoritos() {
  lidas ||= api('/api/favoritos/chaves')
    .then((chaves) => (favoritos.value = new Set(chaves)))
    .catch(() => (lidas = null))
  return lidas
}

// Marca na hora e desfaz se o servidor recusar.
export async function alternarFavorito(tipo, id) {
  const chave = `${tipo}:${id}`
  const era = favoritos.value.has(chave)
  const novo = new Set(favoritos.value)
  era ? novo.delete(chave) : novo.add(chave)
  favoritos.value = novo
  try {
    await api(`/api/favoritos/${tipo}/${id}`, { metodo: era ? 'DELETE' : 'PUT' })
  } catch (e) {
    const volta = new Set(favoritos.value)
    era ? volta.add(chave) : volta.delete(chave)
    favoritos.value = volta
    throw e
  }
  return !era
}
