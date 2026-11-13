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
