<script setup>
import { computed, onBeforeUnmount, onMounted, provide, ref } from 'vue'
import Ajustes from './Ajustes.vue'
import BarraDoControle from './BarraDoControle.vue'
import Canais from './Canais.vue'
import Catalogo from './Catalogo.vue'
import Controle from './Controle.vue'
import Favoritos from './Favoritos.vue'
import Filme from './Filme.vue'
import Icone from './Icone.vue'
import Archive from './Archive.vue'
import Inicio from './Inicio.vue'
import Player from './Player.vue'
import Serie from './Serie.vue'
import Torrent from './Torrent.vue'
import Titulo from './Titulo.vue'
import Torrents from './Torrents.vue'
import { api } from './api.js'

// Rotas no hash, como nas outras apps: o "voltar" do celular volta para a tela anterior.
// O que vem depois de "?" no hash são opções da tela (título e posição no player).
const hash = ref(location.hash)
const recado = ref(null) // { texto, erro, acao } — aviso rápido; acao: { rotulo, fazer }, como "Desfazer"
const versao = ref('')

const rota = computed(() => {
  const [caminho, busca = ''] = hash.value.replace(/^#\/?/, '').split('?')
  const [nome, ...resto] = decodeURIComponent(caminho).split('/')
  return { nome: nome || 'inicio', param: resto.join('/'), opcoes: new URLSearchParams(busca) }
})

const MENU = [
  { rota: 'inicio', nome: 'Início', icone: 'inicio' },
  { rota: 'favoritos', nome: 'Favoritos', icone: 'favorito' },
  { rota: 'canais', nome: 'Canais', icone: 'canais' },
  { rota: 'filmes', nome: 'Filmes', icone: 'filmes' },
  { rota: 'series', nome: 'Séries', icone: 'series' },
  { rota: 'torrents', nome: 'Downloads', icone: 'torrents' },
  { rota: 'ajustes', nome: 'Ajustes', icone: 'ajustes' },
]

const secao = computed(() => {
  const n = rota.value.nome
  if (n === 'filme') return 'filmes'
  if (n === 'serie') return 'series'
  if (n === 'titulo') return 'inicio'
  if (n === 'torrent' || n === 'archive') return 'torrents'
  return n
})

// No player e no controle, a tela é do vídeo: o menu some.
const telaCheia = computed(() => ['assistir', 'controle'].includes(rota.value.nome))

function ir(destino) {
  location.hash = `#/${destino}`
}

const telaAnterior = ref('')
function aoMudarHash() {
  if (location.hash.startsWith('#/ajustes') && !hash.value.startsWith('#/ajustes')) telaAnterior.value = hash.value || '#/inicio'
  hash.value = location.hash
}

let tempoDoRecado
function mostrarRecado(texto, erro = false, acao = null) {
  recado.value = { texto, erro, acao }
  clearTimeout(tempoDoRecado)
  tempoDoRecado = setTimeout(() => (recado.value = null), acao ? 6000 : 4000)
}

function fazerDoRecado() {
  const acao = recado.value?.acao
  recado.value = null
  acao?.fazer()
}
provide('recado', mostrarRecado)

onMounted(async () => {
  window.addEventListener('hashchange', aoMudarHash)
  try {
    versao.value = (await api('/api/health', { tempo: 4000 })).version
  } catch {
    versao.value = ''
  }
})

onBeforeUnmount(() => window.removeEventListener('hashchange', aoMudarHash))
</script>

<template>
  <div class="app" :class="{ 'tela-cheia': telaCheia }">
    <aside class="menu" aria-label="Menu">
      <div class="marca"><span class="marca-icone"><Icone nome="tocar" /></span>Streaming</div>
      <nav>
        <button
          v-for="item in MENU"
          :key="item.rota"
          class="item"
          :class="{ ativo: secao === item.rota }"
          :aria-current="secao === item.rota ? 'page' : undefined"
          @click="ir(item.rota)"
        >
          <Icone :nome="item.icone" />
          <span class="nome">{{ item.nome }}</span>
        </button>
      </nav>
      <div class="rodape">{{ versao ? `versão ${versao.slice(0, 7)}` : 'sem conexão' }}</div>
    </aside>

    <main class="conteudo">
      <Player
        v-if="rota.nome === 'assistir'"
        :key="rota.param + rota.opcoes.toString()"
        :chave="rota.param"
        :titulo="rota.opcoes.get('titulo') || ''"
        :inicio="Number(rota.opcoes.get('t')) || 0"
      />
      <Controle v-else-if="rota.nome === 'controle'" :key="rota.param" :id="rota.param" />
      <Filme v-else-if="rota.nome === 'filme'" :key="rota.param" :id="rota.param" />
      <Serie v-else-if="rota.nome === 'serie'" :key="rota.param" :id="rota.param" />
      <Catalogo
        v-else-if="rota.nome === 'canais' && rota.param.startsWith('todos')"
        :key="`canal${rota.param}`"
        tipo="canal"
        :categoria="rota.param.replace(/^todos\/?/, '')"
      />
      <Canais v-else-if="rota.nome === 'canais'" />
      <Catalogo v-else-if="rota.nome === 'filmes'" :key="`filme${rota.param}`" tipo="filme" :categoria="rota.param" />
      <Catalogo v-else-if="rota.nome === 'series'" :key="`serie${rota.param}`" tipo="serie" :categoria="rota.param" />
      <Favoritos v-else-if="rota.nome === 'favoritos'" />
      <Torrents v-else-if="rota.nome === 'torrents'" :busca="rota.opcoes.get('q') || ''" />
      <Titulo v-else-if="rota.nome === 'titulo'" :key="rota.param" :param="rota.param" />
      <Torrent v-else-if="rota.nome === 'torrent'" :key="rota.param" :hash="rota.param" />
      <Archive v-else-if="rota.nome === 'archive'" />
      <Ajustes v-else-if="rota.nome === 'ajustes'" :tela="telaAnterior" :secao="rota.param" />
      <Inicio v-else :busca="rota.opcoes.get('q') || ''" />
    </main>

    <BarraDoControle v-if="!telaCheia" />

    <Transition name="recado">
      <div v-if="recado" class="recado vidro" :class="{ erro: recado.erro }" role="status">
        {{ recado.texto }}
        <button v-if="recado.acao" class="acao-do-recado" @click="fazerDoRecado">{{ recado.acao.rotulo }}</button>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.app {
  min-height: 100vh;
}

.menu {
  position: fixed;
  inset: 0 auto 0 0;
  z-index: 20;
  display: flex;
  flex-direction: column;
  width: 216px;
  padding: max(28px, env(safe-area-inset-top)) 12px 16px;
  border-right: 1px solid var(--linha);
  background: var(--lateral);
}

/* A cor do app aparece aqui, no item ativo e no botão principal. */
.marca {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 8px 24px;
  font-size: 1.02rem;
  font-weight: 700;
  letter-spacing: -0.015em;
}

.marca-icone {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 8px;
  background: var(--marca);
  color: var(--marca-texto);
}

.marca-icone svg {
  width: 15px;
  height: 15px;
}

nav {
  display: grid;
  gap: 1px;
}

.item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  min-height: 34px;
  padding: 0 10px;
  border: 0;
  border-radius: var(--raio-p);
  background: transparent;
  color: var(--texto-suave);
  font-size: 0.9rem;
  text-align: left;
  cursor: pointer;
  transition: background 0.15s ease, color 0.15s ease;
}

