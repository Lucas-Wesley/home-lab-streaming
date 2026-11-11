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

// Resposta que chega aos poucos (uma linha JSON por vez, S36): cada linha vai para aoReceber assim
// que chega. Termina quando o servidor fecha, ou com o sinal (a página saiu).
export async function aosPoucos(caminho, aoReceber, { tempo = 100000, sinal } = {}) {
  const sinais = [AbortSignal.timeout(tempo), sinal].filter(Boolean)
  let resposta
  try {
    resposta = await fetch(caminho, { signal: AbortSignal.any(sinais) })
  } catch {
    throw new ErroApi('Sem conexão com o servidor.', 0)
  }
  if (!resposta.ok) {
    const dados = await resposta.json().catch(() => null)
    throw new ErroApi(dados?.erro || `Erro ${resposta.status}`, resposta.status, dados)
  }
  const leitor = resposta.body.pipeThrough(new TextDecoderStream()).getReader()
  let resto = ''
  try {
    for (;;) {
      const { value, done } = await leitor.read()
      if (done) break
      const linhas = (resto + value).split('\n')
      resto = linhas.pop()
      for (const l of linhas) if (l.trim()) aoReceber(JSON.parse(l))
    }
  } catch (e) {
    if (sinal?.aborted) return
    throw new ErroApi(e instanceof SyntaxError ? 'Resposta estranha do servidor.' : 'A busca demorou demais.', 0)
  }
  if (resto.trim()) aoReceber(JSON.parse(resto))
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
