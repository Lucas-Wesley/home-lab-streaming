// O link do Telegram dentro de um texto: o compartilhar do Android manda o link junto de outras
// palavras, e a área de transferência pode ter espaços. Sem link, devolve o texto como veio.
export function linkDoTelegram(texto) {
  const s = String(texto || '').trim()
  const m = s.match(/(?:https?:\/\/)?(?:t|telegram)\.me\/[^\s<>"']+/i)
  if (!m) return s
  return m[0].startsWith('http') ? m[0] : `https://${m[0]}`
}

// Do endereço do compartilhar (/compartilhar?text=…&url=…), o link que veio.
export function linkCompartilhado(busca) {
  const q = new URLSearchParams(busca)
  return linkDoTelegram([q.get('url'), q.get('text'), q.get('title')].filter(Boolean).join(' '))
}

// O link que identifica o salvo: só o do grupo (https://t.me/c/123) ou o da mensagem. Igual ao
// servidor (linkDoGrupo e linkDaMensagem). Vazio quando não dá.
export function chaveDoSalvo(link, tudo) {
  const m = linkDoTelegram(link).match(/^https?:\/\/(?:t|telegram)\.me\/(c\/\d+|[A-Za-z][A-Za-z0-9_]{3,})((?:\/\d+){0,2})\/?(?:\?.*)?$/)
  if (!m) return ''
  const base = `https://t.me/${m[1]}`
  if (tudo) return base
  const mensagem = m[2].split('/').filter(Boolean).at(-1)
  return mensagem ? `${base}/${mensagem}` : ''
}