.item:hover {
  background: var(--linha);
  color: var(--texto);
}

.item.ativo {
  background: var(--item-ativo);
  box-shadow: 0 0 0 1px var(--linha), 0 1px 2px rgb(23 25 30 / 5%);
  color: var(--texto);
  font-weight: 600;
}

.item.ativo svg {
  color: var(--marca);
}

.rodape {
  margin-top: auto;
  padding: 0 10px;
  color: var(--texto-fraco);
  font-size: 0.72rem;
}

.conteudo {
  margin-left: 216px;
  min-height: 100vh;
}

.tela-cheia .menu {
  display: none;
}

.tela-cheia .conteudo {
  margin-left: 0;
}

.recado {
  position: fixed;
  left: 50%;
  bottom: calc(24px + env(safe-area-inset-bottom));
  z-index: 40;
  max-width: calc(100% - 32px);
  padding: 10px 16px;
  border: 1px solid var(--vidro-borda);
  border-radius: 999px;
  box-shadow: var(--sombra);
  font-size: 0.88rem;
  transform: translateX(-50%);
}

.recado.erro {
  color: var(--vermelho);
}

.acao-do-recado {
  margin-left: 12px;
  padding: 0;
  border: 0;
  background: none;
  color: var(--marca);
  font: inherit;
  font-weight: 600;
  cursor: pointer;
}

.recado-enter-active,
.recado-leave-active {
  transition: opacity 0.2s ease, transform 0.2s ease;
}

.recado-enter-from,
.recado-leave-to {
  opacity: 0;
  transform: translate(-50%, 8px);
}

/* Celular: o menu vira uma barra de abas de vidro no pé da tela. */
@media (max-width: 820px) {
  .menu {
    inset: auto 0 0 0;
    width: auto;
    padding: 6px 8px max(6px, env(safe-area-inset-bottom));
    border-right: 0;
    border-top: 1px solid var(--linha);
    background: var(--vidro);
    -webkit-backdrop-filter: var(--blur);
    backdrop-filter: var(--blur);
  }

  .marca,
  .rodape {
    display: none;
  }

  nav {
    grid-template-columns: repeat(7, 1fr);
  }

  .item {
    position: relative;
    flex-direction: column;
    justify-content: center;
    gap: 2px;
    min-height: 48px;
    padding: 4px 0 2px;
    font-size: 0.66rem;
    font-weight: 500;
  }

  .item.ativo,
  .item:hover {
    background: transparent;
    box-shadow: none;
    font-weight: 600;
  }

  .item.ativo {
    color: var(--marca);
  }

  .conteudo {
    margin-left: 0;
  }

  .recado {
    bottom: calc(80px + env(safe-area-inset-bottom));
  }
}
</style>
