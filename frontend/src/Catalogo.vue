<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import Grade from './Grade.vue'
import { api, lembrar } from './api.js'

// Canais, Filmes ou Séries: as categorias do provedor em pílulas e os itens da escolhida, que
// vão carregando conforme a página desce.
const props = defineProps({
  tipo: { type: String, required: true },
  categoria: { type: String, default: '' },
})

const TITULOS = { canal: 'Canais', filme: 'Filmes', serie: 'Séries' }
const ROTAS = { canal: 'canais', filme: 'filmes', serie: 'series' }
const POR_VEZ = 60

const categorias = ref([])
const itens = ref([])
const carregando = ref(false)
const acabou = ref(false)
const erro = ref('')
const fim = ref(null)
const pilulas = ref(null)

// Canais não têm "Recentes": a ordem dos canais é a das categorias.
const comRecentes = computed(() => props.tipo !== 'canal')
const atual = computed(() => {
  if (props.categoria) return props.categoria
  const guardada = lembrar(`categoria-${props.tipo}`)
  if (guardada && categorias.value.some((c) => c.id === guardada)) return guardada
  return comRecentes.value ? '' : categorias.value[0]?.id || ''
})

function escolher(id) {
  lembrar(`categoria-${props.tipo}`, id)
  location.hash = `#/${ROTAS[props.tipo]}${id ? `/${id}` : ''}`
}

async function carregarMais() {
  if (carregando.value || acabou.value) return
  carregando.value = true
  try {
    const q = new URLSearchParams({ limite: POR_VEZ, pular: itens.value.length })
    if (atual.value) q.set('categoria', atual.value)
    const lote = await api(`/api/itens/${props.tipo}?${q}`)
    itens.value.push(...lote)
    acabou.value = lote.length < POR_VEZ
  } catch (e) {
    erro.value = e.status === 0 ? 'Sem conexão: o Streaming só abre na rede de casa.' : e.message
    acabou.value = true
  } finally {
    carregando.value = false
  }
}

let observador
onMounted(async () => {
  try {
    categorias.value = await api(`/api/categorias/${props.tipo}`)
  } catch (e) {
    erro.value = e.status === 0 ? 'Sem conexão: o Streaming só abre na rede de casa.' : e.message
    return
  }
  await carregarMais()
  await nextTick()
  pilulas.value?.querySelector('.ativa')?.scrollIntoView({ block: 'nearest', inline: 'center' })
  observador = new IntersectionObserver((entradas) => entradas[0].isIntersecting && carregarMais(), { rootMargin: '800px' })
  if (fim.value) observador.observe(fim.value)
})

onBeforeUnmount(() => observador?.disconnect())
</script>

<template>
  <div class="pagina">
    <h1>{{ TITULOS[tipo] }}</h1>
    <div v-if="erro" class="aviso erro">{{ erro }}</div>

    <div v-if="categorias.length" ref="pilulas" class="pilulas" role="tablist">
      <button v-if="comRecentes" class="pilula" :class="{ ativa: atual === '' }" role="tab" :aria-selected="atual === ''" @click="escolher('')">Recentes</button>
      <button
        v-for="c in categorias"
        :key="c.id"
        class="pilula"
        :class="{ ativa: atual === c.id }"
        role="tab"
        :aria-selected="atual === c.id"
        @click="escolher(c.id)"
      >
        {{ c.nome }}
      </button>
    </div>
    <p v-else-if="!erro && !carregando" class="vazio">
      O catálogo ainda está vazio. Cole o acesso da assinatura em <a href="#/ajustes">Ajustes</a> e espere a primeira sincronização.
    </p>

    <Grade :itens="itens" />
    <div ref="fim" class="fim">{{ carregando ? 'Carregando…' : '' }}</div>
  </div>
</template>

<style scoped>
.pagina {
  max-width: 1080px;
}

/* As categorias passam de lado, numa linha só, como num app de streaming. */
.pilulas {
  display: flex;
  gap: 4px;
  margin: 0 calc(-1 * var(--margem)) 20px;
  padding: 0 var(--margem) 4px;
  overflow-x: auto;
  scrollbar-width: none;
}

.pilulas::-webkit-scrollbar {
  display: none;
}

.pilula {
  flex: none;
}

.fim {
  min-height: 40px;
  padding: 16px 0;
  color: var(--texto-fraco);
  font-size: 0.85rem;
  text-align: center;
}
</style>
