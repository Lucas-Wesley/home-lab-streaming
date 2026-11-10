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
        :disabled="!!enviando"
        :title="t.ligada ? '' : 'Não respondeu agora: está ligada?'"
        @click="naTela(t, continuar ? posicao : 0)"
      >
        <Icone nome="tela" />
        {{ enviando === t.id ? 'Mandando…' : t.nome }}
        <span v-if="!t.ligada" class="chip">desligada?</span>
      </button>
    </div>
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
