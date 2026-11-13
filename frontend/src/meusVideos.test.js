import { describe, expect, it } from 'vitest'
import { linkCompartilhado, linkDoTelegram } from './meusVideos.js'

describe('linkDoTelegram', () => {
  it('acha o link no meio do texto', () => {
    expect(linkDoTelegram('olha esse https://t.me/c/123/4 da viagem')).toBe('https://t.me/c/123/4')
    expect(linkDoTelegram('  t.me/c/123/4\n')).toBe('https://t.me/c/123/4')
  })
  it('sem link, devolve o texto', () => {
    expect(linkDoTelegram(' qualquer coisa ')).toBe('qualquer coisa')
    expect(linkDoTelegram(null)).toBe('')
  })
  it('lê o que o Android compartilhou', () => {
    expect(linkCompartilhado('?title=Telegram&text=https%3A%2F%2Ft.me%2Fc%2F123%2F4')).toBe('https://t.me/c/123/4')
    expect(linkCompartilhado('?url=https%3A%2F%2Ft.me%2Fnome%2F9')).toBe('https://t.me/nome/9')
  })
})
