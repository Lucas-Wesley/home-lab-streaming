<script setup>
// Andamento da busca nos indexadores (S36): uma barra fina com a porcentagem. Antes da primeira
// opção chegar, fica no lugar da lista; depois, discreta embaixo dela.
defineProps({ porcento: { type: Number, required: true }, primeira: { type: Boolean, default: false } })
</script>

<template>
  <div class="andamento" :class="{ primeira }" role="progressbar" aria-label="Procurando nos indexadores" :aria-valuenow="porcento" aria-valuemin="0" aria-valuemax="100">
    <p>
      <span>{{ primeira ? 'Procurando nos indexadores…' : 'Procurando mais opções…' }}</span>
      <span class="numero">{{ porcento }}%</span>
    </p>
    <span class="trilho"><span class="barra" :style="{ width: `${Math.max(porcento, 4)}%` }"></span></span>
  </div>
</template>

<style scoped>
.andamento {
  display: grid;
  gap: 6px;
  margin: 10px 0 4px;
  color: var(--texto-fraco);
  font-size: 0.8rem;
}

.andamento.primeira {
  max-width: 320px;
  margin: 28px auto;
  font-size: 0.9rem;
}

p {
  display: flex;
  justify-content: space-between;
  margin: 0;
}

.numero {
  font-variant-numeric: tabular-nums;
}

.trilho {
  height: 3px;
  overflow: hidden;
  border-radius: 999px;
  background: var(--linha);
}

.barra {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: var(--marca);
  transition: width 0.3s ease;
}

@media (prefers-reduced-motion: reduce) {
  .barra {
    transition: none;
  }
}
</style>
