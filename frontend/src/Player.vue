<script setup>
import { computed, inject, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import EscolherLegenda from './EscolherLegenda.vue'
import Folha from './Folha.vue'
import Icone from './Icone.vue'
import { api } from './api.js'
import { acompanharGeracao, enderecoDaLegenda, esperarAjuste } from './legendas.js'
import { carregarTelas, telas, tocarNaTela } from './telas.js'
import { enderecoDoVideo, separarChave } from './tempo.js'
import { voltar } from './voltar.js'

// Player do aparelho. Antes de tocar, o vídeo assume a assinatura (uma conexão só, S5); o
// controle é o do próprio navegador, que já sabe tela cheia, legenda e Picture-in-Picture.
const props = defineProps({
  chave: { type: String, required: true },
  titulo: { type: String, default: '' },
  inicio: { type: Number, default: 0 },
})
const recado = inject('recado')

const video = ref(null)
const erro = ref('')
const outraTela = ref(false)
const escolhendoTela = ref(false)
const enviando = ref('')
const { tipo } = separarChave(props.chave)
const aoVivo = tipo === 'canal'
const guardaProgresso = tipo === 'filme' || tipo === 'episodio' || tipo === 'arquivo' || tipo === 'proprio'
const temLegenda = !aoVivo && tipo !== 'proprio' // meus vídeos (S42) não têm legenda
const nome = computed(() => props.titulo || (aoVivo ? 'Ao vivo' : ''))
const legenda = ref(null) // a escolhida para este vídeo (S21)
const escolhendoLegenda = ref(false)
const semSom = ref(false)

let hls = null
let relogioDoProgresso
let ultimoGuardado = -1

async function assumir() {
  await api('/api/reproducao', { metodo: 'POST', corpo: { chave: props.chave } })
}

async function abrir() {
  erro.value = ''
  outraTela.value = false
  try {
    await assumir()
  } catch (e) {
    erro.value = e.message
    return
  }
  const v = video.value
  const endereco = enderecoDoVideo(props.chave)
  if (aoVivo && !v.canPlayType('application/vnd.apple.mpegurl')) {
    const { default: Hls } = await import('hls.js')
    if (!Hls.isSupported()) {
      erro.value = 'Este navegador não toca canais ao vivo.'
      return
    }
    hls?.destroy()
    hls = new Hls({ lowLatencyMode: false, backBufferLength: 30 })
    hls.on(Hls.Events.ERROR, (_, dados) => dados.fatal && falhou())
    hls.loadSource(endereco)
    hls.attachMedia(v)
  } else {
    v.src = endereco
  }
  v.play().catch(() => {})
}

let retomarEm = null // depois de parar para o servidor puxar o áudio da assinatura
function aoCarregar() {
  const alvo = retomarEm ?? props.inicio
  retomarEm = null
  if (alvo > 0 && !aoVivo) video.value.currentTime = alvo
}

// O vídeo não abriu: ou outro aparelho assumiu a assinatura, ou o formato não toca aqui.
async function falhou() {
  try {
    const r = await fetch(enderecoDoVideo(props.chave), { method: 'HEAD' })
    if (r.status === 409) {
      outraTela.value = true
      erro.value = 'Outro aparelho começou a assistir, e a assinatura só deixa um por vez.'
      return
    }
  } catch {
    erro.value = 'Sem conexão com o servidor de casa.'
    return
  }
  erro.value = aoVivo
    ? 'O canal não abriu. Ele pode estar fora do ar agora.'
    : 'O vídeo não abriu neste aparelho. O formato pode não ser aceito pelo navegador: tente numa tela.'
}

// Guarda onde parou: a cada 10 s, ao pausar, ao sair e quando o app vai para o fundo ou é
// fechado (aí com keepalive, para o pedido sair mesmo com a página indo embora).
async function guardarProgresso(saindo = false) {
  const v = video.value
  if (!guardaProgresso || !v || !v.duration || Math.abs(v.currentTime - ultimoGuardado) < 2) return
  ultimoGuardado = v.currentTime
  const corpo = JSON.stringify({ posicao: v.currentTime, duracao: v.duration })
  try {
    await fetch(`/api/progresso/${props.chave}`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: corpo, keepalive: saindo === true })
  } catch {
    // Sem rede agora: guarda na próxima.
  }
}

function aoEsconder() {
  if (document.visibilityState === 'hidden') guardarProgresso(true)
}

// Vídeo com imagem e sem som: o áudio é de um tipo que o navegador não toca (Dolby, DTS).
function conferirSom() {
  setTimeout(() => {
    const v = video.value
    if (!v || v.paused || v.muted) return
    const semAudioNoChrome = v.webkitAudioDecodedByteCount === 0 && v.webkitVideoDecodedByteCount > 0
    if (semAudioNoChrome || v.mozHasAudio === false) semSom.value = true
  }, 5000)
}

