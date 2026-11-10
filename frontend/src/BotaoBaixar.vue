<script setup>
import { inject, onMounted, ref } from 'vue'
import Icone from './Icone.vue'
import { api } from './api.js'

// Baixar um filme ou episódio da assinatura para o servidor (S28), nas páginas da assinatura que
// não têm título no TMDB. Mostra como está o download, se já houver.
const props = defineProps({ chave: { type: String, required: true } })
const recado = inject('recado')
const pedido = ref(null)
const pedindo = ref(false)

const ESTADOS = { 'na fila': 'Na fila para baixar', baixando: 'Baixando…', esperando: 'Esperando a assinatura ficar livre', baixado: 'Baixado no servidor', erro: 'Erro ao baixar' }

onMounted(async () => {
  try {
    pedido.value = (await api('/api/assinatura/downloads')).find((d) => d.chave === props.chave) || null
  } catch {
    // Sem a lista, o botão continua.
  }
})

async function baixar() {
  pedindo.value = true
  try {
    pedido.value = await api(`/api/assinatura/downloads/${props.chave}`, { metodo: 'POST' })
    recado('Vai baixar quando a assinatura estiver livre. Acompanhe em Downloads.')
  } catch (e) {
    recado(e.message, true)
  } finally {
    pedindo.value = false
  }
}
</script>

<template>
  <span v-if="pedido" class="chip" :class="{ ok: pedido.estado === 'baixado' }">{{ ESTADOS[pedido.estado] || pedido.estado }}</span>
  <button v-else class="botao" :disabled="pedindo" @click="baixar"><Icone nome="torrents" />{{ pedindo ? 'Pedindo…' : 'Baixar para o servidor' }}</button>
</template>
