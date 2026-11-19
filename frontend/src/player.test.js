import { describe, expect, it } from 'vitest'
import { nomeDoAparelho } from './aparelho.js'
import { folgaCarregada, perdeuAConexao, proximaVersao, rotuloDaVersao, textoDeQuemPegou } from './player.js'

describe('nomeDoAparelho', () => {
  it('diz o tipo de aparelho pelo navegador', () => {
    expect(nomeDoAparelho('Mozilla/5.0 (Linux; Android 14; SM-S918B) AppleWebKit/537.36 Chrome/131 Mobile Safari/537.36')).toBe('o celular')
    expect(nomeDoAparelho('Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/131')).toBe('o computador Windows')
    expect(nomeDoAparelho('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)')).toBe('o Mac')
    expect(nomeDoAparelho('')).toBe('outro navegador')
  })
})

describe('versões do canal', () => {
  const versoes = [
    { id: 1, qualidade: 'FHD' },
    { id: 2, qualidade: 'HD', reserva: true },
    { id: 3, qualidade: '', h265: true },
  ]
  it('rótulo curto', () => {
    expect(rotuloDaVersao(versoes[0])).toBe('FHD')
    expect(rotuloDaVersao(versoes[1])).toBe('HD · reserva')
    expect(rotuloDaVersao(versoes[2])).toBe('Padrão · H265')
  })
  it('a próxima depois da que travou, voltando ao começo', () => {
    expect(proximaVersao(versoes, 1).id).toBe(2)
    expect(proximaVersao(versoes, 3).id).toBe(1)
    expect(proximaVersao([versoes[0]], 1)).toBeNull()
  })
})

describe('quem pegou a conexão', () => {
  const eu = 'app:aaa'
  it('só avisa com outro dono de verdade', () => {
    expect(perdeuAConexao({ dono: eu, tipo: 'aparelho', chave: 'canal:1' }, eu, 'canal:1')).toBe(false)
    expect(perdeuAConexao({ dono: 'servidor', tipo: 'servidor', chave: 'filme:2' }, eu, 'canal:1')).toBe(false)
    expect(perdeuAConexao({ dono: 'app', tipo: 'aparelho', chave: 'canal:1' }, eu, 'canal:1')).toBe(false)
    expect(perdeuAConexao({ dono: 'app', tipo: 'aparelho', chave: 'filme:2' }, eu, 'canal:1')).toBe(true)
    expect(perdeuAConexao({ dono: 'app:bbb', tipo: 'aparelho', chave: 'canal:1' }, eu, 'canal:1')).toBe(true)
    expect(perdeuAConexao({ dono: 'tv', tipo: 'tela', chave: 'canal:1' }, eu, 'canal:1')).toBe(true)
  })
  it('explica quem e o quê', () => {
    expect(textoDeQuemPegou({ tipo: 'aparelho', nome: 'o computador Windows', chave: 'canal:1', titulo: 'Premiere 3' }, 'canal:1')).toBe(
      'O computador Windows começou a assistir este mesmo vídeo.',
    )
    expect(textoDeQuemPegou({ tipo: 'aparelho', nome: 'o celular', chave: 'filme:2', titulo: 'Duna' }, 'canal:1')).toBe('O celular começou a assistir Duna.')
    expect(textoDeQuemPegou({ tipo: 'tela', nome: 'TV da sala', chave: 'filme:2' }, 'canal:1')).toBe('TV da sala começou a tocar outro vídeo.')
  })
})

describe('folgaCarregada', () => {
  const faixas = (lista) => ({ length: lista.length, start: (i) => lista[i][0], end: (i) => lista[i][1] })
  it('conta só a faixa onde o vídeo está', () => {
    expect(folgaCarregada(faixas([[0, 10], [20, 50]]), 25)).toBe(25)
    expect(folgaCarregada(faixas([[0, 10]]), 15)).toBe(0)
  })
})
