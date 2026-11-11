<script setup>
import { computed, inject, onBeforeUnmount, onMounted, ref } from 'vue'
import Icone from './Icone.vue'
import Sugestoes from './Sugestoes.vue'
import { api } from './api.js'
import { carregarTelas, telas } from './telas.js'

const props = defineProps({
  tela: { type: String, default: '' },
  secao: { type: String, default: '' },
})

// Ajustes em níveis (design system): a primeira tela lista as seções; cada uma abre numa página.
const SECOES = [
  { id: 'assinatura', nome: 'Assinatura', descricao: 'O acesso do provedor IPTV, a situação e o vencimento.' },
  { id: 'catalogo', nome: 'Catálogo', descricao: 'Canais, filmes e séries da assinatura; atualizar e categorias adultas.' },
  { id: 'telas', nome: 'Telas', descricao: 'TVs e projetores da casa que tocam vídeo pela rede.' },
  { id: 'torrents', nome: 'Busca de torrents', descricao: 'A conexão com o Prowlarr e os indexadores.' },
  { id: 'tmdb', nome: 'Filmes e séries (TMDB)', descricao: 'Pôsteres e temporadas na busca de torrents.' },
  { id: 'legendas', nome: 'Legendas (OpenSubtitles)', descricao: 'Legendas em português e inglês para o player e as telas.' },
  { id: 'sugestoes', nome: 'Sugestões de melhoria', descricao: 'Anote o que pode melhorar no Streaming; o Claude faz e responde aqui.' },
]
const atual = computed(() => SECOES.find((s) => s.id === props.secao))
const recado = inject('recado')

const conta = ref(null)
const trocando = ref(false)
const texto = ref('')
const salvando = ref(false)
const erroDoAcesso = ref('')
const catalogo = ref(null)
const procurando = ref(false)
const mostrarAdulto = ref(false)
const erro = ref('')
const prowlarr = ref(null)
const mudandoProwlarr = ref(false)
const urlDoProwlarr = ref('')
const chaveDoProwlarr = ref('')
const salvandoProwlarr = ref(false)
const erroDoProwlarr = ref('')
const legendas = ref(null)
const mudandoLegendas = ref(false)
const chaveDasLegendas = ref('')
const salvandoLegendas = ref(false)
const erroDasLegendas = ref('')
const tmdb = ref(null)
const mudandoTMDB = ref(false)
const chaveDoTMDB = ref('')
const salvandoTMDB = ref(false)
const erroDoTMDB = ref('')

const NOMES = { canal: ['canal', 'canais'], filme: ['filme', 'filmes'], serie: ['série', 'séries'] }
const contagem = computed(() =>
  Object.entries(catalogo.value?.itens || {})
    .filter(([, n]) => n)
    .map(([tipo, n]) => `${n.toLocaleString('pt-BR')} ${NOMES[tipo][n === 1 ? 0 : 1]}`),
)
const venceLogo = computed(() => {
  if (!conta.value?.vence_em) return false
  return new Date(conta.value.vence_em) - Date.now() < 7 * 86400000
})

const data = (iso, comHora = false) =>
  new Date(iso).toLocaleString('pt-BR', { day: '2-digit', month: 'short', year: 'numeric', ...(comHora ? { hour: '2-digit', minute: '2-digit' } : {}) })

async function salvarAcesso() {
  salvando.value = true
  erroDoAcesso.value = ''
  try {
    conta.value = await api('/api/acesso', { metodo: 'PUT', corpo: { texto: texto.value }, tempo: 30000 })
    texto.value = ''
    trocando.value = false
    recado('Acesso guardado. Baixando o catálogo…')
    acompanharCatalogo()
  } catch (e) {
    erroDoAcesso.value = e.message
  } finally {
    salvando.value = false
  }
}

let relogio
async function lerCatalogo() {
  try {
    catalogo.value = await api('/api/catalogo')
  } catch (e) {
    erro.value = e.message
  }
  if (!catalogo.value?.sincronizacao?.rodando) {
    clearInterval(relogio)
    relogio = null
  }
}

function acompanharCatalogo() {
  lerCatalogo()
  if (!relogio) relogio = setInterval(lerCatalogo, 2000)
}

async function sincronizar() {
  try {
    await api('/api/catalogo/sincronizar', { metodo: 'POST' })
    if (catalogo.value) catalogo.value.sincronizacao = { rodando: true, etapa: 'Começando' }
    acompanharCatalogo()
  } catch (e) {
    recado(e.message, true)
  }
}

