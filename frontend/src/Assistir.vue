<script setup>
import { computed, inject, onBeforeUnmount, onMounted, ref } from 'vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { GRAVANDO, gravadoAte, podeTocar, textoDaDupla } from './duplas.js'
import { noAparelho } from './formatos.js'
import { carregarTelas, telas, tocarNaTela } from './telas.js'
import { relogio, rotaDoPlayer } from './tempo.js'

// Os botões de assistir de um vídeo: aqui (no aparelho) ou numa das telas da casa.
const props = defineProps({
  chave: { type: String, required: true },
  titulo: { type: String, default: '' },
  capa: { type: String, default: '' },
  posicao: { type: Number, default: 0 },
  formatos: { type: Object, default: null }, // lidos pelo servidor: avisa antes do play se não toca aqui
})
const emit = defineEmits(['escolheu'])
const recado = inject('recado')
const enviando = ref('')

const continuar = computed(() => props.posicao > 30)
const aviso = computed(() => noAparelho(props.formatos))
// S43: o projetor (2K) para no 0 com vídeo 4K; o botão dele avisa antes de mandar.
const e4K = computed(() => (props.formatos?.largura || 0) > 2560 || (props.formatos?.altura || 0) > 1600)
const semAs4K = computed(() => (e4K.value ? telas.value.filter((t) => t.toca_4k === false) : []))
const naoToca = (t) => semAs4K.value.includes(t) && !duplaEscolhida.value // a dupla sai em 1080p

// Legenda dupla (S46), só em vídeo de torrent: inglês e português gravados na imagem. Pronta, toca
// em tudo; enquanto grava, só nas telas, a partir de um ponto já gravado. "Preparar e tocar" pede a
// gravação a partir do ponto e manda para a tela quando houver 1 min gravado à frente.
const temDupla = props.chave.startsWith('arquivo:')
const dupla = ref(null)
const duplaEscolhida = ref(false)
const esperando = ref(null) // { tela, posicao }: manda quando a gravação chegar lá
const pedindo = ref(false)
const textoDupla = computed(() => textoDaDupla(dupla.value))
const pronta = computed(() => dupla.value?.estado === 'pronta')
const gravando = computed(() => GRAVANDO.includes(dupla.value?.estado))
let relogioDaDupla

async function lerDupla() {
  try {
    dupla.value = await api(`/api/legenda-dupla/${props.chave}`)
  } catch {
    return
  }
  if (dupla.value?.estado === 'pronta' && !duplaEscolhida.value && dupla.value.tamanho) duplaEscolhida.value = true
  if (esperando.value) {
    if (dupla.value?.estado === 'erro') {
      recado(textoDaDupla(dupla.value), true)
      esperando.value = null
    } else if (podeTocar(dupla.value, esperando.value.posicao)) {
      const { tela, posicao } = esperando.value
      esperando.value = null
      naTela(tela, posicao)
    }
  }
}

async function pedirDupla(posicao, esperar = false) {
  pedindo.value = true
  try {
    dupla.value = await api(`/api/legenda-dupla/${props.chave}`, { metodo: 'POST', corpo: { comeco: posicao, esperar } })
    return true
  } catch (e) {
    recado(e.message, true)
    return false
  } finally {
    pedindo.value = false
  }
}

async function soPreparar() {
  if (await pedirDupla(continuar.value ? props.posicao : 0)) recado('Gravando a legenda dupla. Aviso no celular quando ficar pronta.')
}

function aqui(posicao) {
  emit('escolheu')
  location.hash = rotaDoPlayer(props.chave, { titulo: props.titulo, posicao, dupla: duplaEscolhida.value })
}

async function naTela(tela, posicao) {
  if (duplaEscolhida.value && !podeTocar(dupla.value, posicao)) {
    // Ainda não gravado neste ponto: pede (se preciso) e manda quando houver folga gravada.
    if (!gravando.value && !(await pedirDupla(posicao))) return
    esperando.value = { tela, posicao }
    return
  }
  enviando.value = tela.id
  try {
    await tocarNaTela(tela, props.chave, { posicao, capa: props.capa, dupla: duplaEscolhida.value })
    emit('escolheu')
    location.hash = `#/controle/${encodeURIComponent(tela.id)}`
  } catch (e) {
    recado(e.message, true)
    carregarTelas(true)
  } finally {
    enviando.value = ''
  }
}

// Quanto falta gravar até poder mandar à tela, para o recado de espera.
const faltaParaTocar = computed(() => {
  if (!esperando.value || !dupla.value) return ''
  const p = esperando.value.posicao
  return gravadoAte(dupla.value.faixas, p) > p ? 'Gravando à frente do ponto…' : 'Começando a gravar deste ponto…'
})

