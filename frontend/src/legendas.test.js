import { describe, expect, it } from 'vitest'
import { falaAtual, lerVTT } from './legendas.js'

const vtt = `WEBVTT

1
00:00:01.500 --> 00:00:03.000
<i>- We're not</i>
cooking anymore.

2
01:02:03.000 --> 01:02:05.250 line:90%
{\\an8}Say my name.

00:00:04.000 --> 00:00:05.000
`

describe('lerVTT', () => {
  it('lê tempos, junta as linhas e tira as marcas', () => {
    expect(lerVTT(vtt)).toEqual([
      { inicio: 1.5, fim: 3, texto: "- We're not\ncooking anymore." },
      { inicio: 3723, fim: 3725.25, texto: 'Say my name.' },
    ])
  })
})

describe('falaAtual', () => {
  const falas = [{ inicio: 1 }, { inicio: 5 }, { inicio: 9 }]
  it('acha a última fala que já começou', () => {
    expect(falaAtual(falas, 0)).toBe(-1)
    expect(falaAtual(falas, 5)).toBe(1)
    expect(falaAtual(falas, 100)).toBe(2)
  })
})
