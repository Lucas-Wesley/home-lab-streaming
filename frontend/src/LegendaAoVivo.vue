<script setup>
import { computed, inject, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { enderecoDaLegenda, falaAtual, lerVTT } from './legendas.js'

// A legenda passando no celular enquanto o vídeo toca na tela (S31). A fala que está na tela agora
// fica no meio, em destaque; entre uma fala e outra, nenhuma fica em destaque e a próxima já espera
// no meio. As de antes continuam ali para dar tempo de tocar. Tocar numa fala a escolhe (tocar na
// vizinha junta as duas, para a frase cortada ao meio) e "Gerar flashcard" vira cartão no
// baralho Inglês, traduzido pelo Claude; "Com áudio" leva junto o trecho da fala cortado do vídeo (S39,
// só no torrent ou no vídeo baixado). Rolar com o dedo solta a legenda até "Voltar ao ponto" (a legenda
// volta para o vídeo); "Tocar desta fala" faz o inverso: o vídeo vai para a fala escolhida (S40).
// "Ampliar" abre a legenda na tela inteira, com letra maior, para achar e escolher as falas com calma.
const props = defineProps({
  chave: { type: String, required: true },
  posicao: { type: Number, required: true }, // segundos, já contando o tempo desde a última leitura
  versao: { type: Number, default: 0 }, // muda quando a legenda é trocada ou ajustada no controle
  tocando: { type: Boolean, default: false },
})
const recado = inject('recado')
const emit = defineEmits(['falas', 'ir', 'alternar'])

const legenda = ref(null)
const falas = ref([])
const escolhidas = ref([]) // índices seguidos
const mandando = ref('') // 'texto' ou 'audio' enquanto manda
const flashcards = ref(false)
const comAudio = ref(false) // o servidor consegue cortar o áudio deste vídeo
const seguindo = ref(true)
const ampliada = ref(false)
const lista = ref(null)

const atual = computed(() => falaAtual(falas.value, props.posicao))
// Na tela agora: a última que começou, se ainda não acabou.
const ativa = computed(() => {
  const f = falas.value[atual.value]
  return f && props.posicao < f.fim ? atual.value : -1
})
// No meio da lista: a fala na tela ou, no intervalo, a próxima.
const centro = computed(() => (ativa.value >= 0 ? ativa.value : Math.min(atual.value + 1, falas.value.length - 1)))
const podeMandar = computed(() => flashcards.value && (legenda.value?.idioma || '').startsWith('en'))

let marca = ''
async function carregar() {
  try {
    const l = (await api(`/api/legendas/${props.chave}`)).escolhida
    const nova = l ? enderecoDaLegenda(l) : ''
    if (nova === marca) return
    marca = nova
    legenda.value = l
    if (!l) {
      falas.value = []
      return
    }
    const r = await fetch(nova)
    falas.value = r.ok ? lerVTT(await r.text()) : []
    escolhidas.value = []
  } catch {
    // Sem a legenda agora: tenta na próxima.
  }
}

// Pela posição na tela (e não por offsetTop, que conta a partir de outro elemento).
function centralizar(suave = true) {
  const caixa = lista.value
  const el = caixa?.querySelector(`[data-i="${Math.max(0, centro.value)}"]`)
  if (!el) return
  const a = caixa.getBoundingClientRect()
  const b = el.getBoundingClientRect()
  const top = caixa.scrollTop + (b.top - a.top) - (a.height - b.height) / 2
  caixa.scrollTo({ top, behavior: suave ? 'smooth' : 'auto' })
}

watch(centro, () => seguindo.value && nextTick(centralizar))
watch(falas, (f) => {
  emit('falas', f.length > 0)
  nextTick(() => centralizar(false))
})
watch(() => props.versao, carregar)

// O dedo na lista solta a legenda; o rolar feito pelo próprio app não conta.
function soltou() {
  seguindo.value = false
}
function voltarAoPonto() {
  seguindo.value = true
  escolhidas.value = []
  centralizar()
}

// O inverso de "Voltar ao ponto": o vídeo vai para a fala escolhida (meio segundo antes, para não
// perder o começo da voz). A escolha continua, para gerar o cartão depois de ouvir.
function tocarDaqui() {
  const e = escolhidas.value
  if (!e.length) return
  emit('ir', Math.max(0, falas.value[e[0]].inicio - 0.5))
  seguindo.value = true
}

function ampliar(sim) {
  ampliada.value = sim
  nextTick(() => centralizar(false))
}

function tocar(i) {
  seguindo.value = false // a fala escolhida fica onde está, até "Voltar ao ponto"
  const e = escolhidas.value
  if (e.includes(i)) escolhidas.value = e.length > 1 && (i === e[0] || i === e[e.length - 1]) ? e.filter((x) => x !== i) : []
  else if (e.length && (i === e[0] - 1 || i === e[e.length - 1] + 1) && e.length < 4) escolhidas.value = [...e, i].sort((a, b) => a - b)
  else escolhidas.value = [i]
}

async function mandar(audio = false) {
  const e = escolhidas.value
  if (!e.length) return
  mandando.value = audio ? 'audio' : 'texto'
  try {
    const f = falas.value
    await api('/api/flashcards', {
      metodo: 'POST',
      corpo: {
        chave: props.chave,
        falas: e.map((i) => f[i].texto),
        antes: f.slice(Math.max(0, e[0] - 4), e[0]).map((x) => x.texto),
        depois: f.slice(e[e.length - 1] + 1, e[e.length - 1] + 3).map((x) => x.texto),
        ...(audio && { audio: true, inicio: f[e[0]].inicio, fim: f[e[e.length - 1]].fim }),
      },
    })
    escolhidas.value = []
    recado(audio ? 'Mandado para o Flashcards, com o áudio da fala.' : 'Mandado para o Flashcards.')
  } catch (err) {
    recado(err.message, true)
  } finally {
    mandando.value = ''
  }
}

// A legenda gerada cresce, e a escolhida pode mudar em outro aparelho: confere de tempos em tempos.
let relogio
onMounted(async () => {
  carregar()
  relogio = setInterval(carregar, 20000)
  try {
    const d = await api(`/api/flashcards?chave=${encodeURIComponent(props.chave)}`)
    flashcards.value = d.disponivel
    comAudio.value = !!d.audio
  } catch {
    flashcards.value = false
  }
})
onBeforeUnmount(() => clearInterval(relogio))
</script>

<template>
  <section v-if="falas.length" class="legenda-ao-vivo" :class="{ ampliada }">
    <header v-if="ampliada" class="cabeca">
      <strong>Legenda</strong>
      <button class="botao icone" :aria-label="tocando ? 'Pausar' : 'Continuar'" @click="emit('alternar')"><Icone :nome="tocando ? 'pausar' : 'tocar'" /></button>
      <button class="botao icone" aria-label="Fechar a legenda ampliada" @click="ampliar(false)"><Icone nome="reduzir" /></button>
    </header>
    <div class="caixa">
      <ol ref="lista" class="falas" @touchstart.passive="soltou" @wheel.passive="soltou">
        <li
          v-for="(f, i) in falas"
          :key="i"
          :data-i="i"
          class="fala"
          :class="{ atual: i === ativa, passada: i < centro && i !== ativa, escolhida: escolhidas.includes(i) }"
          :aria-current="i === ativa ? 'true' : null"
          @click="tocar(i)"
        >
          {{ f.texto }}
        </li>
      </ol>
      <button v-if="!ampliada" class="botao icone ampliar" aria-label="Ampliar a legenda" title="Ampliar a legenda" @click="ampliar(true)"><Icone nome="ampliar" /></button>
    </div>
    <div class="acoes-da-legenda">
      <div v-if="escolhidas.length || !seguindo" class="linha">
        <button v-if="escolhidas.length" class="botao" @click="tocarDaqui">Tocar desta fala</button>
        <button v-if="!seguindo" class="botao" @click="voltarAoPonto">Voltar ao ponto</button>
      </div>
      <div v-if="escolhidas.length" class="linha">
        <button v-if="podeMandar" class="botao principal" :disabled="!!mandando" @click="mandar()">
          {{ mandando === 'texto' ? 'Mandando…' : escolhidas.length > 1 ? `Gerar flashcard (${escolhidas.length})` : 'Gerar flashcard' }}
        </button>
        <button v-if="podeMandar && comAudio" class="botao principal" :disabled="!!mandando" @click="mandar(true)">
          {{ mandando === 'audio' ? 'Mandando…' : 'Com áudio' }}
        </button>
        <button class="botao fechar" :disabled="!!mandando" aria-label="Cancelar" title="Cancelar" @click="escolhidas = []">✕</button>
      </div>
      <p v-else-if="seguindo" class="dica">{{ podeMandar ? 'Toque numa fala para ouvir de novo ou gerar um flashcard.' : 'Toque numa fala para ouvir de novo.' }}</p>
    </div>
  </section>
</template>

<style scoped>
.legenda-ao-vivo {
  width: 100%;
  margin: 4px 0 6px;
}

.caixa {
  position: relative;
}

.falas {
  height: 300px;
  margin: 0;
  padding: 130px 4px;
  overflow-y: auto;
  list-style: none;
  border-radius: var(--raio);
  background: var(--superficie);
  mask-image: linear-gradient(transparent, #000 22%, #000 78%, transparent);
  scrollbar-width: none;
}

.fala {
  padding: 8px 12px;
  border-radius: var(--raio-p);
  color: var(--texto-suave);
  font-size: 1rem;
  line-height: 1.4;
  white-space: pre-line;
  cursor: pointer;
  transition: color 0.2s ease, background 0.2s ease;
}

.fala.passada {
  color: var(--texto-fraco);
}

/* A fala na tela agora: mesmo tamanho das outras (a lista não pula), mas inconfundível. */
.fala.atual {
  background: var(--superficie-forte);
  box-shadow: inset 3px 0 0 var(--marca);
  color: var(--texto);
  font-weight: 650;
}

.fala.escolhida {
  background: var(--marca-fundo);
  color: var(--marca);
}

.ampliar {
  position: absolute;
  top: 6px;
  right: 6px;
  width: 34px;
  height: 34px;
  min-height: 34px;
  background: var(--fundo);
  opacity: 0.85;
}

/* Ampliada: a tela inteira para a legenda (os avisos continuam por cima), letra maior. */
.ampliada {
  position: fixed;
  inset: 0;
  z-index: 35;
  display: flex;
  flex-direction: column;
  margin: 0;
  padding: max(8px, env(safe-area-inset-top)) 12px max(12px, env(safe-area-inset-bottom));
  background: var(--fundo);
}

.cabeca {
  display: flex;
  align-items: center;
  gap: 6px;
  padding-bottom: 8px;
}

.cabeca strong {
  flex: 1;
  text-align: left;
}

.ampliada .caixa {
  display: flex;
  flex: 1;
  min-height: 0;
}

.ampliada .falas {
  flex: 1;
  height: auto;
  padding: 40vh 6px;
}

.ampliada .fala {
  padding: 10px 14px;
  font-size: 1.2rem;
}

/* As ações em até duas linhas: ir e voltar no vídeo; o cartão. */
.acoes-da-legenda {
  display: grid;
  gap: 6px;
  min-height: 36px;
  margin-top: 8px;
}

.linha {
  display: flex;
  gap: 6px;
  justify-content: center;
}

.acoes-da-legenda .botao {
  flex: 0 1 auto;
  justify-content: center;
  min-width: 0;
  min-height: 36px;
  padding: 0 10px;
  font-size: 0.82rem;
}

.acoes-da-legenda .fechar {
  width: 36px;
  padding: 0;
}

.dica {
  margin: 0;
  color: var(--texto-fraco);
  font-size: 0.8rem;
  line-height: 36px;
}
</style>
