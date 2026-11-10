<script setup>
import { computed, onMounted, ref } from 'vue'
import Grade from './Grade.vue'
import { api } from './api.js'
import { favoritos } from './favoritos.js'

// Os favoritos por tipo. Desmarcar um (na página dele ou na folha do canal) tira daqui na hora.
const lista = ref(null)
const erro = ref('')

const GRUPOS = [
  { tipo: 'canal', nome: 'Canais' },
  { tipo: 'filme', nome: 'Filmes' },
  { tipo: 'serie', nome: 'Séries' },
]

const visiveis = computed(() => {
  if (!lista.value) return null
  const r = {}
  for (const g of GRUPOS) r[g.tipo] = lista.value[g.tipo].filter((it) => favoritos.value.has(`${it.tipo}:${it.id}`))
  return r
})
const vazia = computed(() => visiveis.value && GRUPOS.every((g) => !visiveis.value[g.tipo].length))

onMounted(async () => {
  try {
    const [itens, chaves] = await Promise.all([api('/api/favoritos'), api('/api/favoritos/chaves')])
    favoritos.value = new Set(chaves)
    lista.value = itens
  } catch (e) {
    erro.value = e.status === 0 ? 'Sem conexão: o Streaming só abre na rede de casa.' : e.message
  }
})
</script>

<template>
  <div class="pagina">
    <h1>Favoritos</h1>
    <div v-if="erro" class="aviso erro">{{ erro }}</div>
    <p v-if="vazia" class="vazio">
      Nada por aqui ainda. Toque em <strong>Favoritar</strong> na página de um filme ou de uma série, ou ao abrir um canal.
    </p>
    <template v-if="visiveis">
      <template v-for="g in GRUPOS" :key="g.tipo">
        <section v-if="visiveis[g.tipo].length">
          <p class="rotulo">{{ g.nome }}</p>
          <Grade :itens="visiveis[g.tipo]" />
        </section>
      </template>
    </template>
  </div>
</template>

<style scoped>
.pagina {
  max-width: 1080px;
}

.rotulo:first-of-type {
  margin-top: 8px;
}
</style>
