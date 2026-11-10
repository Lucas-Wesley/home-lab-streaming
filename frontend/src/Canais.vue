<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import Assistir from './Assistir.vue'
import BotaoFavorito from './BotaoFavorito.vue'
import Folha from './Folha.vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { diaDoJogo, faseDoJogo, hora, nomeDoCanal, ordenarJogos } from './jogos.js'

// Canais (S33): primeiro os jogos do dia, pela agenda do ge, com o canal da assinatura que passa
// cada um (ou o grupo da emissora, ou o site, se não está na assinatura); depois os canais de
// esporte, um por linha, com o que passa agora. O resto dos canais fica em "Todos os canais".
const jogos = ref([])
const hoje = ref('')
const erroDaAgenda = ref('')
const grupos = ref([])
const erro = ref('')
const carregando = ref(true)
const dia = ref('')
const canal = ref(null) // { id, nome, capa, versoes }
const grupoAberto = ref(null)

const dias = computed(() => {
  const lista = [...new Set(jogos.value.map((j) => j.dia))].filter((d) => d >= hoje.value).sort()
  if (hoje.value && !lista.includes(hoje.value)) lista.unshift(hoje.value)
  return lista.slice(0, 5)
})
const diaEscolhido = computed(() => dia.value || hoje.value)
const doDia = computed(() => ordenarJogos(jogos.value.filter((j) => j.dia === diaEscolhido.value)))
const proximo = computed(() => jogos.value.find((j) => j.dia > diaEscolhido.value))
const esportes = computed(() => grupos.value.filter((g) => g.principal))

function abrirCanal(c) {
  grupoAberto.value = null
  canal.value = { id: c.id, nome: nomeDoCanal(c.nome), capa: c.capa || '', versoes: c.versoes || [] }
}

function abrirFonte(f) {
  if (f.canal) abrirCanal(f.canal)
  else if (f.grupo) grupoAberto.value = grupos.value.find((g) => g.id === f.grupo) || null
}

async function carregarJogos() {
  try {
    const r = await api('/api/jogos', { tempo: 40000 })
    jogos.value = r.jogos
    hoje.value = r.hoje
    erroDaAgenda.value = r.erro || ''
  } catch (e) {
    erroDaAgenda.value = e.status === 0 ? '' : e.message
  }
}

async function carregarCanais() {
  try {
    grupos.value = (await api('/api/canais/esportes', { tempo: 40000 })).grupos
  } catch (e) {
    erro.value = e.status === 0 ? 'Sem conexão: o Streaming só abre na rede de casa.' : e.message
  }
}

// O placar anda: relê os jogos a cada minuto (o servidor só vai ao ge quando precisa) e os canais
// a cada 5, para o "agora" do guia.
let relogio
let voltas = 0
onMounted(async () => {
  await Promise.all([carregarJogos(), carregarCanais()])
  carregando.value = false
  relogio = setInterval(() => {
    if (document.hidden) return
    carregarJogos()
    if (++voltas % 5 === 0) carregarCanais()
  }, 60000)
})
onBeforeUnmount(() => clearInterval(relogio))
</script>

