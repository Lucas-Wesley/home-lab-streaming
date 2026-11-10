<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import BotaoFavorito from './BotaoFavorito.vue'
import Folha from './Folha.vue'
import Icone from './Icone.vue'
import OpcaoDaAssinatura from './OpcaoDaAssinatura.vue'
import OpcaoDeTorrent from './OpcaoDeTorrent.vue'
import { api, lembrar } from './api.js'
import { tamanho } from './tamanho.js'
import { ajusteDaVerificacao, carregarVerificacoes, recomendavel, verificacoes, verificar } from './verificacoes.js'
import { voltar } from './voltar.js'

// Um filme ou série do TMDB e onde assisti-lo (S20, S28): a assinatura primeiro, se tiver, e os
// torrents organizados: versões do filme; na série, temporada por temporada, com o pacote da
// temporada e cada episódio. A melhor versão de torrent vem marcada. É a tela final para assistir.
const props = defineProps({ param: { type: String, required: true } })
const [tipo, id] = props.param.split('/')

const titulo = ref(null)
const assinatura = ref([])
const erro = ref('')
const geral = ref(null) // torrents da busca pelo título
const daTemporada = ref({}) // número → torrents da busca por aquela temporada
const procurando = ref(false)
const erroDosTorrents = ref('')
const temporada = ref(0)
const episodios = ref({}) // número da temporada → episódios do TMDB
const episodioAberto = ref(null)
const verTodas = ref({}) // seção → mostrar as opções escondidas
const filmesDaAssinatura = ref([]) // { id, nome, capa, posicao }
const episodiosDaAssinatura = ref({}) // "temporada:número" → episódio do provedor
const downloads = ref({}) // chave → download da assinatura

const POUCAS = 3

const ehSerie = tipo === 'serie'
const temporadas = computed(() => {
  const lista = (titulo.value?.temporadas || []).filter((s) => s.numero > 0 && s.episodios > 0)
  const especiais = (titulo.value?.temporadas || []).find((s) => s.numero === 0)
  return especiais && pacotesDe(0).length + Object.keys(episodiosComTorrent(0)).length ? [...lista, especiais] : lista
})

// Junta as opções das duas buscas (a geral e a da temporada), sem repetir, melhores primeiro. A
// verificação (S23) muda a ordem: o que não é o título vai para o fim e não é recomendado.
function juntar(...listas) {
  const vistas = new Set()
  const todas = []
  for (const o of listas.flat()) {
    if (o && !vistas.has(o.chave)) {
      vistas.add(o.chave)
      todas.push({ ...o, recomendado: false, nota: o.pontos + ajusteDaVerificacao(verificacoes[o.chave]) })
    }
  }
  todas.sort((a, b) => b.nota - a.nota)
  const melhor = todas.find((o) => recomendavel(o, verificacoes[o.chave]))
  if (melhor) melhor.recomendado = true
  return todas
}

const fontes = (n) => [geral.value, daTemporada.value[n]].filter(Boolean)
const organizadaDe = (fonte, n) => (fonte.temporadas || []).find((s) => s.numero === n)
const pacotesDe = (n) => juntar(...fontes(n).map((f) => organizadaDe(f, n)?.pacotes || []))
function episodiosComTorrent(n) {
  const porEpisodio = {}
  for (const f of fontes(n)) {
    for (const [ep, ops] of Object.entries(organizadaDe(f, n)?.episodios || {})) {
      porEpisodio[ep] = [...(porEpisodio[ep] || []), ...ops]
    }
  }
  return Object.fromEntries(Object.entries(porEpisodio).map(([ep, ops]) => [ep, juntar(ops)]))
}

const versoes = computed(() => juntar(geral.value?.versoes || []))
const completas = computed(() => juntar(...[geral.value, ...Object.values(daTemporada.value)].filter(Boolean).map((f) => f.completas || [])))
const outros = computed(() => juntar(...[geral.value, ...Object.values(daTemporada.value)].filter(Boolean).map((f) => f.outros || [])))
const pacotes = computed(() => pacotesDe(temporada.value))
const torrentsDosEpisodios = computed(() => episodiosComTorrent(temporada.value))

