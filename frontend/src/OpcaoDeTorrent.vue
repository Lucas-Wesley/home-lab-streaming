<script setup>
import { computed, inject, ref } from 'vue'
import Folha from './Folha.vue'
import FolhaDeAssistir from './FolhaDeAssistir.vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { noAparelho } from './formatos.js'
import { tamanho } from './tamanho.js'
import { verificacoes, verificar } from './verificacoes.js'

// Uma versão de torrent (resultado do Prowlarr). Na página do título vem avaliada (recomendado, toca
// em tudo, pesado, alertas) e pode ser verificada (S23). "Assistir" prepara o torrent no servidor e
// abre a folha de assistir no vídeo certo; "Baixar" deixa o vídeo (ou a temporada) no servidor (S28).
const props = defineProps({
  r: { type: Object, required: true },
  mostrarTipo: { type: Boolean, default: false },
  tmdb: { type: String, default: '' }, // "serie/1396": o título da busca, para achar a legenda certa e verificar
  temporada: { type: Number, default: 0 },
  episodio: { type: Number, default: 0 },
  rotulo: { type: String, default: '' }, // o título na folha de assistir ("Breaking Bad · T1 E3")
  capa: { type: String, default: '' },
  baixarTemporada: { type: Boolean, default: false }, // pacote: "Baixar" leva todos os episódios da temporada
  semDestaque: { type: Boolean, default: false }, // a assinatura está acima: o "Assistir" dela é o destacado
})
const recado = inject('recado')
const preparando = ref('')
const aberta = ref(null) // { hash, arquivo } com a folha de assistir aberta
const vendoDetalhes = ref(false)

const v = computed(() => verificacoes[props.r.chave])
const aviso = computed(() => noAparelho(v.value?.formatos))
const contexto = computed(() => ({ tmdb: props.tmdb, temporada: props.temporada, episodio: props.episodio }))

const ESTADOS = {
  verificando: { texto: 'Verificando…', classe: '' },
  ok: { texto: '✓ Confere', classe: 'ok' },
  suspeito: { texto: '⚠ Suspeito', classe: 'atencao' },
  errado: { texto: '✗ Não é este título', classe: 'erro' },
  indefinido: { texto: '? Sem como comparar', classe: '' },
  falhou: { texto: 'Não deu para verificar', classe: '' },
}

const meta = computed(() => {
  const r = props.r
  const partes = []
  if (r.indexador) partes.push(r.indexador)
  if (r.tamanho > 0) partes.push(tamanho(r.tamanho) + (r.episodios > 1 ? ` (${tamanho(r.tamanho / r.episodios)} por episódio)` : ''))
  return partes
})

async function preparar() {
  const corpo = { chave: props.r.chave, magnet: props.r.magnet, link: props.r.link, nome: props.r.titulo, ...contexto.value }
  return api('/api/opcoes/preparar', { metodo: 'POST', corpo, tempo: 110000 })
}

async function assistir() {
  preparando.value = 'assistir'
  try {
    aberta.value = await preparar()
    // Quem escolhe uma versão não verificada fica sabendo, enquanto assiste, se ela é o título.
    if (props.tmdb && !v.value) verificar(props.r, contexto.value)
  } catch (e) {
    recado(e.message, true)
  } finally {
    preparando.value = ''
  }
}

async function baixar() {
  preparando.value = 'baixar'
  try {
    const { hash, arquivo } = await preparar()
    let ids = [arquivo.id]
    if (props.baixarTemporada) {
      const d = await api(`/api/torrents/${hash}`)
      ids = d.arquivos.filter((f) => f.tipo === 'video' && (!props.temporada || f.temporada === props.temporada)).map((f) => f.id)
    }
    for (const id of ids) await api(`/api/arquivos/${id}/baixar`, { metodo: 'POST' })
    recado(ids.length > 1 ? `Baixando ${ids.length} episódios para o servidor. Acompanhe em Downloads.` : 'Baixando para o servidor. Acompanhe em Downloads.')
  } catch (e) {
    recado(e.message, true)
  } finally {
    preparando.value = ''
  }
}

