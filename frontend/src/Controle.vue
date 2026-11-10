<script setup>
import { computed, inject, onBeforeUnmount, onMounted, ref } from 'vue'
import EscolherLegenda from './EscolherLegenda.vue'
import Folha from './Folha.vue'
import Icone from './Icone.vue'
import LegendaAoVivo from './LegendaAoVivo.vue'
import { api } from './api.js'
import { esperarAjuste } from './legendas.js'
import { carregarTelas, comandar, telas, tocarNaTela } from './telas.js'
import { relogio, rotaDoPlayer } from './tempo.js'
import { voltar } from './voltar.js'

// O celular como controle remoto da tela: o servidor fala com a TV ou o projetor por DLNA.
const props = defineProps({ id: { type: String, required: true } })
const recado = inject('recado')

const estado = ref(null)
const erro = ref('')
const arrastando = ref(null) // posição escolhida na barra, enquanto o dedo está nela
const ocupado = ref(false)

const tela = computed(() => telas.value.find((t) => t.id === props.id))
const tocando = computed(() => ['PLAYING', 'TRANSITIONING'].includes(estado.value?.estado))
const sessao = computed(() => estado.value?.sessao)
const aoVivo = computed(() => sessao.value?.ao_vivo)
const posicao = computed(() => arrastando.value ?? estado.value?.posicao ?? 0)
const parada = computed(() => !sessao.value && ['STOPPED', 'NO_MEDIA_PRESENT'].includes(estado.value?.estado))

// A legenda no celular (S31) precisa do segundo exato: entre uma leitura e outra (1,5 s), o tempo
// anda pelo relógio do celular enquanto a tela toca.
const lidoEm = ref(0)
const agora = ref(Date.now())
const versaoDaLegenda = ref(0)
const posicaoAoVivo = computed(() => {
  const p = estado.value?.posicao || 0
  return estado.value?.estado === 'PLAYING' ? p + Math.min(5, (agora.value - lidoEm.value) / 1000) : p
})

let relogioDoEstado
let relogioDaLegenda
async function lerEstado() {
  if (document.visibilityState !== 'visible') return
  try {
    estado.value = await api(`/api/telas/${encodeURIComponent(props.id)}/estado`, { tempo: 6000 })
    lidoEm.value = Date.now()
    erro.value = ''
  } catch (e) {
    erro.value = e.message
  }
}

async function enviar(comando, valor = 0) {
  ocupado.value = true
  try {
    await comandar(props.id, comando, valor)
    await lerEstado()
  } catch (e) {
    recado(e.message, true)
  } finally {
    ocupado.value = false
  }
}

function soltarBarra() {
  if (arrastando.value === null) return
  const alvo = arrastando.value
  enviar('ir', alvo).finally(() => (arrastando.value = null))
}

function mudarVolume(delta) {
  const atual = estado.value?.volume ?? 0
  const novo = Math.max(0, Math.min(100, atual + delta))
  if (estado.value) estado.value.volume = novo
  enviar('volume', novo)
}

// A legenda só entra na tela junto com o vídeo: escolher outra, ou ajustar a sincronia, manda o
// vídeo de novo, do mesmo ponto.
const escolhendoLegenda = ref(false)
const temLegenda = ref(false) // a legenda passa no celular: a capa fica pequena
async function reenviar(mensagem) {
  const s = sessao.value
  if (!s || !tela.value) return
  ocupado.value = true
  try {
    await tocarNaTela(tela.value, s.chave, { posicao: estado.value?.posicao || 0, capa: s.capa })
    versaoDaLegenda.value++
    recado(mensagem)
    await lerEstado()
  } catch (e) {
    recado(e.message, true)
  } finally {
    ocupado.value = false
  }
}

async function legendaEscolhida(l) {
  escolhendoLegenda.value = false
  if (l?.geracao) {
    gerada(l)
    return
  }
  if (l?.alinhando) {
    recado('Conferindo a sincronia da legenda…')
    l = await esperarAjuste(sessao.value.chave, l)
  }
  reenviar(l ? `Legenda escolhida${l.ajuste ? ` (${l.ajuste})` : ''}: o vídeo recomeça do mesmo ponto.` : 'Legenda tirada.')
}