// A lista de episódios vem do TMDB; um episódio com torrent que o TMDB ainda não lista entra também.
const listaDeEpisodios = computed(() => {
  const doTMDB = episodios.value[temporada.value] || []
  const numeros = new Set(doTMDB.map((e) => e.numero))
  const extras = Object.keys(torrentsDosEpisodios.value)
    .map(Number)
    .filter((n) => !numeros.has(n))
    .map((n) => ({ numero: n, nome: '' }))
  for (const k of Object.keys(episodiosDaAssinatura.value)) {
    const [t, n] = k.split(':').map(Number)
    if (t === temporada.value && !numeros.has(n) && !extras.some((e) => e.numero === n)) extras.push({ numero: n, nome: episodiosDaAssinatura.value[k].titulo })
  }
  return [...doTMDB, ...extras]
    .sort((a, b) => a.numero - b.numero)
    .map((e) => ({ ...e, opcoes: torrentsDosEpisodios.value[e.numero] || [], assinatura: episodiosDaAssinatura.value[`${temporada.value}:${e.numero}`] || null }))
})

const rotuloDoEpisodio = (e) => `${titulo.value?.titulo} · T${temporada.value} E${e.numero}`

// O que a assinatura tem deste título: o filme (com onde parou) ou os episódios da série.
async function carregarAssinatura() {
  if (!assinatura.value.length) return
  try {
    downloads.value = Object.fromEntries((await api('/api/assinatura/downloads')).map((d) => [d.chave, d]))
  } catch {
    // Sem a lista, os botões de baixar continuam.
  }
  for (const a of assinatura.value) {
    if (a.tipo === 'filme') {
      let f = null
      try {
        f = await api(`/api/filmes/${a.id}`, { tempo: 20000 })
      } catch {
        // Sem os detalhes, o filme ainda toca.
      }
      filmesDaAssinatura.value = [...filmesDaAssinatura.value, { id: a.id, nome: a.nome, capa: f?.item.capa || '', posicao: f?.progresso?.posicao || 0 }]
    } else {
      try {
        const s = await api(`/api/series/${a.id}`, { tempo: 30000 })
        const eps = { ...episodiosDaAssinatura.value }
        for (const e of s.episodios) eps[`${e.temporada}:${e.numero}`] ??= { ...e, nomeNoCatalogo: a.nome }
        episodiosDaAssinatura.value = eps
      } catch {
        // O provedor não respondeu: ficam só os torrents.
      }
    }
  }
}

const visiveis = (secao, ops) => (verTodas.value[secao] ? ops : ops.filter((o) => !o.pesado).slice(0, POUCAS))
const escondidas = (secao, ops) => ops.length - visiveis(secao, ops).length

const buscasFeitas = computed(() => [...(geral.value?.buscas || []), ...(daTemporada.value[temporada.value]?.buscas || [])])
const nenhumTorrent = computed(() => {
  if (!geral.value) return false
  return ehSerie ? !geral.value.total && !Object.values(daTemporada.value).some((d) => d.total) : !versoes.value.length && !outros.value.length
})

// As verificações já feitas vêm junto; as melhores opções ainda não verificadas são verificadas
// sozinhas: as 3 primeiras e, se alguma der errado, as seguintes, até 5 por página.
const pedidas = new Set()
const LIMITE_AUTOMATICO = 5

function chavesDe(r) {
  const ops = [...(r.versoes || []), ...(r.completas || []), ...(r.outros || [])]
  for (const s of r.temporadas || []) ops.push(...s.pacotes, ...Object.values(s.episodios).flat())
  return ops.map((o) => o.chave)
}