function quando(iso) {
  const dias = Math.floor((Date.now() - new Date(iso)) / 86400000)
  if (!iso || Number.isNaN(dias)) return ''
  if (dias < 1) return 'hoje'
  if (dias < 60) return `há ${dias} ${dias === 1 ? 'dia' : 'dias'}`
  return new Date(iso).toLocaleDateString('pt-BR', { month: 'short', year: 'numeric' })
}
</script>

<template>
  <div class="opcao" :class="{ recomendada: r.recomendado }">
    <span class="texto">
      <span class="titulo"><span class="selo-t" title="Torrent">T</span>{{ r.titulo }}</span>
      <span class="meta">
        <template v-for="p in meta" :key="p">{{ p }} · </template>
        <span :class="{ fraco: r.semeadores < 3 }">{{ r.semeadores }} {{ r.semeadores === 1 ? 'semeador' : 'semeadores' }}</span><template v-if="quando(r.data)"> · {{ quando(r.data) }}</template>
      </span>
      <span class="chips">
        <span v-if="r.recomendado" class="chip ok">recomendado</span>
        <span v-if="r.toca_em_tudo" class="chip">toca em tudo</span>
        <span v-if="r.pesado" class="chip erro">pesado</span>
        <span v-if="r.suspeito" class="chip erro">suspeito</span>
        <span v-if="mostrarTipo && r.tipo" class="chip">{{ r.tipo === 'filme' ? 'filme' : 'série' }}</span>
        <span v-for="q in r.qualidade" :key="q" class="chip">{{ q }}</span>
      </span>
      <span v-for="a in r.alertas" :key="a" class="alerta">{{ a }}</span>
      <span v-if="v" class="verificacao">
        <span class="chip" :class="ESTADOS[v.estado]?.classe">{{ ESTADOS[v.estado]?.texto || v.estado }}</span>
        <span v-if="v.estado !== 'verificando'" class="resumo">{{ v.resumo }}</span>
        <span v-if="aviso" class="chip atencao">{{ aviso.texto }}</span>
        <button v-if="v.detalhes?.length || v.quadros?.length" class="link" @click="vendoDetalhes = true">Detalhes</button>
      </span>
      <span v-if="v?.parecer" class="parecer">Claude: {{ v.parecer }}</span>
    </span>
    <span class="botoes">
      <button class="botao" :class="{ principal: r.recomendado && !semDestaque }" :disabled="!!preparando" @click="assistir">
        <Icone nome="tocar" />{{ preparando === 'assistir' ? 'Abrindo…' : 'Assistir' }}
      </button>
      <span class="secundarios">
        <button class="link" :disabled="!!preparando" @click="baixar">{{ preparando === 'baixar' ? 'Preparando…' : baixarTemporada ? 'Baixar temporada' : 'Baixar' }}</button>
        <button v-if="tmdb && !v" class="link" @click="verificar(r, contexto)">Verificar</button>
      </span>
    </span>

    <FolhaDeAssistir
      v-if="aberta"
      :titulo="rotulo || r.titulo"
      origem="torrent"
      :nome="r.titulo"
      :chave="`arquivo:${aberta.arquivo.id}`"
      :capa="capa"
      :posicao="aberta.arquivo.posicao || 0"
      :formatos="aberta.arquivo.formatos || v?.formatos || null"
      @fechar="aberta = null"
    >
      <p v-if="v && v.estado !== 'ok'" class="suave nota-da-folha">Verificação: {{ ESTADOS[v.estado]?.texto }} {{ v.estado === 'verificando' ? '' : v.resumo }}</p>
    </FolhaDeAssistir>

    <Folha v-if="vendoDetalhes && v" titulo="Verificação" @fechar="vendoDetalhes = false">
      <p class="suave nome-do-arquivo">{{ v.arquivo || r.titulo }}</p>
      <p class="resumo-grande"><span class="chip" :class="ESTADOS[v.estado]?.classe">{{ ESTADOS[v.estado]?.texto }}</span> {{ v.resumo }}</p>
      <p v-if="v.parecer" class="parecer-grande"><strong>Parecer do Claude:</strong> {{ v.parecer }}</p>
      <div v-if="v.quadros?.length" class="quadros">
        <img v-for="q in v.quadros" :key="q" :src="`/api/quadros/${q}`" alt="Quadro do vídeo" loading="lazy" />
      </div>
      <p class="suave nota">Os quadros são de 20%, 50% e 80% do vídeo: compare com as imagens do filme para ver se é ele.</p>
      <ul class="detalhes">
        <li v-for="d in v.detalhes" :key="d">{{ d }}</li>
        <li v-if="aviso">{{ aviso.texto }}: use a TV ou o projetor.</li>
      </ul>
      <div class="linha-acoes">
        <button class="botao" @click="verificar(r, contexto, true)">Verificar de novo</button>
      </div>
    </Folha>
  </div>
