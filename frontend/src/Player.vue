<script setup>
import { computed, inject, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import EscolherLegenda from './EscolherLegenda.vue'
import Icone from './Icone.vue'
import OpcoesDoAoVivo from './OpcoesDoAoVivo.vue'
import { api } from './api.js'
import { meuAparelho } from './aparelho.js'
import { acompanharGeracao, enderecoDaLegenda, esperarAjuste } from './legendas.js'
import { ESPERAS, faltaParaOFim, folgaCarregada, passagem, perdeuAConexao, proximaVersao, rotuloDaVersao, textoDeQuemPegou } from './player.js'
import { carregarTelas, telas, tocarNaTela } from './telas.js'
import { enderecoDoVideo, relogio, rotaDoPlayer, separarChave } from './tempo.js'
import { voltar } from './voltar.js'

// Player do aparelho (S51), com controles próprios: barra com o que já carregou, pular 10 s (dois
// toques no celular), velocidade, tela cheia, Picture-in-Picture e atalhos de teclado. Antes de
// tocar, o vídeo assume a assinatura (uma conexão só, S5); se outro aparelho pegar, o player para e
// diz quem foi. Quando a conexão cai, tenta de novo sozinho (4 vezes) e mostra o que está fazendo;
// no ao vivo, as outras versões do canal (FHD, HD, SD, servidores reserva) ficam à mão.
const props = defineProps({
  chave: { type: String, required: true },
  titulo: { type: String, default: '' },
  inicio: { type: Number, default: 0 },
  dupla: { type: Boolean, default: false }, // a versão com a legenda dupla gravada na imagem (S46)
  minimizado: { type: Boolean, default: false }, // fora da rota do player: Picture-in-Picture ou mini player (S53)
})
const emit = defineEmits(['fechar'])
const recado = inject('recado')

const chave = ref(props.chave) // no ao vivo, muda ao trocar de versão
const { tipo } = separarChave(props.chave)
const aoVivo = tipo === 'canal'
const guardaProgresso = tipo === 'filme' || tipo === 'episodio' || tipo === 'arquivo' || tipo === 'proprio'
// Meus vídeos (S42) não têm legenda; a dupla já tem as duas na imagem (S46).
const temLegenda = !aoVivo && tipo !== 'proprio' && !props.dupla
const enderecoDaqui = () => enderecoDoVideo(chave.value) + (props.dupla ? '?dupla=1' : '')
const eu = meuAparelho()
const meuDono = `app:${eu.id}`

const caixa = ref(null)
const video = ref(null)
// conectando | carregando | tocando | pausado | travado | reconectando | erro | perdeu
const estado = ref('conectando')
const mensagem = ref('')
const quemPegou = ref('')
const tentativa = ref(0)
const quedas = ref(0)
const podeOutraTela = ref(false)
const gaveta = ref('') // '' | 'opcoes' | 'legenda' | 'tela'
const enviando = ref('')
const semSom = ref(false)

// Tempo e barra
const tempo = ref(0)
const duracaoTotal = ref(0)
const faixas = ref([]) // o que já carregou, em % da duração
const folga = ref(0)
const arrastando = ref(null) // segundos enquanto arrasta
const dicaDaBarra = ref(null) // { t, x } ao passar o mouse
const atrasado = ref(false)

// Som, velocidade, tela
const volume = ref(lerNumero('volume', 1))
const mudo = ref(false)
const velocidade = ref(1)
const telaCheia = ref(false)
const temPip = typeof document !== 'undefined' && document.pictureInPictureEnabled
const controles = ref(true)
const pulo = ref(null) // { lado, texto } do aviso de "−10 s"

// Ao vivo: versões, níveis do fluxo e estimativa de velocidade
const alternativas = ref(null)
const niveis = ref([])
const nivel = ref(-1)
const banda = ref(0)

const idAtual = computed(() => Number(separarChave(chave.value).id))
const versaoAtual = computed(() => alternativas.value?.versoes?.find((v) => v.id === idAtual.value))
const melhor = computed(() => alternativas.value?.versoes?.[0])
const proxima = computed(() => proximaVersao(alternativas.value?.versoes, idAtual.value))
const nome = computed(() => (aoVivo && alternativas.value?.nome) || props.titulo || (aoVivo ? 'Ao vivo' : ''))
const subtitulo = computed(() => (aoVivo && versaoAtual.value ? rotuloDaVersao(versaoAtual.value) : ''))
const ocupado = computed(() => ['conectando', 'carregando', 'reconectando'].includes(estado.value))
const posicaoVisivel = computed(() => arrastando.value ?? tempo.value)
const pct = (t) => (duracaoTotal.value ? Math.min(100, (t / duracaoTotal.value) * 100) : 0)
const temDuracao = computed(() => !aoVivo && Number.isFinite(duracaoTotal.value) && duracaoTotal.value > 0)

const textoDoEstado = computed(() => {
  if (estado.value === 'conectando') return aoVivo ? 'Abrindo o canal…' : 'Abrindo o vídeo…'
  if (estado.value === 'carregando') return 'Carregando…'
  if (estado.value === 'reconectando') return `A conexão caiu. Tentando de novo (${tentativa.value} de ${ESPERAS.length})…`
  return ''
})

function lerNumero(nomeGuardado, padrao) {
  try {
    const v = Number(localStorage.getItem(nomeGuardado))
    return localStorage.getItem(nomeGuardado) != null && Number.isFinite(v) ? v : padrao
  } catch {
    return padrao
  }
}

let hls = null
let Hls = null
let relogioDoProgresso
let relogioDoDono
let esperaDaTentativa
let esperaDoTravado
let esperaDaRecarga
let esconderControles
let ultimoGuardado = -1
let vigiando = false // este aparelho é o dono da conexão (vídeo da assinatura): confere se continua
let recuperouMidia = false
let saindo = false

// ---------- Abrir, tentar de novo e perder a conexão ----------

async function assumir() {
  await api('/api/reproducao', { metodo: 'POST', corpo: { chave: chave.value, aparelho: eu.id, nome: eu.nome, titulo: nome.value } })
}

async function lerQuem() {
  try {
    return await api('/api/reproducao', { tempo: 5000 })
  } catch {
    return null
  }
}

function soltarVideo() {
  hls?.destroy()
  hls = null
  const v = video.value
  if (v) {
    v.pause()
    v.removeAttribute('src')
    v.load()
  }
}

async function abrir({ outraTentativa = false } = {}) {
  clearTimeout(esperaDaTentativa)
  limparTravado()
  if (!outraTentativa) tentativa.value = 0
  estado.value = outraTentativa ? 'reconectando' : 'conectando'
  mensagem.value = ''
  quemPegou.value = ''
  podeOutraTela.value = false
  try {
    await assumir()
  } catch (e) {
    if (e.status === 0) return falhou()
    return problema(e.message)
  }
  // Só o vídeo da assinatura disputa a conexão; torrent e baixado não (o servidor nem troca o dono).
  lerQuem().then((q) => (vigiando = q?.dono === meuDono))
  const v = video.value
  if (!v || saindo) return
  const endereco = enderecoDaqui()
  if (aoVivo && !v.canPlayType('application/vnd.apple.mpegurl')) {
    Hls ??= (await import('hls.js')).default
    if (!Hls.isSupported()) return problema('Este navegador não toca canais ao vivo.')
    hls?.destroy()
    recuperouMidia = false
    // Mais folga que o padrão: fica uns 4 pedaços atrás do ao vivo e guarda até 40 s à frente, para
    // aguentar uma oscilação da internet sem travar.
    hls = new Hls({
      lowLatencyMode: false,
      backBufferLength: 30,
      liveSyncDurationCount: 4,
      liveMaxLatencyDurationCount: 12,
      maxBufferLength: 40,
      maxMaxBufferLength: 90,
      fragLoadingMaxRetry: 6,
      fragLoadingRetryDelay: 1000,
      fragLoadingMaxRetryTimeout: 8000,
      levelLoadingMaxRetry: 4,
      manifestLoadingMaxRetry: 2,
    })
    hls.on(Hls.Events.ERROR, (_, d) => {
      if (!d.fatal) return
      if (d.type === Hls.ErrorTypes.MEDIA_ERROR && !recuperouMidia) {
        recuperouMidia = true
        hls.recoverMediaError()
        return
      }
      falhou()
    })
    hls.on(Hls.Events.MANIFEST_PARSED, (_, d) => {
      niveis.value = (d.levels || []).map((l, i) => ({ i, altura: l.height, bitrate: l.bitrate }))
    })
    hls.on(Hls.Events.FRAG_LOADED, () => (banda.value = hls?.bandwidthEstimate || 0))
    hls.loadSource(endereco)
    hls.attachMedia(v)
    if (nivel.value >= 0) hls.currentLevel = nivel.value
  } else {
    v.src = endereco
  }
  v.play().catch(() => pausadoSemQuerer())
}

// O navegador não deixou tocar sozinho (autoplay): mostra o botão de tocar em vez de "Abrindo…".
function pausadoSemQuerer() {
  const v = video.value
  if (v?.paused && ['conectando', 'reconectando', 'carregando'].includes(estado.value) && v.readyState >= 2) estado.value = 'pausado'
}

let retomarEm = null // ponto para voltar depois de abrir de novo
function aoCarregar() {
  const v = video.value
  duracaoTotal.value = v.duration
  const alvo = retomarEm ?? props.inicio
  retomarEm = null
  if (alvo > 0 && !aoVivo) v.currentTime = alvo
  v.playbackRate = velocidade.value
}

function problema(texto) {
  limparTravado()
  estado.value = 'erro'
  mensagem.value = texto
}

function perdeu(quem) {
  guardarProgresso()
  vigiando = false
  clearTimeout(esperaDaTentativa)
  limparTravado()
  soltarVideo()
  estado.value = 'perdeu'
  quemPegou.value = textoDeQuemPegou(quem, chave.value)
}

// O vídeo não abriu ou caiu: outro aparelho pegou a conexão, o servidor de casa sumiu, o formato
// não toca aqui, ou foi só a internet (aí tenta de novo, com esperas cada vez maiores).
async function falhou() {
  if (saindo || estado.value === 'perdeu' || estado.value === 'erro') return
  const v = video.value
  if (!aoVivo && v?.error?.code === 4 && v.currentTime < 1) {
    podeOutraTela.value = true
    return problema('O vídeo não abriu neste aparelho. O formato pode não ser aceito pelo navegador: tente numa tela.')
  }
  const quem = await lerQuem()
  if (quem && perdeuAConexao(quem, meuDono, chave.value) && (vigiando || quem.chave !== chave.value)) return perdeu(quem)
  if (!quem) {
    // Nem o servidor de casa respondeu: é a rede deste aparelho.
    mensagem.value = 'Sem conexão com o servidor de casa (Wi-Fi?).'
  }
  if (tentativa.value < ESPERAS.length) {
    quedas.value++
    if (!aoVivo && v?.currentTime) retomarEm = v.currentTime
    const espera = ESPERAS[tentativa.value++]
    estado.value = 'reconectando'
    soltarVideo()
    esperaDaTentativa = setTimeout(() => abrir({ outraTentativa: true }), espera)
    return
  }
  if (!quem) return problema('Sem conexão com o servidor de casa: confira o Wi-Fi deste aparelho.')
  problema(
    aoVivo
      ? `O canal não voltou depois de ${ESPERAS.length} tentativas. Esta versão pode ter caído no provedor: tente outra.`
      : 'O vídeo não voltou depois de algumas tentativas. Pode ser o provedor ou este arquivo.',
  )
}

// Confere a cada 5 s se a conexão ainda é deste aparelho (só o que é da assinatura).
async function conferirDono() {
  if (!vigiando || document.hidden || ['perdeu', 'erro'].includes(estado.value)) return
  const quem = await lerQuem()
  if (!quem || !vigiando) return
  if (perdeuAConexao(quem, meuDono, chave.value)) return perdeu(quem)
  // O servidor reiniciou e o primeiro pedido virou "app": volta a ser deste aparelho, em silêncio.
  if (quem.dono === 'app' && quem.chave === chave.value) assumir().catch(() => {})
}

function recarregar() {
  guardarProgresso()
  if (!aoVivo && video.value?.currentTime) retomarEm = video.value.currentTime
  soltarVideo()
  abrir()
}

function assistirAquiDeNovo() {
  if (!aoVivo && retomarEm == null) retomarEm = tempo.value
  abrir()
}

// ---------- Travando ----------

function limparTravado() {
  clearTimeout(esperaDoTravado)
  clearTimeout(esperaDaRecarga)
}

function aoEsperar() {
  if (['perdeu', 'erro', 'reconectando', 'conectando'].includes(estado.value)) return
  estado.value = 'carregando'
  limparTravado()
  // 10 s parado: avisa com as saídas; 25 s: abre de novo sozinho (o ao vivo pode ter saído da janela).
  esperaDoTravado = setTimeout(async () => {
    if (estado.value !== 'carregando') return
    const quem = vigiando ? await lerQuem() : null
    if (quem && perdeuAConexao(quem, meuDono, chave.value)) return perdeu(quem)
    estado.value = 'travado'
    quedas.value++
  }, 10000)
  esperaDaRecarga = setTimeout(() => {
    if (estado.value === 'travado') recarregar()
  }, 25000)
}

function aoTocar() {
  limparTravado()
  estado.value = 'tocando'
  tentativa.value = 0
  mensagem.value = ''
  conferirSom()
  mostrarControles()
}

function aoPausar() {
  if (estado.value === 'tocando') estado.value = 'pausado'
  controles.value = true
  guardarProgresso()
}

// ---------- Tempo, barra e ao vivo ----------

function aoAndar() {
  const v = video.value
  if (!v) return
  tempo.value = v.currentTime
  folga.value = folgaCarregada(v.buffered, v.currentTime)
  if (aoVivo) {
    const borda = hls?.liveSyncPosition ?? (v.seekable.length ? v.seekable.end(v.seekable.length - 1) - 10 : null)
    atrasado.value = borda != null && v.currentTime < borda - 15
  }
}

function aoCarregarMais() {
  const v = video.value
  if (!v || !temDuracao.value) return
  const lista = []
  for (let i = 0; i < v.buffered.length; i++) lista.push({ de: pct(v.buffered.start(i)), ate: pct(v.buffered.end(i)) })
  faixas.value = lista
  folga.value = folgaCarregada(v.buffered, v.currentTime)
}

function irAoVivo() {
  const v = video.value
  if (!v) return
  const borda = hls?.liveSyncPosition ?? (v.seekable.length ? v.seekable.end(v.seekable.length - 1) - 6 : null)
  if (borda != null) v.currentTime = borda
  v.play().catch(() => {})
}

function irPara(t) {
  const v = video.value
  if (!v || !temDuracao.value) return
  v.currentTime = Math.max(0, Math.min(duracaoTotal.value - 1, t))
  tempo.value = v.currentTime
}

function pular(segundos, lado) {
  if (aoVivo) return
  irPara((video.value?.currentTime || 0) + segundos)
  pulo.value = { lado: lado || (segundos < 0 ? 'esquerda' : 'direita'), texto: `${segundos > 0 ? '+' : '−'}${Math.abs(segundos)} s` }
  clearTimeout(pular.espera)
  pular.espera = setTimeout(() => (pulo.value = null), 650)
}

const trilho = ref(null)
function tempoDoPonteiro(e) {
  const r = trilho.value.getBoundingClientRect()
  const x = Math.min(Math.max(e.clientX - r.left, 0), r.width)
  return { t: (x / r.width) * duracaoTotal.value, x }
}

function comecarArraste(e) {
  if (!temDuracao.value) return
  trilho.value.setPointerCapture(e.pointerId)
  arrastando.value = tempoDoPonteiro(e).t
}

function moverNaBarra(e) {
  if (!temDuracao.value) return
  const p = tempoDoPonteiro(e)
  if (arrastando.value != null) arrastando.value = p.t
  dicaDaBarra.value = e.pointerType === 'mouse' || arrastando.value != null ? p : null
  mostrarControles()
}

function soltarArraste() {
  if (arrastando.value == null) return
  irPara(arrastando.value)
  arrastando.value = null
  dicaDaBarra.value = null
}

// ---------- Tocar, som, tela cheia ----------

function alternarPlay() {
  const v = video.value
  if (!v || ['perdeu', 'erro'].includes(estado.value)) return
  if (v.paused) v.play().catch(() => {})
  else v.pause()
}

function mudarVolume(valor) {
  volume.value = Math.min(1, Math.max(0, valor))
  mudo.value = volume.value === 0
  try {
    localStorage.setItem('volume', String(volume.value))
  } catch {
    // sem armazenamento: vale só agora
  }
}
watch([volume, mudo], () => {
  if (!video.value) return
  video.value.volume = volume.value
  video.value.muted = mudo.value
})

function mudarVelocidade(v) {
  velocidade.value = v
  if (video.value) video.value.playbackRate = v
}

async function alternarTelaCheia() {
  try {
    if (document.fullscreenElement) {
      await document.exitFullscreen()
      return
    }
    // A página inteira (o player já a cobre): assim a tela cheia continua no próximo episódio.
    const el = document.documentElement
    if (el.requestFullscreen) {
      await el.requestFullscreen({ navigationUI: 'hide' })
      // No celular, tela cheia é deitado.
      await screen.orientation?.lock?.('landscape').catch(() => {})
    } else {
      video.value?.webkitEnterFullscreen?.()
    }
  } catch {
    // o navegador recusou: segue como está
  }
}

function aoMudarTelaCheia() {
  telaCheia.value = !!document.fullscreenElement
  if (!telaCheia.value) screen.orientation?.unlock?.()
}

async function alternarPip() {
  try {
    if (document.pictureInPictureElement) await document.exitPictureInPicture()
    else await video.value?.requestPictureInPicture()
  } catch (e) {
    recado('Picture-in-Picture não abriu neste navegador.', true)
  }
}

// ---------- Controles que somem ----------

// Somem depois de 3 s com o mouse parado; no celular, 5 s depois do toque (dá tempo de achar o
// botão). Tocar em qualquer controle conta o tempo de novo.
let ultimoPonteiro = 'mouse'
function mostrarControles() {
  controles.value = true
  clearTimeout(esconderControles)
  esconderControles = setTimeout(() => {
    if (estado.value === 'tocando' && !gaveta.value && arrastando.value == null && !proximoNaTela.value) controles.value = false
  }, ultimoPonteiro === 'mouse' ? 3000 : 5000)
}

// Só o mouse mostra os controles ao mexer: no celular, o toque também solta um "pointermove", e
// aí o mesmo toque mostrava e escondia os controles na hora (S52).
function aoMexer(e) {
  if (e.pointerType === 'mouse') {
    ultimoPonteiro = 'mouse'
    mostrarControles()
  }
}

function aoTocarNosControles(e) {
  ultimoPonteiro = e.pointerType || 'mouse'
  mostrarControles()
}

// Toque no vídeo: no celular, mostra e esconde os controles, e dois toques nas laterais pulam 10 s;
// com o mouse, um clique pausa e dois põem em tela cheia.
let ultimoToque = { quando: 0, lado: '' }
let cliqueAdiado
function tocouNoPalco(e) {
  if (e.target !== e.currentTarget && e.target !== video.value) return
  if (props.minimizado) return expandir()
  const r = e.currentTarget.getBoundingClientRect()
  const x = (e.clientX - r.left) / r.width
  const lado = x < 0.35 ? 'esquerda' : x > 0.65 ? 'direita' : 'meio'
  const agora = Date.now()
  const duplo = agora - ultimoToque.quando < 320 && ultimoToque.lado === lado
  ultimoToque = { quando: duplo ? 0 : agora, lado }
  ultimoPonteiro = e.pointerType || 'mouse'
  if (e.pointerType === 'mouse') {
    clearTimeout(cliqueAdiado)
    if (duplo) return alternarTelaCheia()
    cliqueAdiado = setTimeout(alternarPlay, 230)
    mostrarControles()
    return
  }
  if (duplo && lado !== 'meio' && !aoVivo) {
    pular(lado === 'esquerda' ? -10 : 10, lado)
    mostrarControles()
    return
  }
  if (controles.value && estado.value === 'tocando') {
    controles.value = false
    clearTimeout(esconderControles)
  } else mostrarControles()
}

function aoTeclar(e) {
  if (props.minimizado || gaveta.value || e.target.closest?.('input, textarea, select, [contenteditable]')) return
  const teclas = {
    ' ': alternarPlay,
    k: alternarPlay,
    ArrowLeft: () => pular(-10),
    ArrowRight: () => pular(10),
    j: () => pular(-10),
    l: () => pular(10),
    ArrowUp: () => mudarVolume(volume.value + 0.1),
    ArrowDown: () => mudarVolume(volume.value - 0.1),
    m: () => (mudo.value = !mudo.value),
    f: alternarTelaCheia,
  }
  const acao = teclas[e.key]
  if (!acao) return
  e.preventDefault()
  acao()
  mostrarControles()
}

// ---------- Versões do canal ----------

async function carregarAlternativas() {
  try {
    alternativas.value = await api(`/api/canais/${idAtual.value}/alternativas`, { tempo: 10000 })
  } catch {
    alternativas.value = null
  }
}

function trocarVersao(v) {
  gaveta.value = ''
  soltarVideo()
  chave.value = `canal:${v.id}`
  nivel.value = -1
  niveis.value = []
  history.replaceState(null, '', rotaDoPlayer(chave.value, { titulo: v.outroCanal ? v.nome : props.titulo }))
  if (v.outroCanal) carregarAlternativas()
  recado(v.outroCanal ? `Abrindo ${v.nome}.` : `Trocando para ${rotuloDaVersao(v)}.`)
  abrir()
}

function trocarNivel(i) {
  nivel.value = i
  if (hls) hls.currentLevel = i
}

// ---------- Sair sem parar: Picture-in-Picture e mini player (S53) ----------

const emPip = ref(false)
let foiParaTela = false

// O "voltar" do player é um toque: o navegador só abre o Picture-in-Picture com um toque. Pelo
// voltar do celular (sem toque), o vídeo vai para o mini player no canto do app.
function sair() {
  const v = video.value
  if (temPip && v && !v.paused && !document.pictureInPictureElement && ['tocando', 'carregando', 'travado'].includes(estado.value)) {
    v.requestPictureInPicture().catch(() => {})
  }
  voltar('#/inicio')
}

function expandir() {
  location.hash = rotaDoPlayer(props.chave, { titulo: props.titulo, dupla: props.dupla })
}

function fechar() {
  guardarProgresso()
  if (document.pictureInPictureElement) document.exitPictureInPicture().catch(() => {})
  emit('fechar')
}

function aoEntrarNoPip() {
  emPip.value = true
}

// Fechou a janelinha: no X o navegador pausa o vídeo, e aí ele para de vez; no "voltar para o app"
// o vídeo segue tocando, e o player abre de novo.
function aoSairDoPip() {
  emPip.value = false
  if (!props.minimizado) return
  if (video.value?.paused) fechar()
  else expandir()
}

watch(
  () => props.minimizado,
  async (mini) => {
    if (!mini) {
      if (document.pictureInPictureElement) document.exitPictureInPicture().catch(() => {})
      mostrarControles()
      return
    }
    gaveta.value = ''
    // Sem o que continuar (erro, outro aparelho, mandou para a tela, acabou): fecha.
    if (foiParaTela || ['erro', 'perdeu'].includes(estado.value) || video.value?.ended) return emit('fechar')
    if (document.fullscreenElement) await document.exitFullscreen().catch(() => {})
  },
)

// Minimizado, o player não insiste: problema que pararia a tela fecha com um recado.
watch(estado, (e) => {
  if (!props.minimizado) return
  if (e === 'perdeu') {
    recado(quemPegou.value, true)
    fechar()
  } else if (e === 'erro') {
    recado(mensagem.value || 'O vídeo parou.', true)
    fechar()
  }
})

// Com o app indo para o fundo, o Chrome pode abrir o Picture-in-Picture sozinho (onde houver).
function pedirPipAutomatico() {
  try {
    navigator.mediaSession?.setActionHandler?.('enterpictureinpicture', () => video.value?.requestPictureInPicture().catch(() => {}))
  } catch {
    // navegador sem essa ação
  }
}

// ---------- Próximo episódio (S52) ----------

const proximo = ref(null) // { id, temporada, numero, titulo, posicao } e o nome da série
const nomeDaSerie = ref('')
const contagem = ref(0) // segundos até ir sozinho, depois que o episódio acaba
const dispensado = ref(false)
let relogioDaContagem

async function carregarProximo() {
  if (tipo !== 'episodio') return
  try {
    const r = await api(`/api/episodios/${separarChave(props.chave).id}/proximo`)
    proximo.value = r.proximo
    nomeDaSerie.value = r.serie
  } catch {
    proximo.value = null
  }
}

// O cartão aparece nos últimos 40 s (os créditos), ou no fim.
const proximoNaTela = computed(() => !!proximo.value && !dispensado.value && temDuracao.value && faltaParaOFim(tempo.value, duracaoTotal.value) <= 40)
const rotuloDoProximo = computed(() => (proximo.value ? `T${proximo.value.temporada} E${proximo.value.numero}${proximo.value.titulo ? ` · ${proximo.value.titulo}` : ''}` : ''))

function irParaOProximo() {
  const p = proximo.value
  if (!p) return
  clearInterval(relogioDaContagem)
  guardarProgresso()
  passagem.telaCheia = !!document.fullscreenElement
  const titulo = nomeDaSerie.value ? `${nomeDaSerie.value} T${p.temporada} E${p.numero}` : `T${p.temporada} E${p.numero}`
  location.hash = rotaDoPlayer(`episodio:${p.id}`, { titulo, posicao: p.posicao || 0 })
}

function dispensarProximo() {
  dispensado.value = true
  clearInterval(relogioDaContagem)
  contagem.value = 0
}

function aoAcabar() {
  controles.value = true
  if (!proximo.value || dispensado.value || props.minimizado) return
  contagem.value = 10
  clearInterval(relogioDaContagem)
  relogioDaContagem = setInterval(() => {
    if (--contagem.value <= 0) irParaOProximo()
  }, 1000)
}

// ---------- Progresso ----------

// Guarda onde parou: a cada 10 s, ao pausar, ao sair e quando o app vai para o fundo ou é
// fechado (aí com keepalive, para o pedido sair mesmo com a página indo embora).
async function guardarProgresso(saindoDaPagina = false) {
  const v = video.value
  if (!guardaProgresso || !v || !v.duration || Math.abs(v.currentTime - ultimoGuardado) < 2) return
  ultimoGuardado = v.currentTime
  const corpo = JSON.stringify({ posicao: v.currentTime, duracao: v.duration })
  try {
    await fetch(`/api/progresso/${props.chave}`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: corpo, keepalive: saindoDaPagina === true })
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

// ---------- Legendas ----------

const legenda = ref(null) // a escolhida para este vídeo (S21)

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
  vigiando = false // o servidor vai usar a conexão por trás: não é outro aparelho
  soltarVideo()
}

async function legendaEscolhida(l) {
  legenda.value = l
  gaveta.value = ''
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
  gaveta.value = ''
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

// ---------- Telas ----------

async function mandarParaTela(tela) {
  enviando.value = tela.id
  const posicao = aoVivo ? 0 : video.value?.currentTime || 0
  try {
    await guardarProgresso()
    vigiando = false // a tela vai assumir: não é para avisar que "outro aparelho pegou"
    foiParaTela = true
    await tocarNaTela(tela, chave.value, { posicao, dupla: props.dupla })
    video.value?.pause()
    gaveta.value = ''
    location.hash = `#/controle/${encodeURIComponent(tela.id)}`
  } catch (e) {
    recado(e.message, true)
  } finally {
    enviando.value = ''
  }
}

const SITUACAO = { conectando: 'Abrindo…', carregando: 'Carregando…', tocando: 'Tocando', pausado: 'Pausado', travado: 'Travando', reconectando: 'Reconectando', erro: 'Parado', perdeu: 'Com outro aparelho' }
const VELOCIDADES = [0.75, 1, 1.25, 1.5, 1.75, 2]
const mbps = computed(() => (banda.value ? `${(banda.value / 1e6).toFixed(1).replace('.', ',')} Mb/s` : ''))

watch(gaveta, (g) => {
  if (g) controles.value = true
  else mostrarControles()
})

onMounted(() => {
  if (aoVivo) carregarAlternativas()
  carregarProximo()
  pedirPipAutomatico()
  abrir()
  carregarTelas()
  relogioDoProgresso = setInterval(guardarProgresso, 10000)
  relogioDoDono = setInterval(conferirDono, 5000)
  document.addEventListener('visibilitychange', aoEsconder)
  document.addEventListener('fullscreenchange', aoMudarTelaCheia)
  window.addEventListener('pagehide', aoEsconder)
  window.addEventListener('keydown', aoTeclar)
  if (video.value) {
    video.value.volume = volume.value
  }
  mostrarControles()
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
  saindo = true
  pararDeAcompanhar()
  clearInterval(relogioDoProgresso)
  clearInterval(relogioDoDono)
  clearTimeout(esperaDaTentativa)
  clearTimeout(esconderControles)
  clearTimeout(cliqueAdiado)
  clearInterval(relogioDaContagem)
  limparTravado()
  document.removeEventListener('visibilitychange', aoEsconder)
  document.removeEventListener('fullscreenchange', aoMudarTelaCheia)
  window.removeEventListener('pagehide', aoEsconder)
  window.removeEventListener('keydown', aoTeclar)
  guardarProgresso(true)
  if (document.fullscreenElement && !passagem.telaCheia) document.exitFullscreen().catch(() => {})
  passagem.telaCheia = false
  soltarVideo()
})
</script>

<template>
  <div ref="caixa" class="player" :class="{ 'ao-vivo': aoVivo, 'sem-controles': !controles, 'tela-cheia': telaCheia, mini: minimizado && !emPip, escondido: minimizado && emPip }" @pointermove="aoMexer">
    <div class="palco" @pointerup="tocouNoPalco">
      <video
        ref="video"
        playsinline
        autoplay
        preload="auto"
        @loadedmetadata="aoCarregar"
        @canplay="pausadoSemQuerer"
        @durationchange="duracaoTotal = video.duration"
        @playing="aoTocar"
        @pause="aoPausar"
        @waiting="aoEsperar"
        @timeupdate="aoAndar"
        @progress="aoCarregarMais"
        @error="falhou"
        @ended="aoAcabar"
        @enterpictureinpicture="aoEntrarNoPip"
        @leavepictureinpicture="aoSairDoPip"
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

      <!-- O que está acontecendo, no meio do vídeo -->
      <div v-if="ocupado" class="meio" role="status" aria-live="polite">
        <span class="girando" aria-hidden="true"></span>
        <span class="texto-do-estado">{{ textoDoEstado }}</span>
        <span v-if="estado === 'reconectando' && mensagem" class="suave-escuro">{{ mensagem }}</span>
      </div>
      <button v-else-if="estado === 'pausado' && controles" class="grande" aria-label="Tocar" @click="alternarPlay"><Icone nome="tocar" /></button>
      <div v-if="pulo" class="pulo" :class="pulo.lado" aria-hidden="true">{{ pulo.texto }}</div>

      <!-- Mini player no canto (S53): tocar no vídeo abre de novo; o X para. -->
      <div v-if="minimizado" class="mini-controles">
        <button class="botao icone" :aria-label="video && !video.paused ? 'Pausar' : 'Tocar'" @click.stop="alternarPlay"><Icone :nome="video && !video.paused ? 'pausar' : 'tocar'" /></button>
        <button class="botao icone" aria-label="Abrir o player" @click.stop="expandir"><Icone nome="ampliar" /></button>
        <button v-if="temPip" class="botao icone" aria-label="Picture-in-Picture" @click.stop="alternarPip"><Icone nome="pip" /></button>
        <button class="botao icone" aria-label="Fechar o vídeo" @click.stop="fechar"><Icone nome="fechar" /></button>
      </div>

      <!-- Próximo episódio: nos créditos, no canto; no fim, conta 10 s e vai sozinho. -->
      <div v-if="proximoNaTela" class="proximo-episodio" role="status">
        <p class="rotulo-do-proximo">Próximo episódio</p>
        <p class="nome-do-proximo">{{ rotuloDoProximo }}</p>
        <div class="linha-acoes">
          <button class="botao principal" @click="irParaOProximo">
            <Icone nome="tocar" />{{ contagem > 0 ? `Assistir em ${contagem} s` : 'Assistir agora' }}
          </button>
          <button class="botao" @click="dispensarProximo">{{ contagem > 0 ? 'Cancelar' : 'Agora não' }}</button>
        </div>
      </div>

      <!-- Avisos discretos no alto -->
      <div v-if="puxandoAudio" class="aviso-no-alto">
        <span>Puxando o áudio da assinatura para gerar a legenda (uns 15 s). O vídeo volta sozinho do mesmo ponto.</span>
      </div>
      <div v-if="semSom && estado !== 'erro'" class="aviso-no-alto">
        <span>Sem som neste aparelho: o áudio deste vídeo (Dolby ou DTS) o navegador não toca. Na TV, ele toca com som.</span>
        <button v-if="telas.length" class="botao" @click="gaveta = 'tela'"><Icone nome="tela" />Numa tela</button>
        <button class="botao icone" aria-label="Fechar aviso" @click="semSom = false"><Icone nome="fechar" /></button>
      </div>

      <!-- Controles -->
      <header class="topo controles-do-player" @pointerdown="aoTocarNosControles">
        <button class="botao icone" aria-label="Voltar" @click="sair"><Icone nome="voltar" /></button>
        <div class="nome-do-video">
          <span class="titulo">{{ nome }}</span>
          <span v-if="subtitulo" class="subtitulo">
            {{ subtitulo }}
            <button v-if="melhor && versaoAtual && melhor.id !== versaoAtual.id" class="melhor" @click="trocarVersao(melhor)">{{ rotuloDaVersao(melhor) }} disponível</button>
          </span>
        </div>
        <button v-if="temLegenda" class="botao icone" :class="{ ligada: legenda }" aria-label="Legendas" title="Legendas" @click="gaveta = 'legenda'">
          <Icone nome="legenda" />
        </button>
        <button v-if="telas.length" class="botao icone" aria-label="Assistir numa tela" title="Assistir numa tela" @click="gaveta = 'tela'">
          <Icone nome="tela" />
        </button>
        <button class="botao icone" :aria-label="aoVivo ? 'Qualidade e outros canais' : 'Opções'" :title="aoVivo ? 'Qualidade e outros canais' : 'Opções'" @click="gaveta = 'opcoes'">
          <Icone nome="qualidade" />
        </button>
      </header>

      <footer class="base controles-do-player" @pointerdown="aoTocarNosControles">
        <div
          v-if="temDuracao"
          ref="trilho"
          class="trilho"
          role="slider"
          aria-label="Posição no vídeo"
          :aria-valuemin="0"
          :aria-valuemax="Math.round(duracaoTotal)"
          :aria-valuenow="Math.round(posicaoVisivel)"
          :aria-valuetext="relogio(posicaoVisivel)"
          @pointerdown="comecarArraste"
          @pointermove="moverNaBarra"
          @pointerup="soltarArraste"
          @pointercancel="soltarArraste"
          @pointerleave="arrastando == null && (dicaDaBarra = null)"
        >
          <div class="trilho-fundo">
            <div v-for="(f, i) in faixas" :key="i" class="carregado" :style="{ left: `${f.de}%`, width: `${f.ate - f.de}%` }"></div>
            <div class="tocado" :style="{ width: `${pct(posicaoVisivel)}%` }"></div>
          </div>
          <div class="bolinha" :style="{ left: `${pct(posicaoVisivel)}%` }"></div>
          <div v-if="dicaDaBarra" class="dica-de-tempo" :style="{ left: `${dicaDaBarra.x}px` }">{{ relogio(dicaDaBarra.t) }}</div>
        </div>

        <div class="linha-de-controles">
          <button class="botao icone" :aria-label="estado === 'tocando' || estado === 'carregando' || estado === 'travado' ? 'Pausar' : 'Tocar'" @click="alternarPlay">
            <Icone :nome="video && !video.paused ? 'pausar' : 'tocar'" />
          </button>
          <template v-if="!aoVivo">
            <button class="botao icone" aria-label="Voltar 10 segundos" title="Voltar 10 s (←)" @click="pular(-10)"><Icone nome="menos10" /></button>
            <button class="botao icone" aria-label="Avançar 10 segundos" title="Avançar 10 s (→)" @click="pular(10)"><Icone nome="mais10" /></button>
          </template>
          <div class="volume">
            <button class="botao icone" :aria-label="mudo ? 'Ligar o som' : 'Tirar o som'" @click="mudo = !mudo"><Icone :nome="mudo || volume === 0 ? 'mudo' : 'volume'" /></button>
            <input type="range" min="0" max="1" step="0.05" :value="mudo ? 0 : volume" aria-label="Volume" @input="mudarVolume(Number($event.target.value))" />
          </div>
          <span v-if="temDuracao" class="tempos">{{ relogio(posicaoVisivel) }} <span class="fraco">/ {{ relogio(duracaoTotal) }}</span></span>
          <button v-if="aoVivo" class="selo-ao-vivo" :class="{ atrasado }" :disabled="!atrasado" :title="atrasado ? 'Ir para o ao vivo' : 'No ao vivo'" @click="irAoVivo">
            <span class="ponto"></span>{{ atrasado ? 'Voltar ao vivo' : 'Ao vivo' }}
          </button>
          <span class="espaco"></span>
          <button v-if="proximo" class="botao icone" :aria-label="`Próximo episódio: ${rotuloDoProximo}`" :title="`Próximo: ${rotuloDoProximo}`" @click="irParaOProximo"><Icone nome="proximo" /></button>
          <button v-if="velocidade !== 1" class="botao fino" title="Velocidade" @click="gaveta = 'opcoes'">{{ String(velocidade).replace('.', ',') }}×</button>
          <button class="botao icone" aria-label="Atualizar" title="Atualizar (abre o vídeo de novo)" @click="recarregar"><Icone nome="atualizar" /></button>
          <button v-if="temPip" class="botao icone" aria-label="Picture-in-Picture" title="Picture-in-Picture" @click="alternarPip"><Icone nome="pip" /></button>
          <button class="botao icone" :aria-label="telaCheia ? 'Sair da tela cheia' : 'Tela cheia'" :title="telaCheia ? 'Sair da tela cheia (F)' : 'Tela cheia (F)'" @click="alternarTelaCheia">
            <Icone :nome="telaCheia ? 'reduzir' : 'ampliar'" />
          </button>
        </div>
      </footer>
    </div>

    <!-- Problemas: sobre o vídeo; no celular em pé, logo abaixo dele, sem cobrir a imagem. -->
    <div v-if="['perdeu', 'erro', 'travado'].includes(estado)" class="cartoes">
        <div v-if="estado === 'perdeu'" class="cartao" role="alert">
          <p class="titulo-do-cartao">Outro aparelho pegou a assinatura</p>
          <p>{{ quemPegou }} A assinatura só aceita um por vez, por isso este parou.</p>
          <div class="linha-acoes">
            <button class="botao principal" @click="assistirAquiDeNovo"><Icone nome="tocar" />Assistir aqui</button>
            <button class="botao" @click="voltar('#/inicio')">Voltar</button>
          </div>
          <p class="nota-do-cartao">Assistir aqui para o outro aparelho.</p>
        </div>

        <div v-else-if="estado === 'erro'" class="cartao" role="alert">
          <p class="titulo-do-cartao">{{ aoVivo ? 'O canal não abriu' : 'O vídeo não abriu' }}</p>
          <p>{{ mensagem }}</p>
          <div class="linha-acoes">
            <button class="botao principal" @click="abrir()"><Icone nome="atualizar" />Tentar de novo</button>
            <button v-if="proxima" class="botao" @click="trocarVersao(proxima)">Ir para {{ rotuloDaVersao(proxima) }}</button>
            <button v-if="aoVivo && (alternativas?.irmaos?.length || alternativas?.versoes?.length > 2)" class="botao" @click="gaveta = 'opcoes'">Outras opções</button>
            <button v-if="!aoVivo && podeOutraTela && telas.length" class="botao" @click="gaveta = 'tela'"><Icone nome="tela" />Numa tela</button>
          </div>
        </div>

        <div v-else-if="estado === 'travado'" class="cartao travado" role="status">
          <p class="titulo-do-cartao">Está travando</p>
          <p>
            O vídeo parou de chegar.
            {{ folga < 1 ? 'Pode ser a internet ou esta versão no provedor.' : '' }}
            Abro de novo sozinho em alguns segundos.
          </p>
          <div class="linha-acoes">
            <button class="botao principal" @click="recarregar"><Icone nome="atualizar" />Atualizar agora</button>
            <button v-if="proxima" class="botao" @click="trocarVersao(proxima)">Ir para {{ rotuloDaVersao(proxima) }}</button>
          </div>
        </div>
    </div>

    <!-- No celular em pé, o espaço embaixo do vídeo mostra as versões do canal (S51). -->
    <section v-if="aoVivo" class="painel-ao-vivo">
      <p class="situacao">
        <span class="ponto" :class="estado"></span>
        {{ SITUACAO[estado] }}<template v-if="estado === 'reconectando'"> ({{ tentativa }} de {{ ESPERAS.length }})</template>
        <span v-if="estado === 'tocando'" class="fraco">· {{ Math.round(folga) }} s carregados{{ mbps ? ` · ${mbps}` : '' }}</span>
        <button class="botao fino" @click="recarregar"><Icone nome="atualizar" />Atualizar</button>
      </p>
      <OpcoesDoAoVivo :alternativas="alternativas" :atual="idAtual" :niveis="niveis" :nivel="nivel" @versao="trocarVersao" @nivel="trocarNivel" />
    </section>

    <!-- Gaveta: fica dentro do player para aparecer também em tela cheia. -->
    <div v-if="gaveta" class="veu-do-player" @click.self="gaveta = ''">
      <section class="gaveta" role="dialog" :aria-label="gaveta === 'legenda' ? 'Legendas' : gaveta === 'tela' ? 'Assistir numa tela' : 'Opções'">
        <header>
          <h2>{{ gaveta === 'legenda' ? 'Legendas' : gaveta === 'tela' ? 'Assistir numa tela' : aoVivo ? 'Qualidade e canais' : 'Opções' }}</h2>
          <button class="botao icone" aria-label="Fechar" @click="gaveta = ''"><Icone nome="fechar" /></button>
        </header>

        <template v-if="gaveta === 'opcoes'">
          <OpcoesDoAoVivo v-if="aoVivo" :alternativas="alternativas" :atual="idAtual" :niveis="niveis" :nivel="nivel" @versao="trocarVersao" @nivel="trocarNivel" />
          <template v-else>
            <p class="rotulo-da-gaveta">Velocidade</p>
            <div class="pilulas">
              <button v-for="v in VELOCIDADES" :key="v" class="pilula" :class="{ ativa: velocidade === v }" @click="mudarVelocidade(v)">{{ String(v).replace('.', ',') }}×</button>
            </div>
          </template>
          <p class="rotulo-da-gaveta">Conexão</p>
          <p class="diagnostico">
            {{ Math.round(folga) }} s carregados à frente<template v-if="mbps"> · {{ mbps }}</template>
            <template v-if="quedas"> · {{ quedas }} {{ quedas === 1 ? 'queda' : 'quedas' }}</template>
          </p>
          <p class="atalhos">Atalhos: espaço pausa, ← → pulam 10 s, ↑ ↓ volume, M tira o som, F tela cheia. No celular, dois toques nas laterais pulam 10 s.</p>
        </template>

        <EscolherLegenda v-else-if="gaveta === 'legenda'" :chave="chave" @escolheu="legendaEscolhida" @ajustou="(l) => (legenda = l)" @sincronizar="sincronizarLegenda" />

        <template v-else-if="gaveta === 'tela'">
          <p class="explicacao">{{ aoVivo ? 'O canal abre na tela escolhida.' : 'O vídeo continua na tela escolhida, do ponto em que está.' }}</p>
          <ul class="lista-de-telas">
            <li v-for="t in telas" :key="t.id">
              <button class="escolha" :disabled="!!enviando" @click="mandarParaTela(t)">
                <Icone nome="tela" />
                <span>{{ enviando === t.id ? 'Mandando…' : t.nome }}</span>
                <span v-if="!t.ligada" class="chip">desligada?</span>
              </button>
            </li>
          </ul>
        </template>
      </section>
    </div>
  </div>
</template>

<style scoped>
/* O player é sempre escuro: as cores do design system são trocadas aqui dentro. */
.player {
  --fundo: #121418;
  --superficie: rgb(255 255 255 / 8%);
  --superficie-forte: rgb(255 255 255 / 14%);
  --texto: #f3f4f6;
  --texto-suave: rgb(255 255 255 / 72%);
  --texto-fraco: rgb(255 255 255 / 52%);
  --linha: rgb(255 255 255 / 10%);
  --linha-forte: rgb(255 255 255 / 18%);
  --marca: #f5a65b;
  --marca-fundo: rgb(245 166 91 / 16%);

  position: fixed;
  inset: 0;
  display: grid;
  grid-template-areas: 'palco';
  grid-template-rows: minmax(0, 1fr);
  background: #000000;
  color: var(--texto);
  user-select: none;
}

.palco {
  grid-area: palco;
  position: relative;
  min-height: 0;
  overflow: hidden;
  touch-action: manipulation;
}

video {
  display: block;
  width: 100%;
  height: 100%;
  background: #000000;
}

video::cue {
  background: rgb(0 0 0 / 55%);
  font-size: clamp(1rem, 2.6vw, 1.6rem);
  line-height: 1.35;
}

.sem-controles,
.sem-controles video {
  cursor: none;
}

/* ---------- Controles ---------- */

.controles-do-player {
  position: absolute;
  right: 0;
  left: 0;
  z-index: 2;
  transition: opacity 0.25s ease;
}

.sem-controles .controles-do-player {
  opacity: 0;
  pointer-events: none;
}

.botao.icone {
  color: rgb(255 255 255 / 88%);
}

.botao.icone:hover {
  background: rgb(255 255 255 / 12%);
  color: #ffffff;
}

.botao.icone.ligada {
  color: var(--marca);
}

.topo {
  top: 0;
  display: flex;
  gap: 4px;
  align-items: center;
  padding: max(8px, env(safe-area-inset-top)) max(10px, env(safe-area-inset-right)) 28px max(10px, env(safe-area-inset-left));
  background: linear-gradient(rgb(0 0 0 / 70%), transparent);
}

.nome-do-video {
  display: grid;
  flex: 1;
  min-width: 0;
  padding-left: 4px;
}

.titulo {
  overflow: hidden;
  font-weight: 600;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.subtitulo {
  display: flex;
  gap: 8px;
  align-items: center;
  color: var(--texto-suave);
  font-size: 0.78rem;
}

.melhor {
  padding: 1px 8px;
  border: 1px solid color-mix(in srgb, var(--marca) 60%, transparent);
  border-radius: 999px;
  background: none;
  color: var(--marca);
  font: inherit;
  cursor: pointer;
}

.base {
  bottom: 0;
  padding: 36px max(12px, env(safe-area-inset-right)) max(8px, env(safe-area-inset-bottom)) max(12px, env(safe-area-inset-left));
  background: linear-gradient(transparent, rgb(0 0 0 / 75%));
}

.linha-de-controles {
  display: flex;
  gap: 2px;
  align-items: center;
}

.espaco {
  flex: 1;
}

.tempos {
  margin-left: 8px;
  font-size: 0.85rem;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.fraco {
  color: var(--texto-fraco);
}

.botao.fino {
  min-height: 30px;
  padding: 0 10px;
  border-color: rgb(255 255 255 / 25%);
  background: transparent;
  color: #ffffff;
  font-size: 0.8rem;
}

.botao.fino svg {
  width: 15px;
  height: 15px;
}

/* Barra do tempo: o que já carregou mais claro, o que já passou na cor do app. */
.trilho {
  position: relative;
  height: 22px;
  margin: 0 6px 2px;
  cursor: pointer;
  touch-action: none;
}

.trilho-fundo {
  position: absolute;
  top: 9px;
  right: 0;
  left: 0;
  height: 4px;
  overflow: hidden;
  border-radius: 2px;
  background: rgb(255 255 255 / 22%);
  transition: height 0.12s ease, top 0.12s ease;
}

.trilho:hover .trilho-fundo {
  top: 8px;
  height: 6px;
}

.carregado {
  position: absolute;
  top: 0;
  bottom: 0;
  background: rgb(255 255 255 / 38%);
}

.tocado {
  position: absolute;
  top: 0;
  bottom: 0;
  left: 0;
  background: var(--marca);
}

.bolinha {
  position: absolute;
  top: 5px;
  width: 12px;
  height: 12px;
  margin-left: -6px;
  border-radius: 50%;
  background: var(--marca);
  box-shadow: 0 0 0 3px rgb(0 0 0 / 25%);
}

.dica-de-tempo {
  position: absolute;
  bottom: 24px;
  padding: 2px 7px;
  border-radius: 6px;
  background: rgb(0 0 0 / 80%);
  font-size: 0.78rem;
  font-variant-numeric: tabular-nums;
  transform: translateX(-50%);
  pointer-events: none;
}

.volume {
  display: flex;
  align-items: center;
}

.volume input {
  width: 0;
  opacity: 0;
  accent-color: var(--marca);
  transition: width 0.2s ease, opacity 0.2s ease;
}

.volume:hover input,
.volume:focus-within input {
  width: 80px;
  opacity: 1;
}

/* No celular o volume é o do aparelho: fica só o botão de tirar o som. */
@media (pointer: coarse) {
  .volume input {
    display: none;
  }
}

.selo-ao-vivo {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  margin-left: 8px;
  padding: 3px 10px;
  border: 0;
  border-radius: 999px;
  background: rgb(255 255 255 / 10%);
  color: #ffffff;
  font-size: 0.72rem;
  font-weight: 650;
  letter-spacing: 0.05em;
  text-transform: uppercase;
}

.selo-ao-vivo .ponto {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #ef4444;
}

.selo-ao-vivo.atrasado {
  cursor: pointer;
}

.selo-ao-vivo.atrasado .ponto {
  background: rgb(255 255 255 / 45%);
}

.selo-ao-vivo.atrasado:hover {
  background: rgb(255 255 255 / 20%);
}

/* ---------- Meio do vídeo ---------- */

.meio {
  position: absolute;
  inset: 0;
  z-index: 1;
  display: grid;
  place-content: center;
  justify-items: center;
  gap: 12px;
  padding: 20px;
  text-align: center;
  pointer-events: none;
}

.girando {
  width: 44px;
  height: 44px;
  border: 3px solid rgb(255 255 255 / 20%);
  border-top-color: var(--marca);
  border-radius: 50%;
  animation: girar 0.9s linear infinite;
}

@keyframes girar {
  to {
    transform: rotate(360deg);
  }
}

.texto-do-estado {
  padding: 4px 12px;
  border-radius: 999px;
  background: rgb(0 0 0 / 55%);
  font-size: 0.92rem;
}

.suave-escuro {
  color: var(--texto-suave);
  font-size: 0.82rem;
}

.grande {
  position: absolute;
  top: 50%;
  left: 50%;
  z-index: 1;
  display: grid;
  place-items: center;
  width: 72px;
  height: 72px;
  border: 0;
  border-radius: 50%;
  background: rgb(0 0 0 / 50%);
  color: #ffffff;
  transform: translate(-50%, -50%);
  cursor: pointer;
}

.grande svg {
  width: 34px;
  height: 34px;
  margin-left: 4px;
}

.pulo {
  position: absolute;
  top: 50%;
  z-index: 1;
  padding: 10px 18px;
  border-radius: 999px;
  background: rgb(0 0 0 / 55%);
  font-weight: 650;
  transform: translateY(-50%);
  pointer-events: none;
}

.pulo.esquerda {
  left: 12%;
}

.pulo.direita {
  right: 12%;
}

/* ---------- Próximo episódio ---------- */

.proximo-episodio {
  position: absolute;
  right: max(16px, env(safe-area-inset-right));
  bottom: 96px;
  z-index: 3;
  width: min(320px, calc(100% - 32px));
  padding: 14px 16px;
  border: 1px solid var(--linha);
  border-radius: var(--raio);
  background: rgb(22 24 29 / 94%);
}

.proximo-episodio p {
  margin: 0;
}

.rotulo-do-proximo {
  color: var(--texto-fraco);
  font-size: 0.75rem;
  font-weight: 650;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.nome-do-proximo {
  margin: 2px 0 12px !important;
  font-weight: 600;
  line-height: 1.35;
}

.proximo-episodio .botao:not(.principal) {
  border-color: rgb(255 255 255 / 25%);
  background: transparent;
  color: #ffffff;
}

/* ---------- Cartões de problema ---------- */

.cartoes {
  grid-area: cartao;
}

.cartao {
  position: absolute;
  top: 50%;
  left: 50%;
  z-index: 4;
  width: min(460px, calc(100% - 32px));
  padding: 18px 20px;
  border: 1px solid var(--linha);
  border-radius: var(--raio);
  background: rgb(22 24 29 / 94%);
  transform: translate(-50%, -50%);
  text-align: center;
}

.cartao p {
  margin: 0 0 14px;
  line-height: 1.5;
}

.titulo-do-cartao {
  margin-bottom: 6px !important;
  font-size: 1.05rem;
  font-weight: 650;
}

.nota-do-cartao {
  margin: 10px 0 0 !important;
  color: var(--texto-fraco);
  font-size: 0.8rem;
}

.cartao .linha-acoes {
  justify-content: center;
}

.cartao .botao:not(.principal),
.aviso-no-alto .botao:not(.icone) {
  border-color: rgb(255 255 255 / 25%);
  background: transparent;
  color: #ffffff;
}

/* Travando não cobre o vídeo: fica no alto, e o vídeo volta sozinho se puder. */
.cartao.travado {
  top: max(70px, calc(env(safe-area-inset-top) + 60px));
  transform: translateX(-50%);
}

.aviso-no-alto {
  position: absolute;
  top: max(64px, calc(env(safe-area-inset-top) + 56px));
  left: 50%;
  z-index: 3;
  display: flex;
  gap: 10px;
  align-items: center;
  width: min(560px, calc(100% - 32px));
  padding: 10px 10px 10px 16px;
  border-radius: var(--raio);
  background: rgb(22 24 29 / 92%);
  font-size: 0.88rem;
  line-height: 1.45;
  transform: translateX(-50%);
}

.aviso-no-alto span {
  flex: 1;
}

/* ---------- Painel do ao vivo (celular em pé) ---------- */

.painel-ao-vivo {
  grid-area: painel;
  display: none;
  overflow: auto;
  padding: 14px 16px calc(20px + env(safe-area-inset-bottom));
  border-top: 1px solid var(--linha);
  background: var(--fundo);
}

.situacao {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 8px;
  align-items: center;
  margin: 0 0 16px;
  font-size: 0.9rem;
  font-weight: 550;
}

.situacao .botao {
  margin-left: auto;
}

.situacao .ponto {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: var(--texto-fraco);
}

.situacao .ponto.tocando {
  background: #22c55e;
}

.situacao .ponto.travado,
.situacao .ponto.erro,
.situacao .ponto.perdeu {
  background: #ef4444;
}

.situacao .ponto.reconectando,
.situacao .ponto.carregando,
.situacao .ponto.conectando {
  background: var(--marca);
}

@media (orientation: portrait) and (max-width: 760px) {
  .player.ao-vivo:not(.tela-cheia) {
    grid-template-areas: 'palco' 'cartao' 'painel';
    grid-template-rows: auto auto minmax(0, 1fr);
  }

  .player.ao-vivo:not(.tela-cheia) .palco {
    aspect-ratio: 16 / 9;
    margin-top: env(safe-area-inset-top);
  }

  .player.ao-vivo:not(.tela-cheia) .topo {
    padding-top: 6px;
  }

  .player.ao-vivo:not(.tela-cheia) .painel-ao-vivo {
    display: block;
  }

  .player.ao-vivo:not(.tela-cheia) .cartao {
    position: static;
    width: auto;
    margin: 12px 16px 0;
    padding: 14px 16px;
    font-size: 0.9rem;
    transform: none;
  }
}

/* ---------- Mini player e Picture-in-Picture (S53) ---------- */

.mini-controles {
  display: none;
}

.player.mini {
  inset: auto max(12px, env(safe-area-inset-right)) calc(84px + env(safe-area-inset-bottom)) auto;
  z-index: 40;
  display: block;
  width: min(300px, 62vw);
  aspect-ratio: 16 / 9;
  overflow: hidden;
  border-radius: var(--raio);
  box-shadow: 0 8px 28px rgb(0 0 0 / 45%);
}

@media (min-width: 821px) {
  .player.mini {
    bottom: 24px;
    width: 340px;
  }
}

.player.mini .palco {
  height: 100%;
  aspect-ratio: auto !important;
  margin: 0 !important;
}

.player.mini .controles-do-player,
.player.mini .cartoes,
.player.mini .painel-ao-vivo,
.player.mini .proximo-episodio,
.player.mini .aviso-no-alto,
.player.mini .pulo,
.player.mini .grande,
.player.mini .texto-do-estado,
.player.mini .suave-escuro {
  display: none !important;
}

.player.mini .girando {
  width: 28px;
  height: 28px;
}

.player.mini .mini-controles {
  position: absolute;
  right: 0;
  bottom: 0;
  left: 0;
  z-index: 2;
  display: flex;
  justify-content: space-around;
  padding: 18px 4px 2px;
  background: linear-gradient(transparent, rgb(0 0 0 / 70%));
}

.player.mini .mini-controles .botao.icone {
  width: 36px;
  height: 36px;
}

/* No Picture-in-Picture, o vídeo continua no DOM, mas fora de vista. */
.player.escondido {
  inset: auto auto 0 0;
  width: 2px;
  height: 2px;
  overflow: hidden;
  opacity: 0;
  pointer-events: none;
}

/* ---------- Gaveta ---------- */

.veu-do-player {
  position: absolute;
  inset: 0;
  z-index: 10;
  display: flex;
  align-items: flex-end;
  justify-content: center;
  background: rgb(0 0 0 / 45%);
}

.gaveta {
  width: min(560px, 100%);
  max-height: 80%;
  overflow: auto;
  padding: 14px 20px calc(20px + env(safe-area-inset-bottom));
  border: 1px solid var(--linha);
  border-bottom: 0;
  border-radius: var(--raio-g) var(--raio-g) 0 0;
  background: var(--fundo);
  user-select: text;
}

/* No computador (ou deitado), a gaveta fica do lado, sem cobrir o vídeo inteiro. */
@media (min-width: 900px), (orientation: landscape) and (max-height: 560px) {
  .veu-do-player {
    align-items: stretch;
    justify-content: flex-end;
    background: rgb(0 0 0 / 30%);
  }

  .gaveta {
    width: min(400px, 100%);
    max-height: none;
    border: 0;
    border-left: 1px solid var(--linha);
    border-radius: 0;
  }
}

.gaveta header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}

.gaveta h2 {
  margin: 0;
  font-size: 1.05rem;
  font-weight: 650;
}

.rotulo-da-gaveta {
  margin: 18px 0 6px;
  font-size: 0.95rem;
  font-weight: 650;
}

.rotulo-da-gaveta:first-of-type {
  margin-top: 0;
}

.pilulas {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.diagnostico,
.atalhos,
.explicacao {
  margin: 0;
  color: var(--texto-suave);
  font-size: 0.85rem;
  line-height: 1.5;
}

.atalhos {
  margin-top: 14px;
  color: var(--texto-fraco);
  font-size: 0.78rem;
}

.explicacao {
  margin-bottom: 10px;
}

.lista-de-telas {
  margin: 0;
  padding: 0;
  list-style: none;
}

.escolha {
  display: flex;
  gap: 12px;
  align-items: center;
  width: 100%;
  min-height: 52px;
  padding: 0;
  border: 0;
  background: none;
  color: inherit;
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
