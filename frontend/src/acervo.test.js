import { describe, expect, it } from 'vitest'
import { andando, ehDocumentario, estadoDoEnvio, lancados } from './acervo.js'

describe('acervo', () => {
  it('mostra a etapa enquanto vai e o estado no fim', () => {
    expect(estadoDoEnvio({ estado: 'enviando', etapa: 'Tirando o áudio original…' }).rotulo).toBe('Tirando o áudio original…')
    expect(estadoDoEnvio({ estado: 'enviando' }).rotulo).toBe('Indo para o Acervo…')
    expect(estadoDoEnvio({ estado: 'enviado', etapa: 'x' })).toMatchObject({ rotulo: 'No Acervo', classe: 'ok' })
    expect(estadoDoEnvio({ estado: 'falhou' }).classe).toBe('erro')
    expect(estadoDoEnvio(null)).toBe(null)
  })
  it('reconhece documentário pelo gênero do TMDB', () => {
    expect(ehDocumentario(['História', 'Documentário'])).toBe(true)
    expect(ehDocumentario(['Documentary'])).toBe(true)
    expect(ehDocumentario(['Drama'])).toBe(false)
    expect(ehDocumentario(undefined)).toBe(false)
  })
  it('sabe se ainda há envio andando e quantos episódios saíram', () => {
    expect(andando([{ estado: 'enviado' }, { estado: 'na_fila' }])).toBe(true)
    expect(andando([{ estado: 'enviado' }, { estado: 'falhou' }])).toBe(false)
    expect(lancados([{ data: '2020-01-01' }, { data: '2099-01-01' }, {}], '2026-11-11')).toBe(2)
  })
})
