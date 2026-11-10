import { expect, it } from 'vitest'
import { tamanho } from './tamanho.js'

it('mostra MB ou GB', () => {
  expect(tamanho(0)).toBe('')
  expect(tamanho(300 * 2 ** 20)).toBe('300 MB')
  expect(tamanho(1.44 * 2 ** 30)).toBe('1,4 GB')
})
