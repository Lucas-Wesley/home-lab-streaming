<script setup>
import { computed, inject, onMounted, ref, watch } from 'vue'
import Assistir from './Assistir.vue'
import Folha from './Folha.vue'
import Grade from './Grade.vue'
import Icone from './Icone.vue'
import SalvarBusca from './SalvarBusca.vue'
import { api } from './api.js'
import { ONDE, enderecoDaBusca } from './buscasSalvas.js'
import { relogio } from './tempo.js'

// Início: a busca em tudo (canais, filmes e séries da assinatura e, com o TMDB, os títulos que só há
// por torrent: S28), o que está pela metade e as novidades.
const props = defineProps({ busca: { type: String, default: '' } })
const recado = inject('recado')

const texto = ref(props.busca)
const resultado = ref(null)
const buscando = ref(false)
const continuar = ref([])
const recentes = ref({ filme: [], serie: [] })
const situacao = ref(null)
const erro = ref('')
const escolhido = ref(null) // item de "Continuar assistindo" com a folha aberta
const salvas = ref([]) // buscas salvas (S44), no pé da página sem busca

// Canais por último: quem busca quase sempre quer um filme ou uma série.
const GRUPOS = [
  { tipo: 'filme', nome: 'Filmes' },
  { tipo: 'serie', nome: 'Séries' },
  { tipo: 'canal', nome: 'Canais' },
]

const nadaAchado = computed(() => resultado.value && GRUPOS.every((g) => !resultado.value[g.tipo].length))

let espera
let pedido = 0
watch(texto, (q) => {
  clearTimeout(espera)
  // A busca fica no endereço: voltar do filme para a busca mantém o que foi digitado.
  history.replaceState(null, '', q.trim() ? `#/inicio?q=${encodeURIComponent(q)}` : '#/inicio')
  if (!q.trim()) {
    resultado.value = null
    return
  }
  espera = setTimeout(() => buscar(q), 250)
})

async function buscar(q) {
  const este = ++pedido
  buscando.value = true
  try {
    const r = await api(`/api/buscar?q=${encodeURIComponent(q)}`)
    if (este === pedido) resultado.value = r
  } catch (e) {
    if (este === pedido) erro.value = e.message
  } finally {
    if (este === pedido) buscando.value = false
  }
}

function abrirContinuar(p) {
  escolhido.value = p
}

// Tirar some na hora; "Desfazer" guarda de novo o mesmo ponto.
async function tirarDeContinuar(p) {
  const lugar = continuar.value.findIndex((c) => c.chave === p.chave)
  continuar.value = continuar.value.filter((c) => c.chave !== p.chave)
  escolhido.value = null
  try {
    await api(`/api/progresso/${p.chave}`, { metodo: 'DELETE' })
    recado('Tirado de Continuar assistindo.', false, { rotulo: 'Desfazer', fazer: () => devolver(p, lugar) })
  } catch (e) {
    continuar.value.splice(lugar, 0, p)
    recado(e.message, true)
  }
}

async function carregarSalvas() {
  try {
    salvas.value = await api('/api/buscas-salvas')
  } catch {
    salvas.value = []
  }
}

async function apagarSalva(b) {
  salvas.value = salvas.value.filter((x) => x.id !== b.id)
  try {
    await api(`/api/buscas-salvas/${b.id}`, { metodo: 'DELETE' })
  } catch (e) {
    recado(e.message, true)
    carregarSalvas()
  }
}

// A busca do próprio Início roda aqui mesmo: o endereço muda, mas a página não é refeita.
function abrirSalva(evento, b) {
  if (b.onde !== 'inicio') return
  evento.preventDefault()
  texto.value = new URLSearchParams(b.endereco.split('?')[1]).get('q') || b.texto
}

// Sem busca na tela (voltou de uma, ou abriu o Início): a lista pode ter mudado.
watch(resultado, (r) => {
  if (!r) carregarSalvas()
})

async function devolver(p, lugar) {
  try {
    await api(`/api/progresso/${p.chave}`, { metodo: 'PUT', corpo: { posicao: p.posicao, duracao: p.duracao } })
    continuar.value.splice(Math.min(lugar, continuar.value.length), 0, p)
  } catch (e) {
    recado(e.message, true)
  }
}

