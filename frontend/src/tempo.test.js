import { describe, expect, it } from 'vitest'
import { duracao, enderecoDoVideo, relogio, rotaDoPlayer } from './tempo.js'

describe('relogio', () => {
  it('mostra minutos e segundos, e horas quando há', () => {
    expect(relogio(0)).toBe('0:00')
    expect(relogio(754)).toBe('12:34')
    expect(relogio(3723.9)).toBe('1:02:03')
  })
})

describe('duracao', () => {
  it('arredonda para minutos', () => {
    expect(duracao(0)).toBe('')
    expect(duracao(45 * 60)).toBe('45 min')
    expect(duracao(105 * 60)).toBe('1 h 45 min')
    expect(duracao(120 * 60)).toBe('2 h')
  })
})

describe('enderecoDoVideo', () => {
  it('canal em HLS, o resto pelo arquivo', () => {
    expect(enderecoDoVideo('canal:179')).toBe('/api/video/canal/179.m3u8')
    expect(enderecoDoVideo('filme:12')).toBe('/api/video/filme/12.mp4')
    expect(enderecoDoVideo('episodio:5')).toBe('/api/video/episodio/5.mp4')
  })
})

describe('rotaDoPlayer', () => {
  it('leva título e posição no endereço', () => {
    expect(rotaDoPlayer('filme:1')).toBe('#/assistir/filme:1')
    expect(rotaDoPlayer('filme:1', { titulo: 'Ação & Cia', posicao: 61.7 })).toBe('#/assistir/filme:1?titulo=A%C3%A7%C3%A3o+%26+Cia&t=61')
  })
})