async function procurarTelas() {
  procurando.value = true
  try {
    telas.value = await api('/api/telas/procurar', { metodo: 'POST', tempo: 30000 })
    recado(telas.value.length ? `${telas.value.length} ${telas.value.length === 1 ? 'tela achada' : 'telas achadas'}.` : 'Nenhuma tela respondeu. Elas estão ligadas?')
  } catch (e) {
    recado(e.message, true)
  } finally {
    procurando.value = false
  }
}

async function renomear(t) {
  const nome = prompt('Nome desta tela (ex.: TV da sala, Projetor):', t.nome)
  if (nome === null) return
  try {
    await api(`/api/telas/${encodeURIComponent(t.id)}`, { metodo: 'PATCH', corpo: { nome } })
    await carregarTelas(true)
  } catch (e) {
    recado(e.message, true)
  }
}

async function esquecer(t) {
  if (!confirm(`Tirar “${t.nome}” da lista? Ela volta na próxima procura, se estiver ligada.`)) return
  try {
    await api(`/api/telas/${encodeURIComponent(t.id)}`, { metodo: 'DELETE' })
    await carregarTelas(true)
  } catch (e) {
    recado(e.message, true)
  }
}

function abrirProwlarr() {
  urlDoProwlarr.value = prowlarr.value?.url || ''
  chaveDoProwlarr.value = ''
  erroDoProwlarr.value = ''
  mudandoProwlarr.value = true
}

async function salvarProwlarr() {
  salvandoProwlarr.value = true
  erroDoProwlarr.value = ''
  try {
    prowlarr.value = await api('/api/prowlarr', { metodo: 'PUT', corpo: { url: urlDoProwlarr.value, chave: chaveDoProwlarr.value }, tempo: 30000 })
    mudandoProwlarr.value = false
    recado('Prowlarr conectado.')
  } catch (e) {
    erroDoProwlarr.value = e.message
  } finally {
    salvandoProwlarr.value = false
  }
}

async function desconectarProwlarr() {
  if (!confirm('Desconectar o Prowlarr? A busca de torrents some até conectar de novo.')) return
  try {
    prowlarr.value = await api('/api/prowlarr', { metodo: 'DELETE' })
  } catch (e) {
    recado(e.message, true)
  }
}

async function salvarLegendas() {
  salvandoLegendas.value = true
  erroDasLegendas.value = ''
  try {
    legendas.value = await api('/api/opensubtitles', { metodo: 'PUT', corpo: { chave: chaveDasLegendas.value }, tempo: 30000 })
    chaveDasLegendas.value = ''
    mudandoLegendas.value = false
    recado('OpenSubtitles conectado.')
  } catch (e) {
    erroDasLegendas.value = e.message
  } finally {
    salvandoLegendas.value = false
  }
}

async function desconectarLegendas() {
  if (!confirm('Desconectar o OpenSubtitles? As legendas já escolhidas continuam funcionando.')) return
  try {
    legendas.value = await api('/api/opensubtitles', { metodo: 'DELETE' })
  } catch (e) {
    recado(e.message, true)
  }
}

async function salvarTMDB() {
  salvandoTMDB.value = true
  erroDoTMDB.value = ''
  try {
    tmdb.value = await api('/api/tmdb', { metodo: 'PUT', corpo: { chave: chaveDoTMDB.value }, tempo: 30000 })
    chaveDoTMDB.value = ''
    mudandoTMDB.value = false
    recado('TMDB conectado.')
  } catch (e) {
    erroDoTMDB.value = e.message
  } finally {
    salvandoTMDB.value = false
  }
}

async function desconectarTMDB() {
  if (!confirm('Desconectar o TMDB? A busca da aba Torrents volta a ir direto aos indexadores.')) return
  try {
    tmdb.value = await api('/api/tmdb', { metodo: 'DELETE' })
  } catch (e) {
    recado(e.message, true)
  }
}

async function mudarAdulto() {
  try {
    const r = await api('/api/ajustes', { metodo: 'PATCH', corpo: { mostrar_adulto: mostrarAdulto.value } })
    mostrarAdulto.value = r.mostrar_adulto
  } catch (e) {
    mostrarAdulto.value = !mostrarAdulto.value
    recado(e.message, true)
  }
}

