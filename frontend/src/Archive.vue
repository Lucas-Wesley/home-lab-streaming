<script setup>
import { onMounted, ref, watch } from 'vue'
import Folha from './Folha.vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { voltar } from './voltar.js'

// Filmes e desenhos de domínio público do Internet Archive. Adicionar traz o torrent oficial do
// item para a aba Torrents; nada é baixado até assistir ou pedir para baixar.
const COLECOES = [
  { id: 'feature_films', nome: 'Filmes' },
  { id: 'animationandcartoons', nome: 'Animação' },
  { id: 'classic_tv', nome: 'TV clássica' },
  { id: 'silent_films', nome: 'Cinema mudo' },
]

const colecao = ref('feature_films')
const texto = ref('')
const itens = ref([])
const carregando = ref(false)
const erro = ref('')
const escolhido = ref(null)
const adicionando = ref(false)
const erroAoAdicionar = ref('')

let pedido = 0
async function buscar() {
  const este = ++pedido
  carregando.value = true
  erro.value = ''
  try {
    const q = new URLSearchParams({ colecao: colecao.value, q: texto.value.trim() })
    const r = await api(`/api/archive?${q}`, { tempo: 30000 })
    if (este === pedido) itens.value = r
  } catch (e) {
    if (este === pedido) erro.value = e.message
  } finally {
    if (este === pedido) carregando.value = false
  }
}

let espera
watch(texto, () => {
  clearTimeout(espera)
  espera = setTimeout(buscar, 400)
})
watch(colecao, buscar)

async function adicionar(it) {
  adicionando.value = true
  erroAoAdicionar.value = ''
  try {
    const r = await api('/api/torrents', { metodo: 'POST', corpo: { texto: `ia:${it.id}` }, tempo: 60000 })
    location.hash = `#/torrent/${r.hash}`
  } catch (e) {
    erroAoAdicionar.value = e.message
  } finally {
    adicionando.value = false
  }
}

onMounted(buscar)
</script>

<template>
  <div class="pagina">
    <a class="voltar" href="#/torrents" @click.prevent="voltar('#/torrents')"><Icone nome="voltar" />Downloads</a>
    <h1>Internet Archive</h1>
    <p class="suave explicacao">Filmes, desenhos e TV de domínio público, com torrent oficial do archive.org.</p>

    <label class="busca">
      <Icone nome="pesquisar" />
      <input v-model="texto" class="campo" type="search" placeholder="Buscar no Internet Archive" aria-label="Buscar no Internet Archive" enterkeyhint="search" />
    </label>
    <div class="pilulas" role="tablist">
      <button v-for="c in COLECOES" :key="c.id" class="pilula" :class="{ ativa: colecao === c.id }" role="tab" :aria-selected="colecao === c.id" @click="colecao = c.id">
        {{ c.nome }}
      </button>
    </div>

    <div v-if="erro" class="aviso erro">{{ erro }}</div>
    <p v-if="carregando && !itens.length" class="vazio">Buscando…</p>
    <p v-else-if="!carregando && !itens.length && !erro" class="vazio">Nada encontrado.</p>

    <ul class="grade" :class="{ esmaecida: carregando }">
      <li v-for="it in itens" :key="it.id">
        <button class="cartaz" @click="escolhido = it">
          <span class="capa"><img :src="it.capa" alt="" loading="lazy" /></span>
          <span class="nome">{{ it.titulo }}</span>
          <span v-if="it.ano" class="meta">{{ it.ano }}</span>
        </button>
      </li>
    </ul>

    <Folha v-if="escolhido" :titulo="escolhido.titulo" @fechar="escolhido = null">
      <p v-if="escolhido.ano" class="suave">{{ escolhido.ano }}</p>
      <p v-if="escolhido.sinopse" class="sinopse">{{ escolhido.sinopse }}</p>
      <div v-if="erroAoAdicionar" class="aviso erro">{{ erroAoAdicionar }}</div>
      <div class="linha-acoes">
        <button class="botao principal" :disabled="adicionando" @click="adicionar(escolhido)">{{ adicionando ? 'Adicionando…' : 'Adicionar aos torrents' }}</button>
        <a class="botao" :href="`https://archive.org/details/${escolhido.id}`" target="_blank" rel="noopener">Ver no archive.org</a>
      </div>
    </Folha>
  </div>
</template>

<style scoped>
.pagina {
  max-width: 1080px;
}

.voltar {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  margin: -8px 0 16px -6px;
  color: var(--texto-suave);
  font-size: 0.9rem;
  text-decoration: none;
}

.voltar svg {
  width: 18px;
  height: 18px;
}

h1 {
  margin-bottom: 6px !important;
}

.explicacao {
  margin: 0 0 16px;
  font-size: 0.9rem;
}

.busca {
  position: relative;
  display: block;
  margin-bottom: 10px;
}

.busca svg {
  position: absolute;
  top: 50%;
  left: 12px;
  width: 18px;
  height: 18px;
  color: var(--texto-fraco);
  transform: translateY(-50%);
}

.busca .campo {
  min-height: 42px;
  padding-left: 38px;
}

.pilulas {
  display: flex;
  gap: 4px;
  margin: 0 calc(-1 * var(--margem)) 20px;
  padding: 0 var(--margem) 4px;
  overflow-x: auto;
}

.pilula {
  flex: none;
}

.grade {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(132px, 1fr));
  gap: 22px 14px;
  margin: 0;
  padding: 0;
  list-style: none;
  transition: opacity 0.2s ease;
}

.grade.esmaecida {
  opacity: 0.5;
}

.cartaz {
  display: grid;
  gap: 6px;
  width: 100%;
  padding: 0;
  border: 0;
  background: none;
  text-align: left;
  cursor: pointer;
}

.capa {
  display: block;
  aspect-ratio: 2 / 3;
  overflow: hidden;
  border-radius: var(--raio-p);
  background: var(--superficie);
}

.capa img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.nome {
  display: -webkit-box;
  overflow: hidden;
  font-size: 0.88rem;
  font-weight: 550;
  line-height: 1.3;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.cartaz:hover .nome {
  color: var(--marca);
}

.meta {
  margin-top: -3px;
  color: var(--texto-fraco);
  font-size: 0.75rem;
}

.sinopse {
  margin: 0 0 16px;
  line-height: 1.55;
  white-space: pre-line;
}

.aviso {
  margin-bottom: 12px;
}

@media (max-width: 820px) {
  .grade {
    grid-template-columns: repeat(auto-fill, minmax(104px, 1fr));
    gap: 18px 10px;
  }
}
</style>
