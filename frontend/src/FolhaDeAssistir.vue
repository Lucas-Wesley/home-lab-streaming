<script setup>
import { computed, onMounted, ref } from 'vue'
import Assistir from './Assistir.vue'
import Folha from './Folha.vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { resumoDosFormatos } from './formatos.js'

// A folha de assistir de uma opção da página do título (S28), igual para a assinatura e para o
// torrent: de onde vem, o idioma de cada áudio e o vídeo (lidos pelo servidor na hora em que a
// folha abre, se ainda não se sabe), e os botões de assistir aqui ou numa tela.
const props = defineProps({
  titulo: { type: String, required: true },
  origem: { type: String, required: true }, // assinatura ou torrent
  nome: { type: String, default: '' }, // o nome no catálogo ou o do lançamento
  chave: { type: String, required: true },
  capa: { type: String, default: '' },
  posicao: { type: Number, default: 0 },
  formatos: { type: Object, default: null },
})
defineEmits(['fechar'])

const lidos = ref(props.formatos)
const lendo = ref(!props.formatos)
const erro = ref('')
const resumo = computed(() => resumoDosFormatos(lidos.value))

onMounted(async () => {
  if (lidos.value) return
  try {
    const r = await api(`/api/formatos?chave=${encodeURIComponent(props.chave)}`, { tempo: 70000 })
    lidos.value = r.formatos || null
    erro.value = r.erro || ''
  } catch (e) {
    erro.value = e.message
  } finally {
    lendo.value = false
  }
})
</script>

<template>
  <Folha :titulo="titulo" @fechar="$emit('fechar')">
    <p class="de-onde">
      <span v-if="origem === 'assinatura'" class="origem assinatura"><Icone nome="canais" />Assinatura</span>
      <span v-else class="origem torrent"><span class="t">T</span>Torrent</span>
      <span v-if="nome" class="nome">{{ nome }}</span>
    </p>

    <div class="ficha">
      <p v-if="lendo" class="suave">Lendo o áudio e o vídeo… (pode levar alguns segundos)</p>
      <template v-else-if="resumo">
        <p class="rotulo-pequeno">Áudio</p>
        <ul v-if="resumo.audios.length" class="audios">
          <li v-for="(a, i) in resumo.audios" :key="i">
            <strong>{{ a.idioma }}</strong><span class="suave">{{ a.detalhe }}<template v-if="a.ouvido"> · reconhecido pelo som</template></span>
          </li>
        </ul>
        <p v-else class="suave">Sem áudio.</p>
        <p class="outros">
          <span v-if="resumo.video">{{ resumo.video }}</span>
          <span v-if="resumo.duracao">{{ resumo.duracao }}</span>
          <span v-if="resumo.legendas.length">Legendas no arquivo: {{ resumo.legendas.join(', ') }}</span>
        </p>
      </template>
      <p v-else-if="erro" class="suave">{{ erro }}</p>
    </div>

    <Assistir :chave="chave" :titulo="titulo" :capa="capa" :posicao="posicao" :formatos="lidos" @escolheu="$emit('fechar')" />
    <slot />
  </Folha>
</template>

<style scoped>
.de-onde {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 10px;
  align-items: center;
  margin: -4px 0 14px;
}

.origem {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  padding: 3px 9px 3px 5px;
  border-radius: 999px;
  font-size: 0.8rem;
  font-weight: 600;
}

.origem svg {
  width: 15px;
  height: 15px;
}

.origem.assinatura {
  background: var(--marca-fundo);
  color: var(--marca);
}

.origem.torrent {
  background: color-mix(in srgb, var(--verde) 16%, transparent);
  color: var(--verde);
}

.t {
  display: grid;
  place-items: center;
  width: 16px;
  height: 16px;
  border-radius: 4px;
  background: var(--verde);
  color: #fff;
  font-size: 0.68rem;
}

.nome {
  overflow-wrap: anywhere;
  color: var(--texto-suave);
  font-size: 0.8rem;
}

.ficha {
  margin-bottom: 16px;
  padding: 12px 14px;
  border-radius: var(--raio);
  background: var(--superficie);
}

.ficha p {
  margin: 0;
}

.rotulo-pequeno {
  margin-bottom: 6px !important;
  color: var(--texto-suave);
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.audios {
  display: grid;
  gap: 4px;
  margin: 0 0 10px;
  padding: 0;
  list-style: none;
}

.audios li {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 10px;
  align-items: baseline;
  font-size: 0.95rem;
}

.audios .suave {
  font-size: 0.8rem;
}

.outros {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  color: var(--texto-suave);
  font-size: 0.82rem;
}
</style>
