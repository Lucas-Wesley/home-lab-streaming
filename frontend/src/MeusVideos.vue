<script setup>
import { computed, inject, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Assistir from './Assistir.vue'
import Folha from './Folha.vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { chaveDoSalvo, linkDoTelegram } from './meusVideos.js'
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
const rastreio = ref(null) // { grupo, pasta, videos, novos, livre } do link de agora
const rastreando = ref(false)
const escolhidos = ref(new Set()) // mensagens marcadas na lista do rastreio
const salvando = ref(false)
const formulario = ref(null)
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

// Salvos: grupos e vídeos guardados para baixar depois, sem caçar o link no Telegram de novo.
const salvos = computed(() => dados.value?.salvos || [])
const salvoAtual = computed(() => {
  const chave = chaveDoSalvo(form.value.link, form.value.tudo)
  return (chave && salvos.value.find((s) => s.link === chave && s.tudo === form.value.tudo)) || null
})
const podeSalvar = computed(() => (form.value.tudo ? !!rastreio.value : !!chaveDoSalvo(form.value.link, false)))

function resumoDoSalvo(s) {
  const baixados = s.videos.filter((v) => v.ja)
  if (!s.tudo) {
    const v = s.videos[0] || {}
    return ['Vídeo', duracao(v.duracao), tamanho(v.tamanho), v.ja ? 'já baixado' : ''].filter(Boolean).join(' · ')
  }
  const partes = [`Grupo · ${quantos(s.videos.length)}`]
  if (s.videos.length && baixados.length === s.videos.length) partes.push('todos baixados')
  else {
    if (baixados.length) partes.push(`${baixados.length} ${baixados.length === 1 ? 'baixado' : 'baixados'}`)
    const falta = s.videos.filter((v) => !v.ja).reduce((soma, v) => soma + (v.tamanho || 0), 0)
    if (falta) partes.push(`${baixados.length ? 'faltam' : ''} ${tamanho(falta)}`.trim())
  }
  return partes.join(' · ')
}

const dataCurta = (iso) =>
  new Date(iso).toLocaleString('pt-BR', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' }).replace(',', ' às')

async function salvar({ calado = false } = {}) {
  salvando.value = true
  try {
    const link = linkDoTelegram(form.value.link)
    const corpo = form.value.tudo
      ? { link, tudo: true, titulo: rastreio.value.grupo, pasta: form.value.pasta, videos: rastreio.value.videos }
      : { link, tudo: false, nome: form.value.nome, pasta: form.value.pasta }
    const s = await api('/api/meus-videos/salvos', { metodo: 'POST', corpo, tempo: 90000 })
    if (rastreio.value && form.value.tudo) rastreio.value = { ...rastreio.value, salvo: s.id, de: s.atualizado_em }
    await carregar()
    if (!calado) recado('Salvo: fica em Salvos, nesta página, para baixar quando quiser.')
  } catch (e) {
    if (calado) throw e
    erroDoForm.value = e.message
  } finally {
    salvando.value = false
  }
}

async function tirarSalvo(s) {
  if (!confirm(`Tirar "${s.titulo}" dos Salvos? Os vídeos já baixados ficam.`)) return
  try {
    await api(`/api/meus-videos/salvos/${s.id}`, { metodo: 'DELETE' })
    await carregar()
  } catch (e) {
    recado(e.message, true)
  }
}

// Abre o salvo no formulário com a lista guardada: nada de Telegram até "Buscar de novo".
async function abrirSalvo(s) {
  erroDoForm.value = ''
  adicionando.value = true
  form.value = { link: s.link, nome: s.nome || '', pasta: s.pasta || form.value.pasta, tudo: s.tudo }
  await nextTick() // o watch do link limpa o rastreio antes
  if (s.tudo) {
    rastreio.value = {
      grupo: s.titulo,
      pasta: s.pasta,
      link: s.link,
      videos: s.videos,
      novos: s.videos.filter((v) => !v.ja).length,
      livre: dados.value?.livre || 0,
      salvo: s.id,
      de: s.atualizado_em,
    }
    escolhidos.value = new Set() // voltou para baixar alguns: marca quem quiser
  }
  await nextTick()
  formulario.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

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

// Outro link: o rastreio de antes não vale mais.
watch(
  () => form.value.link,
  () => {
    rastreio.value = null
  },
)

// Os já baixados não entram na escolha: baixar de novo não faz sentido.
const disponiveis = computed(() => (rastreio.value?.videos || []).filter((v) => !v.ja))
const escolhidosDaLista = computed(() => disponiveis.value.filter((v) => escolhidos.value.has(v.mensagem)))
const pesoEscolhido = computed(() => escolhidosDaLista.value.reduce((soma, v) => soma + (v.tamanho || 0), 0))
const naoCabe = computed(() => rastreio.value?.livre > 0 && pesoEscolhido.value > rastreio.value.livre)
const todosMarcados = computed(() => disponiveis.value.length > 0 && escolhidosDaLista.value.length === disponiveis.value.length)

function marcar(v) {
  if (v.ja) return
  const novo = new Set(escolhidos.value)
  novo.has(v.mensagem) ? novo.delete(v.mensagem) : novo.add(v.mensagem)
  escolhidos.value = novo
}

function marcarTodos() {
  escolhidos.value = todosMarcados.value ? new Set() : new Set(disponiveis.value.map((v) => v.mensagem))
}

const quantos = (n) => `${n} ${n === 1 ? 'vídeo' : 'vídeos'}`

// Rastrear: o servidor lê o grupo do link e mostra os vídeos com o nome que cada um vai ganhar.
async function rastrear() {
  erroDoForm.value = ''
  rastreando.value = true
  try {
    const r = await api('/api/meus-videos/telegram/rastrear', { metodo: 'POST', corpo: { link: linkDoTelegram(form.value.link) }, tempo: 200000 })
    rastreio.value = r.salvo ? { ...r, de: new Date().toISOString() } : r
    escolhidos.value = new Set(r.videos.filter((v) => !v.ja).map((v) => v.mensagem))
    if (!form.value.pasta.trim()) form.value.pasta = r.pasta
    if (r.salvo) await carregar() // a lista salva virou esta
  } catch (e) {
    erroDoForm.value = e.message
  } finally {
    rastreando.value = false
  }
}

async function pedir() {
  erroDoForm.value = ''
  if (form.value.tudo && !rastreio.value) return rastrear()
  enviando.value = true
  try {
    const corpo = { ...form.value, link: linkDoTelegram(form.value.link) }
    if (form.value.tudo) {
      if (!escolhidosDaLista.value.length) throw new Error('Marque ao menos um vídeo.')
      corpo.mensagens = escolhidosDaLista.value.map((v) => v.mensagem)
    }
    // Baixou só alguns do grupo: o grupo vai para os Salvos, para pegar o resto depois.
    const sobra = form.value.tudo && !salvoAtual.value && escolhidosDaLista.value.length < disponiveis.value.length
    if (sobra) await salvar({ calado: true })
    await api('/api/meus-videos/telegram', { metodo: 'POST', corpo })
    recado(sobra ? 'Na fila. O grupo ficou em Salvos para baixar o resto depois.' : 'Na fila: o servidor baixa e avisa quando terminar.')
    form.value = { link: '', nome: '', pasta: form.value.pasta, tudo: false }
    rastreio.value = null
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

    <form v-if="adicionando" ref="formulario" class="cartao formulario" @submit.prevent="pedir">
      <label>
        <span>Link da mensagem</span>
        <span class="com-botao">
          <input v-model="form.link" class="campo" type="url" inputmode="url" placeholder="https://t.me/c/…/4" autocomplete="off" required />
          <button type="button" class="botao" @click="colar">Colar</button>
        </span>
        <small class="suave">No Telegram: segure a mensagem de um vídeo › Copiar link.</small>
      </label>
      <div class="modos" role="radiogroup" aria-label="O que baixar">
        <button type="button" role="radio" class="pilula" :class="{ ativa: !form.tudo }" :aria-checked="!form.tudo" @click="form.tudo = false">Só este vídeo</button>
        <button type="button" role="radio" class="pilula" :class="{ ativa: form.tudo }" :aria-checked="form.tudo" @click="form.tudo = true">O grupo todo</button>
      </div>

      <template v-if="!form.tudo">
        <p v-if="salvoAtual" class="video-salvo">
          <Icone nome="salvo-cheio" />
          <span class="sobre">
            <span class="nome-do-video">{{ salvoAtual.titulo }}</span>
            <span class="peso">{{ resumoDoSalvo(salvoAtual) }} · salvo em {{ dataCurta(salvoAtual.criado_em) }}</span>
          </span>
        </p>
        <label>
          <span>Nome do vídeo <small class="suave">(opcional)</small></span>
          <input v-model="form.nome" class="campo" type="text" placeholder="Vazio: o texto da mensagem ou o nome do arquivo" />
        </label>
      </template>
      <template v-else-if="rastreio">
        <div class="rastreio">
          <div class="cabeca-do-rastreio">
            <p class="resumo">
              <strong>{{ rastreio.grupo || 'Grupo' }}</strong>
              <span class="suave">
                {{ quantos(rastreio.videos.length) }}<template v-if="rastreio.videos.length - rastreio.novos"> · {{ rastreio.videos.length - rastreio.novos }} já {{ rastreio.videos.length - rastreio.novos === 1 ? 'baixado' : 'baixados' }}</template>
              </span>
              <span v-if="rastreio.de" class="suave de-quando">
                Lista de {{ dataCurta(rastreio.de) }} ·
                <button type="button" class="link" :disabled="rastreando" @click="rastrear">{{ rastreando ? 'buscando…' : 'buscar de novo' }}</button>
              </span>
            </p>
            <button v-if="disponiveis.length" type="button" class="pilula" @click="marcarTodos">
              {{ todosMarcados ? 'Desmarcar todos' : 'Marcar todos' }}
            </button>
          </div>
          <ul class="escolha" aria-label="Vídeos do grupo">
            <li v-for="v in rastreio.videos" :key="v.mensagem">
              <label class="opcao-de-video" :class="{ ja: v.ja }">
                <input type="checkbox" :checked="v.ja || escolhidos.has(v.mensagem)" :disabled="v.ja" @change="marcar(v)" />
                <span class="sobre">
                  <span class="nome-do-video">{{ v.nome }}</span>
                  <span class="peso">
                    <template v-if="v.ja">já baixado</template>
                    <template v-else>{{ [duracao(v.duracao), tamanho(v.tamanho)].filter(Boolean).join(' · ') || 'tamanho desconhecido' }}</template>
                  </span>
                </span>
              </label>
            </li>
          </ul>
          <p class="total" :class="{ erro: naoCabe }">
            <span><strong>{{ quantos(escolhidosDaLista.length) }}</strong><template v-if="pesoEscolhido"> · {{ tamanho(pesoEscolhido) }}</template></span>
            <span v-if="rastreio.livre > 0" class="livre">
              {{ naoCabe ? 'Não cabe: ' : '' }}{{ tamanho(rastreio.livre) }} livres no servidor
            </span>
          </p>
        </div>
      </template>
      <p v-else class="suave explicacao">
        Rastrear lê o grupo e mostra os vídeos com o nome de cada um (do texto da mensagem ou do arquivo), antes de baixar.
      </p>

      <label v-if="!form.tudo || rastreio">
        <span>Pasta</span>
        <input v-model="form.pasta" class="campo" type="text" list="pastas-existentes" placeholder="Viagens, Trabalho…" required />
        <datalist id="pastas-existentes">
          <option v-for="p in pastas" :key="p" :value="p" />
        </datalist>
      </label>
      <div v-if="erroDoForm" class="aviso erro">{{ erroDoForm }}</div>
      <div class="linha-acoes">
        <button v-if="form.tudo && !rastreio" class="botao principal" type="submit" :disabled="rastreando">
          {{ rastreando ? 'Rastreando…' : 'Rastrear o grupo' }}
        </button>
        <button v-else-if="form.tudo" class="botao principal" type="submit" :disabled="enviando || !escolhidosDaLista.length || naoCabe">
          {{ enviando ? 'Mandando…' : !rastreio.novos ? 'Nada novo para baixar' : escolhidosDaLista.length ? `Baixar ${quantos(escolhidosDaLista.length)}` : 'Marque os vídeos' }}
        </button>
        <button v-else class="botao principal" type="submit" :disabled="enviando">{{ enviando ? 'Mandando…' : 'Baixar' }}</button>
        <button
          v-if="podeSalvar"
          type="button"
          class="botao"
          :class="{ salvo: salvoAtual }"
          :aria-pressed="!!salvoAtual"
          :disabled="salvando"
          @click="salvoAtual ? tirarSalvo(salvoAtual) : salvar()"
        >
          <Icone :nome="salvoAtual ? 'salvo-cheio' : 'salvo'" />{{ salvando ? 'Salvando…' : salvoAtual ? 'Salvo' : 'Salvar para depois' }}
        </button>
        <button class="botao" type="button" @click="adicionando = false">Fechar</button>
      </div>
    </form>

    <section v-if="pedidos.length">
      <p class="rotulo">Do Telegram</p>
      <ul class="lista pedidos">
        <li v-for="p in pedidos" :key="p.id">
          <div class="pedido">
            <div class="texto">
              <span class="nome-do-pedido">{{ p.nome || (p.tudo ? `Grupo › ${p.pasta}` : 'Vídeo') }}</span>
              <span class="meta">
                <span class="chip" :class="{ erro: p.estado === 'erro', andando: p.estado === 'baixando' }">{{ ESTADOS[p.estado] || p.estado }}</span>
                {{ p.pasta }}
                <template v-if="p.estado === 'baixando' && p.total"> · {{ p.videos }} de {{ p.total }}</template>
                <template v-if="p.baixado"> · {{ tamanho(p.baixado) }}<template v-if="p.estado === 'baixando' && p.previsto"> de {{ tamanho(p.previsto) }}</template></template>
                <template v-if="p.estado !== 'baixando' && p.videos > 1"> · {{ p.videos }} vídeos</template>
              </span>
              <span v-if="p.mensagem" class="mensagem" :class="{ erro: p.estado === 'erro' }">{{ p.mensagem }}</span>
            </div>
            <button v-if="p.estado === 'erro'" class="botao" @click="retomar(p)">Tentar de novo</button>
            <button class="botao icone" :aria-label="p.estado === 'baixando' ? 'Cancelar' : 'Tirar da lista'" @click="tirarPedido(p)"><Icone nome="fechar" /></button>
          </div>
        </li>
      </ul>
    </section>

    <section v-if="salvos.length">
      <p class="rotulo">Salvos <span class="quantos">{{ salvos.length }}</span></p>
      <ul class="lista salvos">
        <li v-for="s in salvos" :key="s.id">
          <div class="pedido">
            <button class="abrir-salvo" @click="abrirSalvo(s)">
              <span class="icone-do-salvo"><Icone :nome="s.tudo ? 'pasta' : 'tocar'" /></span>
              <span class="texto">
                <span class="nome-do-pedido">{{ s.titulo }}</span>
                <span class="meta">{{ resumoDoSalvo(s) }}</span>
                <span class="meta fraco">{{ s.pasta && s.pasta !== s.titulo ? `pasta ${s.pasta} · ` : '' }}lista de {{ dataCurta(s.atualizado_em) }}</span>
              </span>
              <Icone nome="avancar" class="seta" />
            </button>
            <button class="botao icone" aria-label="Tirar dos Salvos" @click="tirarSalvo(s)"><Icone nome="fechar" /></button>
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
  grid-template-columns: minmax(0, 1fr);
  gap: 14px;
  margin-bottom: 8px;
}

/* Nada passa da largura da tela: os filhos da grade podem encolher. */
.formulario > *,
.formulario label > * {
  min-width: 0;
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

.com-botao .campo {
  flex: 1;
  min-width: 0;
}

.com-botao .botao {
  flex-shrink: 0;
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

.modos {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin: -4px 0 -2px;
}

.explicacao {
  margin: 0;
  font-size: 0.85rem;
  line-height: 1.45;
}

.rastreio {
  display: grid;
  gap: 10px;
}

.cabeca-do-rastreio {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
}

.resumo {
  display: grid;
  gap: 2px;
  min-width: 0;
  margin: 0;
  font-size: 0.9rem;
  overflow-wrap: anywhere;
}

.resumo .suave {
  font-size: 0.82rem;
}

.cabeca-do-rastreio .pilula {
  flex-shrink: 0;
}

/* A lista rola por dentro: o botão de baixar fica à vista. */
.escolha {
  max-height: min(55vh, 520px);
  margin: 0;
  padding: 0;
  overflow-y: auto;
  border: 1px solid var(--linha);
  border-radius: var(--raio-p);
  list-style: none;
  overscroll-behavior: contain;
}

.escolha li + li {
  border-top: 1px solid var(--linha);
}

.opcao-de-video {
  display: flex !important;
  align-items: flex-start;
  gap: 12px !important;
  padding: 10px 12px;
  font-weight: 400 !important;
  cursor: pointer;
}

.opcao-de-video input {
  flex-shrink: 0;
  width: 18px;
  height: 18px;
  margin: 1px 0 0;
  accent-color: var(--marca);
}

.opcao-de-video.ja {
  cursor: default;
  opacity: 0.55;
}

.sobre {
  display: grid;
  gap: 2px;
  min-width: 0;
}

.nome-do-video {
  font-size: 0.9rem;
  line-height: 1.35;
  overflow-wrap: anywhere;
}

.peso {
  color: var(--texto-suave);
  font-size: 0.78rem;
}

.total {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 10px;
  margin: 0;
  font-size: 0.88rem;
}

.total .livre {
  color: var(--texto-suave);
}

.total.erro .livre {
  color: var(--vermelho);
  font-weight: 600;
}

.mais {
  margin-top: 14px;
}

.de-quando {
  font-size: 0.8rem;
}

.link {
  padding: 0;
  border: 0;
  background: none;
  color: var(--marca);
  font: inherit;
  text-decoration: underline;
  text-underline-offset: 2px;
  cursor: pointer;
}

.botao.salvo {
  color: var(--marca);
}

.video-salvo {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin: 0;
  padding: 10px 12px;
  border: 1px solid var(--linha);
  border-radius: var(--raio-p);
  color: var(--marca);
}

.video-salvo .sobre {
  color: var(--texto);
}

.abrir-salvo {
  display: flex;
  flex: 1;
  align-items: center;
  gap: 12px;
  min-width: 0;
  padding: 0;
  border: 0;
  background: none;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.abrir-salvo .texto {
  display: grid;
  flex: 1;
  gap: 3px;
  min-width: 0;
}

.abrir-salvo:hover .nome-do-pedido {
  color: var(--marca);
}

.icone-do-salvo {
  display: grid;
  flex-shrink: 0;
  place-items: center;
  width: 40px;
  height: 40px;
  border-radius: var(--raio-p);
  background: var(--superficie);
  color: var(--marca);
}

.seta {
  flex-shrink: 0;
  color: var(--texto-fraco);
}

.meta.fraco {
  color: var(--texto-fraco);
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
  overflow-wrap: anywhere;
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
