<script setup>
import { onMounted, ref } from 'vue'
import Assistir from './Assistir.vue'
import BotaoBaixar from './BotaoBaixar.vue'
import BotaoFavorito from './BotaoFavorito.vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { voltar } from './voltar.js'
import { duracao } from './tempo.js'

const props = defineProps({ id: { type: String, required: true } })

const filme = ref(null)
const erro = ref('')

onMounted(async () => {
  try {
    filme.value = await api(`/api/filmes/${props.id}`, { tempo: 20000 })
    // Conhecido no TMDB: a página do título junta a assinatura e as versões de torrent (S28).
    if (filme.value.detalhes?.tmdb) location.replace(`#/titulo/filme/${filme.value.detalhes.tmdb}`)
  } catch (e) {
    erro.value = e.status === 0 ? 'Sem conexão: o Streaming só abre na rede de casa.' : e.message
  }
})
</script>

<template>
  <div class="pagina">
    <a class="voltar" href="#/filmes" @click.prevent="voltar('#/filmes')"><Icone nome="voltar" />Filmes</a>
    <div v-if="erro" class="aviso erro">{{ erro }}</div>

    <article v-if="filme" class="filme">
      <img v-if="filme.item.capa" class="cartaz" :src="filme.item.capa" alt="" referrerpolicy="no-referrer" />
      <div class="sobre">
        <h1>{{ filme.item.nome }}</h1>
        <p class="meta">
          <span v-if="filme.item.ano">{{ filme.item.ano }}</span>
          <span v-if="filme.detalhes?.duracao">{{ duracao(filme.detalhes.duracao) }}</span>
          <span v-if="filme.item.nota">★ {{ filme.item.nota.toFixed(1) }}</span>
          <span v-if="filme.detalhes?.genero">{{ filme.detalhes.genero }}</span>
        </p>
        <Assistir
          :chave="`filme:${filme.item.id}`"
          :titulo="filme.item.nome"
          :capa="filme.item.capa"
          :posicao="filme.progresso?.posicao || 0"
        />
        <div class="linha-acoes extras"><BotaoFavorito tipo="filme" :id="filme.item.id" /><BotaoBaixar :chave="`filme:${filme.item.id}`" /></div>
        <p v-if="filme.detalhes?.sinopse" class="sinopse">{{ filme.detalhes.sinopse }}</p>
        <dl v-if="filme.detalhes?.elenco || filme.detalhes?.diretor">
          <template v-if="filme.detalhes.diretor"><dt>Direção</dt><dd>{{ filme.detalhes.diretor }}</dd></template>
          <template v-if="filme.detalhes.elenco"><dt>Elenco</dt><dd>{{ filme.detalhes.elenco }}</dd></template>
        </dl>
      </div>
    </article>
  </div>
</template>

<style scoped>
.pagina {
  max-width: 920px;
}

.voltar {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  margin: -8px 0 16px -6px;
  color: var(--texto-suave);
  font-size: 0.9rem;
  text-decoration: none;
}

.voltar svg {
  width: 18px;
  height: 18px;
}

.filme {
  display: grid;
  grid-template-columns: 220px 1fr;
  gap: 28px;
  align-items: start;
}

.cartaz {
  width: 100%;
  border-radius: var(--raio);
  background: var(--superficie);
}

h1 {
  margin-bottom: 8px !important;
}

.meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 14px;
  margin: 0 0 18px;
  color: var(--texto-suave);
  font-size: 0.88rem;
}

.extras {
  margin-top: 10px;
}

.sinopse {
  margin: 22px 0 0;
  line-height: 1.6;
}

dl {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 6px 14px;
  margin: 18px 0 0;
  font-size: 0.88rem;
}

dt {
  color: var(--texto-suave);
  font-weight: 600;
}

dd {
  margin: 0;
  color: var(--texto-suave);
  line-height: 1.45;
}

@media (max-width: 820px) {
  .filme {
    grid-template-columns: 1fr;
    gap: 18px;
  }

  .cartaz {
    width: 46%;
    max-width: 200px;
  }
}
</style>
