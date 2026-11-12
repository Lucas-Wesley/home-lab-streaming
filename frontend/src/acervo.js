// Documentários para o Acervo (S41): como cada envio aparece na página do título.

const ESTADOS = {
  na_fila: { rotulo: 'Na fila para o Acervo', curto: 'Na fila do Acervo', classe: 'fila' },
  enviando: { rotulo: 'Indo para o Acervo…', curto: 'Indo para o Acervo', classe: 'indo' },
  enviado: { rotulo: 'No Acervo', curto: 'No Acervo', classe: 'ok' },
  falhou: { rotulo: 'Não foi para o Acervo', curto: 'Não foi ao Acervo', classe: 'erro' },
}

// estadoDoEnvio: o rótulo (com a etapa, quando há) e a classe da cor.
export function estadoDoEnvio(e) {
  const base = ESTADOS[e?.estado]
  if (!base) return null
  return { ...base, rotulo: (e.estado === 'na_fila' || e.estado === 'enviando') && e.etapa ? e.etapa : base.rotulo }
}

export const ehDocumentario = (generos) => (generos || []).some((g) => /^document/i.test(g))

export const andando = (envios) => (envios || []).some((e) => e.estado === 'na_fila' || e.estado === 'enviando')

// Quantos episódios já saíram (os que estreiam depois não vão).
export const lancados = (episodios, hoje) => (episodios || []).filter((e) => !e.data || e.data <= hoje).length