function autoVerificar() {
  let alvos
  if (ehSerie) {
    const primeiroEp = listaDeEpisodios.value.find((e) => e.opcoes.length)
    alvos = [
      ...pacotes.value.filter((o) => !o.pesado).slice(0, 2).map((o) => [o, { temporada: temporada.value, episodio: 1 }]),
      ...(primeiroEp ? [[primeiroEp.opcoes[0], { temporada: temporada.value, episodio: primeiroEp.numero }]] : []),
    ]
  } else {
    alvos = versoes.value.filter((o) => !o.pesado && !o.suspeito).map((o) => [o, {}])
  }
  // Conta só as que ainda podem ser boas: as erradas abrem vaga para a próxima.
  const boas = alvos.filter(([o]) => verificacoes[o.chave] && verificacoes[o.chave].estado !== 'errado').length
  let vagas = 3 - boas
  for (const [o, contexto] of alvos) {
    if (vagas <= 0 || pedidas.size >= LIMITE_AUTOMATICO) break
    if (verificacoes[o.chave] || pedidas.has(o.chave)) continue
    pedidas.add(o.chave)
    vagas--
    verificar(o, { tmdb: props.param, ...contexto })
  }
}
watch(
  () => Object.values(verificacoes).filter((v) => v?.estado === 'errado').length,
  () => autoVerificar(),
)

async function buscarTorrents(n = 0) {
  procurando.value = true
  erroDosTorrents.value = ''
  try {
    const r = await api(`/api/titulos/${tipo}/${id}/torrents${n ? `?temporada=${n}` : ''}`, { tempo: 100000 })
    if (n) daTemporada.value = { ...daTemporada.value, [n]: r }
    else geral.value = r
    await carregarVerificacoes(chavesDe(r))
    if (!ehSerie || n) autoVerificar()
  } catch (e) {
    erroDosTorrents.value = e.message
  } finally {
    procurando.value = false
  }
}

async function carregarEpisodios(n) {
  if (episodios.value[n]) return
  try {
    episodios.value = { ...episodios.value, [n]: await api(`/api/titulos/serie/${id}/temporada/${n}`) }
  } catch {
    episodios.value = { ...episodios.value, [n]: [] }
  }
}

// Abrir uma temporada busca também por ela ("Nome S02"): nomes curtos se perdem na busca geral.
watch(temporada, (n) => {
  if (!n) return
  lembrar(`temporada-tmdb:${id}`, String(n))
  carregarEpisodios(n)
  if (!daTemporada.value[n]) buscarTorrents(n)
  else autoVerificar()
})

const meta = computed(() => {
  const t = titulo.value
  if (!t) return []
  const partes = [t.tipo === 'filme' ? 'Filme' : 'Série']
  if (t.ano) partes.push(t.ano)
  if (t.duracao) partes.push(`${Math.floor(t.duracao / 60) ? `${Math.floor(t.duracao / 60)} h ` : ''}${t.duracao % 60} min`)
  if (ehSerie && temporadas.value.length) partes.push(`${temporadas.value.length} ${temporadas.value.length === 1 ? 'temporada' : 'temporadas'}`)
  if (t.nota) partes.push(`★ ${t.nota.toFixed(1)}`)
  if (t.generos?.length) partes.push(t.generos.slice(0, 3).join(', '))
  return partes
})

onMounted(async () => {
  try {
    const r = await api(`/api/titulos/${tipo}/${id}`, { tempo: 30000 })
    titulo.value = r.titulo
    assinatura.value = r.assinatura
  } catch (e) {
    erro.value = e.status === 0 ? 'Sem conexão: o Streaming só abre na rede de casa.' : e.message
    return
  }
  carregarAssinatura()
  await buscarTorrents()
  if (ehSerie && temporadas.value.length) {
    const lembrada = Number(lembrar(`temporada-tmdb:${id}`))
    temporada.value = temporadas.value.some((s) => s.numero === lembrada) ? lembrada : temporadas.value[0].numero
  }
})
</script>

