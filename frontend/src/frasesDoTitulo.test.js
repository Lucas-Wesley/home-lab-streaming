import { describe, expect, it } from 'vitest'
import { andandoAsFrases, estadoDasFrases, podeRefazerAudios } from './frasesDoTitulo.js'

describe('frases de um título', () => {
  it('diz o estado do pedido', () => {
    expect(estadoDasFrases({ estado: 'escolhendo', etapa: 'O Claude está escolhendo entre 150 frases…' })).toEqual({
      rotulo: 'Escolhendo as frases',
      classe: 'indo',
      detalhe: 'O Claude está escolhendo entre 150 frases…',
    })
    const cortando = estadoDasFrases({ estado: 'enviado', frases: 32, com_audio: 12, audio: 'cortando', audio_msg: 'Cortando os áudios: 13 de 32…' })
    expect(cortando.rotulo).toBe('Cortando os áudios')
    expect(cortando.detalhe).toBe('32 frases no Flashcards · 12 com áudio. Cortando os áudios: 13 de 32…')
    expect(estadoDasFrases({ estado: 'enviado', frases: 2, com_audio: 1, audio: 'pronto' }).rotulo).toBe('Pronto (alguns sem áudio)')
    expect(estadoDasFrases(null)).toBe(null)
  })

  it('sabe quando ler de novo e quando dá para refazer os áudios', () => {
    expect(andandoAsFrases({ estado: 'enviado', audio: 'esperando' })).toBe(true)
    expect(andandoAsFrases({ estado: 'enviado', audio: 'pronto' })).toBe(false)
    expect(podeRefazerAudios({ estado: 'enviado', audio: 'sem_video', frases: 3, com_audio: 0 })).toBe(true)
    expect(podeRefazerAudios({ estado: 'enviado', audio: 'pronto', frases: 3, com_audio: 3 })).toBe(false)
    expect(podeRefazerAudios({ estado: 'enviado', audio: 'cortando', frases: 3, com_audio: 1 })).toBe(false)
  })
})
