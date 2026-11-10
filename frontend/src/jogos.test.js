import { describe, expect, it } from 'vitest'
import { diaDoJogo, faseDoJogo, nomeDoCanal, ordenarJogos } from './jogos.js'

describe('jogos do dia', () => {
  it('diz o dia em relação a hoje', () => {
    expect(diaDoJogo('2026-11-10', '2026-11-10')).toBe('Hoje')
    expect(diaDoJogo('2026-11-11', '2026-11-10')).toBe('Amanhã')
    expect(diaDoJogo('2026-11-14', '2026-11-10')).toBe('Sáb 14/11')
    expect(diaDoJogo('2026-12-01', '2026-11-30')).toBe('Amanhã')
  })

  it('encurta o nome da Globo', () => {
    expect(nomeDoCanal('Globo SP: São Paulo')).toBe('Globo SP')
    expect(nomeDoCanal('SporTV 2')).toBe('SporTV 2')
    expect(nomeDoCanal('Disney+: Canal 01')).toBe('Disney+: Canal 01')
  })

  it('põe o ao vivo na frente e os encerrados no fim', () => {
    const lista = ordenarJogos([
      { id: 1, momento: 'encerrado', hora: '11:00' },
      { id: 2, momento: 'antes', hora: '21:30' },
      { id: 3, momento: 'ao_vivo', hora: '19:00' },
      { id: 4, momento: 'antes', hora: '16:00' },
      { id: 5, momento: 'antes', hora: '' },
    ])
    expect(lista.map((j) => j.id)).toEqual([3, 4, 2, 5, 1])
  })

  it('mostra a fase só quando acrescenta', () => {
    expect(faseDoJogo({ campeonato: 'Copa do Brasil', fase: 'Semifinal' })).toBe('Semifinal')
    expect(faseDoJogo({ campeonato: 'Campeonato Brasileiro Série B', fase: 'Fase única' })).toBe('')
    expect(faseDoJogo({ campeonato: 'Amistosos da Seleção Brasileira', fase: 'Amistoso' })).toBe('')
    expect(faseDoJogo({ campeonato: 'Premier League' })).toBe('')
  })
})