// Legenda gerada pelo áudio (S29). Na assinatura, o servidor para a tela, puxa o áudio e manda o
// vídeo de volta sozinho. Fora dela, espera 3 min ficarem prontos e manda de novo; depois,
// o servidor atualiza a tela quando a legenda que ela tem está acabando.
async function gerada(l) {
  if (l.geracao.puxar) {
    recado('A TV para uns segundos enquanto o servidor puxa o áudio da assinatura, e volta do mesmo ponto.')
    return
  }
  recado('Gerando a legenda: ela entra na tela em alguns segundos.')
  for (let i = 0; i < 40; i++) {
    await new Promise((ok) => setTimeout(ok, 3000))
    const g = (await api(`/api/legendas/${sessao.value?.chave}`).catch(() => null))?.escolhida?.geracao
    if (!g || g.estado === 'erro' || g.estado === 'parada') return
    if (g.ate > (estado.value?.posicao || 0) + 180 || g.estado === 'pronta') break
  }
  reenviar('Legenda gerada na tela: o vídeo recomeça do mesmo ponto.')
}

// Vários toques em ±0,5 s viram um envio só.
let esperaDoAjuste
function legendaAjustada() {
  clearTimeout(esperaDoAjuste)
  esperaDoAjuste = setTimeout(() => reenviar('Sincronia ajustada: o vídeo recomeça do mesmo ponto.'), 1500)
}

// Trazer de volta para o celular: o player assume a assinatura e a tela para sozinha.
function assistirAqui() {
  const s = sessao.value
  if (!s) return
  location.hash = rotaDoPlayer(s.chave, { titulo: s.titulo, posicao: s.ao_vivo ? 0 : estado.value?.posicao || 0 })
}

onMounted(() => {
  carregarTelas()
  lerEstado()
  relogioDoEstado = setInterval(lerEstado, 1500)
  relogioDaLegenda = setInterval(() => (agora.value = Date.now()), 250)
  document.addEventListener('visibilitychange', lerEstado)
})

onBeforeUnmount(() => {
  clearInterval(relogioDoEstado)
  clearInterval(relogioDaLegenda)
  document.removeEventListener('visibilitychange', lerEstado)
})
</script>

<template>
  <div class="controle">
    <header class="barra">
      <button class="botao icone" aria-label="Voltar" @click="voltar('#/inicio')"><Icone nome="voltar" /></button>
      <span class="onde"><Icone nome="tela" />{{ tela?.nome || 'Tela' }}</span>
    </header>

    <div class="corpo" :class="{ 'com-legenda': temLegenda }">
      <div class="capa">
        <img v-if="sessao?.capa" :src="sessao.capa" alt="" referrerpolicy="no-referrer" />
        <Icone v-else nome="tela" />
      </div>
      <h1>{{ sessao?.titulo || (parada ? 'Nada tocando' : '…') }}</h1>
      <p class="suave estado">
        <template v-if="erro">{{ erro }}</template>
        <template v-else-if="aoVivo">Ao vivo</template>
        <template v-else-if="estado?.estado === 'TRANSITIONING'">Abrindo…</template>
        <template v-else-if="estado?.estado === 'PAUSED_PLAYBACK'">Pausado</template>
        <template v-else-if="parada">Escolha algo no Início e toque no nome desta tela.</template>
      </p>

      <LegendaAoVivo v-if="sessao && !aoVivo" :key="sessao.chave" :chave="sessao.chave" :posicao="posicaoAoVivo" :versao="versaoDaLegenda" @falas="temLegenda = $event" />

      <div v-if="sessao && !aoVivo && estado?.duracao" class="tempo">
        <input
          type="range"
          min="0"
          :max="Math.floor(estado.duracao)"
          :value="Math.floor(posicao)"
          aria-label="Posição no vídeo"
          @input="arrastando = Number($event.target.value)"
          @change="soltarBarra"
        />
        <div class="numeros">
          <span>{{ relogio(posicao) }}</span>
          <span>−{{ relogio(estado.duracao - posicao) }}</span>
        </div>
      </div>

      <div class="principais">
        <button class="redondo" :disabled="!sessao || aoVivo || ocupado" aria-label="Voltar 30 segundos" @click="enviar('pular', -30)"><Icone nome="menos30" /></button>
        <button class="redondo grande" :disabled="!sessao || ocupado" :aria-label="tocando ? 'Pausar' : 'Continuar'" @click="enviar(tocando ? 'pausar' : 'continuar')">
          <Icone :nome="tocando ? 'pausar' : 'tocar'" />
        </button>
        <button class="redondo" :disabled="!sessao || aoVivo || ocupado" aria-label="Avançar 30 segundos" @click="enviar('pular', 30)"><Icone nome="mais30" /></button>
      </div>

      <div class="secundarios">
        <button class="botao icone" aria-label="Diminuir volume" @click="mudarVolume(-3)"><Icone nome="volume-menos" /></button>
        <span class="volume">{{ estado?.mudo ? 'mudo' : estado?.volume ?? '–' }}</span>
        <button class="botao icone" aria-label="Aumentar volume" @click="mudarVolume(3)"><Icone nome="volume-mais" /></button>
        <button class="botao icone" :aria-label="estado?.mudo ? 'Tirar o mudo' : 'Mudo'" @click="enviar('mudo', estado?.mudo ? 0 : 1)"><Icone nome="mudo" /></button>
        <button v-if="sessao && !aoVivo" class="botao icone" aria-label="Legendas" title="Legendas" :disabled="ocupado" @click="escolhendoLegenda = true">
          <Icone nome="legenda" />
        </button>
      </div>

      <div class="linha-acoes acoes">
        <button class="botao" :disabled="!sessao" @click="assistirAqui">Assistir no aparelho</button>
        <button class="botao perigo" :disabled="!sessao" @click="enviar('parar')"><Icone nome="parar" />Parar</button>
      </div>
    </div>

    <Folha v-if="escolhendoLegenda && sessao" titulo="Legendas" @fechar="escolhendoLegenda = false">
      <EscolherLegenda :chave="sessao.chave" liberar @escolheu="legendaEscolhida" @ajustou="legendaAjustada" />
    </Folha>
  </div>
