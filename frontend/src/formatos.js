// Se este aparelho toca o som e a imagem de um vídeo, pelos formatos que o servidor leu (S23).
// Quem responde é o próprio navegador (canPlayType): o Chrome do Mac toca H.265, o de um celular
// pode não tocar; nenhum Chrome toca o som Dolby e DTS.
const VIDEO = {
  h264: 'video/mp4; codecs="avc1.640028"',
  hevc: 'video/mp4; codecs="hvc1.1.6.L120.90"',
  av1: 'video/mp4; codecs="av01.0.08M.08"',
  vp9: 'video/webm; codecs="vp9"',
  vp8: 'video/webm; codecs="vp8"',
}
const AUDIO = {
  aac: 'audio/mp4; codecs="mp4a.40.2"',
  mp3: 'audio/mpeg',
  opus: 'audio/webm; codecs="opus"',
  vorbis: 'audio/webm; codecs="vorbis"',
  flac: 'audio/flac',
  eac3: 'audio/mp4; codecs="ec-3"',
  ac3: 'audio/mp4; codecs="ac-3"',
}

let video
function tocaPadrao(tipo) {
  video ??= document.createElement('video')
  return video.canPlayType(tipo) !== ''
}

// noAparelho devolve o que não vai funcionar aqui, ou null se tudo toca (ou se não se sabe).
export function noAparelho(formatos, toca = tocaPadrao) {
  if (!formatos?.video) return null
  const tipoDoVideo = VIDEO[formatos.video]
  const semImagem = !tipoDoVideo || !toca(tipoDoVideo)
  const audios = formatos.audios || []
  const semSom = audios.length > 0 && !audios.some((a) => AUDIO[a.codec] && toca(AUDIO[a.codec]))
  if (!semImagem && !semSom) return null
  const texto = semImagem && semSom ? 'Não toca neste aparelho' : semImagem ? 'Sem imagem neste aparelho' : 'Sem som neste aparelho'
  return { semImagem, semSom, texto }
}

// Nome do idioma de uma trilha (o ffprobe dá o código ISO 639-2, "por", ou o de 2 letras, "pt").
const IDIOMAS = {
  por: 'Português', pt: 'Português', 'pt-br': 'Português', eng: 'Inglês', en: 'Inglês', spa: 'Espanhol', es: 'Espanhol',
  fre: 'Francês', fra: 'Francês', fr: 'Francês', ita: 'Italiano', it: 'Italiano', ger: 'Alemão', deu: 'Alemão', de: 'Alemão',
  jpn: 'Japonês', ja: 'Japonês', kor: 'Coreano', ko: 'Coreano', chi: 'Chinês', zho: 'Chinês', zh: 'Chinês', rus: 'Russo', ru: 'Russo',
  hin: 'Hindi', hi: 'Hindi', ara: 'Árabe', ar: 'Árabe', pol: 'Polonês', pl: 'Polonês', tur: 'Turco', tr: 'Turco', dut: 'Holandês', nld: 'Holandês',
}

export function nomeDoIdioma(codigo) {
  const c = (codigo || '').toLowerCase()
  if (!c || ['und', 'unk', 'mis', 'zxx', '?'].includes(c)) return 'Idioma não informado'
  return IDIOMAS[c] || c.toUpperCase()
}

const CANAIS = { 1: 'mono', 2: 'estéreo', 6: '5.1', 8: '7.1' }
const CODECS = { aac: 'AAC', ac3: 'Dolby Digital', eac3: 'Dolby Digital Plus', truehd: 'Dolby TrueHD', dts: 'DTS', mp3: 'MP3', opus: 'Opus', flac: 'FLAC' }

// resumo: o que a folha de assistir mostra de um vídeo. O áudio vem primeiro e por idioma (é o que
// decide entre a assinatura, quase sempre dublada, e uma versão com o original).
export function resumoDosFormatos(f) {
  if (!f) return null
  const audios = (f.audios || []).map((a) => ({
    idioma: nomeDoIdioma(a.idioma),
    ouvido: !!a.ouvido, // o arquivo não dizia; o servidor reconheceu pelo som
    detalhe: [CODECS[a.codec] || (a.codec || '').toUpperCase(), CANAIS[a.canais] || (a.canais ? `${a.canais} canais` : '')].filter(Boolean).join(' '),
  }))
  const altura = f.altura || 0
  const resolucao = altura >= 2000 ? '4K' : altura >= 1000 ? '1080p' : altura >= 700 ? '720p' : altura ? `${altura}p` : ''
  const video = [resolucao, { h264: 'H.264', hevc: 'H.265', av1: 'AV1', mpeg2video: 'MPEG-2' }[f.video] || (f.video || '').toUpperCase()].filter(Boolean).join(' ')
  const legendas = [...new Set((f.legendas || []).map(nomeDoIdioma))]
  const minutos = Math.round((f.duracao || 0) / 60)
  const duracao = minutos ? (minutos >= 60 ? `${Math.floor(minutos / 60)} h ${minutos % 60} min` : `${minutos} min`) : ''
  return { audios, video, legendas, duracao }
}
