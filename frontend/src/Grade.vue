<script setup>
import { ref } from 'vue'
import Assistir from './Assistir.vue'
import BotaoFavorito from './BotaoFavorito.vue'
import Folha from './Folha.vue'
import Icone from './Icone.vue'

// Itens do catálogo: capas em grade para filmes e séries; para canais, uma lista com o logo.
// Um canal abre a folha de assistir (aqui ou numa tela); filme e série abrem a página deles. Na busca
// do Início (S28), cada capa tem um selo: a TV para o que está na assinatura, o T para o que só há
// por torrent.
defineProps({ itens: { type: Array, required: true } })
const canal = ref(null)

function link(it) {
  return it.tipo === 'canal' ? undefined : it.pagina || `#/${it.tipo}/${it.id}`
}

function abrir(it, e) {
  if (it.tipo !== 'canal') return
  e.preventDefault()
  canal.value = it
}

function semImagem(e) {
  e.target.closest('.capa')?.classList.add('sem-imagem')
}
</script>

<template>
  <div>
    <ul v-if="itens.some((i) => i.tipo !== 'canal')" class="grade">
      <li v-for="it in itens.filter((i) => i.tipo !== 'canal')" :key="it.pagina || it.tipo + it.id">
        <a :href="link(it)" class="cartaz">
          <span class="capa" :data-inicial="it.nome.slice(0, 1)">
            <img v-if="it.capa" :src="it.capa" alt="" loading="lazy" referrerpolicy="no-referrer" @error="semImagem" />
            <span v-if="it.origem === 'assinatura'" class="selo assinatura" title="Na sua assinatura"><Icone nome="canais" /></span>
            <span v-else-if="it.origem === 'torrent'" class="selo torrent" title="Por torrent">T</span>
          </span>
          <span class="nome">{{ it.nome }}</span>
          <span v-if="it.ano || it.nota" class="meta">{{ [it.ano, it.nota ? `★ ${it.nota.toFixed(1)}` : ''].filter(Boolean).join('  ') }}</span>
        </a>
      </li>
    </ul>
    <ul v-if="itens.some((i) => i.tipo === 'canal')" class="lista canais">
      <li v-for="it in itens.filter((i) => i.tipo === 'canal')" :key="'canal' + it.id">
        <a href="#" class="canal" @click="abrir(it, $event)">
          <span class="logo capa" :data-inicial="it.nome.slice(0, 1)">
            <img v-if="it.capa" :src="it.capa" alt="" loading="lazy" referrerpolicy="no-referrer" @error="semImagem" />
            <span v-if="it.origem === 'assinatura'" class="selo assinatura" title="Na sua assinatura"><Icone nome="canais" /></span>
            <span v-else-if="it.origem === 'torrent'" class="selo torrent" title="Por torrent">T</span>
          </span>
          <span class="nome">{{ it.nome }}</span>
        </a>
      </li>
    </ul>

    <Folha v-if="canal" :titulo="canal.nome" @fechar="canal = null">
      <Assistir :chave="`canal:${canal.id}`" :titulo="canal.nome" :capa="canal.capa" @escolheu="canal = null" />
      <div class="linha-acoes favoritar"><BotaoFavorito tipo="canal" :id="canal.id" /></div>
    </Folha>
  </div>
</template>

<style scoped>
.grade {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(132px, 1fr));
  gap: 22px 14px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.cartaz {
  display: grid;
  gap: 6px;
  color: inherit;
  text-decoration: none;
}

.capa {
  position: relative;
  display: block;
  overflow: hidden;
  border-radius: var(--raio-p);
  background: var(--superficie);
}

.cartaz .capa {
  aspect-ratio: 2 / 3;
}

.capa img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}

/* Sem capa (ou capa quebrada): a inicial do nome, em cinza. */
.capa:not(:has(img))::after,
.capa.sem-imagem::after {
  content: attr(data-inicial);
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: var(--texto-fraco);
  font-size: 1.6rem;
  font-weight: 600;
}

.selo {
  position: absolute;
  z-index: 1;
  top: 6px;
  left: 6px;
  display: grid;
  place-items: center;
  width: 22px;
  height: 22px;
  border-radius: 6px;
  box-shadow: 0 1px 3px rgb(0 0 0 / 35%);
  font-size: 0.75rem;
  font-weight: 700;
  line-height: 1;
}

.selo svg {
  width: 15px;
  height: 15px;
  stroke-width: 2;
}

.selo.assinatura {
  background: var(--marca);
  color: var(--marca-texto);
}

.selo.torrent {
  background: var(--verde);
  color: #fff;
}

.capa.sem-imagem img {
  display: none;
}

.nome {
  display: -webkit-box;
  overflow: hidden;
  font-size: 0.88rem;
  font-weight: 550;
  line-height: 1.3;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  transition: color 0.15s ease;
}

.cartaz:hover .nome,
.canal:hover .nome {
  color: var(--marca);
}

.meta {
  margin-top: -3px;
  color: var(--texto-fraco);
  font-size: 0.75rem;
  white-space: pre;
}

.canais {
  margin-top: 4px;
}

.canal {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 0;
  color: inherit;
  text-decoration: none;
}

.logo {
  flex: none;
  width: 56px;
  height: 36px;
  padding: 4px;
  border-radius: 6px;
}

.logo img {
  object-fit: contain;
}

.canal .nome {
  font-size: 0.95rem;
  font-weight: 500;
}

.favoritar {
  margin-top: 10px;
}

@media (max-width: 820px) {
  .grade {
    grid-template-columns: repeat(auto-fill, minmax(104px, 1fr));
    gap: 18px 10px;
  }
}
</style>
