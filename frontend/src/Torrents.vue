<script setup>
import { computed, inject, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Assistir from './Assistir.vue'
import Folha from './Folha.vue'
import Icone from './Icone.vue'
import SalvarBusca from './SalvarBusca.vue'
import { enderecoDaBusca } from './buscasSalvas.js'
import OpcaoDeTorrent from './OpcaoDeTorrent.vue'
import { api } from './api.js'
import { tamanho } from './tamanho.js'

// Downloads (S45): uma lista só, vídeo por vídeo, do que está no servidor ou baixando (da assinatura
// e de torrent), com o nome do título. Os torrents assistidos sem baixar não aparecem aqui.
//
// Filmes e séries se buscam no Início (assinatura e torrents juntos). Aqui fica a busca direta nos
// indexadores, para o que o TMDB não conhece, e o "Adicionar link magnet", para um torrent de fora.
const props = defineProps({ busca: { type: String, default: '' } })
const recado = inject('recado')

const daAssinatura = ref(null)
const deTorrent = ref(null)
const livre = ref(0)
const erro = ref('')
const adicionando = ref(false)
const texto = ref('')
const enviando = ref(false)
const erroAoAdicionar = ref('')
const arquivo = ref(null)

const TIPOS = [
  { id: '', nome: 'Tudo' },
  { id: 'filme', nome: 'Filmes' },
  { id: 'serie', nome: 'Séries' },
]

const busca = ref(props.busca)
const tipo = ref('')
const resultados = ref(null)
const buscando = ref(false)
const erroDaBusca = ref('')
const assistindo = ref(null) // vídeo com a folha de assistir aberta

const buscaAberta = computed(() => buscando.value || resultados.value || erroDaBusca.value)

const ESTADOS = { 'na fila': 'Na fila', baixando: 'Baixando', esperando: 'Esperando a assinatura ficar livre', baixado: 'No servidor', erro: 'Erro' }
const episodio = (v) => (v.episodio ? ` · T${v.temporada} E${v.episodio}` : '')

// Os dois tipos de download numa lista só: o que está baixando primeiro.
const videos = computed(() => {
  const assinatura = (daAssinatura.value || []).map((d) => ({
    chave: d.chave, titulo: d.titulo, capa: d.capa, origem: 'Assinatura', tamanho: d.tamanho, baixado: d.baixado, estado: d.estado, mensagem: d.mensagem,
    apagar: () => api(`/api/assinatura/downloads/${d.chave}`, { metodo: 'DELETE' }),
  }))
  const torrent = (deTorrent.value || []).map((v) => ({
    chave: `arquivo:${v.id}`, titulo: v.titulo + episodio(v), capa: v.capa, origem: 'Torrent', detalhe: v.torrent !== v.titulo ? v.torrent : '',
    tamanho: v.tamanho, baixado: v.baixado, estado: v.estado, pagina: v.tmdb ? `#/titulo/${v.tmdb}` : '',
    dupla: v.dupla || '', duplaParte: v.dupla_parte || 0, posicao: v.posicao || 0,
    apagar: () => api(`/api/arquivos/${v.id}/baixar`, { metodo: 'DELETE' }),
  }))
  return [...assinatura, ...torrent].sort((a, b) => (a.estado === 'baixado') - (b.estado === 'baixado'))
})
const carregado = computed(() => daAssinatura.value && deTorrent.value)
const meta = (v) =>
  [v.origem, tamanho(v.tamanho), (ESTADOS[v.estado] || v.estado) + (v.estado !== 'baixado' && v.tamanho ? ` ${porcentagem(v)}%` : '')].filter(Boolean).join(' · ')
const porcentagem = (v) => (v.tamanho ? Math.floor((v.baixado / v.tamanho) * 100) : 0)

// Legenda dupla (S46): pedir daqui grava o filme inteiro (do começo); se ainda baixa, espera o fim.
const DUPLA = { 'na fila': 'na fila', sincronizando: 'sincronizando', gravando: 'gravando', juntando: 'quase pronta', pronta: 'pronta', erro: 'deu erro' }
const textoDaDupla = (v) => `Legenda dupla ${DUPLA[v.dupla] || v.dupla}${v.dupla === 'gravando' ? ` ${Math.floor(v.duplaParte * 100)}%` : ''}`

async function prepararDupla(v) {
  try {
    await api(`/api/legenda-dupla/${v.chave}`, { metodo: 'POST', corpo: { comeco: 0, esperar: v.estado !== 'baixado' } })
    recado(v.estado === 'baixado' ? 'Gravando a legenda dupla. Aviso no celular quando ficar pronta.' : 'A legenda dupla grava quando o download terminar.')
    carregar()
  } catch (e) {
    recado(e.message, true)
  }
}

async function apagar(v) {
  if (!confirm(`Apagar "${v.titulo}" do servidor?`)) return
  try {
    await v.apagar()
    carregar()
  } catch (e) {
    recado(e.message, true)
  }
}

let pedido = 0
async function rodar(caminho, destino, tempo) {
  const este = ++pedido
  buscando.value = true
  erroDaBusca.value = ''
  try {
    const r = await api(caminho, { tempo })
    if (este === pedido) destino.value = r
  } catch (e) {
    if (este === pedido) erroDaBusca.value = e.message
  } finally {
    if (este === pedido) buscando.value = false
  }
}

function buscarDireto() {
  const q = busca.value.trim()
  if (!q) return
  rodar(`/api/procurar?${new URLSearchParams({ q, tipo: tipo.value })}`, resultados, 100000)
}

function enviar() {
  buscarDireto()
}

function limparBusca() {
  pedido++
  busca.value = ''
  resultados.value = null
  erroDaBusca.value = ''
  buscando.value = false
}

function mudarTipo(t) {
  tipo.value = t
  if (resultados.value && busca.value.trim()) buscarDireto()
}

// A busca direta só roda com Enter (demora); fica no endereço para voltar a ela.
watch(busca, (q) => {
  history.replaceState(null, '', q.trim() ? `#/torrents?q=${encodeURIComponent(q)}` : '#/torrents')
  if (!q.trim()) limparBusca()
})

async function carregar() {
  try {
    const [d, t, torrents] = await Promise.all([api('/api/assinatura/downloads'), api('/api/downloads/torrents'), api('/api/torrents')])
    daAssinatura.value = d
    deTorrent.value = t
    livre.value = torrents.espaco_livre
    erro.value = ''
  } catch (e) {
    erro.value = e.status === 0 ? 'Sem conexão: o Streaming só abre na rede de casa.' : e.message
  }
}

async function adicionar() {
  enviando.value = true
  erroAoAdicionar.value = ''
  try {
    let r
    const escolhido = arquivo.value?.files?.[0]
    if (escolhido) {
      const corpo = new FormData()
      corpo.append('arquivo', escolhido)
      const resposta = await fetch('/api/torrents', { method: 'POST', body: corpo })
      r = await resposta.json().catch(() => null)
      if (!resposta.ok) throw new Error(r?.erro || `Erro ${resposta.status}`)
    } else {
      r = await api('/api/torrents', { metodo: 'POST', corpo: { texto: texto.value }, tempo: 60000 })
    }
    adicionando.value = false
    texto.value = ''
    location.hash = `#/torrent/${r.hash}`
  } catch (e) {
    erroAoAdicionar.value = e.message
  } finally {
    enviando.value = false
  }
}

let relogio
onMounted(async () => {
  carregar()
  relogio = setInterval(carregar, 4000)
  if (busca.value.trim()) enviar()
})
onBeforeUnmount(() => clearInterval(relogio))
</script>

<template>
  <div class="pagina">
    <h1>Downloads</h1>
    <div v-if="erro" class="aviso erro">{{ erro }}</div>

    <template v-if="!buscaAberta">
      <p v-if="livre > 0" class="suave estado">{{ tamanho(livre) }} livres no servidor</p>
      <p v-if="carregado && !videos.length" class="vazio">Nada no servidor. Na página de um filme ou série, “Baixar” deixa o vídeo aqui.</p>
      <ul v-if="videos.length" class="lista">
        <li v-for="v in videos" :key="v.chave" class="download">
          <span class="capa"><img v-if="v.capa" :src="v.capa" alt="" loading="lazy" referrerpolicy="no-referrer" /><Icone v-else :nome="v.origem === 'Torrent' ? 'torrents' : 'canais'" /></span>
          <span class="texto">
            <a v-if="v.pagina" class="titulo" :href="v.pagina">{{ v.titulo }}</a>
            <span v-else class="titulo">{{ v.titulo }}</span>
            <span class="meta">{{ meta(v) }}</span>
            <span v-if="v.estado !== 'baixado' && v.tamanho" class="progresso"><span :style="{ width: `${porcentagem(v)}%` }" /></span>
            <span v-if="v.detalhe" class="suave detalhe">{{ v.detalhe }}</span>
            <span v-if="v.mensagem" class="suave mensagem">{{ v.mensagem }}</span>
            <span v-if="v.dupla" class="chip" :class="{ ok: v.dupla === 'pronta', erro: v.dupla === 'erro', andando: !['pronta', 'erro'].includes(v.dupla) }">{{ textoDaDupla(v) }}</span>
            <button v-else-if="v.origem === 'Torrent'" class="limpar dupla" type="button" @click="prepararDupla(v)">Preparar legenda dupla</button>
          </span>
          <span class="acoes">
            <button v-if="v.estado === 'baixado'" class="botao principal" @click="assistindo = v"><Icone nome="tocar" />Assistir</button>
            <button class="botao icone" :aria-label="`Apagar ${v.titulo}`" title="Apagar do servidor" @click="apagar(v)"><Icone nome="lixeira" /></button>
          </span>
        </li>
      </ul>
    </template>

    <p class="rotulo busca-direta">Busca direta nos indexadores</p>
    <p class="suave nota">Filmes e séries se buscam no Início. Aqui, o que o TMDB não conhece (Enter para buscar), ou <button class="limpar" type="button" @click="adicionando = true">cole um link magnet</button>.</p>
    <form class="busca" @submit.prevent="enviar">
      <Icone nome="pesquisar" />
      <input v-model="busca" class="campo" type="search" placeholder="Nome do torrent" aria-label="Buscar nos indexadores" autocomplete="off" enterkeyhint="search" />
    </form>
    <div class="pilulas" role="tablist">
      <button v-for="t in TIPOS" :key="t.id" class="pilula" :class="{ ativa: tipo === t.id }" role="tab" :aria-selected="tipo === t.id" @click="mudarTipo(t.id)">
        {{ t.nome }}
      </button>
    </div>
    <section v-if="buscaAberta" class="resultados">
      <p class="rotulo">Resultados <button class="limpar" type="button" @click="limparBusca">Voltar aos downloads</button></p>
      <div v-if="erroDaBusca" class="aviso erro">
        {{ erroDaBusca }} <a v-if="erroDaBusca.includes('Ajustes')" :href="erroDaBusca.includes('TMDB') ? '#/ajustes/tmdb' : '#/ajustes/torrents'">Abrir Ajustes</a>
      </div>
      <p v-if="buscando" class="vazio">Perguntando aos indexadores…</p>
      <p v-else-if="resultados && !resultados.length" class="vazio">Nada encontrado para “{{ busca }}”.</p>
      <ul v-if="resultados?.length" class="lista" :class="{ esmaecida: buscando }">
        <li v-for="r in resultados" :key="r.indexador + r.titulo + r.tamanho"><OpcaoDeTorrent :r="r" mostrar-tipo /></li>
      </ul>
      <SalvarBusca v-if="!buscando && busca.trim()" :texto="busca" onde="torrents" :endereco="enderecoDaBusca('torrents', busca)" />
    </section>

    <Folha v-if="assistindo" :titulo="assistindo.titulo" @fechar="assistindo = null">
      <Assistir :chave="assistindo.chave" :titulo="assistindo.titulo" :capa="assistindo.capa" :posicao="assistindo.posicao || 0" @escolheu="assistindo = null" />
    </Folha>

    <Folha v-if="adicionando" titulo="Adicionar link magnet" @fechar="adicionando = false">
      <form class="adicionar" @submit.prevent="adicionar">
        <p class="suave explicacao">Cole um link magnet (de um site ou de alguém) ou escolha um arquivo .torrent. Nada é baixado agora: abre a lista de arquivos, para assistir ou baixar.</p>
        <textarea v-model="texto" class="campo" rows="3" placeholder="magnet:?xt=urn:btih:…" aria-label="Link do torrent" />
        <label class="arquivo">Arquivo .torrent <input ref="arquivo" type="file" accept=".torrent,application/x-bittorrent" /></label>
        <div v-if="erroAoAdicionar" class="aviso erro">{{ erroAoAdicionar }}</div>
        <div class="linha-acoes"><button class="botao principal" :disabled="enviando">{{ enviando ? 'Adicionando…' : 'Adicionar' }}</button></div>
      </form>
    </Folha>
  </div>
</template>

<style scoped>
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
  margin-bottom: 18px;
}

