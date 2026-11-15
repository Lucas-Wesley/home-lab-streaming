<script setup>
import { computed, inject, onMounted, ref } from 'vue'
import Icone from './Icone.vue'
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
const naoToca = (t) => semAs4K.value.includes(t)

function aqui(posicao) {
  emit('escolheu')
  location.hash = rotaDoPlayer(props.chave, { titulo: props.titulo, posicao })
}

async function naTela(tela, posicao) {
  enviando.value = tela.id
  try {
    await tocarNaTela(tela, props.chave, { posicao, capa: props.capa })
    emit('escolheu')
    location.hash = `#/controle/${encodeURIComponent(tela.id)}`
  } catch (e) {
    recado(e.message, true)
    carregarTelas(true)
  } finally {
    enviando.value = ''
  }
}

onMounted(() => carregarTelas())
</script>

<template>
  <div class="assistir">
    <div class="linha-acoes">
      <button class="botao principal" @click="aqui(continuar ? posicao : 0)">
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
        :disabled="!!enviando || naoToca(t)"
        :title="naoToca(t) ? 'Esta tela não toca 4K' : t.ligada ? '' : 'Não respondeu agora: está ligada?'"
        @click="naTela(t, continuar ? posicao : 0)"
      >
        <Icone nome="tela" />
        {{ enviando === t.id ? 'Mandando…' : t.nome }}
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
</style>
