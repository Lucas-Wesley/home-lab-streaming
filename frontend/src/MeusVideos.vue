<script setup>
import { computed, inject, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Assistir from './Assistir.vue'
import Folha from './Folha.vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { linkDoTelegram } from './meusVideos.js'
import { tamanho } from './tamanho.js'
import { duracao, relogio } from './tempo.js'

// Meus vídeos (S42): vídeos próprios (viagens, trabalho), em pastas. Chegam pelo link de uma
// mensagem do Telegram (copiar link, ou compartilhar com o Streaming): o servidor baixa com a conta
// do Lucas e guarda com o nome e a pasta escolhidos.
const props = defineProps({
  pasta: { type: String, default: '' },
  link: { type: String, default: '' }, // veio do compartilhar do Android
})
const recado = inject('recado')

const dados = ref(null)
const erro = ref('')
const adicionando = ref(!!props.link)
const form = ref({ link: linkDoTelegram(props.link), nome: '', pasta: props.pasta, tudo: false })
const enviando = ref(false)
const erroDoForm = ref('')
const aberto = ref(null) // vídeo com a folha aberta
const editando = ref(null) // { nome, pasta } na folha

const pastas = computed(() => {
  const nomes = new Set((dados.value?.videos || []).map((v) => v.pasta))
  return [...nomes].sort((a, b) => a.localeCompare(b, 'pt-BR'))
})
const grupos = computed(() => {
  const r = []
  for (const p of pastas.value) {
    if (props.pasta && p !== props.pasta) continue
    r.push({ pasta: p, videos: dados.value.videos.filter((v) => v.pasta === p) })
  }
  return r
})
const pedidos = computed(() => (dados.value?.pedidos || []).filter((p) => p.estado !== 'pronto' || recente(p)))
const andando = computed(() => pedidos.value.some((p) => p.estado === 'na fila' || p.estado === 'baixando'))

// Os prontos ficam na lista por um dia, para se ver o que chegou.
const recente = (p) => Date.now() - Date.parse(p.criado_em) < 24 * 3600 * 1000

const ESTADOS = { 'na fila': 'na fila', baixando: 'baixando', pronto: 'pronto', erro: 'erro' }

async function carregar() {
  try {
    dados.value = await api('/api/meus-videos')
    erro.value = ''
  } catch (e) {
    erro.value = e.status === 0 ? 'Sem conexão: o Streaming só abre na rede de casa.' : e.message
  }
}

let relogioDaFila
watch(andando, (sim) => {
  clearInterval(relogioDaFila)
  if (sim) relogioDaFila = setInterval(carregar, 3000)
})

async function colar() {
  try {
    form.value.link = linkDoTelegram(await navigator.clipboard.readText())
  } catch {
    recado('Não consegui ler a área de transferência: cole no campo.', true)
  }
}

async function pedir() {
  erroDoForm.value = ''
  enviando.value = true
  try {
    await api('/api/meus-videos/telegram', { metodo: 'POST', corpo: { ...form.value, link: linkDoTelegram(form.value.link) } })
    recado('Na fila: o servidor baixa e avisa quando terminar.')
    form.value = { link: '', nome: '', pasta: form.value.pasta, tudo: false }
    adicionando.value = false
    await carregar()
  } catch (e) {
    erroDoForm.value = e.message
  } finally {
    enviando.value = false
  }
}

async function tirarPedido(p) {
  if (p.estado === 'baixando' && !confirm('Cancelar este download?')) return
  try {
    await api(`/api/meus-videos/telegram/${p.id}`, { metodo: 'DELETE' })
    await carregar()
  } catch (e) {
    recado(e.message, true)
  }
}

async function retomar(p) {
  try {
    await api(`/api/meus-videos/telegram/${p.id}/retomar`, { metodo: 'POST' })
    await carregar()
  } catch (e) {
    recado(e.message, true)
  }
}

function abrir(v) {
  aberto.value = v
  editando.value = null
}

async function guardarEdicao() {
  try {
    const v = await api(`/api/meus-videos/${aberto.value.id}`, { metodo: 'PATCH', corpo: editando.value })
    aberto.value = { ...aberto.value, ...v }
    editando.value = null
    await carregar()
  } catch (e) {
    recado(e.message, true)
  }
}

async function apagar() {
  const v = aberto.value
  if (!confirm(`Apagar "${v.nome}" do servidor? Não dá para desfazer.`)) return
  try {
    await api(`/api/meus-videos/${v.id}`, { metodo: 'DELETE' })
    aberto.value = null
    await carregar()
  } catch (e) {
    recado(e.message, true)
  }
}

function irParaPasta(p) {
  location.hash = p ? `#/meus-videos/${encodeURIComponent(p)}` : '#/meus-videos'
}

function semImagem(e) {
  e.target.closest('.capa')?.classList.add('sem-imagem')
}

onMounted(carregar)
onBeforeUnmount(() => clearInterval(relogioDaFila))
</script>

<template>
  <div class="pagina">
    <div class="topo">
      <h1>Meus vídeos</h1>
      <button v-if="!adicionando" class="botao principal" @click="adicionando = true">Baixar do Telegram</button>
    </div>
    <div v-if="erro" class="aviso erro">{{ erro }}</div>
    <div v-if="dados && !dados.telegram.instalado" class="aviso erro">O tdl não está instalado no servidor: dá para ver os vídeos, mas não baixar novos.</div>

    <form v-if="adicionando" class="cartao formulario" @submit.prevent="pedir">
      <label>
        <span>Link da mensagem</span>
        <span class="com-botao">
          <input v-model="form.link" class="campo" type="url" inputmode="url" placeholder="https://t.me/c/…/4" autocomplete="off" required />
          <button type="button" class="botao" @click="colar">Colar</button>
        </span>
        <small class="suave">No Telegram: segure a mensagem do vídeo › Copiar link.</small>
      </label>
      <label>
        <span>{{ form.tudo ? 'Nome (opcional: sem ele, fica o nome do arquivo)' : 'Nome do vídeo' }}</span>
        <input v-model="form.nome" class="campo" type="text" placeholder="Praia do Forte, dia 2" :required="!form.tudo" />
      </label>
      <label>
        <span>Pasta</span>
        <input v-model="form.pasta" class="campo" type="text" list="pastas-existentes" placeholder="Viagens, Trabalho…" required />
        <datalist id="pastas-existentes">
          <option v-for="p in pastas" :key="p" :value="p" />
        </datalist>
      </label>
      <label class="opcao">
        <input v-model="form.tudo" type="checkbox" />
        Todos os vídeos do grupo (só os que ainda não vieram)
      </label>
      <div v-if="erroDoForm" class="aviso erro">{{ erroDoForm }}</div>
      <div class="linha-acoes">
        <button class="botao principal" type="submit" :disabled="enviando">{{ enviando ? 'Mandando…' : 'Baixar' }}</button>
        <button class="botao" type="button" @click="adicionando = false">Cancelar</button>
      </div>
    </form>

    <section v-if="pedidos.length">
      <p class="rotulo">Do Telegram</p>
      <ul class="lista pedidos">
        <li v-for="p in pedidos" :key="p.id">
          <div class="pedido">
            <div class="texto">
              <span class="nome-do-pedido">{{ p.nome || (p.tudo ? 'Todos os vídeos do grupo' : 'Vídeo') }}</span>
              <span class="meta">
                <span class="chip" :class="{ erro: p.estado === 'erro', andando: p.estado === 'baixando' }">{{ ESTADOS[p.estado] || p.estado }}</span>
                {{ p.pasta }}
                <template v-if="p.baixado"> · {{ tamanho(p.baixado) }}</template>
                <template v-if="p.estado === 'pronto' && p.videos > 1"> · {{ p.videos }} vídeos</template>
              </span>
              <span v-if="p.mensagem" class="mensagem" :class="{ erro: p.estado === 'erro' }">{{ p.mensagem }}</span>
            </div>
            <button v-if="p.estado === 'erro'" class="botao" @click="retomar(p)">Tentar de novo</button>
            <button class="botao icone" :aria-label="p.estado === 'baixando' ? 'Cancelar' : 'Tirar da lista'" @click="tirarPedido(p)"><Icone nome="fechar" /></button>
          </div>
        </li>
      </ul>
    </section>

    <div v-if="pastas.length > 1" class="pastas">
      <button class="pilula" :class="{ ativa: !pasta }" @click="irParaPasta('')">Todas</button>
      <button v-for="p in pastas" :key="p" class="pilula" :class="{ ativa: pasta === p }" @click="irParaPasta(p)">{{ p }}</button>
    </div>

    <p v-if="dados && !dados.videos.length && !adicionando" class="vazio">
      Nenhum vídeo ainda. Toque em <strong>Baixar do Telegram</strong> e cole o link de uma mensagem com vídeo.
    </p>

    <section v-for="g in grupos" :key="g.pasta">
      <p class="rotulo">{{ g.pasta }} <span class="quantos">{{ g.videos.length }}</span></p>
      <ul class="grade">
        <li v-for="v in g.videos" :key="v.id">
          <button class="cartaz" @click="abrir(v)">
            <span class="capa" :data-inicial="v.nome.slice(0, 1)">
              <img v-if="v.capa" :src="v.capa" alt="" loading="lazy" @error="semImagem" />
              <span v-if="v.duracao" class="tempo">{{ relogio(v.duracao) }}</span>
              <span v-if="v.posicao && v.duracao" class="visto" :style="{ width: `${Math.min(100, (v.posicao / v.duracao) * 100)}%` }" />
            </span>
            <span class="nome">{{ v.nome }}</span>
          </button>
        </li>
      </ul>
    </section>

    <Folha v-if="aberto" :titulo="aberto.nome" @fechar="aberto = null">
      <p class="detalhes suave">
        {{ [aberto.pasta, duracao(aberto.duracao), tamanho(aberto.tamanho)].filter(Boolean).join(' · ') }}
      </p>
      <Assistir :chave="`proprio:${aberto.id}`" :titulo="aberto.nome" :posicao="aberto.posicao || 0" @escolheu="aberto = null" />
      <form v-if="editando" class="formulario edicao" @submit.prevent="guardarEdicao">
        <label><span>Nome</span><input v-model="editando.nome" class="campo" type="text" required /></label>
        <label>
          <span>Pasta</span>
          <input v-model="editando.pasta" class="campo" type="text" list="pastas-existentes" required />
        </label>
        <div class="linha-acoes">
          <button class="botao principal" type="submit">Guardar</button>
          <button class="botao" type="button" @click="editando = null">Cancelar</button>
        </div>
      </form>
      <div v-else class="linha-acoes mais">
        <button class="botao" @click="editando = { nome: aberto.nome, pasta: aberto.pasta }">Renomear ou mudar de pasta</button>
        <button class="botao perigo" @click="apagar"><Icone nome="lixeira" />Apagar</button>
      </div>
    </Folha>
  </div>
</template>

<style scoped>
.pagina {
  max-width: 1080px;
}

.topo {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.formulario {
  display: grid;
  gap: 14px;
  margin-bottom: 8px;
}

.formulario label {
  display: grid;
  gap: 6px;
  font-size: 0.9rem;
  font-weight: 550;
}

.formulario small {
  font-weight: 400;
  font-size: 0.8rem;
}

.com-botao {
  display: flex;
  gap: 8px;
}

.opcao {
  display: flex !important;
  align-items: center;
  gap: 8px !important;
  font-weight: 400 !important;
}

.edicao {
  margin-top: 16px;
}

.mais {
  margin-top: 14px;
}

.detalhes {
  margin: -6px 0 14px;
  font-size: 0.85rem;
}

.pedido {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 0;
}

.pedido .texto {
  display: grid;
  flex: 1;
  gap: 3px;
  min-width: 0;
}

.nome-do-pedido {
  font-weight: 550;
}

.meta {
  color: var(--texto-suave);
  font-size: 0.8rem;
}

.mensagem {
  color: var(--texto-suave);
  font-size: 0.82rem;
  line-height: 1.4;
}

.mensagem.erro {
  color: var(--vermelho);
}

.pastas {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 24px;
}

.quantos {
  color: var(--texto-fraco);
  font-size: 0.9rem;
  font-weight: 500;
}

.grade {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 20px 14px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.cartaz {
  display: grid;
  gap: 6px;
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
  display: block;
  overflow: hidden;
  aspect-ratio: 16 / 9;
  border-radius: var(--raio-p);
  background: var(--superficie);
}

.capa img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

.capa:not(:has(img))::after,
.capa.sem-imagem::after {
  content: attr(data-inicial);
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: var(--texto-fraco);
  font-size: 1.6rem;
  font-weight: 600;
}

.capa.sem-imagem img {
  display: none;
}

.tempo {
  position: absolute;
  right: 6px;
  bottom: 6px;
  padding: 1px 6px;
  border-radius: 4px;
  background: rgb(0 0 0 / 70%);
  color: #fff;
  font-size: 0.72rem;
  font-weight: 600;
}

.visto {
  position: absolute;
  left: 0;
  bottom: 0;
  height: 3px;
  background: var(--marca);
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

@media (max-width: 820px) {
  .grade {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