onMounted(async () => {
  try {
    const [c, a] = await Promise.all([api('/api/acesso'), api('/api/ajustes')])
    conta.value = c
    mostrarAdulto.value = a.mostrar_adulto
  } catch (e) {
    erro.value = e.status === 0 ? 'Sem conexão: o Streaming só abre na rede de casa.' : e.message
  }
  acompanharCatalogo()
  carregarTelas(true)
  api('/api/tmdb')
    .then((t) => (tmdb.value = t))
    .catch(() => {})
  api('/api/opensubtitles')
    .then((l) => (legendas.value = l))
    .catch(() => {})
  api('/api/prowlarr', { tempo: 30000 })
    .then((p) => (prowlarr.value = p))
    .catch(() => {})
})

onBeforeUnmount(() => clearInterval(relogio))
</script>

<template>
  <div class="pagina">
    <template v-if="!atual">
      <h1>Ajustes</h1>
      <nav class="lista-de-ajustes" aria-label="Seções dos ajustes">
        <a v-for="s in SECOES" :key="s.id" :href="`#/ajustes/${s.id}`">
          <span>
            <span class="nome">{{ s.nome }}</span>
            <span class="descricao">{{ s.descricao }}</span>
          </span>
          <Icone nome="avancar" class="seta" />
        </a>
      </nav>
    </template>
    <template v-else>
      <a href="#/ajustes" class="voltar-ajustes"><Icone nome="voltar" /> Ajustes</a>
      <h1>{{ atual.nome }}</h1>
    </template>
    <div v-if="erro" class="aviso erro">{{ erro }}</div>

    <template v-if="atual?.id === 'assinatura'">
    <template v-if="conta?.configurado && !trocando">
      <dl class="dados">
        <dt>Servidor</dt>
        <dd>{{ conta.servidor }}</dd>
        <dt>Usuário</dt>
        <dd>{{ conta.usuario }}</dd>
        <template v-if="conta.status">
          <dt>Situação</dt>
          <dd><span class="chip" :class="conta.status === 'Active' ? 'ok' : 'erro'">{{ conta.status === 'Active' ? 'Ativa' : conta.status }}</span></dd>
        </template>
        <template v-if="conta.vence_em">
          <dt>Vence em</dt>
          <dd :class="{ atencao: venceLogo }">{{ data(conta.vence_em, true) }}</dd>
        </template>
        <template v-if="conta.conexoes">
          <dt>Conexões</dt>
          <dd>{{ conta.conexoes }} {{ conta.conexoes === 1 ? '(um aparelho por vez)' : 'ao mesmo tempo' }}</dd>
        </template>
      </dl>
      <div class="linha-acoes"><button class="botao" @click="trocando = true">Trocar o acesso</button></div>
    </template>
    <form v-else class="acesso" @submit.prevent="salvarAcesso">
      <p class="suave explicacao">
        Cole a mensagem que o provedor mandou, do jeito que veio, ou só o link da lista M3U. O servidor tira dela o endereço, o usuário e a senha e
        confere com o provedor. Fica guardado só no servidor de casa.
      </p>
      <textarea v-model="texto" class="campo" rows="6" placeholder="Usuário: …&#10;Senha: …&#10;DNS: http://…" aria-label="Mensagem do provedor" />
      <div v-if="erroDoAcesso" class="aviso erro">{{ erroDoAcesso }}</div>
      <div class="linha-acoes">
        <button class="botao principal" :disabled="salvando || !texto.trim()">{{ salvando ? 'Conferindo…' : 'Guardar acesso' }}</button>
        <button v-if="conta?.configurado" type="button" class="botao" @click="trocando = false">Cancelar</button>
      </div>
    </form>

    </template>

    <template v-else-if="atual?.id === 'catalogo'">
    <p class="suave explicacao">
      <template v-if="catalogo?.sincronizacao?.rodando">{{ catalogo.sincronizacao.etapa }}…</template>
      <template v-else-if="catalogo?.sincronizacao?.terminada_em">Atualizado em {{ data(catalogo.sincronizacao.terminada_em, true) }}. Atualiza sozinho uma vez por dia.</template>
      <template v-else>Ainda não foi baixado.</template>
    </p>
    <p v-if="contagem.length" class="chips">
      <span v-for="c in contagem" :key="c" class="chip">{{ c }}</span>
    </p>
    <div v-if="catalogo?.sincronizacao?.erro" class="aviso erro">A última atualização falhou: {{ catalogo.sincronizacao.erro }}</div>
    <div class="linha-acoes">
      <button class="botao" :disabled="!conta?.configurado || catalogo?.sincronizacao?.rodando" @click="sincronizar">
        <Icone nome="atualizar" />Atualizar agora
      </button>
    </div>
    <label class="opcao">
      <input v-model="mostrarAdulto" type="checkbox" @change="mudarAdulto" />
      Mostrar as categorias adultas
    </label>

    </template>

    <template v-else-if="atual?.id === 'telas'">
    <p class="suave explicacao">TVs e projetores da casa que tocam vídeo pela rede (DLNA). Toque no nome para trocar.</p>
    <ul v-if="telas.length" class="lista">
      <li v-for="t in telas" :key="t.id" class="tela">
        <button class="nome-da-tela" @click="renomear(t)">
          <span class="nome">{{ t.nome }} <span class="chip" :class="{ ok: t.ligada }">{{ t.ligada ? 'ligada' : 'não respondeu' }}</span></span>
          <span class="detalhe">{{ t.fabricante }} · {{ t.endereco }}<template v-if="t.nome !== t.nome_original"> · {{ t.nome_original }}</template></span>
        </button>
        <button class="botao icone" aria-label="Tirar da lista" title="Tirar da lista" @click="esquecer(t)"><Icone nome="fechar" /></button>
      </li>
    </ul>
    <div class="linha-acoes">
      <button class="botao" :disabled="procurando" @click="procurarTelas">{{ procurando ? 'Procurando…' : 'Procurar telas' }}</button>
    </div>

    </template>

    <template v-else-if="atual?.id === 'torrents'">
    <p class="suave explicacao">
      A busca da aba Torrents pergunta ao Prowlarr, que fica no servidor. Os indexadores são cadastrados na interface dele, em
      <a href="https://prowlarr.internal" target="_blank" rel="noopener">prowlarr.internal</a>.
    </p>
    <template v-if="prowlarr?.configurado && !mudandoProwlarr">
      <dl class="dados">
        <dt>Endereço</dt>
        <dd>{{ prowlarr.url }}</dd>
        <dt>Situação</dt>
        <dd>
          <span v-if="prowlarr.erro" class="chip erro">{{ prowlarr.erro }}</span>
          <span v-else class="chip" :class="{ ok: prowlarr.indexadores }">
            {{ prowlarr.indexadores ? `${prowlarr.indexadores} ${prowlarr.indexadores === 1 ? 'indexador ativo' : 'indexadores ativos'}` : 'nenhum indexador ativo' }}
          </span>
        </dd>
      </dl>
      <div class="linha-acoes">
        <button class="botao" @click="abrirProwlarr">Trocar</button>
        <button class="botao" @click="desconectarProwlarr">Desconectar</button>
      </div>
    </template>
    <div v-else-if="!mudandoProwlarr" class="linha-acoes">
      <button class="botao" :disabled="!prowlarr" @click="abrirProwlarr">Conectar o Prowlarr</button>
    </div>
    <form v-else class="acesso" @submit.prevent="salvarProwlarr">
      <input v-model="urlDoProwlarr" class="campo" type="url" placeholder="http://prowlarr:9696" aria-label="Endereço do Prowlarr" autocomplete="off" />
      <input
        v-model="chaveDoProwlarr"
        class="campo"
        type="password"
        :placeholder="prowlarr?.configurado ? 'Chave da API (deixe vazio para manter)' : 'Chave da API (Settings → General → API Key)'"
        aria-label="Chave da API do Prowlarr"
        autocomplete="off"
      />
      <div v-if="erroDoProwlarr" class="aviso erro">{{ erroDoProwlarr }}</div>
      <div class="linha-acoes">
        <button class="botao principal" :disabled="salvandoProwlarr">{{ salvandoProwlarr ? 'Conferindo…' : 'Conectar' }}</button>
        <button type="button" class="botao" @click="mudandoProwlarr = false">Cancelar</button>
      </div>
    </form>

    </template>

    <template v-else-if="atual?.id === 'tmdb'">
    <p class="suave explicacao">
      Com o TMDB, a busca da aba Torrents mostra primeiro os filmes e séries, com pôster; ao abrir um, os torrents vêm organizados por temporada e
      episódio. A chave é de graça, em themoviedb.org → Configurações → API.
    </p>
    <template v-if="tmdb?.configurado && !mudandoTMDB">
      <p class="chips"><span class="chip ok">conectado</span></p>
      <div class="linha-acoes">
        <button class="botao" @click="mudandoTMDB = true">Trocar a chave</button>
        <button class="botao" @click="desconectarTMDB">Desconectar</button>
      </div>
    </template>
    <div v-else-if="!mudandoTMDB" class="linha-acoes">
      <button class="botao" :disabled="!tmdb" @click="mudandoTMDB = true">Conectar o TMDB</button>
    </div>
    <form v-else class="acesso" @submit.prevent="salvarTMDB">
      <input v-model="chaveDoTMDB" class="campo" type="password" placeholder="Chave da API ou token de leitura" aria-label="Chave da API do TMDB" autocomplete="off" />
      <div v-if="erroDoTMDB" class="aviso erro">{{ erroDoTMDB }}</div>
      <div class="linha-acoes">
        <button class="botao principal" :disabled="salvandoTMDB || !chaveDoTMDB.trim()">{{ salvandoTMDB ? 'Conferindo…' : 'Conectar' }}</button>
        <button type="button" class="botao" @click="mudandoTMDB = false">Cancelar</button>
      </div>
    </form>
    <p class="suave atribuicao">Este produto usa a API do TMDB, mas não é endossado nem certificado pelo TMDB.</p>

    </template>

    <template v-else-if="atual?.id === 'legendas'">
    <p class="suave explicacao">
      Legendas em português e inglês para filmes, episódios e torrents, no player e nas telas. A chave é de graça, em opensubtitles.com → API
      consumers. Cada legenda escolhida é baixada uma vez e fica guardada no servidor.
    </p>
    <template v-if="legendas?.configurado && !mudandoLegendas">
      <p class="chips"><span class="chip ok">conectado</span></p>
      <div class="linha-acoes">
        <button class="botao" @click="mudandoLegendas = true">Trocar a chave</button>
        <button class="botao" @click="desconectarLegendas">Desconectar</button>
      </div>
    </template>
    <div v-else-if="!mudandoLegendas" class="linha-acoes">
      <button class="botao" :disabled="!legendas" @click="mudandoLegendas = true">Conectar o OpenSubtitles</button>
    </div>
    <form v-else class="acesso" @submit.prevent="salvarLegendas">
      <input v-model="chaveDasLegendas" class="campo" type="password" placeholder="Chave da API (API key)" aria-label="Chave da API do OpenSubtitles" autocomplete="off" />
      <div v-if="erroDasLegendas" class="aviso erro">{{ erroDasLegendas }}</div>
      <div class="linha-acoes">
        <button class="botao principal" :disabled="salvandoLegendas || !chaveDasLegendas.trim()">{{ salvandoLegendas ? 'Conferindo…' : 'Conectar' }}</button>
        <button type="button" class="botao" @click="mudandoLegendas = false">Cancelar</button>
      </div>
    </form>

    </template>

    <template v-else-if="atual?.id === 'sugestoes'">
    <p class="suave explicacao">Anote o que pode melhorar no Streaming. Depois é só pedir ao Claude para ver as sugestões: ele faz as mudanças e responde aqui.</p>
    <Sugestoes :tela="tela" />
    </template>
  </div>
