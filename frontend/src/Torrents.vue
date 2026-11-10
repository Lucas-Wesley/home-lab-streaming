<script setup>
import { computed, inject, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Assistir from './Assistir.vue'
import Folha from './Folha.vue'
import Icone from './Icone.vue'
import OpcaoDeTorrent from './OpcaoDeTorrent.vue'
import { api } from './api.js'
import { tamanho } from './tamanho.js'

// Downloads (S28): o que está no servidor ou baixando, da assinatura e dos torrents; abaixo, os
// torrents assistidos sem baixar. Ver a lista não baixa nada; o motor de torrent só liga para ler
// fichas, baixar ou assistir.
//
// Filmes e séries se buscam no Início (assinatura e torrents juntos). Aqui fica a busca direta nos
// indexadores, para o que o TMDB não conhece.
const props = defineProps({ busca: { type: String, default: '' } })
const recado = inject('recado')

const dados = ref(null)
const erro = ref('')
const adicionando = ref(false)
const texto = ref('')
const enviando = ref(false)
const erroAoAdicionar = ref('')
const arquivo = ref(null)

const ORIGENS = { ia: 'Internet Archive', magnet: 'Link magnet', arquivo: 'Arquivo .torrent' }
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
const daAssinatura = ref([])
const verOutros = ref(false)
const assistindo = ref(null) // download da assinatura com a folha de assistir aberta

const buscaAberta = computed(() => buscando.value || resultados.value || erroDaBusca.value)
const noServidor = computed(() => (dados.value?.torrents || []).filter((t) => t.baixados || t.baixando))
const outros = computed(() => (dados.value?.torrents || []).filter((t) => !t.baixados && !t.baixando))

const ESTADOS = { 'na fila': 'na fila', baixando: 'baixando', esperando: 'esperando a assinatura', baixado: 'baixado', erro: 'erro' }
const porcentagem = (d) => (d.tamanho ? Math.floor((d.baixado / d.tamanho) * 100) : 0)

async function apagarDaAssinatura(d) {
  if (!confirm(`Apagar "${d.titulo}" do servidor?`)) return
  try {
    await api(`/api/assinatura/downloads/${d.chave}`, { metodo: 'DELETE' })
    daAssinatura.value = daAssinatura.value.filter((x) => x.chave !== d.chave)
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
    const [t, d] = await Promise.all([api('/api/torrents'), api('/api/assinatura/downloads')])
    dados.value = t
    daAssinatura.value = d
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

async function atualizar() {
  try {
    const r = await api('/api/torrents/atualizar', { metodo: 'POST' })
    recado(r.pendentes ? `Buscando a lista de ${r.pendentes} ${r.pendentes === 1 ? 'torrent' : 'torrents'}…` : 'Todas as listas já estão aqui.')
  } catch (e) {
    recado(e.message, true)
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

    <div class="linha-acoes topo">
      <button class="botao principal" @click="adicionando = true"><Icone nome="torrents" />Adicionar torrent</button>
      <a class="botao" href="#/archive">Explorar o Internet Archive</a>
      <button class="botao" @click="atualizar"><Icone nome="atualizar" />Atualizar listas</button>
    </div>
    <p v-if="dados" class="suave estado">
      Torrent {{ dados.motor_ligado ? 'ligado agora' : 'desligado' }}<template v-if="dados.espaco_livre > 0"> · {{ tamanho(dados.espaco_livre) }} livres no servidor</template>
    </p>

    <template v-if="!buscaAberta">
      <section v-if="daAssinatura.length">
        <p class="rotulo">Da assinatura</p>
        <ul class="lista">
          <li v-for="d in daAssinatura" :key="d.chave" class="download">
            <span class="capa"><img v-if="d.capa" :src="d.capa" alt="" loading="lazy" referrerpolicy="no-referrer" /><Icone v-else nome="canais" /></span>
            <span class="texto">
              <span class="titulo">{{ d.titulo }}</span>
              <span class="meta">
                <template v-if="d.tamanho">{{ tamanho(d.tamanho) }} · </template>{{ ESTADOS[d.estado] || d.estado }}<template v-if="d.estado !== 'baixado' && d.tamanho"> {{ porcentagem(d) }}%</template>
              </span>
              <span v-if="d.estado !== 'baixado' && d.tamanho" class="progresso"><span :style="{ width: `${porcentagem(d)}%` }" /></span>
              <span v-if="d.mensagem" class="suave mensagem">{{ d.mensagem }}</span>
            </span>
            <span class="acoes">
              <button v-if="d.estado === 'baixado'" class="botao principal" @click="assistindo = d"><Icone nome="tocar" />Assistir</button>
              <button class="botao icone" :aria-label="`Apagar ${d.titulo}`" title="Apagar do servidor" @click="apagarDaAssinatura(d)"><Icone nome="lixeira" /></button>
            </span>
          </li>
        </ul>
      </section>

      <section>
        <p class="rotulo">Torrents no servidor</p>
        <p v-if="dados && !noServidor.length" class="vazio">Nenhum torrent baixado. Na página de um filme ou série, “Baixar” deixa o vídeo aqui.</p>
        <ul v-if="noServidor.length" class="lista">
          <li v-for="t in noServidor" :key="t.hash"><a class="torrent" :href="`#/torrent/${t.hash}`">
            <span class="capa"><img v-if="t.capa" :src="t.capa" alt="" loading="lazy" referrerpolicy="no-referrer" /><Icone v-else nome="torrents" /></span>
            <span class="texto">
              <span class="titulo">{{ t.titulo }}</span>
              <span class="meta">{{ ORIGENS[t.origem] }}<template v-if="t.tamanho"> · {{ tamanho(t.tamanho) }}</template></span>
              <span class="chips">
                <span v-if="t.baixando" class="chip andando">{{ t.baixando }} baixando</span>
                <span v-if="t.baixados" class="chip ok">{{ t.baixados }} {{ t.baixados === 1 ? 'baixado' : 'baixados' }}</span>
                <span v-if="t.erro" class="chip erro">{{ t.erro }}</span>
              </span>
            </span>
          </a></li>
        </ul>
      </section>

      <section v-if="outros.length">
        <button class="limpar outros" @click="verOutros = !verOutros">
          {{ verOutros ? 'Esconder' : 'Mostrar' }} {{ outros.length }} {{ outros.length === 1 ? 'torrent assistido' : 'torrents assistidos' }} sem baixar
        </button>
        <ul v-if="verOutros" class="lista">
          <li v-for="t in outros" :key="t.hash"><a class="torrent" :href="`#/torrent/${t.hash}`">
            <span class="capa"><img v-if="t.capa" :src="t.capa" alt="" loading="lazy" referrerpolicy="no-referrer" /><Icone v-else nome="torrents" /></span>
            <span class="texto">
              <span class="titulo">{{ t.titulo }}</span>
              <span class="meta">{{ ORIGENS[t.origem] }}<template v-if="t.tamanho"> · {{ tamanho(t.tamanho) }}</template></span>
              <span class="chips">
                <span v-if="!t.tem_ficha && !t.erro" class="chip andando">buscando a lista</span>
                <span v-if="t.erro" class="chip erro">{{ t.erro }}</span>
              </span>
            </span>
          </a></li>
        </ul>
      </section>
    </template>

    <p class="rotulo busca-direta">Busca direta nos indexadores</p>
    <p class="suave nota">Filmes e séries se buscam no Início. Aqui, o que o TMDB não conhece (Enter para buscar).</p>
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
        {{ erroDaBusca }} <a v-if="erroDaBusca.includes('Ajustes')" href="#/ajustes">Abrir Ajustes</a>
      </div>
      <p v-if="buscando" class="vazio">Perguntando aos indexadores…</p>
      <p v-else-if="resultados && !resultados.length" class="vazio">Nada encontrado para “{{ busca }}”.</p>
      <ul v-if="resultados?.length" class="lista" :class="{ esmaecida: buscando }">
        <li v-for="r in resultados" :key="r.indexador + r.titulo + r.tamanho"><OpcaoDeTorrent :r="r" mostrar-tipo /></li>
      </ul>
    </section>

    <Folha v-if="assistindo" :titulo="assistindo.titulo" @fechar="assistindo = null">
      <Assistir :chave="assistindo.chave" :titulo="assistindo.titulo" :capa="assistindo.capa" @escolheu="assistindo = null" />
    </Folha>

    <Folha v-if="adicionando" titulo="Adicionar torrent" @fechar="adicionando = false">
      <form class="adicionar" @submit.prevent="adicionar">
        <p class="suave explicacao">Cole um link magnet ou um link do Internet Archive, ou escolha um arquivo .torrent. Nada é baixado agora: só a lista de arquivos.</p>
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

.direto {
  margin: 18px 0 0;
  color: var(--texto-suave);
  font-size: 0.88rem;
}

.direto .limpar {
  font-size: 0.88rem;
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

.cartaz {
  display: grid;
  gap: 6px;
  color: inherit;
  text-decoration: none;
}

.cartaz .capa {
  display: grid;
  place-items: center;
  width: 100%;
  height: auto;
  aspect-ratio: 2 / 3;
}

.cartaz .nome {
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

.cartaz .meta {
  margin-top: -3px;
  color: var(--texto-fraco);
  font-size: 0.75rem;
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

.outros {
  margin: 18px 0 4px;
}

.busca-direta {
  margin-top: 32px;
}

.nota {
  margin: -4px 0 10px;
  font-size: 0.82rem;
}

.topo {
  margin-bottom: 10px;
}

.botao svg {
  width: 16px;
  height: 16px;
}

.estado {
  margin: 0 0 18px;
  font-size: 0.82rem;
}

.torrent {
  display: flex;
  gap: 14px;
  padding: 14px 0;
  color: inherit;
  text-decoration: none;
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

.torrent:hover .titulo {
  color: var(--marca);
}

.meta {
  color: var(--texto-suave);
  font-size: 0.82rem;
}

.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 2px;
}

.chips .chip.erro {
  white-space: normal;
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

@media (max-width: 820px) {
  .grade {
    grid-template-columns: repeat(auto-fill, minmax(104px, 1fr));
    gap: 18px 10px;
  }
}
</style>
