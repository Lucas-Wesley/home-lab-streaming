<script setup>
import { inject, onMounted, ref } from 'vue'
import Folha from './Folha.vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { carregarPastas, criarPasta, desfavoritar, favoritar, pastas } from './favoritos.js'

// A folha do coração (S50): escolher a pasta ao favoritar, mudar a pasta de um favorito e tirar
// dos favoritos. Criar uma pasta aqui já guarda o favorito nela.
const props = defineProps({
  tipo: { type: String, required: true },
  id: { type: [String, Number], required: true },
})
const emit = defineEmits(['fechar'])
const recado = inject('recado')

const marcado = ref(false)
const pastaAtual = ref(0)
const novaPasta = ref('')
const ocupado = ref(false)

function nomeDaPasta(id) {
  return pastas.value.find((p) => p.id === id)?.nome || ''
}

async function escolher(pasta) {
  ocupado.value = true
  try {
    await favoritar(props.tipo, props.id, pasta)
    recado(marcado.value ? (pasta ? `Movido para ${nomeDaPasta(pasta)}.` : 'Fora da pasta, segue nos favoritos.') : 'Nos favoritos.')
    emit('fechar')
  } catch (e) {
    recado(e.message, true)
  } finally {
    ocupado.value = false
  }
}

async function criar() {
  const nome = novaPasta.value.trim()
  if (!nome) return
  ocupado.value = true
  try {
    const p = await criarPasta(nome)
    await favoritar(props.tipo, props.id, p.id)
    recado(`Nos favoritos, na pasta ${p.nome}.`)
    emit('fechar')
  } catch (e) {
    recado(e.message, true)
  } finally {
    ocupado.value = false
  }
}

async function tirar() {
  ocupado.value = true
  try {
    await desfavoritar(props.tipo, props.id)
    recado('Fora dos favoritos.')
    emit('fechar')
  } catch (e) {
    recado(e.message, true)
  } finally {
    ocupado.value = false
  }
}

onMounted(async () => {
  carregarPastas()
  try {
    const estado = await api(`/api/favoritos/${props.tipo}/${props.id}`)
    marcado.value = estado.marcado
    pastaAtual.value = estado.pasta
  } catch {
    // sem o estado, a folha abre mesmo assim (o coração já diz se é favorito)
  }
})
</script>

<template>
  <Folha :titulo="marcado ? 'Favorito' : 'Favoritar'" @fechar="emit('fechar')">
    <p class="dica">{{ marcado ? 'Mudar de pasta:' : 'Guardar em qual pasta?' }}</p>
    <ul class="opcoes">
      <li v-for="p in pastas" :key="p.id">
        <button class="opcao" :disabled="ocupado" @click="escolher(p.id)">
          <Icone nome="pasta" />
          <span class="nome">{{ p.nome }}</span>
          <Icone v-if="marcado && pastaAtual === p.id" nome="confirmado" class="atual" />
        </button>
      </li>
      <li>
        <button class="opcao" :disabled="ocupado" @click="escolher(0)">
          <Icone nome="favorito" />
          <span class="nome">Sem pasta</span>
          <Icone v-if="marcado && pastaAtual === 0" nome="confirmado" class="atual" />
        </button>
      </li>
    </ul>

    <form class="nova" @submit.prevent="criar">
      <input v-model="novaPasta" class="campo" type="text" maxlength="60" placeholder="Nova pasta…" aria-label="Nome da nova pasta" :disabled="ocupado" />
      <button class="botao" type="submit" :disabled="ocupado || !novaPasta.trim()">Criar</button>
    </form>

    <button v-if="marcado" class="botao tirar" :disabled="ocupado" @click="tirar"><Icone nome="lixeira" />Tirar dos favoritos</button>
  </Folha>
</template>

<style scoped>
.dica {
  margin: 0 0 4px;
  color: var(--texto-suave);
  font-size: 0.85rem;
}

.opcoes {
  margin: 0;
  padding: 0;
  list-style: none;
}

.opcao {
  display: flex;
  gap: 12px;
  align-items: center;
  width: 100%;
  padding: 12px 8px;
  border: 0;
  border-bottom: 1px solid var(--linha);
  border-radius: 0;
  background: none;
  color: inherit;
  text-align: left;
  cursor: pointer;
}

.opcao svg {
  flex: none;
  width: 18px;
  height: 18px;
  color: var(--texto-suave);
}

.opcao svg.atual {
  color: var(--marca);
}

.nome {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-size: 0.95rem;
  font-weight: 550;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.opcao:hover .nome {
  color: var(--marca);
}

.nova {
  display: flex;
  gap: 8px;
  margin-top: 14px;
}

.nova .campo {
  flex: 1;
  min-width: 0;
}

.tirar {
  margin-top: 14px;
  color: var(--vermelho);
}

.tirar svg {
  width: 16px;
  height: 16px;
}
</style>