onMounted(async () => {
  if (texto.value.trim()) buscar(texto.value)
  else carregarSalvas()
  try {
    const [s, c, f, se] = await Promise.all([
      api('/api/acesso'),
      api('/api/progresso'),
      api('/api/itens/filme?limite=18'),
      api('/api/itens/serie?limite=18'),
    ])
    situacao.value = s
    continuar.value = c
    recentes.value = { filme: f, serie: se }
  } catch (e) {
    erro.value = e.status === 0 ? 'Sem conexão: o Streaming só abre na rede de casa.' : e.message
  }
})
</script>

<template>
  <div class="pagina">
    <h1>Início</h1>
    <label class="busca">
      <Icone nome="pesquisar" />
      <input v-model="texto" class="campo" type="search" placeholder="Buscar filmes, séries e canais" aria-label="Buscar" autocomplete="off" enterkeyhint="search" />
    </label>

    <div v-if="erro" class="aviso erro">{{ erro }}</div>
    <div v-if="situacao && !situacao.configurado" class="aviso">
      Para começar, cole a mensagem do seu provedor em <a href="#/ajustes/assinatura">Ajustes</a>.
    </div>

    <template v-if="resultado">
      <p v-if="nadaAchado && !buscando" class="vazio">Nada com “{{ texto }}”.</p>
      <template v-for="g in GRUPOS" :key="g.tipo">
        <section v-if="resultado[g.tipo].length">
          <p class="rotulo">{{ g.nome }}</p>
          <Grade :itens="resultado[g.tipo]" />
        </section>
      </template>
      <SalvarBusca v-if="!buscando && texto.trim()" :texto="texto" onde="inicio" :endereco="enderecoDaBusca('inicio', texto)" />
    </template>

    <template v-else>
      <section v-if="continuar.length">
        <p class="rotulo">Continuar assistindo</p>
        <ul class="continuar">
          <li v-for="p in continuar" :key="p.chave" class="pela-metade">
            <button class="abrir" @click="abrirContinuar(p)">
              <span class="capa">
                <img v-if="p.capa" :src="p.capa" alt="" loading="lazy" referrerpolicy="no-referrer" />
                <Icone v-else nome="filmes" />
                <span class="barra"><span :style="{ width: `${Math.min(100, (p.posicao / (p.duracao || 1)) * 100)}%` }" /></span>
              </span>
              <span class="nome">{{ p.titulo }}</span>
              <span class="meta">{{ p.subtitulo }}</span>
            </button>
            <button class="tirar-rapido" title="Tirar de Continuar assistindo" :aria-label="`Tirar ${p.titulo} de Continuar assistindo`" @click="tirarDeContinuar(p)">
              <Icone nome="lixeira" />
            </button>
          </li>
        </ul>
      </section>

      <section v-if="recentes.filme.length">
        <p class="rotulo">Filmes adicionados há pouco</p>
        <Grade :itens="recentes.filme" />
      </section>
      <section v-if="recentes.serie.length">
        <p class="rotulo">Séries atualizadas há pouco</p>
        <Grade :itens="recentes.serie" />
      </section>

      <section v-if="salvas.length" class="salvas">
        <p class="rotulo">Buscas salvas</p>
        <ul>
          <li v-for="b in salvas" :key="b.id">
            <a :href="b.endereco" @click="abrirSalva($event, b)">
              <Icone nome="pesquisar" />
              <span class="texto">{{ b.texto }}</span>
              <span class="onde">{{ ONDE[b.onde] }}</span>
            </a>
            <button class="apagar" :aria-label="`Apagar a busca ${b.texto}`" title="Apagar" @click="apagarSalva(b)"><Icone nome="fechar" /></button>
          </li>
        </ul>
      </section>
    </template>

    <Folha v-if="escolhido" :titulo="escolhido.titulo" @fechar="escolhido = null">
      <p class="suave detalhe">{{ escolhido.subtitulo }} · parou em {{ relogio(escolhido.posicao) }}</p>
      <Assistir :chave="escolhido.chave" :titulo="escolhido.titulo" :capa="escolhido.capa" :posicao="escolhido.posicao" @escolheu="escolhido = null" />
      <p class="ir-para">
        <a v-if="escolhido.pagina" :href="escolhido.pagina">{{ escolhido.serie_id ? 'Ver a série' : 'Ver a página' }}</a>
        <button class="tirar" @click="tirarDeContinuar(escolhido)">Tirar de Continuar assistindo</button>
      </p>
    </Folha>
  </div>
