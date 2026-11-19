<script setup>
import { computed, onMounted, ref } from 'vue'
import Grade from './Grade.vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { favoritos } from './favoritos.js'

// Favoritos em níveis (S50), como os Ajustes e as pastas de Meus vídeos: a aba lista as pastas
// (com "Todos" e "Sem pasta"); cada uma abre numa página própria, em #/favoritos/<pasta>.
// Desmarcar um item na folha do coração tira daqui na hora.
const props = defineProps({ secao: { type: String, default: '' } })

const lista = ref(null)
const erro = ref('')

const GRUPOS = [
  { tipo: 'canal', nome: 'Canais' },
  { tipo: 'filme', nome: 'Filmes' },
  { tipo: 'serie', nome: 'Séries' },
]

const visiveis = (itens) => (itens || []).filter((it) => favoritos.value.has(`${it.tipo}:${it.id}`))
const quantos = (n) => `${n} ${n === 1 ? 'item' : 'itens'}`

const pastas = computed(() => (lista.value?.pastas || []).map((p) => ({ ...p, itens: visiveis(p.itens) })))
const semPasta = computed(() => {
  if (!lista.value) return []
  return GRUPOS.flatMap((g) => visiveis(lista.value.itens[g.tipo]))
})
const tudo = computed(() => [...pastas.value.flatMap((p) => p.itens), ...semPasta.value])

// A seção aberta: "todos", "sem-pasta" ou o id de uma pasta. Sem seção, a lista de pastas.
const aberta = computed(() => {
  if (props.secao === 'todos') return { nome: 'Todos', itens: tudo.value }
  if (props.secao === 'sem-pasta') return { nome: 'Sem pasta', itens: semPasta.value }
  return pastas.value.find((p) => String(p.id) === props.secao) || null
})

const grupos = computed(() =>
  GRUPOS.map((g) => ({ ...g, itens: (aberta.value?.itens || []).filter((it) => it.tipo === g.tipo) })).filter((g) => g.itens.length),
)

const vazia = computed(() => lista.value && !tudo.value.length)

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
    <a v-if="aberta" href="#/favoritos" class="voltar-ajustes"><Icone nome="voltar" /> Favoritos</a>
    <h1>{{ aberta ? aberta.nome : 'Favoritos' }}</h1>
    <div v-if="erro" class="aviso erro">{{ erro }}</div>
    <p v-if="vazia" class="vazio">
      Nada por aqui ainda. Toque em <strong>Favoritar</strong> na página de um filme ou de uma série, ou ao abrir um canal, e escolha a pasta.
    </p>

    <nav v-if="lista && !aberta && !vazia" class="lista-de-ajustes" aria-label="Pastas de favoritos">
      <a href="#/favoritos/todos">
        <span class="icone-da-pasta"><Icone nome="favorito" /></span>
        <span>
          <span class="nome">Todos</span>
          <span class="descricao">{{ quantos(tudo.length) }}</span>
        </span>
        <Icone nome="avancar" class="seta" />
      </a>
      <a v-for="p in pastas" :key="p.id" :href="`#/favoritos/${p.id}`">
        <span class="icone-da-pasta"><Icone nome="pasta" /></span>
        <span>
          <span class="nome">{{ p.nome }}</span>
          <span class="descricao">{{ quantos(p.itens.length) }}</span>
        </span>
        <Icone nome="avancar" class="seta" />
      </a>
      <a v-if="semPasta.length" href="#/favoritos/sem-pasta">
        <span class="icone-da-pasta"><Icone nome="favorito" /></span>
        <span>
          <span class="nome">Sem pasta</span>
          <span class="descricao">{{ quantos(semPasta.length) }}</span>
        </span>
        <Icone nome="avancar" class="seta" />
      </a>
    </nav>

    <template v-if="aberta">
      <p v-if="!aberta.itens.length" class="vazio">Nada nesta pasta ainda.</p>
      <section v-for="g in grupos" :key="g.tipo">
        <p class="rotulo">{{ g.nome }}</p>
        <Grade :itens="g.itens" />
      </section>
    </template>
  </div>
</template>

<style scoped>
.pagina {
  max-width: 1080px;
}

.icone-da-pasta {
  display: grid;
  flex-shrink: 0;
  place-items: center;
  width: 40px;
  height: 40px;
  border-radius: var(--raio-p);
  background: var(--superficie);
  color: var(--marca);
}

.rotulo:first-of-type {
  margin-top: 8px;
}
</style>
