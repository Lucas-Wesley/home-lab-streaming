<script setup>
import { onBeforeUnmount, onMounted, ref } from 'vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { comandar } from './telas.js'

// Enquanto uma tela toca algo mandado pelo app, uma barra de vidro acima das abas leva ao controle.
const sessoes = ref([])
const tocando = ref({}) // id da tela → está tocando?

let relogio
async function ler() {
  if (document.visibilityState !== 'visible') return
  try {
    sessoes.value = await api('/api/telas/sessoes', { tempo: 5000 })
    for (const s of sessoes.value) {
      api(`/api/telas/${encodeURIComponent(s.tela.id)}/estado`, { tempo: 5000 })
        .then((e) => (tocando.value[s.tela.id] = e.estado === 'PLAYING'))
        .catch(() => {})
    }
  } catch {
    // Sem rede: a barra fica como estava.
  }
}

async function alternar(s) {
  const id = s.tela.id
  const estava = tocando.value[id]
  tocando.value[id] = !estava
  try {
    await comandar(id, estava ? 'pausar' : 'continuar')
  } catch {
    tocando.value[id] = estava
  }
}

onMounted(() => {
  ler()
  relogio = setInterval(ler, 5000)
  document.addEventListener('visibilitychange', ler)
  window.addEventListener('hashchange', ler)
})

onBeforeUnmount(() => {
  clearInterval(relogio)
  document.removeEventListener('visibilitychange', ler)
  window.removeEventListener('hashchange', ler)
})
</script>

<template>
  <div v-if="sessoes.length" class="barras">
    <div v-for="s in sessoes" :key="s.tela.id" class="barra vidro">
      <a class="abrir" :href="`#/controle/${encodeURIComponent(s.tela.id)}`">
        <img v-if="s.sessao.capa" :src="s.sessao.capa" alt="" referrerpolicy="no-referrer" />
        <span class="texto">
          <span class="titulo">{{ s.sessao.titulo }}</span>
          <span class="onde"><Icone nome="tela" />{{ s.tela.nome }}</span>
        </span>
      </a>
      <button class="botao icone" :aria-label="tocando[s.tela.id] ? 'Pausar' : 'Continuar'" @click="alternar(s)">
        <Icone :nome="tocando[s.tela.id] ? 'pausar' : 'tocar'" />
      </button>
    </div>
  </div>
</template>

<style scoped>
.barras {
  position: fixed;
  right: 20px;
  bottom: 20px;
  z-index: 30;
  display: grid;
  gap: 8px;
  width: min(360px, calc(100% - 32px));
}

.barra {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 8px 8px 10px;
  border: 1px solid var(--vidro-borda);
  border-radius: var(--raio);
  box-shadow: var(--sombra);
}

.abrir {
  display: flex;
  flex: 1;
  align-items: center;
  gap: 10px;
  min-width: 0;
  color: inherit;
  text-decoration: none;
}

.abrir img {
  width: 30px;
  height: 44px;
  border-radius: 4px;
  object-fit: cover;
}

.texto {
  display: grid;
  min-width: 0;
}

.titulo {
  overflow: hidden;
  font-size: 0.88rem;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.onde {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: var(--marca);
  font-size: 0.75rem;
  font-weight: 550;
}

.onde svg {
  width: 13px;
  height: 13px;
}

.botao.icone svg {
  width: 18px;
  height: 18px;
}

@media (max-width: 820px) {
  .barras {
    left: 8px;
    right: 8px;
    bottom: calc(70px + env(safe-area-inset-bottom));
    width: auto;
  }
}
</style>
