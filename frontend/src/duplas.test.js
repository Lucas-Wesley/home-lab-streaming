import { describe, expect, it } from 'vitest'
import { fundoDaBarra, gravadoAte, podeTocar, textoDaDupla } from './duplas.js'

describe('legenda dupla', () => {
  const d = { estado: 'gravando', duracao: 600, gravado: 150, faixas: [[0, 30], [300, 420]] }

  it('sabe até onde está gravado a partir de um ponto', () => {
    expect(gravadoAte(d.faixas, 310)).toBe(420)
    expect(gravadoAte(d.faixas, 100)).toBe(100)
  })

  it('só manda à tela com 1 min gravado à frente, ou pronta', () => {
    expect(podeTocar(d, 310)).toBe(true)
    expect(podeTocar(d, 380)).toBe(false)
    expect(podeTocar(d, 10)).toBe(false)
    expect(podeTocar({ ...d, estado: 'pronta' }, 10)).toBe(true)
    expect(podeTocar({ ...d, faixas: [[560, 600]] }, 570)).toBe(true) // o fim já está gravado
  })

  it('descreve a gravação', () => {
    expect(textoDaDupla(d)).toBe('Gravando: 25% (2:30 de 10:00).')
    expect(textoDaDupla({ estado: 'erro', mensagem: 'sem legenda em inglês' })).toBe('Não deu certo: sem legenda em inglês.')
    expect(textoDaDupla({ estado: '' })).toBe('')
  })

  it('desenha o visto e o carregado na barra', () => {
    const f = fundoDaBarra(60, 600, [[0, 120], [300, 420]])
    expect(f).toContain('var(--marca) 10.00%')
    expect(f).toContain('45%, var(--linha-forte)) 10.00%, color-mix(in srgb, var(--marca) 45%, var(--linha-forte)) 20.00%')
    expect(f).toContain('var(--linha-forte) 20.00%, var(--linha-forte) 50.00%')
    expect(fundoDaBarra(0, 0, null)).toBe('var(--linha-forte)')
  })
})