// A trilha da legenda entra pelo <track>; o navegador só a mostra se o modo for "showing".
async function mostrarLegenda() {
  await nextTick()
  for (const t of video.value?.textTracks || []) t.mode = legenda.value && t.label === legenda.value.nome ? 'showing' : 'disabled'
}
watch(legenda, mostrarLegenda)

// Legenda gerada pelo áudio (S29): a trilha recarrega a cada minuto novo. Na assinatura, o vídeo
// para enquanto o servidor puxa o áudio (uma conexão só) e volta sozinho do mesmo ponto.
const puxandoAudio = ref(false)
let pararDeAcompanhar = () => {}
function acompanharLegendaGerada(l) {
  pararDeAcompanhar()
  if (l.geracao?.puxar) soltarParaOAudio()
  pararDeAcompanhar = acompanharGeracao(props.chave, (nova) => {
    if (legenda.value?.id !== nova.id) return
    legenda.value = nova
    if (puxandoAudio.value && (!nova.geracao.puxar || nova.geracao.estado === 'erro')) {
      puxandoAudio.value = false
      abrir()
    }
  })
}

function soltarParaOAudio() {
  const v = video.value
  if (!v || puxandoAudio.value) return
  guardarProgresso()
  retomarEm = v.currentTime
  puxandoAudio.value = true
  v.pause()
  v.removeAttribute('src')
  v.load()
}

async function legendaEscolhida(l) {
  legenda.value = l
  escolhendoLegenda.value = false
  if (l?.geracao) {
    recado(l.geracao.puxar ? 'O vídeo para uns segundos enquanto o servidor puxa o áudio da assinatura.' : 'Gerando a legenda: o primeiro minuto fica pronto em segundos.')
    acompanharLegendaGerada(l)
    return
  }
  if (!l) {
    recado('Sem legenda.')
    return
  }
  if (!l.alinhando) {
    recado('Legenda escolhida.')
    return
  }
  recado('Legenda escolhida. Conferindo a sincronia…')
  const final = await esperarAjuste(props.chave, l)
  if (legenda.value?.id === final?.id) {
    legenda.value = final
    recado(final.ajuste || 'Legenda pronta.')
  }
}

// Sincronizar de novo (S38): o vídeo pausa enquanto o servidor ouve o trecho em volta deste ponto.
async function sincronizarLegenda() {
  escolhendoLegenda.value = false
  const v = video.value
  try {
    let l = await api(`/api/legendas/${props.chave}/sincronizar`, { metodo: 'POST', corpo: { posicao: v?.currentTime || 0 } })
    const tocava = v && !v.paused
    v?.pause()
    recado('Sincronizando a legenda pelo áudio deste ponto…')
    l = await esperarAjuste(props.chave, l)
    legenda.value = l
    recado(l?.ajuste || 'Legenda sincronizada.')
    if (tocava) v.play().catch(() => {})
  } catch (e) {
    recado(e.message, true)
  }
}

async function mandarParaTela(tela) {
  enviando.value = tela.id
  const posicao = aoVivo ? 0 : video.value?.currentTime || 0
  try {
    await guardarProgresso()
    await tocarNaTela(tela, props.chave, { posicao })
    video.value?.pause()
    escolhendoTela.value = false
    location.hash = `#/controle/${encodeURIComponent(tela.id)}`
  } catch (e) {
    recado(e.message, true)
  } finally {
    enviando.value = ''
  }
}

onMounted(() => {
  abrir()
  carregarTelas()
  relogioDoProgresso = setInterval(guardarProgresso, 10000)
  document.addEventListener('visibilitychange', aoEsconder)
  window.addEventListener('pagehide', aoEsconder)
  if (temLegenda) {
    api(`/api/legendas/${props.chave}`)
      .then((r) => {
        legenda.value = r.escolhida
        const g = r.escolhida?.geracao
        if (g && !['pronta', 'parada', 'erro'].includes(g.estado)) acompanharLegendaGerada(r.escolhida)
      })
      .catch(() => {})
  }
})

onBeforeUnmount(() => {
  pararDeAcompanhar()
  clearInterval(relogioDoProgresso)
  document.removeEventListener('visibilitychange', aoEsconder)
  window.removeEventListener('pagehide', aoEsconder)
  guardarProgresso(true)
  hls?.destroy()
  if (video.value) {
    video.value.pause()
    video.value.removeAttribute('src')
    video.value.load()
  }
})
</script>

