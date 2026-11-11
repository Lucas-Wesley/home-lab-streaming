<script setup>
import { onMounted, ref } from 'vue'
import Icone from './Icone.vue'
import { api } from './api.js'

// Títulos mandados para verificação (S37), pelo menu "⋯" da página do título. O Claude Code lê pela
// API (/api/relatos), investiga e deixa uma resposta aqui.
const relatos = ref([])
const erro = ref('')
const carregado = ref(false)

const NOMES = { nova: 'Para verificar', feita: 'Verificado', descartada: 'Descartado' }

async function carregar() {
  try {
    relatos.value = await api('/api/relatos')
  } catch (e) {
    erro.value = e.status === 0 ? 'Sem conexão: a lista só abre na rede de casa.' : e.message
  } finally {
    carregado.value = true
  }
}

async function apagar(r) {
  if (!confirm(`Tirar ${r.titulo} da lista?`)) return
  try {
    await api(`/api/relatos/${r.id}`, { metodo: 'DELETE' })
    await carregar()
  } catch (e) {
    erro.value = e.message
  }
}

const data = (iso) => new Date(iso).toLocaleDateString('pt-BR', { day: '2-digit', month: 'short' })
const episodio = (r) => (r.episodio ? ` · T${r.temporada} E${r.episodio}` : '')

onMounted(carregar)
</script>

<template>
  <section>
    <div v-if="erro" class="aviso erro">{{ erro }}</div>
    <p v-else-if="carregado && !relatos.length" class="suave vazio">Nenhum título mandado para verificação.</p>
    <ul v-if="relatos.length" class="lista">
      <li v-for="r in relatos" :key="r.id" class="relato" :class="r.estado">
        <div>
          <a class="nome" :href="`#/titulo/${r.tipo}/${r.tmdb}`">{{ r.titulo }}{{ r.ano ? ` (${r.ano})` : '' }}{{ episodio(r) }}</a>
          <p v-if="r.texto" class="texto-relato">{{ r.texto }}</p>
          <p v-if="r.resposta" class="resposta">{{ r.resposta }}</p>
          <p class="meta">
            <span class="chip" :class="{ ok: r.estado === 'feita' }">{{ NOMES[r.estado] || r.estado }}</span>
            {{ data(r.criada_em) }}
          </p>
        </div>
        <button class="botao icone" aria-label="Tirar da lista" title="Tirar da lista" @click="apagar(r)">
          <Icone nome="fechar" />
        </button>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.aviso {
  margin-bottom: 14px;
}

.vazio {
  font-size: 0.9rem;
}

.relato {
  display: grid;
  grid-template-columns: 1fr auto;
  align-items: start;
  gap: 8px;
  padding: 14px 0;
}

.relato p {
  margin: 0;
}

.nome {
  color: var(--texto);
  font-weight: 600;
  text-decoration: none;
}

.relato.feita .nome,
.relato.descartada .nome {
  color: var(--texto-suave);
}

.texto-relato {
  margin-top: 4px !important;
  line-height: 1.5;
  white-space: pre-wrap;
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
