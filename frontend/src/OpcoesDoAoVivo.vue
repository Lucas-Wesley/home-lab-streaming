<script setup>
import { rotuloDaVersao } from './player.js'

// As saídas de um canal que trava (S51): as versões dele (da melhor para a pior), a qualidade do
// fluxo quando o provedor manda mais de uma, e os outros canais do mesmo grupo.
defineProps({
  alternativas: { type: Object, default: null }, // { nome, versoes, grupo, irmaos }
  atual: { type: Number, required: true },
  niveis: { type: Array, default: () => [] },
  nivel: { type: Number, default: -1 },
})
const emit = defineEmits(['versao', 'nivel'])
</script>

<template>
  <div class="opcoes-ao-vivo">
    <template v-if="alternativas?.versoes?.length > 1">
      <p class="rotulo">Versões de {{ alternativas.nome }}</p>
      <ul class="escolhas">
        <li v-for="(v, i) in alternativas.versoes" :key="v.id">
          <button class="escolha" :class="{ atual: v.id === atual }" :aria-current="v.id === atual" @click="v.id !== atual && emit('versao', v)">
            <span class="qualidade">{{ rotuloDaVersao(v) }}</span>
            <span class="detalhe">{{ v.nome }}</span>
            <span v-if="v.id === atual" class="chip marcado">assistindo</span>
            <span v-else-if="i === 0" class="chip">melhor</span>
          </button>
        </li>
      </ul>
      <p class="dica">Se travar, tente a próxima: cada versão vem de um servidor diferente do provedor.</p>
    </template>
    <p v-else-if="alternativas" class="dica">Este canal tem uma versão só.</p>

    <template v-if="niveis.length > 1">
      <p class="rotulo">Qualidade do fluxo</p>
      <div class="pilulas">
        <button class="pilula" :class="{ ativa: nivel === -1 }" @click="emit('nivel', -1)">Automática</button>
        <button v-for="n in niveis" :key="n.i" class="pilula" :class="{ ativa: nivel === n.i }" @click="emit('nivel', n.i)">
          {{ n.altura ? `${n.altura}p` : `${Math.round(n.bitrate / 1000)} kb/s` }}
        </button>
      </div>
    </template>

    <template v-if="alternativas?.irmaos?.length">
      <p class="rotulo">Outros canais {{ alternativas.grupo }}</p>
      <ul class="escolhas">
        <li v-for="c in alternativas.irmaos" :key="c.id">
          <button class="escolha" @click="emit('versao', { id: c.id, nome: c.nome, outroCanal: true })">
            <span class="qualidade">{{ c.nome }}</span>
            <span v-if="c.agora" class="detalhe">{{ c.agora.titulo }}</span>
          </button>
        </li>
      </ul>
    </template>
  </div>
</template>

<style scoped>
.rotulo {
  margin: 18px 0 6px;
  font-size: 0.95rem;
}

.rotulo:first-child {
  margin-top: 0;
}

.escolhas {
  margin: 0;
  padding: 0;
  list-style: none;
}

.escolha {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 0 10px;
  align-items: center;
  width: 100%;
  min-height: 48px;
  padding: 6px 0;
  border: 0;
  border-bottom: 1px solid var(--linha);
  background: none;
  color: inherit;
  text-align: left;
  cursor: pointer;
}

.escolha:hover .qualidade {
  color: var(--marca);
}

.escolha.atual {
  cursor: default;
}

.qualidade {
  font-weight: 600;
}

.detalhe {
  grid-column: 1;
  overflow: hidden;
  color: var(--texto-fraco);
  font-size: 0.8rem;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.chip {
  grid-row: 1 / span 2;
  grid-column: 2;
}

.chip.marcado {
  border-color: transparent;
  background: var(--marca-fundo);
  color: var(--marca);
}

.dica {
  margin: 8px 0 0;
  color: var(--texto-fraco);
  font-size: 0.8rem;
  line-height: 1.45;
}

.pilulas {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
</style>
