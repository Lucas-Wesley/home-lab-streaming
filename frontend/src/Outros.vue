<script setup>
import Icone from './Icone.vue'

// "Outros" (barra do celular): as seções que não cabem nas três abas do pé da tela.
defineProps({ itens: { type: Array, required: true } })
</script>

<template>
  <div class="pagina">
    <h1>Outros</h1>
    <nav class="lista-de-ajustes" aria-label="Outras seções">
      <a v-for="item in itens" :key="item.rota" :href="`#/${item.rota}`">
        <span class="icone"><Icone :nome="item.icone" /></span>
        <span>
          <span class="nome">{{ item.nome }}</span>
          <span class="descricao">{{ item.descricao }}</span>
        </span>
        <Icone nome="avancar" class="seta" />
      </a>
    </nav>
  </div>
</template>

<style scoped>
.icone {
  display: grid;
  flex-shrink: 0;
  place-items: center;
  width: 38px;
  height: 38px;
  border-radius: var(--raio-p);
  background: var(--superficie);
  color: var(--marca);
}

.icone svg {
  width: 20px;
  height: 20px;
}
</style>