.resultados .rotulo {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 12px;
}

.limpar {
  padding: 0;
  border: 0;
  background: none;
  color: var(--marca);
  font: inherit;
  font-size: 0.82rem;
  letter-spacing: normal;
  text-transform: none;
  cursor: pointer;
}

.esmaecida {
  opacity: 0.5;
}

.download {
  display: flex;
  gap: 14px;
  align-items: center;
  padding: 10px 0;
}

.acoes {
  display: flex;
  flex: none;
  gap: 8px;
  align-items: center;
}

.progresso {
  display: block;
  height: 4px;
  margin-top: 2px;
  overflow: hidden;
  border-radius: 2px;
  background: var(--superficie-forte);
}

.progresso span {
  display: block;
  height: 100%;
  background: var(--marca);
}

.mensagem {
  font-size: 0.78rem;
}

.busca-direta {
  margin-top: 32px;
}

.nota {
  margin: -4px 0 10px;
  font-size: 0.82rem;
}

.botao svg {
  width: 16px;
  height: 16px;
}

.estado {
  margin: 0 0 18px;
  font-size: 0.82rem;
}

.capa {
  display: grid;
  flex: none;
  place-items: center;
  width: 64px;
  height: 90px;
  overflow: hidden;
  border-radius: var(--raio-p);
  background: var(--superficie);
  color: var(--texto-fraco);
}

.capa img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.texto {
  display: grid;
  align-content: start;
  gap: 4px;
  min-width: 0;
}

.titulo {
  font-size: 1.02rem;
  font-weight: 650;
  line-height: 1.3;
  transition: color 0.15s ease;
}

a.titulo {
  color: inherit;
  text-decoration: none;
}

a.titulo:hover {
  color: var(--marca);
}

.texto .chip {
  justify-self: start;
}

.dupla {
  justify-self: start;
  font-size: 0.8rem;
}

.detalhe {
  overflow: hidden;
  font-size: 0.75rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.meta {
  color: var(--texto-suave);
  font-size: 0.82rem;
}

.adicionar {
  display: grid;
  gap: 10px;
}

.explicacao {
  margin: -4px 0 2px;
  font-size: 0.88rem;
  line-height: 1.5;
}

.adicionar textarea {
  resize: vertical;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.82rem;
}

.arquivo {
  display: grid;
  gap: 4px;
  color: var(--texto-suave);
  font-size: 0.85rem;
}
</style>
