<script setup>
import { computed, onMounted, ref } from 'vue'
import Assistir from './Assistir.vue'
import BotaoFavorito from './BotaoFavorito.vue'
import BotaoBaixar from './BotaoBaixar.vue'
import Folha from './Folha.vue'
import Icone from './Icone.vue'
import { api, lembrar } from './api.js'
import { voltar } from './voltar.js'
import { duracao, relogio } from './tempo.js'

const props = defineProps({ id: { type: String, required: true } })

const serie = ref(null)
const erro = ref('')
const temporada = ref(0)
const escolhido = ref(null) // episódio com a folha de assistir aberta

const temporadas = computed(() => [...new Set((serie.value?.episodios || []).map((e) => e.temporada))])
const episodios = computed(() => (serie.value?.episodios || []).filter((e) => e.temporada === temporada.value))

// O próximo a ver: o primeiro depois do último que teve progresso, ou o primeiro de todos.
const proximo = computed(() => {
  const todos = serie.value?.episodios || []
  const ultimo = todos.findLastIndex((e) => e.posicao > 0)
  if (ultimo < 0) return todos[0]
  const e = todos[ultimo]
  return e.duracao && e.posicao > e.duracao * 0.95 ? todos[ultimo + 1] || e : e
})

function escolherTemporada(t) {
  temporada.value = t
  lembrar(`temporada-${props.id}`, String(t))
}

async function carregar(atualizar = false) {
  try {
    serie.value = await api(`/api/series/${props.id}${atualizar ? '?atualizar' : ''}`, { tempo: 30000 })
    // Conhecida no TMDB: a página do título junta a assinatura e as versões de torrent (S28).
    if (serie.value.tmdb && !atualizar) {
      location.replace(`#/titulo/serie/${serie.value.tmdb}`)
      return
    }
    const guardada = Number(lembrar(`temporada-${props.id}`))
    temporada.value = temporadas.value.includes(guardada) ? guardada : proximo.value?.temporada || temporadas.value[0] || 0
  } catch (e) {
    erro.value = e.status === 0 ? 'Sem conexão: o Streaming só abre na rede de casa.' : e.message
  }
}

function tituloDe(e) {
  return `${serie.value.item.nome} T${e.temporada} E${e.numero}`
}

onMounted(() => carregar())
</script>

<template>
  <div class="pagina">
    <a class="voltar" href="#/series" @click.prevent="voltar('#/series')"><Icone nome="voltar" />Séries</a>
    <div v-if="erro" class="aviso erro">{{ erro }}</div>

    <template v-if="serie">
      <header class="serie">
        <img v-if="serie.item.capa" class="cartaz" :src="serie.item.capa" alt="" referrerpolicy="no-referrer" />
        <div>
          <h1>{{ serie.item.nome }}</h1>
          <p class="meta">
            <span v-if="serie.item.ano">{{ serie.item.ano }}</span>
            <span v-if="temporadas.length">{{ temporadas.length }} {{ temporadas.length === 1 ? 'temporada' : 'temporadas' }}</span>
            <span v-if="serie.item.nota">★ {{ serie.item.nota.toFixed(1) }}</span>
            <span v-if="serie.detalhes?.genero">{{ serie.detalhes.genero }}</span>
          </p>
          <Assistir
            v-if="proximo"
            :chave="`episodio:${proximo.id}`"
            :titulo="tituloDe(proximo)"
            :capa="serie.item.capa"
            :posicao="proximo.posicao || 0"
          />
          <p v-if="proximo" class="suave proximo">T{{ proximo.temporada }} E{{ proximo.numero }} · {{ proximo.titulo }}</p>
          <div class="linha-acoes extras"><BotaoFavorito tipo="serie" :id="serie.item.id" /></div>
          <p v-if="serie.detalhes?.sinopse" class="sinopse">{{ serie.detalhes.sinopse }}</p>
        </div>
      </header>

      <p v-if="!serie.episodios.length" class="vazio">
        O provedor não mandou episódios desta série.
        <button class="botao" @click="carregar(true)"><Icone nome="atualizar" />Buscar de novo</button>
      </p>

      <div v-if="temporadas.length > 1" class="pilulas">
        <button v-for="t in temporadas" :key="t" class="pilula" :class="{ ativa: t === temporada }" @click="escolherTemporada(t)">Temporada {{ t }}</button>
      </div>
      <p v-else-if="temporadas.length" class="rotulo">Temporada {{ temporadas[0] }}</p>

      <ul class="lista episodios">
        <li v-for="e in episodios" :key="e.id">
          <button class="episodio" @click="escolhido = e">
            <span class="numero">{{ e.numero }}</span>
            <span class="texto">
              <span class="titulo">{{ e.titulo }}</span>
              <span v-if="e.sinopse" class="resumo">{{ e.sinopse }}</span>
              <span class="detalhes">
                <span v-if="e.duracao">{{ duracao(e.duracao) }}</span>
                <span v-if="e.posicao > 0">{{ e.duracao && e.posicao > e.duracao * 0.95 ? 'visto' : `parou em ${relogio(e.posicao)}` }}</span>
              </span>
            </span>
            <span class="tocar"><Icone nome="tocar" /></span>
          </button>
        </li>
      </ul>
    </template>

    <Folha v-if="escolhido" :titulo="`T${escolhido.temporada} E${escolhido.numero} · ${escolhido.titulo}`" @fechar="escolhido = null">
      <Assistir :chave="`episodio:${escolhido.id}`" :titulo="tituloDe(escolhido)" :capa="serie.item.capa" :posicao="escolhido.posicao || 0" @escolheu="escolhido = null" />
      <div class="linha-acoes baixar-episodio"><BotaoBaixar :chave="`episodio:${escolhido.id}`" /></div>
    </Folha>
  </div>
