<script setup>
import { inject, ref, watch } from 'vue'
import Icone from './Icone.vue'
import { salvarBusca } from './buscasSalvas.js'

// Salvar busca (S44): discreto, no pé dos resultados (achando ou não), para voltar a ela depois.
const props = defineProps({
  texto: { type: String, required: true },
  onde: { type: String, required: true }, // inicio, torrents ou titulo
  endereco: { type: String, required: true },
})
const recado = inject('recado')
const salva = ref(false)
const salvando = ref(false)

// Outra busca: o botão volta a valer.
watch(() => props.endereco, () => (salva.value = false))

async function salvar() {
  salvando.value = true
  try {
    await salvarBusca(props)
    salva.value = true
    recado('Busca salva. Ela fica no Início, em “Buscas salvas”.')
  } catch (e) {
    recado(e.message, true)
  } finally {
    salvando.value = false
  }
}
</script>

<template>
  <button class="salvar-busca" type="button" :disabled="salva || salvando" @click="salvar">
    <Icone :nome="salva ? 'salvo-cheio' : 'salvo'" />{{ salva ? 'Busca salva' : 'Salvar busca' }}
  </button>
</template>

<style scoped>
.salvar-busca {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-height: 36px;
  margin: 14px 0 4px;
  padding: 0;
  border: 0;
  background: none;
  color: var(--texto-suave);
  font: inherit;
  font-size: 0.86rem;
  cursor: pointer;
}

.salvar-busca svg {
  width: 17px;
  height: 17px;
}

.salvar-busca:hover:not(:disabled) {
  color: var(--marca);
}

.salvar-busca:disabled {
  cursor: default;
}

.salvar-busca:disabled svg {
  color: var(--marca);
}
</style>
