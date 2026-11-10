<script setup>
import { computed, inject, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from './api.js'
import { enderecoDaLegenda, falaAtual, lerVTT } from './legendas.js'

// A legenda passando no celular enquanto o vídeo toca na tela (S31). A fala atual fica no meio, em
// destaque; as de antes continuam ali para dar tempo de tocar. Tocar numa fala a escolhe (tocar na
// vizinha junta as duas, para a frase cortada ao meio) e "Mandar para o Flashcards" vira cartão no
// baralho Inglês, traduzido pelo Claude. Rolar com o dedo solta a legenda até "Voltar ao ponto".
const props = defineProps({
  chave: { type: String, required: true },
  posicao: { type: Number, required: true }, // segundos, já contando o tempo desde a última leitura
  versao: { type: Number, default: 0 }, // muda quando a legenda é trocada ou ajustada no controle
})
const recado = inject('recado')
const emit = defineEmits(['falas'])

const legenda = ref(null)
const falas = ref([])
const escolhidas = ref([]) // índices seguidos
const mandando = ref(false)
const flashcards = ref(false)
const seguindo = ref(true)
const lista = ref(null)

const atual = computed(() => falaAtual(falas.value, props.posicao))
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

function centralizar(suave = true) {
  const el = lista.value?.querySelector(`[data-i="${Math.max(0, atual.value)}"]`)
  if (!el || !lista.value) return
  lista.value.scrollTo({ top: el.offsetTop - lista.value.clientHeight / 2 + el.clientHeight / 2, behavior: suave ? 'smooth' : 'auto' })
}

watch(atual, () => seguindo.value && nextTick(centralizar))
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
  centralizar()
}

function tocar(i) {
  seguindo.value = false // a fala escolhida fica onde está, até "Voltar ao ponto"
  const e = escolhidas.value
  if (e.includes(i)) escolhidas.value = e.length > 1 && (i === e[0] || i === e[e.length - 1]) ? e.filter((x) => x !== i) : []
  else if (e.length && (i === e[0] - 1 || i === e[e.length - 1] + 1) && e.length < 4) escolhidas.value = [...e, i].sort((a, b) => a - b)
  else escolhidas.value = [i]
}

async function mandar() {
  const e = escolhidas.value
  if (!e.length) return
  mandando.value = true
  try {
    const f = falas.value
    const c = await api('/api/flashcards', {
      metodo: 'POST',
      corpo: {
        chave: props.chave,
        falas: e.map((i) => f[i].texto),
        antes: f.slice(Math.max(0, e[0] - 4), e[0]).map((x) => x.texto),
        depois: f.slice(e[e.length - 1] + 1, e[e.length - 1] + 3).map((x) => x.texto),
      },
      tempo: 100000,
    })
    escolhidas.value = []
    recado(`No Flashcards: “${c.verso.split('\n')[0]}”`, false, { rotulo: 'Desfazer', fazer: () => desfazer(c.id) })
  } catch (err) {
    recado(err.message, true)
  } finally {
    mandando.value = false
  }
}

async function desfazer(id) {
  try {
    await api(`/api/flashcards/${id}`, { metodo: 'DELETE' })
    recado('Cartão tirado do Flashcards.')
  } catch (err) {
    recado(err.message, true)
  }
}

// A legenda gerada cresce, e a escolhida pode mudar em outro aparelho: confere de tempos em tempos.
let relogio
onMounted(async () => {
  carregar()
  relogio = setInterval(carregar, 20000)
  try {
    flashcards.value = (await api('/api/flashcards')).disponivel
  } catch {
    flashcards.value = false
  }
})
onBeforeUnmount(() => clearInterval(relogio))
</script>

<template>
  <section v-if="falas.length" class="legenda-ao-vivo">
    <ol ref="lista" class="falas" @touchstart.passive="soltou" @wheel.passive="soltou">
      <li
        v-for="(f, i) in falas"
        :key="i"
        :data-i="i"
        class="fala"
        :class="{ atual: i === atual, passada: i < atual, escolhida: escolhidas.includes(i) }"
        @click="tocar(i)"
      >
        {{ f.texto }}
      </li>
    </ol>
    <div class="acoes-da-legenda">
      <button v-if="!seguindo" class="botao" @click="voltarAoPonto">Voltar ao ponto</button>
      <template v-if="escolhidas.length">
        <button v-if="podeMandar" class="botao principal" :disabled="mandando" @click="mandar">
          {{ mandando ? 'Traduzindo…' : escolhidas.length > 1 ? `Mandar as ${escolhidas.length} falas para o Flashcards` : 'Mandar para o Flashcards' }}
        </button>
        <button class="botao" :disabled="mandando" @click="escolhidas = []">Cancelar</button>
      </template>
      <p v-else-if="podeMandar && seguindo" class="dica">Toque numa fala para mandar ao Flashcards.</p>
    </div>
  </section>
</template>

<style scoped>
.legenda-ao-vivo {
  width: 100%;
  margin: 4px 0 6px;
}

.falas {
  height: 210px;
  margin: 0;
  padding: 80px 4px;
  overflow-y: auto;
  list-style: none;
  border-radius: var(--raio);
  background: var(--superficie);
  mask-image: linear-gradient(transparent, #000 22%, #000 78%, transparent);
  scrollbar-width: none;
}

.fala {
  padding: 6px 10px;
  border-radius: var(--raio-p);
  color: var(--texto-fraco);
  font-size: 0.95rem;
  line-height: 1.4;
  white-space: pre-line;
  cursor: pointer;
  transition: color 0.2s ease, background 0.2s ease;
}

.fala.passada {
  color: var(--texto-suave);
}

.fala.atual {
  color: var(--texto);
  font-size: 1.05rem;
  font-weight: 600;
}

.fala.escolhida {
  background: var(--marca-fundo);
  color: var(--marca);
}

.acoes-da-legenda {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  justify-content: center;
  min-height: 40px;
  margin-top: 8px;
}

.dica {
  margin: 0;
  color: var(--texto-fraco);
  font-size: 0.8rem;
  line-height: 40px;
}
</style>
