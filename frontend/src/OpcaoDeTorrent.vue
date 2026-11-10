<script setup>
import { computed, inject, ref } from 'vue'
import Folha from './Folha.vue'
import FolhaDeAssistir from './FolhaDeAssistir.vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { noAparelho, nomeDoIdioma } from './formatos.js'
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
  verificando: { texto: 'Verificando…', classe: 'girando', icone: '…' },
  ok: { texto: '✓ Confere', classe: 'ok', icone: '✓' },
  suspeito: { texto: '⚠ Suspeito', classe: 'atencao', icone: '⚠' },
  errado: { texto: '✗ Não é este título', classe: 'erro', icone: '✗' },
  indefinido: { texto: '? Sem como comparar', classe: '', icone: '?' },
  falhou: { texto: 'Não deu para verificar', classe: '', icone: '?' },
}

// Tudo numa linha só: qualidade, tamanho, semeadores, data e, depois de verificar, o áudio.
const meta = computed(() => {
  const r = props.r
  const partes = (r.qualidade || []).map((q) => ({ texto: q }))
  if (props.mostrarTipo && r.tipo) partes.push({ texto: r.tipo === 'filme' ? 'filme' : 'série' })
  const idiomas = [...new Set((v.value?.formatos?.audios || []).map((a) => nomeDoIdioma(a.idioma)))]
  if (idiomas.length) partes.push({ texto: `áudio ${idiomas.join(', ')}`, classe: 'audio' })
  if (r.tamanho > 0) partes.push({ texto: tamanho(r.tamanho) + (r.episodios > 1 ? ` (${tamanho(r.tamanho / r.episodios)}/ep.)` : '') })
  partes.push({ texto: `${r.semeadores} sem.`, dica: `${r.semeadores} ${r.semeadores === 1 ? 'semeador' : 'semeadores'}`, classe: r.semeadores < 3 ? 'fraco' : '' })
  if (quando(r.data)) partes.push({ texto: quando(r.data) })
  if (r.indexador) partes.push({ texto: r.indexador, classe: 'indexador' })
  return partes
})

const dicaDaVerificacao = computed(() => {
  const x = v.value
  if (!x) return ''
  return [ESTADOS[x.estado]?.texto || x.estado, x.estado !== 'verificando' && x.resumo, x.parecer && `Claude: ${x.parecer}`].filter(Boolean).join('\n')
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
      <span class="linha-do-titulo">
        <span class="selo-t" title="Torrent">T</span>
        <span class="titulo" :title="r.titulo">{{ r.titulo }}</span>
      </span>
      <span class="meta">
        <span v-if="r.recomendado" class="recomendado">Recomendado</span>
        <span v-for="p in meta" :key="p" :class="p.classe" :title="p.dica">{{ p.texto }}</span>
      </span>
    </span>
    <span class="estado">
      <button v-if="v" class="icone-estado" :class="ESTADOS[v.estado]?.classe" :title="dicaDaVerificacao" @click="vendoDetalhes = true">{{ ESTADOS[v.estado]?.icone }}</button>
      <button v-else-if="tmdb" class="icone-estado apagado" title="Verificar se é este título" @click="verificar(r, contexto)">?</button>
      <span v-if="aviso" class="icone-estado atencao" :title="`${aviso.texto}: use a TV ou o projetor.`"><Icone :nome="aviso.semImagem ? 'sem-imagem' : 'mudo'" /></span>
      <span v-if="r.alertas?.length || r.pesado || r.suspeito" class="icone-estado atencao" :title="[...(r.alertas || []), r.pesado && 'Pesado', r.suspeito && 'Suspeito'].filter(Boolean).join('\n')">!</span>
    </span>
    <span class="botoes">
      <button class="botao" :class="{ principal: r.recomendado && !semDestaque }" :disabled="!!preparando" @click="assistir">
        <Icone nome="tocar" />{{ preparando === 'assistir' ? 'Abrindo…' : 'Assistir' }}
      </button>
      <button class="botao so-icone" :disabled="!!preparando" :title="baixarTemporada ? 'Baixar a temporada para o servidor' : 'Baixar para o servidor'" :aria-label="baixarTemporada ? 'Baixar temporada' : 'Baixar'" @click="baixar">
        <Icone nome="torrents" />
      </button>
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
  gap: 10px;
  align-items: center;
  padding: 9px 0;
}

.texto {
  display: grid;
  flex: 1;
  gap: 3px;
  min-width: 0;
}

.linha-do-titulo {
  display: flex;
  align-items: center;
  min-width: 0;
}

.titulo {
  overflow: hidden;
  font-size: 0.88rem;
  font-weight: 600;
  line-height: 1.35;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.selo-t {
  display: inline-grid;
  flex: none;
  place-items: center;
  width: 18px;
  height: 18px;
  margin-right: 7px;
  border-radius: 5px;
  background: var(--verde);
  color: #fff;
  font-size: 0.68rem;
  font-weight: 700;
}

.meta {
  overflow: hidden;
  color: var(--texto-suave);
  font-size: 0.78rem;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.meta > span + span::before {
  content: ' · ';
  color: var(--texto-fraco);
}

.recomendado {
  color: var(--azul);
  font-weight: 600;
}

.audio {
  color: var(--texto);
}

.fraco {
  color: var(--vermelho);
}

.estado {
  display: flex;
  flex: none;
  gap: 4px;
  align-items: center;
}

.icone-estado {
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  padding: 0;
  border: 0;
  border-radius: 999px;
  background: var(--superficie-forte);
  color: var(--texto-suave);
  font: inherit;
  font-size: 0.78rem;
  font-weight: 700;
  cursor: default;
}

button.icone-estado {
  cursor: pointer;
}

.icone-estado svg {
  width: 15px;
  height: 15px;
  stroke-width: 2;
}

.icone-estado.ok {
  background: var(--azul-fundo);
  color: var(--azul);
}

.icone-estado.atencao,
.icone-estado.erro {
  background: var(--vermelho-fundo);
  color: var(--vermelho);
}

.icone-estado.apagado {
  background: none;
  color: var(--texto-fraco);
  box-shadow: inset 0 0 0 1px var(--linha-forte);
}

.botoes {
  display: flex;
  flex: none;
  gap: 6px;
  align-items: center;
}

.botoes .botao svg {
  width: 16px;
  height: 16px;
}

.so-icone {
  padding: 0 9px;
}

.chip.atencao,
.chip.erro {
  background: var(--vermelho-fundo);
  color: var(--vermelho);
}

/* No celular, o nome ganha a linha toda; as informações, os ícones e os botões dividem a de baixo. */
@media (max-width: 560px) {
  .opcao {
    display: grid;
    grid-template-areas: 'titulo titulo titulo' 'meta estado botoes';
    grid-template-columns: minmax(0, 1fr) auto auto;
    gap: 4px 8px;
  }

  .texto {
    display: contents;
  }

  .linha-do-titulo {
    grid-area: titulo;
  }

  .meta {
    grid-area: meta;
    align-self: center;
  }

  .estado {
    grid-area: estado;
  }

  .botoes {
    grid-area: botoes;
  }
}

.parecer-grande {
  margin: -6px 0 14px;
  font-size: 0.9rem;
  line-height: 1.5;
}

.nota-da-folha {
  margin: 12px 0 0;
  font-size: 0.82rem;
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
