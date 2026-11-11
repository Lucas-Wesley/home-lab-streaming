<script setup>
defineProps({ nome: { type: String, required: true } })
</script>

<template>
  <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path v-if="nome === 'voltar'" d="M15 5l-7 7 7 7" />
    <template v-else-if="nome === 'legenda'">
      <rect x="3.5" y="5.5" width="17" height="13" rx="2.5" />
      <path d="M10.5 10.2a2.3 2.3 0 100 3.6M17 10.2a2.3 2.3 0 100 3.6" />
    </template>
    <template v-else-if="nome === 'lixeira'">
      <path d="M5 7h14M10 7V5h4v2M7 7l1 12h8l1-12" />
    </template>
    <path v-else-if="nome === 'avancar'" d="M9 5l7 7-7 7" />
    <path v-else-if="nome === 'fechar'" d="M6 6l12 12M18 6L6 18" />
    <path v-else-if="nome === 'ampliar'" d="M14 4.5h5.5V10M19.5 4.5L13.5 10.5M10 19.5H4.5V14M4.5 19.5l6-6" />
    <path v-else-if="nome === 'reduzir'" d="M19.5 4.5l-6 6M13.5 5.5v5h5M4.5 19.5l6-6M10.5 18.5v-5h-5" />
    <template v-else-if="nome === 'alerta'">
      <path d="M12 4.5l8.5 15h-17z" />
      <path d="M12 10.5v4M12 17.2v.1" />
    </template>
    <path v-else-if="nome === 'confirmado'" d="M5.5 12.5l4 4 9-9" />
    <path v-else-if="nome === 'externo'" d="M14 4.5h5.5V10M19.5 4.5L11 13M17.5 13.5v4.5a1.5 1.5 0 01-1.5 1.5H6A1.5 1.5 0 014.5 18V8A1.5 1.5 0 016 6.5h4.5" />
    <template v-else-if="nome === 'inicio'">
      <path d="M4 10.5L12 4l8 6.5V19a1 1 0 01-1 1h-4.5v-5.5h-5V20H5a1 1 0 01-1-1z" />
    </template>
    <template v-else-if="nome === 'canais'">
      <rect x="3.5" y="7" width="17" height="12" rx="2" />
      <path d="M8.5 3.5L12 7l3.5-3.5" />
    </template>
    <template v-else-if="nome === 'filmes'">
      <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
      <path d="M7.5 4.5v15M16.5 4.5v15M3.5 9.5h4M3.5 14.5h4M16.5 9.5h4M16.5 14.5h4" />
    </template>
    <template v-else-if="nome === 'series'">
      <rect x="3.5" y="8" width="13" height="11.5" rx="2" />
      <path d="M7 4.5h11a2.5 2.5 0 012.5 2.5v9" />
      <path d="M8.5 11.5v4.5l3.8-2.25z" />
    </template>
    <template v-else-if="nome === 'ajustes'">
      <path d="M4 7.5h9M17 7.5h3M4 16.5h3M11 16.5h9" />
      <circle cx="15" cy="7.5" r="2" />
      <circle cx="9" cy="16.5" r="2" />
    </template>
    <template v-else-if="nome === 'pesquisar'">
      <circle cx="11" cy="11" r="6.5" />
      <path d="M16 16l4.5 4.5" />
    </template>
    <path v-else-if="nome === 'tocar'" d="M7.5 5.5v13l11-6.5z" fill="currentColor" />
    <path v-else-if="nome === 'pausar'" d="M8 5.5v13M16 5.5v13" stroke-width="3" />
    <rect v-else-if="nome === 'parar'" x="6.5" y="6.5" width="11" height="11" rx="1.5" fill="currentColor" />
    <template v-else-if="nome === 'menos30'">
      <path d="M4.5 12a7.5 7.5 0 107.5-7.5H8.5" />
      <path d="M10.5 2L8 4.5 10.5 7" />
      <text x="12" y="15.2" text-anchor="middle" font-size="7" font-weight="600" fill="currentColor" stroke="none">30</text>
    </template>
    <template v-else-if="nome === 'mais30'">
      <path d="M19.5 12A7.5 7.5 0 1112 4.5h3.5" />
      <path d="M13.5 2L16 4.5 13.5 7" />
      <text x="12" y="15.2" text-anchor="middle" font-size="7" font-weight="600" fill="currentColor" stroke="none">30</text>
    </template>
    <template v-else-if="nome === 'tela'">
      <path d="M3.5 15.5V7a2 2 0 012-2h13a2 2 0 012 2v10a2 2 0 01-2 2h-6" />
      <path d="M3.5 19.5h.01M3.5 12a7.5 7.5 0 017.5 7.5M3.5 15.8a3.7 3.7 0 013.7 3.7" />
    </template>
    <template v-else-if="nome === 'volume-menos'">
      <path d="M4 9.5h3.5L12 5.5v13l-4.5-4H4z" />
      <path d="M16 12h5" />
    </template>
    <template v-else-if="nome === 'volume-mais'">
      <path d="M4 9.5h3.5L12 5.5v13l-4.5-4H4z" />
      <path d="M16 12h5M18.5 9.5v5" />
    </template>
    <template v-else-if="nome === 'mudo'">
      <path d="M4 9.5h3.5L12 5.5v13l-4.5-4H4z" />
      <path d="M16 9.5l5 5M21 9.5l-5 5" />
    </template>
    <template v-else-if="nome === 'sem-imagem'">
      <rect x="3.5" y="5.5" width="17" height="12" rx="2" />
      <path d="M4 4l16 16" />
    </template>
    <template v-else-if="nome === 'torrents'">
      <path d="M12 4v11M7.5 10.5L12 15l4.5-4.5" />
      <path d="M4.5 15v3a2 2 0 002 2h11a2 2 0 002-2v-3" />
    </template>
    <path v-else-if="nome === 'favorito'" d="M12 20s-7-4.35-7-10a4 4 0 017-2.65A4 4 0 0119 10c0 5.65-7 10-7 10z" />
    <path v-else-if="nome === 'favorito-cheio'" d="M12 20s-7-4.35-7-10a4 4 0 017-2.65A4 4 0 0119 10c0 5.65-7 10-7 10z" fill="currentColor" />
    <template v-else-if="nome === 'mais-opcoes'">
      <circle cx="5.5" cy="12" r="1.2" fill="currentColor" /><circle cx="12" cy="12" r="1.2" fill="currentColor" /><circle cx="18.5" cy="12" r="1.2" fill="currentColor" />
    </template>
    <path v-else-if="nome === 'atualizar'" d="M19.5 12a7.5 7.5 0 11-2.2-5.3M19.5 4.5v4h-4" />
  </svg>
</template>
