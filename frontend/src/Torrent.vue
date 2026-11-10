<script setup>
import { computed, inject, onBeforeUnmount, onMounted, ref } from 'vue'
import Assistir from './Assistir.vue'
import Folha from './Folha.vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { relogio } from './tempo.js'
import { tamanho } from './tamanho.js'
import { voltar } from './voltar.js'

// Um torrent: os vídeos (por temporada, se for série), cada um para assistir na hora, baixar
// para depois ou marcar como visto (o que apaga do servidor).
const props = defineProps({ hash: { type: String, required: true } })
const recado = inject('recado')

const dados = ref(null)
const erro = ref('')
const escolhido = ref(null)

const videos = computed(() => (dados.value?.arquivos || []).filter((a) => a.tipo === 'video'))
const legendas = computed(() => (dados.value?.arquivos || []).filter((a) => a.tipo === 'legenda'))
const temporadas = computed(() => [...new Set(videos.value.map((v) => v.temporada))])
const ehSerie = computed(() => videos.value.some((v) => v.episodio > 0))

function rotulo(v) {
  return v.episodio ? `E${v.episodio}` : ''
}

function tituloDe(v) {
  const t = dados.value.torrent
  if (v.episodio) return `${t.titulo} T${v.temporada} E${v.episodio}`
  return videos.value.length > 1 ? `${t.titulo} · ${v.nome}` : t.titulo
}

async function carregar() {
  try {
    dados.value = await api(`/api/torrents/${props.hash}`)
    erro.value = ''
  } catch (e) {
    erro.value = e.status === 0 ? 'Sem conexão: o Streaming só abre na rede de casa.' : e.message
  }
}

async function acao(v, caminho, metodo, mensagem) {
  try {
    await api(`/api/arquivos/${v.id}/${caminho}`, { metodo })
    if (mensagem) recado(mensagem)
    await carregar()
  } catch (e) {
    recado(e.message, true)
  }
}

const baixar = (v) => acao(v, 'baixar', 'POST', 'Baixando para o servidor. Pode sair desta tela.')
const cancelar = (v) => acao(v, 'baixar', 'DELETE', 'Download cancelado.')
function apagarBaixado(v) {
  if (!confirm('Apagar do servidor o arquivo baixado? O vídeo continua na lista para assistir pelo torrent.')) return
  acao(v, 'baixar', 'DELETE', 'Arquivo apagado do servidor.')
  escolhido.value = null
}
function visto(v) {
  if (v.download && !confirm('Marcar como visto apaga o que foi baixado deste vídeo. Continuar?')) return
  acao(v, 'visto', 'POST', 'Marcado como visto.')
  escolhido.value = null
}
const naoVisto = (v) => acao(v, 'visto', 'DELETE')

async function remover() {
  if (!confirm(`Remover “${dados.value.torrent.titulo}”? O que foi baixado dele é apagado.`)) return
  try {
    await api(`/api/torrents/${props.hash}`, { metodo: 'DELETE' })
    location.hash = '#/torrents'
  } catch (e) {
    recado(e.message, true)
  }
}

const porcentagem = (v) => Math.floor(((v.baixado || 0) / (v.tamanho || 1)) * 100)

let relogioDaTela
onMounted(() => {
  carregar()
  // Enquanto baixa, ou enquanto a lista do magnet não chegou, a tela se atualiza sozinha.
  relogioDaTela = setInterval(() => {
    if (!dados.value || !dados.value.torrent.tem_ficha || videos.value.some((v) => v.download === 'baixando')) carregar()
  }, 2000)
})
onBeforeUnmount(() => clearInterval(relogioDaTela))
</script>