<template>
  <div class="pagina">
    <a class="voltar" href="#/inicio" @click.prevent="voltar('#/inicio')"><Icone nome="voltar" />Voltar</a>
    <div v-if="erro" class="aviso erro">{{ erro }}</div>

    <template v-if="titulo">
      <header class="cabeca">
        <img v-if="titulo.capa" class="cartaz" :src="titulo.capa" alt="" />
        <div>
          <h1>{{ titulo.titulo }}</h1>
          <p v-if="titulo.original" class="original">{{ titulo.original }}</p>
          <p class="meta">
            <span v-for="m in meta" :key="m">{{ m }}</span>
          </p>
          <div v-if="assinatura.length" class="linha-acoes assinatura-e-favorito">
            <p class="na-assinatura"><Icone nome="canais" />Está na sua assinatura</p>
            <BotaoFavorito :tipo="assinatura[0].tipo" :id="assinatura[0].id" />
          </div>
          <p v-if="titulo.sinopse" class="sinopse">{{ titulo.sinopse }}</p>
        </div>
      </header>

      <div v-if="erroDosTorrents" class="aviso erro">
        {{ erroDosTorrents }} <a v-if="erroDosTorrents.includes('Ajustes')" href="#/ajustes">Abrir Ajustes</a>
      </div>
      <!-- Filme: a assinatura primeiro, depois as versões de torrent -->
      <template v-if="!ehSerie && (filmesDaAssinatura.length || versoes.length)">
        <p class="rotulo">Onde assistir</p>
        <ul class="lista">
          <li v-for="f in filmesDaAssinatura" :key="'a' + f.id">
            <OpcaoDaAssinatura :chave="`filme:${f.id}`" :nome="f.nome" :rotulo="titulo.titulo" :capa="titulo.capa || f.capa" :posicao="f.posicao" :favorito="{ tipo: 'filme', id: f.id }" :download="downloads[`filme:${f.id}`]" />
          </li>
          <li v-for="o in visiveis('versoes', versoes)" :key="o.chave"><OpcaoDeTorrent :r="o" :tmdb="param" :rotulo="titulo.titulo" :capa="titulo.capa" :sem-destaque="filmesDaAssinatura.length > 0" /></li>
        </ul>
        <button v-if="escondidas('versoes', versoes)" class="mais" @click="verTodas.versoes = true">Mostrar mais {{ escondidas('versoes', versoes) }}</button>
      </template>
      <p v-if="procurando && !geral" class="vazio">Procurando nos indexadores…</p>
      <p v-else-if="nenhumTorrent && !procurando" class="vazio">Nenhum torrent deste título nos indexadores.</p>

      <!-- Série -->
      <template v-if="ehSerie && geral">
        <div v-if="temporadas.length" class="pilulas" role="tablist">
          <button
            v-for="s in temporadas"
            :key="s.numero"
            class="pilula"
            :class="{ ativa: temporada === s.numero }"
            role="tab"
            :aria-selected="temporada === s.numero"
            @click="temporada = s.numero"
          >
            {{ s.numero ? `Temporada ${s.numero}` : 'Especiais' }}
          </button>
        </div>

        <template v-if="pacotes.length">
          <p class="rotulo">Temporada {{ temporada }} completa</p>
          <ul class="lista">
            <li v-for="o in visiveis(`pacotes${temporada}`, pacotes)" :key="o.chave"><OpcaoDeTorrent :r="o" :tmdb="param" :temporada="temporada" :episodio="1" :rotulo="`${titulo.titulo} · T${temporada} E1`" :capa="titulo.capa" baixar-temporada /></li>
          </ul>
          <button v-if="escondidas(`pacotes${temporada}`, pacotes)" class="mais" @click="verTodas[`pacotes${temporada}`] = true">
            Mostrar mais {{ escondidas(`pacotes${temporada}`, pacotes) }}
          </button>
        </template>

        <p class="rotulo">Episódios <span v-if="procurando" class="procurando">procurando…</span></p>
        <ul class="lista">
          <li v-for="e in listaDeEpisodios" :key="e.numero">
            <button class="episodio" :disabled="!e.opcoes.length && !e.assinatura && !pacotes.length" @click="episodioAberto = e">
              <span class="numero">E{{ e.numero }}</span>
              <span class="texto">
                <span class="nome">{{ e.nome || `Episódio ${e.numero}` }}</span>
                <span v-if="e.assinatura" class="detalhes na-assinatura-ep"><Icone nome="canais" />Na assinatura{{ e.opcoes.length ? ` · ${e.opcoes.length} ${e.opcoes.length === 1 ? 'versão' : 'versões'} de torrent` : '' }}</span>
                <span v-else-if="e.opcoes.length" class="detalhes">
                  {{ e.opcoes.length }} {{ e.opcoes.length === 1 ? 'opção' : 'opções' }} · melhor: {{ e.opcoes[0].qualidade.join(' ') || '?' }}, {{ tamanho(e.opcoes[0].tamanho) }},
                  {{ e.opcoes[0].semeadores }} {{ e.opcoes[0].semeadores === 1 ? 'semeador' : 'semeadores' }}
                </span>
                <span v-else class="detalhes">{{ pacotes.length || completas.length ? 'só no pacote da temporada ou da série' : 'nenhum torrent avulso' }}</span>
              </span>
              <span v-if="e.opcoes.length || e.assinatura || pacotes.length" class="ir"><Icone nome="avancar" /></span>
            </button>
          </li>
        </ul>
        <p v-if="!listaDeEpisodios.length && !procurando" class="vazio">Sem episódios nesta temporada.</p>

        <template v-if="completas.length">
          <p class="rotulo">Série completa ou várias temporadas</p>
          <ul class="lista">
            <li v-for="o in visiveis('completas', completas)" :key="o.chave"><OpcaoDeTorrent :r="o" :tmdb="param" :temporada="1" :episodio="1" :rotulo="`${titulo.titulo} · T1 E1`" :capa="titulo.capa" /></li>
          </ul>
          <button v-if="escondidas('completas', completas)" class="mais" @click="verTodas.completas = true">Mostrar mais {{ escondidas('completas', completas) }}</button>
        </template>
      </template>

      <template v-if="outros.length">
        <p class="rotulo">Outros deste título</p>
        <p class="suave nota">O nome não diz {{ ehSerie ? 'a temporada ou o episódio' : 'a versão com clareza' }}.</p>
        <ul class="lista">
          <li v-for="o in visiveis('outros', outros)" :key="o.chave"><OpcaoDeTorrent :r="o" :tmdb="param" /></li>
        </ul>
        <button v-if="escondidas('outros', outros)" class="mais" @click="verTodas.outros = true">Mostrar mais {{ escondidas('outros', outros) }}</button>
      </template>

      <p v-if="buscasFeitas.length" class="suave nota buscas">Buscado nos indexadores: {{ buscasFeitas.map((b) => `“${b}”`).join(', ') }}.</p>
      <p class="suave nota">Dados de filmes e séries: TMDB.</p>
    </template>

    <Folha v-if="episodioAberto" :titulo="`E${episodioAberto.numero} · ${episodioAberto.nome || `Episódio ${episodioAberto.numero}`}`" @fechar="episodioAberto = null">
      <p v-if="episodioAberto.sinopse" class="suave sinopse-do-episodio">{{ episodioAberto.sinopse }}</p>
      <ul class="lista">
        <li v-if="episodioAberto.assinatura">
          <OpcaoDaAssinatura
            :chave="`episodio:${episodioAberto.assinatura.id}`"
            :nome="episodioAberto.assinatura.nomeNoCatalogo"
            :rotulo="rotuloDoEpisodio(episodioAberto)"
            :capa="titulo.capa"
            :posicao="episodioAberto.assinatura.posicao || 0"
            :download="downloads[`episodio:${episodioAberto.assinatura.id}`]"
          />
        </li>
        <li v-for="o in episodioAberto.opcoes" :key="o.chave">
          <OpcaoDeTorrent :r="o" :tmdb="param" :temporada="temporada" :episodio="episodioAberto.numero" :rotulo="rotuloDoEpisodio(episodioAberto)" :capa="titulo.capa" :sem-destaque="!!episodioAberto.assinatura" />
        </li>
      </ul>
      <template v-if="pacotes.length">
        <p class="rotulo">Dentro da temporada completa</p>
        <ul class="lista">
          <li v-for="o in pacotes.slice(0, POUCAS)" :key="'p' + o.chave">
            <OpcaoDeTorrent :r="o" :tmdb="param" :temporada="temporada" :episodio="episodioAberto.numero" :rotulo="rotuloDoEpisodio(episodioAberto)" :capa="titulo.capa" />
          </li>
        </ul>
      </template>
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
  margin-bottom: 20px;
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
  margin-bottom: 4px !important;
}

