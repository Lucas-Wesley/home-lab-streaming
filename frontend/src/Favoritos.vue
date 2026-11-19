<script setup>
import { computed, onMounted, ref } from 'vue'
import Grade from './Grade.vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { favoritos } from './favoritos.js'

// Os favoritos por pasta (S50) e os sem pasta por tipo. Desmarcar um (na folha do coração)
// tira daqui na hora.
const lista = ref(null)
const erro = ref('')

const GRUPOS = [
  { tipo: 'canal', nome: 'Canais' },
  { tipo: 'filme', nome: 'Filmes' },
  { tipo: 'serie', nome: 'Séries' },
]

const visiveis = (itens) => (itens || []).filter((it) => favoritos.value.has(`${it.tipo}:${it.id}`))

const pastasComItens = computed(() => (lista.value?.pastas || []).map((p) => ({ ...p, itens: visiveis(p.itens) })).filter((p) => p.itens.length))

const semPasta = computed(() => {
  if (!lista.value) return null
  const r = {}
  for (const g of GRUPOS) r[g.tipo] = visiveis(lista.value.itens[g.tipo])
  return r
})
const algumSemPasta = computed(() => semPasta.value && GRUPOS.some((g) => semPasta.value[g.tipo].length))
const vazia = computed(() => lista.value && !pastasComItens.value.length && !algumSemPasta.value)

onMounted(async () => {
  try {
    const [dados, chaves] = await Promise.all([api('/api/favoritos'), api('/api/favoritos/chaves')])
    favoritos.value = new Set(chaves)
    lista.value = dados
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
      Nada por aqui ainda. Toque em <strong>Favoritar</strong> na página de um filme ou de uma série, ou ao abrir um canal, e escolha a pasta.
    </p>
    <template v-if="lista">
      <section v-for="p in pastasComItens" :key="p.id">
        <p class="rotulo"><Icone nome="pasta" class="icone-pasta" />{{ p.nome }}</p>
        <Grade :itens="p.itens" />
      </section>
      <template v-if="algumSemPasta">
        <p v-if="pastasComItens.length" class="rotulo">Sem pasta</p>
        <section v-for="g in GRUPOS" :key="g.tipo">
          <template v-if="semPasta[g.tipo].length">
            <p class="rotulo" :class="{ sub: pastasComItens.length }">{{ g.nome }}</p>
            <Grade :itens="semPasta[g.tipo]" />
          </template>
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

.icone-pasta {
  display: inline-block;
  width: 15px;
  height: 15px;
  margin-right: 6px;
  vertical-align: -2px;
}

.rotulo.sub {
  margin-top: 4px;
  font-size: 0.8rem;
  font-weight: 500;
  opacity: 0.75;
}
</style>
