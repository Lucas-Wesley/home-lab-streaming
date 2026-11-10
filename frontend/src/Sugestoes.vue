<script setup>
import { onMounted, ref } from 'vue'
import Icone from './Icone.vue'
import { api } from './api.js'

// Sugestões de melhoria do app. O Lucas anota aqui; o Claude Code lê pela API (/api/sugestoes),
// faz a mudança e deixa uma resposta. A tela e a API são iguais em todas as apps do homelab.
const props = defineProps({ tela: { type: String, default: '' } })

const sugestoes = ref([])
const texto = ref('')
const enviando = ref(false)
const erro = ref('')

const NOMES = { nova: 'Nova', feita: 'Feita', descartada: 'Descartada' }

async function carregar() {
  try {
    sugestoes.value = await api('/api/sugestoes')
  } catch (e) {
    erro.value = e.status === 0 ? 'Sem conexão: as sugestões só abrem na rede de casa.' : e.message
  }
}

async function enviar() {
  if (!texto.value.trim()) return
  enviando.value = true
  erro.value = ''
  try {
    await api(`/api/sugestoes/${crypto.randomUUID()}`, { metodo: 'PUT', corpo: { texto: texto.value, tela: props.tela } })
    texto.value = ''
    await carregar()
  } catch (e) {
    // O texto fica no campo para tentar de novo.
    erro.value = e.message
  } finally {
    enviando.value = false
  }
}

async function apagar(s) {
  if (!confirm('Apagar esta sugestão?')) return
  try {
    await api(`/api/sugestoes/${s.id}`, { metodo: 'DELETE' })
    await carregar()
  } catch (e) {
    erro.value = e.message
  }
}

const data = (iso) => new Date(iso).toLocaleDateString('pt-BR', { day: '2-digit', month: 'short' })

onMounted(carregar)
</script>

<template>
  <section class="sugestoes">
    <form class="nova" @submit.prevent="enviar">
      <textarea v-model="texto" class="campo" rows="3" placeholder="O que pode melhorar no app?" aria-label="Sugestão de melhoria" />
      <div class="linha-acoes">
        <button class="botao principal" :disabled="enviando || !texto.trim()">{{ enviando ? 'Enviando…' : 'Enviar sugestão' }}</button>
      </div>
    </form>
    <div v-if="erro" class="aviso erro">{{ erro }}</div>

    <ul v-if="sugestoes.length" class="lista">
      <li v-for="s in sugestoes" :key="s.id" class="sugestao" :class="s.estado">
        <div>
          <p class="texto-sugestao">{{ s.texto }}</p>
          <p v-if="s.resposta" class="resposta">{{ s.resposta }}</p>
          <p class="meta">
            <span class="chip" :class="{ ok: s.estado === 'feita' }">{{ NOMES[s.estado] || s.estado }}</span>
            {{ data(s.criada_em) }}
          </p>
        </div>
        <button class="botao icone" aria-label="Apagar sugestão" title="Apagar" @click="apagar(s)">
          <Icone nome="fechar" />
        </button>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.nova {
  display: grid;
  gap: 8px;
  margin-bottom: 16px;
}

.nova textarea {
  resize: vertical;
  line-height: 1.5;
}

.aviso {
  margin-bottom: 14px;
}

.sugestao {
  display: grid;
  grid-template-columns: 1fr auto;
  align-items: start;
  gap: 8px;
  padding: 14px 0;
}

.sugestao p {
  margin: 0;
}

.texto-sugestao {
  line-height: 1.5;
  white-space: pre-wrap;
}

.sugestao.feita .texto-sugestao,
.sugestao.descartada .texto-sugestao {
  color: var(--texto-suave);
}

.resposta {
  margin-top: 6px !important;
  padding-left: 10px;
  border-left: 2px solid var(--azul);
  color: var(--texto-suave);
  font-size: 0.88rem;
  line-height: 1.45;
}

.meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 6px !important;
  color: var(--texto-fraco);
  font-size: 0.78rem;
}
</style>