</template>

<style scoped>
.opcao {
  display: flex;
  gap: 14px;
  align-items: center;
  padding: 12px 0;
}

.texto {
  display: grid;
  flex: 1;
  gap: 4px;
  min-width: 0;
}

.titulo {
  overflow-wrap: anywhere;
  font-size: 0.9rem;
  font-weight: 600;
  line-height: 1.35;
}

.selo-t {
  display: inline-grid;
  place-items: center;
  width: 18px;
  height: 18px;
  margin-right: 7px;
  border-radius: 5px;
  background: var(--verde);
  color: #fff;
  font-size: 0.68rem;
  font-weight: 700;
  vertical-align: 1px;
}

.meta {
  color: var(--texto-suave);
  font-size: 0.8rem;
}

.fraco {
  color: var(--vermelho);
}

.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 2px;
}

.chips:empty {
  display: none;
}

.alerta {
  color: var(--vermelho);
  font-size: 0.8rem;
  line-height: 1.4;
}

.verificacao {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
  margin-top: 2px;
  font-size: 0.8rem;
}

.resumo {
  color: var(--texto-suave);
}

.chip.atencao {
  background: var(--vermelho-fundo);
  color: var(--vermelho);
}

.chip.erro {
  background: var(--vermelho-fundo);
  color: var(--vermelho);
}

.parecer {
  color: var(--texto-suave);
  font-size: 0.8rem;
  font-style: italic;
  line-height: 1.45;
}

.parecer-grande {
  margin: -6px 0 14px;
  font-size: 0.9rem;
  line-height: 1.5;
}

.botoes {
  display: grid;
  flex: none;
  gap: 6px;
  justify-items: center;
}

.botoes .botao svg {
  width: 16px;
  height: 16px;
}

.secundarios {
  display: flex;
  gap: 12px;
}

.nota-da-folha {
  margin: 12px 0 0;
  font-size: 0.82rem;
}

.link {
  padding: 0;
  border: 0;
  background: none;
  color: var(--marca);
  font: inherit;
  font-size: 0.82rem;
  cursor: pointer;
}

.nome-do-arquivo {
  margin: -4px 0 10px;
  overflow-wrap: anywhere;
  font-size: 0.82rem;
}

.resumo-grande {
  margin: 0 0 14px;
  line-height: 1.5;
}

.quadros {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 6px;
}

.quadros img {
  width: 100%;
  border-radius: var(--raio-p);
  background: var(--superficie);
}

.nota {
  margin: 8px 0 12px;
  font-size: 0.78rem;
}

.detalhes {
  display: grid;
  gap: 4px;
  margin: 0 0 16px;
  padding-left: 18px;
  font-size: 0.86rem;
  line-height: 1.45;
}
</style>
