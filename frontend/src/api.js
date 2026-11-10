// Chamadas à API. Erros viram exceções com a mensagem que o servidor mandou.
export class ErroApi extends Error {
  constructor(mensagem, status, dados) {
    super(mensagem)
    this.status = status
    this.dados = dados
  }
}

export async function api(caminho, { metodo = 'GET', corpo, tempo = 15000 } = {}) {
  let resposta
  try {
    resposta = await fetch(caminho, {
      method: metodo,
      headers: corpo === undefined ? {} : { 'Content-Type': 'application/json' },
      body: corpo === undefined ? undefined : JSON.stringify(corpo),
      signal: AbortSignal.timeout(tempo),
    })
  } catch {
    throw new ErroApi('Sem conexão com o servidor.', 0)
  }
  if (resposta.status === 204) return null
  const dados = await resposta.json().catch(() => null)
  if (!resposta.ok) throw new ErroApi(dados?.erro || `Erro ${resposta.status}`, resposta.status, dados)
  return dados
}

// Guardar a última escolha (categoria, temporada) é conveniência: sem localStorage, segue sem.
export function lembrar(chave, valor) {
  try {
    if (valor === undefined) return localStorage.getItem(`streaming:${chave}`) || ''
    localStorage.setItem(`streaming:${chave}`, valor)
  } catch {
    return ''
  }
}
