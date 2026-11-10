import { describe, expect, it } from 'vitest'
import { noAparelho, nomeDoIdioma, resumoDosFormatos } from './formatos.js'

// Um navegador como o Chrome: toca H.264, H.265 e AAC; não toca Dolby (ec-3, ac-3).
const chrome = (tipo) => !/ec-3|ac-3/.test(tipo)

describe('noAparelho', () => {
  it('não avisa quando tudo toca ou quando não se sabe', () => {
    expect(noAparelho({ video: 'h264', audios: [{ codec: 'aac' }] }, chrome)).toBeNull()
    expect(noAparelho(null, chrome)).toBeNull()
    expect(noAparelho({}, chrome)).toBeNull()
  })
  it('avisa sem som quando nenhuma trilha toca', () => {
    expect(noAparelho({ video: 'hevc', audios: [{ codec: 'eac3' }] }, chrome)).toMatchObject({ semSom: true, semImagem: false, texto: 'Sem som neste aparelho' })
  })
  it('basta uma trilha que toque (ex.: dublado em AAC e original em Dolby)', () => {
    expect(noAparelho({ video: 'h264', audios: [{ codec: 'eac3' }, { codec: 'aac' }] }, chrome)).toBeNull()
  })
  it('avisa sem imagem quando o vídeo não toca', () => {
    expect(noAparelho({ video: 'mpeg2video', audios: [{ codec: 'aac' }] }, chrome)).toMatchObject({ semImagem: true, texto: 'Sem imagem neste aparelho' })
    expect(noAparelho({ video: 'mpeg2video', audios: [{ codec: 'dts' }] }, chrome)?.texto).toBe('Não toca neste aparelho')
  })
})

describe('resumoDosFormatos', () => {
  it('dá o idioma de cada áudio, o vídeo e as legendas de dentro', () => {
    const r = resumoDosFormatos({
      duracao: 8280, video: 'hevc', altura: 1080,
      audios: [{ codec: 'aac', idioma: 'por', canais: 2 }, { codec: 'eac3', idioma: 'en', canais: 6, ouvido: true }, { codec: 'aac', idioma: 'unk' }],
      legendas: ['por', 'eng', 'por'],
    })
    expect(r.audios).toEqual([
      { idioma: 'Português', ouvido: false, detalhe: 'AAC estéreo' },
      { idioma: 'Inglês', ouvido: true, detalhe: 'Dolby Digital Plus 5.1' },
      { idioma: 'Idioma não informado', ouvido: false, detalhe: 'AAC' },
    ])
    expect(r.video).toBe('1080p H.265')
    expect(r.legendas).toEqual(['Português', 'Inglês'])
    expect(r.duracao).toBe('2 h 18 min')
  })
  it('sem formatos, nada', () => {
    expect(resumoDosFormatos(null)).toBeNull()
    expect(nomeDoIdioma('xx')).toBe('XX')
  })
})