</template>

<style scoped>
.atribuicao {
  margin: 10px 0 0;
  font-size: 0.75rem;
}

.explicacao {
  margin: 0 0 12px;
  font-size: 0.88rem;
  line-height: 1.5;
}

.dados {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 8px 16px;
  margin: 0 0 14px;
  font-size: 0.92rem;
}

.dados dt {
  color: var(--texto-suave);
}

.dados dd {
  margin: 0;
  overflow-wrap: anywhere;
}

.atencao {
  color: var(--vermelho);
  font-weight: 600;
}

.acesso {
  display: grid;
  gap: 10px;
}

.acesso textarea {
  resize: vertical;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.85rem;
  line-height: 1.5;
}

.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 0 0 12px;
}

.aviso {
  margin-bottom: 12px;
}

.botao svg {
  width: 16px;
  height: 16px;
}

.opcao {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 16px;
  font-size: 0.92rem;
}

.opcao input {
  accent-color: var(--marca);
}

.tela {
  display: flex;
  align-items: center;
  gap: 8px;
}

.nome-da-tela {
  display: grid;
  flex: 1;
  gap: 3px;
  padding: 12px 0;
  border: 0;
  background: none;
  text-align: left;
  cursor: pointer;
}

.nome {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 600;
}

.nome-da-tela:hover .nome {
  color: var(--marca);
}

.detalhe {
  color: var(--texto-suave);
  font-size: 0.82rem;
}

.lista + .linha-acoes {
  margin-top: 12px;
}
</style>
