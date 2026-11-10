<script setup>
import { onBeforeUnmount, onMounted } from 'vue'
import Icone from './Icone.vue'

// Folha de ações: sobe do pé da tela, com véu de blur leve (design system).
defineProps({ titulo: { type: String, default: '' }, larga: { type: Boolean, default: false } })
const emit = defineEmits(['fechar'])

function aoTeclar(e) {
  if (e.key === 'Escape') emit('fechar')
}
onMounted(() => window.addEventListener('keydown', aoTeclar))
onBeforeUnmount(() => window.removeEventListener('keydown', aoTeclar))
</script>

<template>
  <Teleport to="body">
    <div class="veu" @click.self="emit('fechar')">
      <section class="folha vidro" :class="{ larga }" role="dialog" :aria-label="titulo">
        <header>
          <h2>{{ titulo }}</h2>
          <button class="botao icone" aria-label="Fechar" @click="emit('fechar')"><Icone nome="fechar" /></button>
        </header>
        <slot />
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
.veu {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: flex;
  align-items: flex-end;
  justify-content: center;
  background: rgb(0 0 0 / 25%);
  -webkit-backdrop-filter: blur(4px);
  backdrop-filter: blur(4px);
}

.folha {
  width: min(560px, 100%);
  max-height: 85vh;
  overflow: auto;
  padding: 16px 20px calc(20px + env(safe-area-inset-bottom));
  border: 1px solid var(--vidro-borda);
  border-radius: var(--raio-g) var(--raio-g) 0 0;
  box-shadow: var(--sombra);
}

/* Lista de versões de torrent: os nomes são longos, a folha cresce no computador. */
.folha.larga {
  width: min(760px, 100%);
}

header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}

h2 {
  margin: 0;
  font-size: 1.05rem;
  font-weight: 650;
  line-height: 1.3;
}

@media (min-width: 821px) {
  .veu {
    align-items: center;
  }

  .folha {
    border-radius: var(--raio-g);
  }
}
</style>
