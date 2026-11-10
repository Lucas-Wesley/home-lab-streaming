// "1:02:03" ou "12:34", como num player.
export function relogio(segundos) {
  const s = Math.max(0, Math.floor(segundos || 0))
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const resto = String(s % 60).padStart(2, '0')
  return h ? `${h}:${String(m).padStart(2, '0')}:${resto}` : `${m}:${resto}`
}

// "1 h 45 min" ou "45 min", para a duração de um filme.
export function duracao(segundos) {
  const min = Math.round((segundos || 0) / 60)
  if (!min) return ''
  const h = Math.floor(min / 60)
  return h ? `${h} h${min % 60 ? ` ${min % 60} min` : ''}` : `${min} min`
}

// Chave do vídeo: "filme:123", "episodio:456", "canal:789".
export function separarChave(chave) {
  const [tipo, id] = String(chave).split(':')
  return { tipo, id }
}

// Endereço do vídeo no servidor. Canais vão em HLS; filmes e episódios, no arquivo inteiro com Range.
export function enderecoDoVideo(chave) {
  const { tipo, id } = separarChave(chave)
  return tipo === 'canal' ? `/api/video/canal/${id}.m3u8` : `/api/video/${tipo}/${id}.mp4`
}

export function rotaDoPlayer(chave, { titulo = '', posicao = 0 } = {}) {
  const q = new URLSearchParams()
  if (titulo) q.set('titulo', titulo)
  if (posicao > 0) q.set('t', String(Math.floor(posicao)))
  const extra = q.toString()
  return `#/assistir/${chave}${extra ? `?${extra}` : ''}`
}
