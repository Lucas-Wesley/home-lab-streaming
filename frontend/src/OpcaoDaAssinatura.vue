<script setup>
import { inject, ref } from 'vue'
import BotaoFavorito from './BotaoFavorito.vue'
import FolhaDeAssistir from './FolhaDeAssistir.vue'
import Icone from './Icone.vue'
import { api } from './api.js'

// A assinatura como opção da página do título (S28): vem antes das versões de torrent, com os mesmos
// botões. chave: filme:1 ou episodio:2. "Baixar" deixa o vídeo no servidor: ele baixa quando a
// assinatura está livre e, depois, toca sem usar a conexão.
const props = defineProps({
  chave: { type: String, required: true },
  nome: { type: String, required: true }, // como está no catálogo ("Duna [L]")
  rotulo: { type: String, required: true }, // título da folha
  capa: { type: String, default: '' },
  posicao: { type: Number, default: 0 },
  favorito: { type: Object, default: null }, // { tipo, id } para o botão de favorito
  download: { type: Object, default: null }, // como está o download deste vídeo, se houver
})
const recado = inject('recado')
const aberta = ref(false)
const pedido = ref(props.download)
const pedindo = ref(false)

// "Duna [L]", "El Camino LEG", "Private Eyes (Legendado)": a versão com o áudio original.
const original = /\[L\]|\bLEG\b|legendad/i.test(props.nome)

const ESTADOS = { 'na fila': 'na fila para baixar', baixando: 'baixando', esperando: 'esperando a assinatura ficar livre', baixado: 'baixado no servidor', erro: 'erro ao baixar' }

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
  <div class="opcao">
    <span class="texto">
      <span class="titulo"><span class="selo"><Icone nome="canais" /></span>Assinatura</span>
      <span class="meta" :title="nome">
        <span v-if="original" class="original">Áudio original · </span>{{ nome }}<template v-if="posicao > 30"> · parou no meio</template>
        <template v-if="pedido"> · <span :class="{ ok: pedido.estado === 'baixado', erro: pedido.estado === 'erro' }">{{ ESTADOS[pedido.estado] || pedido.estado }}</span></template>
      </span>
    </span>
    <span class="botoes">
      <button class="botao principal" @click="aberta = true"><Icone nome="tocar" />Assistir</button>
      <button v-if="!pedido" class="botao so-icone" :disabled="pedindo" title="Baixar para o servidor (quando a assinatura estiver livre)" aria-label="Baixar" @click="baixar"><Icone nome="torrents" /></button>
    </span>

    <FolhaDeAssistir v-if="aberta" :titulo="rotulo" origem="assinatura" :nome="nome" :chave="chave" :capa="capa" :posicao="posicao" @fechar="aberta = false">
      <div v-if="favorito" class="linha-acoes favoritar"><BotaoFavorito :tipo="favorito.tipo" :id="favorito.id" /></div>
    </FolhaDeAssistir>
  </div>
</template>

<style scoped>
.opcao {
  display: flex;
  gap: 10px;
  align-items: center;
  padding: 9px 0;
}

.texto {
  display: grid;
  flex: 1;
  gap: 4px;
  min-width: 0;
}

.titulo {
  display: flex;
  gap: 8px;
  align-items: center;
  font-size: 0.9rem;
  font-weight: 600;
}

.selo {
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  border-radius: 5px;
  background: var(--marca);
  color: var(--marca-texto);
}

.selo svg {
  width: 14px;
  height: 14px;
  stroke-width: 2;
}

.meta {
  overflow: hidden;
  color: var(--texto-suave);
  font-size: 0.78rem;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.original {
  color: var(--texto);
  font-weight: 600;
}

.meta .ok {
  color: var(--azul);
}

.meta .erro {
  color: var(--vermelho);
}

.botoes {
  display: flex;
  flex: none;
  gap: 6px;
  align-items: center;
}

.so-icone {
  padding: 0 9px;
}

.botoes .botao svg {
  width: 16px;
  height: 16px;
}

.favoritar {
  margin-top: 12px;
}
</style>
