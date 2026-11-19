// Quem é este aparelho (S51): um código guardado no navegador e um nome legível, para o servidor
// dizer ao outro aparelho quem pegou a conexão da assinatura.

export function nomeDoAparelho(ua = '') {
  if (/iPhone/.test(ua)) return 'o iPhone'
  if (/iPad/.test(ua)) return 'o iPad'
  if (/Android/.test(ua)) return /Mobile/.test(ua) ? 'o celular' : 'o tablet'
  if (/Windows/.test(ua)) return 'o computador Windows'
  if (/Macintosh|Mac OS X/.test(ua)) return 'o Mac'
  if (/CrOS/.test(ua)) return 'o Chromebook'
  if (/Linux/.test(ua)) return 'o computador Linux'
  return 'outro navegador'
}

function codigoNovo() {
  if (globalThis.crypto?.randomUUID) return crypto.randomUUID()
  return Array.from({ length: 4 }, () => Math.random().toString(36).slice(2, 10)).join('-')
}

let guardado = null
export function meuAparelho() {
  if (guardado) return guardado
  let id = ''
  try {
    id = localStorage.getItem('aparelho') || ''
    if (!id) {
      id = codigoNovo()
      localStorage.setItem('aparelho', id)
    }
  } catch {
    id = codigoNovo() // navegador sem armazenamento: vale enquanto a página estiver aberta
  }
  guardado = { id, nome: nomeDoAparelho(globalThis.navigator?.userAgent || '') }
  return guardado
}