onMounted(() => {
  carregarTelas()
  if (temDupla) {
    lerDupla()
    relogioDaDupla = setInterval(() => (gravando.value || esperando.value) && lerDupla(), 3000)
  }
})
onBeforeUnmount(() => clearInterval(relogioDaDupla))
</script>

<template>
  <div class="assistir">
    <div v-if="temDupla" class="legenda-dupla">
      <div class="pilulas" role="radiogroup" aria-label="Legenda">
        <button class="pilula" :class="{ ativa: !duplaEscolhida }" role="radio" :aria-checked="!duplaEscolhida" @click="duplaEscolhida = false">Normal</button>
        <button class="pilula" :class="{ ativa: duplaEscolhida }" role="radio" :aria-checked="duplaEscolhida" @click="duplaEscolhida = true">Legenda dupla</button>
      </div>
      <template v-if="duplaEscolhida">
        <p class="suave nota">
          <template v-if="textoDupla">{{ textoDupla }}</template>
          <template v-else>Inglês e português gravados na imagem (TV e projetor). Começa em poucos minutos; o resto grava enquanto você assiste.</template>
        </p>
        <p v-if="esperando" class="nota esperando">
          {{ faltaParaTocar }} Vai para {{ esperando.tela.nome }} sozinho. <button class="limpar" type="button" @click="esperando = null">Cancelar</button>
        </p>
        <p v-if="!pronta && !gravando" class="nota">
          <button class="limpar" type="button" :disabled="pedindo" @click="soPreparar">Só preparar (aviso quando ficar pronta)</button>
        </p>
      </template>
    </div>
    <div class="linha-acoes">
      <button class="botao principal" :disabled="duplaEscolhida && !pronta" :title="duplaEscolhida && !pronta ? 'No aparelho, só depois de pronta' : ''" @click="aqui(continuar ? posicao : 0)">
        <Icone nome="tocar" />{{ continuar ? `Continuar de ${relogio(posicao)}` : 'Assistir' }}
      </button>
      <button v-if="continuar" class="botao" @click="aqui(0)">Do começo</button>
    </div>
    <p v-if="aviso" class="aviso-do-aparelho">
      <strong>{{ aviso.texto }}</strong>
      {{ aviso.semSom && !aviso.semImagem ? '(o áudio é Dolby ou DTS).' : '(formato que o navegador não toca).' }}
      <template v-if="telas.length">Na TV ou no projetor, toca.</template>
    </p>
    <div v-if="telas.length" class="linha-acoes telas">
      <button
        v-for="t in telas"
        :key="t.id"
        class="botao"
        :disabled="!!enviando || !!esperando || naoToca(t)"
        :title="naoToca(t) ? 'Esta tela não toca 4K' : t.ligada ? '' : 'Não respondeu agora: está ligada?'"
        @click="naTela(t, continuar ? posicao : 0)"
      >
        <Icone nome="tela" />
        {{ enviando === t.id ? 'Mandando…' : esperando?.tela.id === t.id ? 'Esperando…' : t.nome }}
        <span v-if="naoToca(t)" class="chip">não toca 4K</span>
        <span v-else-if="!t.ligada" class="chip">desligada?</span>
      </button>
    </div>
    <p v-if="semAs4K.length" class="aviso-do-aparelho">
      Esta cópia é 4K: {{ semAs4K.map((t) => t.nome).join(' e ') }} não {{ semAs4K.length > 1 ? 'tocam' : 'toca' }}. Para {{ semAs4K.length > 1 ? 'elas' : 'ela' }}, escolha outra cópia deste título.
    </p>
  </div>
</template>

<style scoped>
.assistir {
  display: grid;
  gap: 10px;
}

.botao svg {
  width: 17px;
  height: 17px;
}

.aviso-do-aparelho {
  margin: -2px 0 0;
  color: var(--vermelho);
  font-size: 0.84rem;
  line-height: 1.45;
}

.telas .chip {
  margin-left: 2px;
}

.legenda-dupla {
  display: grid;
  gap: 6px;
}

.pilulas {
  display: flex;
  gap: 4px;
}

.nota {
  margin: 0;
  font-size: 0.84rem;
  line-height: 1.45;
}

.esperando {
  color: var(--marca);
}

.limpar {
  padding: 0;
  border: 0;
  background: none;
  color: var(--marca);
  font: inherit;
  cursor: pointer;
}
</style>