<template>
  <div class="pagina canais">
    <h1>Canais</h1>
    <div v-if="erro" class="aviso erro">{{ erro }}</div>

    <section class="jogos" aria-labelledby="titulo-jogos">
      <div class="cabeca">
        <h2 id="titulo-jogos">Jogos</h2>
        <div v-if="dias.length > 1" class="pilulas" role="tablist">
          <button
            v-for="d in dias"
            :key="d"
            class="pilula"
            :class="{ ativa: diaEscolhido === d }"
            role="tab"
            :aria-selected="diaEscolhido === d"
            @click="dia = d"
          >
            {{ diaDoJogo(d, hoje) }}
          </button>
        </div>
      </div>

      <p v-if="carregando" class="suave carregando">Procurando os jogos…</p>
      <template v-else>
        <ul v-if="doDia.length" class="lista-de-jogos">
          <li v-for="j in doDia" :key="j.id" class="jogo" :class="j.momento">
            <div class="quando">
              <span v-if="j.momento === 'ao_vivo'" class="ao-vivo"><span class="ponto"></span>Ao vivo</span>
              <span v-else-if="j.momento === 'encerrado'" class="fim">Encerrado</span>
              <span v-else class="hora">{{ j.hora || 'A definir' }}</span>
            </div>
            <div class="partida">
              <p class="campeonato">{{ j.campeonato }}<template v-if="faseDoJogo(j)"> · {{ faseDoJogo(j) }}</template></p>
              <p v-for="(t, i) in [j.mandante, j.visitante]" :key="i" class="time">
                <img v-if="t.escudo" :src="t.escudo" alt="" loading="lazy" referrerpolicy="no-referrer" />
                <span v-else class="sem-escudo"></span>
                <span class="nome-do-time">{{ t.nome }}</span>
                <strong v-if="j.placar" class="gols">
                  {{ i === 0 ? j.placar.mandante : j.placar.visitante }}<small v-if="j.placar.penaltis_mandante != null"> ({{ i === 0 ? j.placar.penaltis_mandante : j.placar.penaltis_visitante }})</small>
                </strong>
              </p>
            </div>
            <div class="fontes">
              <template v-for="(f, i) in j.fontes" :key="i">
                <button
                  v-if="f.canal || f.grupo"
                  class="botao"
                  :class="{ principal: f.canal && j.momento !== 'encerrado', grupo: !f.canal }"
                  :title="f.canal?.programa ? `No guia: ${f.canal.programa}` : f.canal ? '' : `Escolher um canal ${f.nome}`"
                  @click="abrirFonte(f)"
                >
                  <Icone nome="tocar" />{{ f.canal ? nomeDoCanal(f.canal.nome) : f.nome }}
                </button>
                <a v-else-if="f.link" class="botao fora" :href="f.link" target="_blank" rel="noopener noreferrer">{{ f.nome }}<Icone nome="externo" /></a>
                <span v-else class="chip">{{ f.nome }}</span>
              </template>
              <span v-if="!j.fontes.length" class="suave sem-fonte">Sem transmissão informada</span>
            </div>
          </li>
        </ul>
        <div v-else class="sem-jogos">
          <p>Nenhum jogo {{ diaEscolhido === hoje ? 'hoje' : 'neste dia' }} nos campeonatos acompanhados.</p>
          <p v-if="proximo" class="suave">
            Próximo: <strong>{{ proximo.mandante.nome }} x {{ proximo.visitante.nome }}</strong>,
            {{ diaDoJogo(proximo.dia, hoje).toLowerCase() }}{{ proximo.hora ? ` às ${proximo.hora}` : '' }}
            · {{ proximo.campeonato }}
            <button class="botao pequeno" @click="dia = proximo.dia">Ver o dia</button>
          </p>
        </div>
        <p v-if="erroDaAgenda" class="aviso erro">{{ erroDaAgenda }}</p>
      </template>
    </section>

    <section v-for="g in esportes" :key="g.id" class="grupo-de-canais">
      <p class="rotulo">{{ g.nome }}</p>
      <ul class="lista">
        <li v-for="c in g.canais" :key="c.id">
          <button class="canal" @click="abrirCanal(c)">
            <span class="logo" :data-inicial="c.nome.slice(0, 1)">
              <img v-if="c.capa" :src="c.capa" alt="" loading="lazy" referrerpolicy="no-referrer" />
            </span>
            <span class="texto">
              <span class="nome">{{ c.nome }}</span>
              <span v-if="c.agora" class="agora">{{ c.agora.titulo }}<span class="suave"> · até {{ hora(c.agora.fim) }}</span></span>
            </span>
          </button>
        </li>
      </ul>
    </section>

    <a class="botao todos" href="#/canais/todos">Todos os canais<Icone nome="avancar" /></a>

    <Folha v-if="grupoAberto" :titulo="grupoAberto.nome" @fechar="grupoAberto = null">
      <p class="suave dica">O guia não diz em qual canal {{ grupoAberto.nome }} está o jogo: escolha um.</p>
      <ul class="lista">
        <li v-for="c in grupoAberto.canais" :key="c.id">
          <button class="canal" @click="abrirCanal(c)">
            <span class="texto">
              <span class="nome">{{ c.nome }}</span>
              <span v-if="c.agora" class="agora">{{ c.agora.titulo }}</span>
            </span>
          </button>
        </li>
      </ul>
    </Folha>

    <Folha v-if="canal" :titulo="canal.nome" @fechar="canal = null">
      <Assistir :key="canal.id" :chave="`canal:${canal.id}`" :titulo="canal.nome" :capa="canal.capa" @escolheu="canal = null" />
      <div v-if="canal.versoes.length > 1" class="versoes">
        <p class="suave">Outras versões, se esta travar:</p>
        <div class="linha-acoes">
          <button v-for="v in canal.versoes" :key="v.id" class="pilula" :class="{ ativa: v.id === canal.id }" @click="canal = { ...canal, id: v.id }">
            {{ v.nome }}
          </button>
        </div>
      </div>
      <div class="linha-acoes favoritar"><BotaoFavorito tipo="canal" :id="canal.id" /></div>
    </Folha>
  </div>
</template>

<style scoped>
.canais {
  max-width: 860px;
}

.cabeca {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  align-items: center;
  margin-bottom: 10px;
}

.cabeca h2 {
  margin: 0;
  font-size: 1.15rem;
  font-weight: 650;
}

.pilulas {
  display: flex;
  gap: 2px;
  overflow-x: auto;
  scrollbar-width: none;
}

.pilula {
  flex: none;
}

