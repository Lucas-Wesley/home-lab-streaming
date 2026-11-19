<script setup>
import { computed, onMounted, ref } from 'vue'
import Icone from './Icone.vue'
import FolhaDePastas from './FolhaDePastas.vue'
import { carregarFavoritos, favoritos } from './favoritos.js'

const props = defineProps({
  tipo: { type: String, required: true },
  id: { type: [String, Number], required: true },
})
const marcado = computed(() => favoritos.value.has(`${props.tipo}:${props.id}`))
const escolhendo = ref(false)

onMounted(carregarFavoritos)
</script>

<template>
  <button
    class="botao favorito"
    :class="{ marcado }"
    :aria-pressed="marcado"
    :aria-label="marcado ? 'Tirar dos favoritos ou mudar de pasta' : 'Favoritar'"
    @click="escolhendo = true"
  >
    <Icone :nome="marcado ? 'favorito-cheio' : 'favorito'" />{{ marcado ? 'Favorito' : 'Favoritar' }}
  </button>
  <FolhaDePastas v-if="escolhendo" :tipo="tipo" :id="id" @fechar="escolhendo = false" />
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