.original {
  margin: 0 0 8px;
  color: var(--texto-suave);
  font-size: 0.9rem;
}

.meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  margin: 0 0 16px;
  color: var(--texto-suave);
  font-size: 0.88rem;
}

.assinatura-e-favorito {
  align-items: center;
  margin-bottom: 14px;
}

.assinatura-e-favorito .na-assinatura {
  margin: 0;
}

.na-assinatura {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  margin: 0 0 14px;
  padding: 4px 10px 4px 8px;
  border-radius: 999px;
  background: var(--marca-fundo);
  color: var(--marca);
  font-size: 0.82rem;
  font-weight: 600;
}

.na-assinatura svg,
.na-assinatura-ep svg {
  width: 15px;
  height: 15px;
  color: var(--marca);
  vertical-align: -3px;
}

.na-assinatura-ep {
  display: inline-flex;
  gap: 5px;
  align-items: center;
}

.sinopse {
  margin: 0;
  line-height: 1.6;
}

.pilulas {
  display: flex;
  gap: 4px;
  margin: 8px calc(-1 * var(--margem)) 4px;
  padding: 0 var(--margem) 4px;
  overflow-x: auto;
}

.pilula {
  flex: none;
}

.procurando {
  margin-left: 8px;
  color: var(--texto-fraco);
  font-weight: 400;
  letter-spacing: normal;
  text-transform: none;
}