</template>

<style scoped>
.pagina {
  max-width: 920px;
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

.serie {
  display: grid;
  grid-template-columns: 180px 1fr;
  gap: 28px;
  align-items: start;
  margin-bottom: 28px;
}

.cartaz {
  width: 100%;
  border-radius: var(--raio);
  background: var(--superficie);
}

h1 {
  margin-bottom: 8px !important;
}

.meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  margin: 0 0 18px;
  color: var(--texto-suave);
  font-size: 0.88rem;
}

.proximo {
  margin: 8px 0 0;
  font-size: 0.85rem;
}

.extras {
  margin-top: 12px;
}

.sinopse {
  margin: 18px 0 0;
  line-height: 1.6;
}

.pilulas {
  display: flex;
  gap: 4px;
  margin: 0 calc(-1 * var(--margem)) 10px;
  padding: 0 var(--margem) 4px;
  overflow-x: auto;
}

.pilula {
  flex: none;
}

.episodio {
  display: grid;
  grid-template-columns: 28px 1fr auto;
  gap: 12px;
  align-items: start;
  width: 100%;
  padding: 14px 0;
  border: 0;
  background: none;
  text-align: left;
  cursor: pointer;
}

.numero {
  color: var(--texto-fraco);
  font-size: 0.95rem;
  font-variant-numeric: tabular-nums;
  line-height: 1.4;
}

.texto {
  display: grid;
  gap: 3px;
}

.titulo {
  font-weight: 600;
  line-height: 1.35;
  transition: color 0.15s ease;
}

.episodio:hover .titulo {
  color: var(--marca);
}

.resumo {
  display: -webkit-box;
  overflow: hidden;
  color: var(--texto-suave);
  font-size: 0.86rem;
  line-height: 1.45;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.detalhes {
  display: flex;
  gap: 12px;
  color: var(--texto-fraco);
  font-size: 0.78rem;
}

.tocar {
  display: grid;
  place-items: center;
  width: 34px;
  height: 34px;
  border-radius: 999px;
  background: var(--superficie);
  color: var(--texto-suave);
}

.tocar svg {
  width: 15px;
  height: 15px;
}

@media (max-width: 820px) {
  .serie {
    grid-template-columns: 1fr;
    gap: 18px;
  }

  .cartaz {
    width: 40%;
    max-width: 180px;
  }
}
.baixar-episodio {
  margin-top: 12px;
}
</style>