<template>
  <div class="pagina">
    <a class="voltar" href="#/torrents" @click.prevent="voltar('#/torrents')"><Icone nome="voltar" />Downloads</a>
    <div v-if="erro" class="aviso erro">{{ erro }}</div>

    <template v-if="dados">
      <header class="cabeca">
        <img v-if="dados.torrent.capa" class="cartaz" :src="dados.torrent.capa" alt="" referrerpolicy="no-referrer" />
        <div>
          <div class="titulo-e-remover">
            <h1>{{ dados.torrent.titulo }}</h1>
            <button class="botao icone" aria-label="Remover torrent" title="Remover torrent (apaga o que foi baixado)" @click="remover"><Icone nome="lixeira" /></button>
          </div>
          <p class="meta">
            <span v-if="dados.torrent.ano">{{ dados.torrent.ano }}</span>
            <span v-if="videos.length">{{ videos.length }} {{ videos.length === 1 ? 'vídeo' : 'vídeos' }}</span>
            <span v-if="dados.torrent.tamanho">{{ tamanho(dados.torrent.tamanho) }}</span>
            <span v-if="legendas.length">{{ legendas.length }} {{ legendas.length === 1 ? 'legenda' : 'legendas' }}</span>
          </p>
          <Assistir
            v-if="videos.length === 1"
            :chave="`arquivo:${videos[0].id}`"
            :titulo="tituloDe(videos[0])"
            :capa="dados.torrent.capa"
            :posicao="videos[0].posicao || 0"
            :formatos="videos[0].formatos"
          />
          <p v-if="dados.torrent.sinopse" class="sinopse">{{ dados.torrent.sinopse }}</p>
        </div>
      </header>

      <p v-if="!dados.torrent.tem_ficha" class="aviso">
        {{ dados.torrent.erro || 'Buscando a lista de arquivos na rede… (o torrent liga só para isso e desliga em seguida)' }}
      </p>

      <template v-for="t in temporadas" :key="t">
        <p class="rotulo">{{ ehSerie ? (t ? `Temporada ${t}` : 'Outros') : videos.length > 1 ? 'Vídeos' : 'Vídeo' }}</p>
        <ul class="lista">
          <li v-for="v in videos.filter((x) => x.temporada === t)" :key="v.id" class="video" :class="{ visto: v.visto }">
            <button class="abrir" @click="escolhido = v">
              <span v-if="rotulo(v)" class="numero">{{ rotulo(v) }}</span>
              <span class="texto">
                <span class="nome">{{ v.nome }}</span>
                <span class="detalhes">
                  <span>{{ tamanho(v.tamanho) }}</span>
                  <span v-if="v.download === 'baixado'" class="chip ok">baixado</span>
                  <span v-if="v.download === 'baixando'" class="chip andando">baixando {{ porcentagem(v) }}%</span>
                  <span v-if="v.visto" class="chip">visto</span>
                  <span v-if="v.posicao > 0 && !v.visto">parou em {{ relogio(v.posicao) }}</span>
                </span>
              </span>
              <span class="tocar"><Icone nome="tocar" /></span>
            </button>
          </li>
        </ul>
      </template>

      <div class="linha-acoes rodape">
        <button class="botao perigo" @click="remover"><Icone nome="lixeira" />Remover torrent</button>
      </div>
      <p class="suave nota">Remover tira o torrent da lista e apaga do servidor tudo o que foi baixado dele.</p>
    </template>

    <Folha v-if="escolhido" :titulo="escolhido.nome" @fechar="escolhido = null">
      <Assistir :chave="`arquivo:${escolhido.id}`" :titulo="tituloDe(escolhido)" :capa="dados.torrent.capa" :posicao="escolhido.posicao || 0" :formatos="escolhido.formatos" @escolheu="escolhido = null" />
      <div class="linha-acoes extras">
        <button v-if="!escolhido.download" class="botao" @click="baixar(escolhido); escolhido = null"><Icone nome="torrents" />Baixar para depois</button>
        <button v-if="escolhido.download === 'baixando'" class="botao" @click="cancelar(escolhido); escolhido = null">Cancelar download</button>
        <button v-if="escolhido.download === 'baixado'" class="botao perigo" @click="apagarBaixado(escolhido)"><Icone nome="lixeira" />Apagar arquivo baixado</button>
        <button v-if="!escolhido.visto" class="botao" @click="visto(escolhido)">Marcar como visto</button>
        <button v-else class="botao" @click="naoVisto(escolhido); escolhido = null">Desmarcar visto</button>
      </div>
      <p class="suave nota">Assistir baixa só o trecho que você vê. “Visto” apaga do servidor o que foi baixado deste vídeo.</p>
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

.cabeca {
  display: grid;
  grid-template-columns: 180px 1fr;
  gap: 28px;
  align-items: start;
  margin-bottom: 12px;
}

.cabeca:not(:has(.cartaz)) {
  grid-template-columns: 1fr;
}

.cartaz {
  width: 100%;
  border-radius: var(--raio);
  background: var(--superficie);
}

h1 {
  margin-bottom: 8px !important;
  overflow-wrap: anywhere;
}

.titulo-e-remover {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  justify-content: space-between;
}

.titulo-e-remover .botao {
  flex: none;
  color: var(--texto-suave);
}

.titulo-e-remover .botao:hover {
  color: var(--perigo);
}

.rodape .botao svg {
  width: 16px;
  height: 16px;
}

.meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  margin: 0 0 18px;
  color: var(--texto-suave);
  font-size: 0.88rem;
}

.sinopse {
  margin: 18px 0 0;
  line-height: 1.6;
  white-space: pre-line;
}

.aviso {
  margin: 12px 0;
}

.abrir {
  display: grid;
  grid-template-columns: auto 1fr auto;
  gap: 12px;
  align-items: center;
  width: 100%;
  padding: 12px 0;
  border: 0;
  background: none;
  text-align: left;
  cursor: pointer;
}

.numero {
  min-width: 28px;
  color: var(--texto-fraco);
  font-variant-numeric: tabular-nums;
}

.texto {
  display: grid;
  gap: 4px;
  min-width: 0;
}

.nome {
  overflow-wrap: anywhere;
  font-weight: 550;
  line-height: 1.35;
}

.abrir:hover .nome {
  color: var(--marca);
}

.visto .nome {
  color: var(--texto-suave);
}

.detalhes {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
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

.extras {
  margin-top: 12px;
}

.extras .botao svg {
  width: 16px;
  height: 16px;
}

.nota {
  margin: 12px 0 0;
  font-size: 0.8rem;
  line-height: 1.45;
}

.rodape {
  margin-top: 28px;
}

@media (max-width: 820px) {
  .cabeca {
    grid-template-columns: 1fr;
    gap: 18px;
  }

  .cartaz {
    width: 40%;
    max-width: 180px;
  }
}
</style>
