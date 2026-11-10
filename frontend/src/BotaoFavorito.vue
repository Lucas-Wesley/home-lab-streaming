<script setup>
import { computed, inject, onMounted } from 'vue'
import Icone from './Icone.vue'
import { alternarFavorito, carregarFavoritos, favoritos } from './favoritos.js'

const props = defineProps({
  tipo: { type: String, required: true },
  id: { type: [String, Number], required: true },
})
const recado = inject('recado')
const marcado = computed(() => favoritos.value.has(`${props.tipo}:${props.id}`))

async function alternar() {
  try {
    const agora = await alternarFavorito(props.tipo, props.id)
    recado(agora ? 'Nos favoritos.' : 'Fora dos favoritos.')
  } catch (e) {
    recado(e.message, true)
  }
}

onMounted(carregarFavoritos)
</script>

<template>
  <button
    class="botao favorito"
    :class="{ marcado }"
    :aria-pressed="marcado"
    :aria-label="marcado ? 'Tirar dos favoritos' : 'Favoritar'"
    @click="alternar"
  >
    <Icone :nome="marcado ? 'favorito-cheio' : 'favorito'" />{{ marcado ? 'Favorito' : 'Favoritar' }}
  </button>
</template>

<style scoped>
.favorito svg {
  width: 17px;
  height: 17px;
}

.favorito.marcado {
  color: var(--marca);
}
</style>