</template>

<style scoped>
.controle {
  min-height: 100vh;
  padding-bottom: env(safe-area-inset-bottom);
}

.barra {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: max(10px, env(safe-area-inset-top)) 12px 10px;
}

.onde {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: var(--texto-suave);
  font-size: 0.9rem;
  font-weight: 600;
}

.onde svg {
  width: 18px;
  height: 18px;
  color: var(--marca);
}

.corpo {
  display: grid;
  grid-template-columns: minmax(0, 1fr);
  justify-items: center;
  gap: 4px;
  max-width: 420px;
  margin: 0 auto;
  padding: 12px 20px 32px;
  text-align: center;
}

.capa {
  display: grid;
  place-items: center;
  width: 180px;
  aspect-ratio: 2 / 3;
  margin-bottom: 18px;
  overflow: hidden;
  border-radius: var(--raio);
  background: var(--superficie);
  color: var(--texto-fraco);
}

.capa img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.capa svg {
  width: 48px;
  height: 48px;
}

.com-legenda .capa {
  width: 72px;
  margin-bottom: 10px;
}

h1 {
  margin: 0;
  overflow-wrap: anywhere;
  font-size: 1.3rem;
  font-weight: 700;
  letter-spacing: -0.015em;
  line-height: 1.3;
}

.estado {
  min-height: 1.4em;
  margin: 4px 0 10px;
  font-size: 0.9rem;
}

.tempo {
  width: 100%;
  margin: 8px 0 6px;
}

input[type='range'] {
  width: 100%;
  accent-color: var(--marca);
}

.numeros {
  display: flex;
  justify-content: space-between;
  color: var(--texto-fraco);
  font-size: 0.78rem;
  font-variant-numeric: tabular-nums;
}

.principais {
  display: flex;
  align-items: center;
  gap: 28px;
  margin: 14px 0 18px;
}

.redondo {
  display: grid;
  place-items: center;
  width: 56px;
  height: 56px;
  border: 0;
  border-radius: 999px;
  background: var(--superficie);
  color: var(--texto);
  cursor: pointer;
}

.redondo svg {
  width: 28px;
  height: 28px;
}

.redondo.grande {
  width: 76px;
  height: 76px;
  background: var(--marca);
  color: var(--marca-texto);
}

.redondo:disabled {
  opacity: 0.4;
  cursor: default;
}

.secundarios {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 18px;
}

.volume {
  min-width: 44px;
  color: var(--texto-suave);
  font-size: 0.85rem;
  font-variant-numeric: tabular-nums;
}

.acoes {
  justify-content: center;
}

.acoes .botao svg {
  width: 14px;
  height: 14px;
}
</style>