<template>
  <div class="player">
    <header class="barra">
      <button class="botao icone" aria-label="Voltar" @click="voltar('#/inicio')"><Icone nome="voltar" /></button>
      <span class="titulo">{{ nome }}</span>
      <button v-if="temLegenda" class="botao icone" :class="{ ligada: legenda }" aria-label="Legendas" title="Legendas" @click="escolhendoLegenda = true">
        <Icone nome="legenda" />
      </button>
      <button v-if="telas.length" class="botao icone" aria-label="Assistir numa tela" title="Assistir numa tela" @click="escolhendoTela = true">
        <Icone nome="tela" />
      </button>
    </header>

    <video
      ref="video"
      controls
      playsinline
      autoplay
      preload="auto"
      @loadedmetadata="aoCarregar"
      @playing="conferirSom"
      @pause="guardarProgresso"
      @error="falhou"
    >
      <track
        v-if="legenda"
        :key="enderecoDaLegenda(legenda)"
        kind="subtitles"
        :srclang="legenda.idioma"
        :label="legenda.nome"
        :src="enderecoDaLegenda(legenda)"
        default
        @load="mostrarLegenda"
      />
    </video>

    <div v-if="puxandoAudio" class="sem-som">
      <span>Puxando o áudio da assinatura para gerar a legenda (uns 15 s). O vídeo volta sozinho do mesmo ponto.</span>
    </div>

    <div v-if="semSom && !erro" class="sem-som">
      <span>Sem som neste aparelho: o áudio deste vídeo (Dolby ou DTS) o navegador não toca. Na TV, ele toca com som.</span>
      <button v-if="telas.length" class="botao" @click="escolhendoTela = true"><Icone nome="tela" />Numa tela</button>
      <button class="botao icone" aria-label="Fechar aviso" @click="semSom = false"><Icone nome="fechar" /></button>
    </div>

    <div v-if="erro" class="problema">
      <p>{{ erro }}</p>
      <div class="linha-acoes">
        <button v-if="outraTela" class="botao principal" @click="abrir">Assistir aqui</button>
        <button v-if="telas.length && !outraTela" class="botao" @click="escolhendoTela = true"><Icone nome="tela" />Numa tela</button>
      </div>
    </div>

    <Folha v-if="escolhendoLegenda" titulo="Legendas" @fechar="escolhendoLegenda = false">
      <EscolherLegenda :chave="chave" @escolheu="legendaEscolhida" @ajustou="(l) => (legenda = l)" @sincronizar="sincronizarLegenda" />
    </Folha>

    <Folha v-if="escolhendoTela" titulo="Assistir numa tela" @fechar="escolhendoTela = false">
      <p class="suave explicacao">{{ aoVivo ? 'O canal abre na tela escolhida.' : 'O vídeo continua na tela escolhida, do ponto em que está.' }}</p>
      <ul class="lista">
        <li v-for="t in telas" :key="t.id">
          <button class="escolha" :disabled="!!enviando" @click="mandarParaTela(t)">
            <Icone nome="tela" />
            <span>{{ enviando === t.id ? 'Mandando…' : t.nome }}</span>
            <span v-if="!t.ligada" class="chip">desligada?</span>
          </button>
        </li>
      </ul>
    </Folha>
  </div>
</template>

<style scoped>
.player {
  position: fixed;
  inset: 0;
  display: grid;
  grid-template-rows: auto 1fr;
  background: #000000;
  color: #ffffff;
}

.barra {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: max(8px, env(safe-area-inset-top)) 12px 8px;
}

.barra .botao.icone {
  color: rgb(255 255 255 / 85%);
}

.barra .botao.icone:hover {
  background: rgb(255 255 255 / 12%);
  color: #ffffff;
}

.barra .botao.icone.ligada {
  color: var(--marca);
}

.sem-som {
  position: absolute;
  left: 50%;
  top: max(64px, calc(env(safe-area-inset-top) + 56px));
  display: flex;
  gap: 10px;
  align-items: center;
  width: min(560px, calc(100% - 32px));
  padding: 10px 10px 10px 16px;
  border-radius: var(--raio);
  background: rgb(28 31 36 / 92%);
  font-size: 0.88rem;
  line-height: 1.45;
  transform: translateX(-50%);
}

.sem-som span {
  flex: 1;
}

.sem-som .botao:not(.icone) {
  flex: none;
  border-color: rgb(255 255 255 / 25%);
  color: #ffffff;
}

.sem-som .botao.icone {
  color: rgb(255 255 255 / 75%);
}

.titulo {
  flex: 1;
  overflow: hidden;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

video {
  width: 100%;
  height: 100%;
  min-height: 0;
  background: #000000;
}

.problema {
  position: absolute;
  left: 50%;
  top: 50%;
  width: min(440px, calc(100% - 32px));
  padding: 18px 20px;
  border-radius: var(--raio);
  background: rgb(28 31 36 / 92%);
  transform: translate(-50%, -50%);
  text-align: center;
}

.problema p {
  margin: 0 0 14px;
  line-height: 1.5;
}

.problema .linha-acoes {
  justify-content: center;
}

.problema .botao:not(.principal) {
  border-color: rgb(255 255 255 / 25%);
  color: #ffffff;
}

.explicacao {
  margin: -4px 0 12px;
  font-size: 0.88rem;
}

.escolha {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  min-height: 52px;
  padding: 0;
  border: 0;
  background: none;
  font-weight: 550;
  text-align: left;
  cursor: pointer;
}

.escolha:hover {
  color: var(--marca);
}

.escolha .chip {
  margin-left: auto;
}
</style>