.episodio {
  display: grid;
  grid-template-columns: auto 1fr auto;
  gap: 12px;
  align-items: center;
  width: 100%;
  padding: 12px 0;
  border: 0;
  background: none;
  color: inherit;
  text-align: left;
  cursor: pointer;
}

.episodio:disabled {
  cursor: default;
}

.episodio:disabled .nome {
  color: var(--texto-suave);
}

.numero {
  min-width: 34px;
  color: var(--texto-fraco);
  font-variant-numeric: tabular-nums;
}

.texto {
  display: grid;
  gap: 3px;
  min-width: 0;
}

.nome {
  font-weight: 550;
  line-height: 1.35;
}

.episodio:not(:disabled):hover .nome {
  color: var(--marca);
}

.detalhes {
  color: var(--texto-fraco);
  font-size: 0.8rem;
}

.ir svg {
  width: 16px;
  height: 16px;
  color: var(--texto-fraco);
}

.mais {
  margin: 4px 0 0;
  padding: 0;
  border: 0;
  background: none;
  color: var(--marca);
  font: inherit;
  font-size: 0.88rem;
  cursor: pointer;
}

.nota {
  margin: 0 0 6px;
  font-size: 0.8rem;
  line-height: 1.45;
}

.buscas {
  margin-top: 28px;
}

.sinopse-do-episodio {
  margin: -4px 0 8px;
  font-size: 0.88rem;
  line-height: 1.5;
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
