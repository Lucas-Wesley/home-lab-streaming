// Árvore de Meus vídeos (S55): o que está no disco junto com o catálogo dos grupos salvos do
// Telegram. Assim a estrutura de um curso (curso › módulo › aula) aparece inteira antes de baixar,
// com as aulas baixadas (tocam) e as que faltam (baixam).

function novaPasta(nome, caminho) {
  return { nome, caminho, pastas: [], aulas: [], porNome: new Map(), total: 0, baixadas: 0, tamanho: 0, falta: 0 }
}

function pastaDe(raiz, caminho) {
  let atual = raiz
  caminho.forEach((nome, i) => {
    let filha = atual.porNome.get(nome)
    if (!filha) {
      filha = novaPasta(nome, caminho.slice(0, i + 1))
      atual.porNome.set(nome, filha)
      atual.pastas.push(filha)
    }
    atual = filha
  })
  return atual
}

function somar(pasta) {
  let total = pasta.aulas.length
  let baixadas = 0
  let tamanho = 0
  let falta = 0
  for (const a of pasta.aulas) {
    if (a.video) {
      baixadas++
      tamanho += a.video.tamanho || 0
    } else falta += a.tamanho || 0
  }
  for (const p of pasta.pastas) {
    somar(p)
    total += p.total
    baixadas += p.baixadas
    tamanho += p.tamanho
    falta += p.falta
  }
  Object.assign(pasta, { total, baixadas, tamanho, falta })
  return pasta
}

// videos: os de /api/meus-videos; salvos: os grupos salvos (cada vídeo com caminho e, se baixado,
// o id em `video`). A ordem é a das mensagens; pastas só do disco vêm depois, em ordem alfabética.
export function montarArvore(videos = [], salvos = []) {
  const raiz = novaPasta('', [])
  const porId = new Map(videos.map((v) => [v.id, v]))
  const doCatalogo = new Set()
  for (const s of salvos) {
    if (!s.tudo) continue
    const grupo = s.pasta || s.titulo
    if (!grupo) continue
    for (const v of s.videos || []) {
      const video = (v.video && porId.get(v.video)) || null
      if (video) doCatalogo.add(video.id)
      pastaDe(raiz, [grupo, ...(v.caminho || [])]).aulas.push({
        chave: `${s.link}#${v.mensagem}`,
        nome: v.nome,
        mensagem: v.mensagem,
        link: s.link,
        grupo,
        tamanho: v.tamanho || 0,
        duracao: v.duracao || video?.duracao || 0,
        video,
      })
    }
  }
  const soDoDisco = videos.filter((v) => !doCatalogo.has(v.id))
  soDoDisco.sort((a, b) => a.pasta.localeCompare(b.pasta, 'pt-BR'))
  for (const v of soDoDisco) {
    pastaDe(raiz, v.pasta.split('/')).aulas.push({ chave: `video:${v.id}`, nome: v.nome, tamanho: v.tamanho || 0, duracao: v.duracao || 0, video: v })
  }
  return somar(raiz)
}

// A pasta do caminho ("Curso/Módulo"), ou null se não existe.
export function acharPasta(raiz, caminho) {
  let atual = raiz
  for (const nome of String(caminho || '').split('/').filter(Boolean)) {
    atual = atual?.porNome.get(nome)
    if (!atual) return null
  }
  return atual
}

// As aulas que faltam baixar na pasta e nas subpastas, agrupadas pelo grupo do Telegram (um
// pedido por grupo).
export function oQueFalta(pasta) {
  const porLink = new Map()
  const juntar = (p) => {
    for (const a of p.aulas) {
      if (a.video || !a.link) continue
      if (!porLink.has(a.link)) porLink.set(a.link, { link: a.link, grupo: a.grupo, mensagens: [], tamanho: 0 })
      const pedido = porLink.get(a.link)
      pedido.mensagens.push(a.mensagem)
      pedido.tamanho += a.tamanho
    }
    p.pastas.forEach(juntar)
  }
  juntar(pasta)
  return [...porLink.values()]
}

// As aulas na fila ou baixando agora: "link#mensagem". Pedido do grupo todo (sem mensagens) marca
// o grupo inteiro, como "link#*".
export function aulasPedidas(pedidos = []) {
  const marcadas = new Set()
  for (const p of pedidos) {
    if (p.estado !== 'na fila' && p.estado !== 'baixando') continue
    if (!p.tudo) continue
    if (!p.mensagens?.length) marcadas.add(`${p.link}#*`)
    else p.mensagens.forEach((m) => marcadas.add(`${p.link}#${m}`))
  }
  return marcadas
}

// Pasta só com vídeos postos à mão ou baixados um a um (sem catálogo): mostra em grade, com capa.
export const soDoDisco = (pasta) => pasta.aulas.every((a) => !a.link)
