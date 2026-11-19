import { describe, expect, it } from 'vitest'
import { acharPasta, aulasPedidas, montarArvore, oQueFalta, soDoDisco } from './pastas.js'

// Dados inventados: um grupo de curso salvo com 3 aulas (uma baixada) e uma pasta posta à mão.
const videos = [
  { id: 7, pasta: 'Grupo/Curso A/Módulo 1', nome: 'Aula 1', tamanho: 100, duracao: 60 },
  { id: 9, pasta: 'Viagens', nome: 'Chile', tamanho: 50, duracao: 30 },
]
const salvos = [
  {
    tudo: true,
    link: 'https://t.me/c/1',
    pasta: 'Grupo',
    titulo: 'Grupo no Telegram',
    videos: [
      { mensagem: 2, nome: 'Aula 1', caminho: ['Curso A', 'Módulo 1'], tamanho: 100, ja: true, video: 7 },
      { mensagem: 3, nome: 'Aula 2', caminho: ['Curso A', 'Módulo 1'], tamanho: 200 },
      { mensagem: 4, nome: 'Outra', caminho: ['Curso B'], tamanho: 300 },
    ],
  },
  { tudo: false, link: 'https://t.me/c/1/9', titulo: 'Vídeo solto', videos: [{ mensagem: 9 }] },
]

describe('montarArvore', () => {
  const raiz = montarArvore(videos, salvos)

  it('junta o catálogo e o disco, na ordem das mensagens', () => {
    expect(raiz.pastas.map((p) => p.nome)).toEqual(['Grupo', 'Viagens'])
    const grupo = acharPasta(raiz, 'Grupo')
    expect(grupo.pastas.map((p) => p.nome)).toEqual(['Curso A', 'Curso B'])
    expect(grupo).toMatchObject({ total: 3, baixadas: 1, tamanho: 100, falta: 500 })
  })

  it('a aula baixada toca; a outra baixa', () => {
    const modulo = acharPasta(raiz, 'Grupo/Curso A/Módulo 1')
    expect(modulo.aulas.map((a) => [a.nome, a.video?.id || null])).toEqual([
      ['Aula 1', 7],
      ['Aula 2', null],
    ])
    expect(soDoDisco(modulo)).toBe(false)
    expect(soDoDisco(acharPasta(raiz, 'Viagens'))).toBe(true)
  })

  it('o vídeo baixado não aparece duas vezes', () => {
    expect(raiz.total).toBe(4)
  })

  it('caminho que não existe', () => {
    expect(acharPasta(raiz, 'Grupo/Nada')).toBeNull()
  })
})

describe('oQueFalta e aulasPedidas', () => {
  const raiz = montarArvore(videos, salvos)

  it('um pedido por grupo, com as mensagens que faltam', () => {
    expect(oQueFalta(acharPasta(raiz, 'Grupo'))).toEqual([{ link: 'https://t.me/c/1', grupo: 'Grupo', mensagens: [3, 4], tamanho: 500 }])
    expect(oQueFalta(acharPasta(raiz, 'Viagens'))).toEqual([])
  })

  it('marca as aulas na fila', () => {
    const marcadas = aulasPedidas([
      { tudo: true, estado: 'baixando', link: 'https://t.me/c/1', mensagens: [3] },
      { tudo: true, estado: 'pronto', link: 'https://t.me/c/1', mensagens: [4] },
      { tudo: true, estado: 'na fila', link: 'https://t.me/c/2' },
    ])
    expect([...marcadas]).toEqual(['https://t.me/c/1#3', 'https://t.me/c/2#*'])
  })
})