</template>

<style scoped>
.pagina {
  max-width: 1080px;
}

.busca {
  position: relative;
  display: block;
  margin-bottom: 8px;
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
  font-size: 1rem;
}

.aviso {
  margin: 12px 0;
}

.continuar {
  display: grid;
  grid-auto-columns: min(232px, 64vw);
  grid-auto-flow: column;
  gap: 14px;
  margin: 0;
  padding: 0 0 6px;
  overflow-x: auto;
  list-style: none;
  scroll-snap-type: x proximity;
  scrollbar-width: thin;
}

.pela-metade {
  position: relative;
  min-width: 0;
  scroll-snap-align: start;
}

.abrir {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  gap: 3px;
  width: 100%;
  padding: 0;
  border: 0;
  background: none;
  color: inherit;
  text-align: left;
  cursor: pointer;
}

.capa {
  position: relative;
  display: grid;
  place-items: center;
  aspect-ratio: 16 / 9;
  margin-bottom: 5px;
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

.barra {
  position: absolute;
  right: 0;
  bottom: 0;
  left: 0;
  height: 3px;
  background: rgb(255 255 255 / 30%);
}

.barra span {
  display: block;
  height: 100%;
  background: var(--marca);
}

.nome {
  display: -webkit-box;
  overflow: hidden;
  font-size: 0.9rem;
  font-weight: 600;
  line-height: 1.3;
  overflow-wrap: anywhere;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.abrir:hover .nome {
  color: var(--marca);
}

.meta {
  overflow: hidden;
  color: var(--texto-suave);
  font-size: 0.78rem;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* A lixeira aparece ao passar o mouse; no celular fica sempre, discreta, no canto da imagem. */
.tirar-rapido {
  position: absolute;
  top: 6px;
  right: 6px;
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  padding: 0;
  border: 0;
  border-radius: 50%;
  background: rgb(0 0 0 / 55%);
  color: #fff;
  opacity: 0;
  cursor: pointer;
  transition: opacity 0.15s ease, background 0.15s ease;
}

.tirar-rapido svg {
  width: 17px;
  height: 17px;
}

.pela-metade:hover .tirar-rapido,
.tirar-rapido:focus-visible {
  opacity: 1;
}

.tirar-rapido:hover {
  background: var(--vermelho);
}

@media (hover: none) {
  .tirar-rapido {
    opacity: 0.8;
  }
}

.detalhe {
  margin: -4px 0 14px;
  font-size: 0.88rem;
}

.ir-para {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 18px;
  margin: 16px 0 0;
  font-size: 0.9rem;
}

.tirar {
  padding: 0;
  border: 0;
  background: none;
  color: var(--texto-suave);
  font: inherit;
  cursor: pointer;
}

.tirar:hover {
  color: var(--perigo);
}

.salvas ul {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.salvas li {
  display: flex;
  align-items: center;
  max-width: 100%;
  border: 1px solid var(--linha);
  border-radius: 999px;
  background: var(--superficie);
}

.salvas a {
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
  min-height: 36px;
  padding: 0 4px 0 12px;
  color: inherit;
  font-size: 0.88rem;
  text-decoration: none;
}

.salvas a:hover .texto {
  color: var(--marca);
}

.salvas svg {
  flex-shrink: 0;
  width: 15px;
  height: 15px;
  color: var(--texto-fraco);
}

.salvas .texto {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.salvas .onde {
  flex-shrink: 0;
  color: var(--texto-fraco);
  font-size: 0.75rem;
}

.salvas .apagar {
  display: grid;
  place-items: center;
  width: 32px;
  height: 36px;
  padding: 0;
  border: 0;
  background: none;
  color: var(--texto-fraco);
  cursor: pointer;
}

.salvas .apagar:hover {
  color: var(--perigo);
}
</style>