.carregando {
  padding: 20px 0;
}

.lista-de-jogos {
  display: grid;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.jogo {
  display: grid;
  grid-template-columns: 76px minmax(0, 1fr) auto;
  gap: 14px;
  align-items: center;
  padding: 12px 14px;
  border: 1px solid var(--linha);
  border-radius: var(--raio);
  background: var(--fundo);
}

.jogo.ao_vivo {
  border-color: color-mix(in srgb, var(--vermelho) 45%, transparent);
}

.jogo.encerrado {
  opacity: 0.72;
}

.quando {
  font-size: 0.85rem;
  font-weight: 650;
}

.hora {
  font-size: 1.15rem;
  font-variant-numeric: tabular-nums;
}

.ao-vivo {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  color: var(--vermelho);
  text-transform: uppercase;
  font-size: 0.75rem;
  letter-spacing: 0.04em;
}

.ponto {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--vermelho);
  animation: piscar 1.6s ease-in-out infinite;
}

@keyframes piscar {
  50% {
    opacity: 0.3;
  }
}

.fim {
  color: var(--texto-fraco);
  font-size: 0.78rem;
}

.partida p {
  margin: 0;
}

.campeonato {
  margin-bottom: 4px !important;
  overflow: hidden;
  color: var(--texto-fraco);
  font-size: 0.75rem;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.time {
  display: flex;
  gap: 8px;
  align-items: center;
  min-height: 26px;
}

.time img,
.sem-escudo {
  flex: none;
  width: 20px;
  height: 20px;
  object-fit: contain;
}

.sem-escudo {
  border-radius: 50%;
  background: var(--superficie-forte);
}

.nome-do-time {
  overflow: hidden;
  font-size: 0.95rem;
  font-weight: 550;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.gols {
  margin-left: auto;
  font-size: 1rem;
  font-variant-numeric: tabular-nums;
}

.gols small {
  color: var(--texto-fraco);
  font-weight: 500;
}

.fontes {
  display: flex;
  flex-direction: column;
  gap: 6px;
  align-items: stretch;
}

.fontes .botao {
  justify-content: center;
}

.fontes .botao svg {
  width: 15px;
  height: 15px;
}

.botao.grupo {
  border-color: color-mix(in srgb, var(--marca) 55%, transparent);
  color: var(--marca);
}

.botao.fora {
  color: var(--texto-suave);
}

.sem-fonte {
  font-size: 0.8rem;
}

.sem-jogos {
  padding: 18px 16px;
  border: 1px dashed var(--linha-forte);
  border-radius: var(--raio);
}

.sem-jogos p {
  margin: 0;
  line-height: 1.6;
}

.sem-jogos p + p {
  margin-top: 6px;
}

.botao.pequeno {
  min-height: 26px;
  margin-left: 6px;
  padding: 0 9px;
  font-size: 0.8rem;
}

.jogos .aviso {
  margin-top: 10px;
}

.grupo-de-canais .rotulo {
  margin-top: 32px;
}

.canal {
  display: flex;
  width: 100%;
  gap: 12px;
  align-items: center;
  padding: 9px 0;
  border: 0;
  background: transparent;
  color: inherit;
  text-align: left;
  cursor: pointer;
}

.canal:hover .nome {
  color: var(--marca);
}

.logo {
  position: relative;
  display: grid;
  flex: none;
  place-items: center;
  width: 52px;
  height: 34px;
  overflow: hidden;
  border-radius: 6px;
  background: var(--superficie);
}

.logo img {
  width: 100%;
  height: 100%;
  padding: 3px;
  object-fit: contain;
}

.logo:not(:has(img))::after {
  content: attr(data-inicial);
  color: var(--texto-fraco);
  font-weight: 600;
}

.texto {
  display: grid;
  min-width: 0;
}

.nome {
  font-size: 0.95rem;
  font-weight: 550;
  transition: color 0.15s ease;
}

.agora {
  overflow: hidden;
  color: var(--texto-suave);
  font-size: 0.82rem;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.todos {
  margin-top: 28px;
}

.todos svg {
  width: 16px;
  height: 16px;
}

.dica {
  margin: -4px 0 10px;
  font-size: 0.85rem;
}

.versoes {
  margin-top: 14px;
}

.versoes p {
  margin: 0 0 6px;
  font-size: 0.82rem;
}

.versoes .pilula {
  font-size: 0.78rem;
}

.favoritar {
  margin-top: 10px;
}

/* No celular, os botões dos canais vão para baixo do jogo, lado a lado. */
@media (max-width: 600px) {
  .jogo {
    grid-template-columns: 62px minmax(0, 1fr);
    gap: 10px 12px;
  }

  .fontes {
    grid-column: 1 / -1;
    flex-direction: row;
    flex-wrap: wrap;
  }

  .fontes .botao {
    flex: 1 1 auto;
  }
}
</style>
