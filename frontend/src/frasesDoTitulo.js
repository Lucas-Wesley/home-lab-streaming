// Flashcards de um título (S49): o que mostrar sobre o pedido de frases.

// andandoAsFrases: o servidor ainda está trabalhando (vale ler de novo daqui a pouco).
export function andandoAsFrases(p) {
  if (!p) return false
  return p.estado === 'na_fila' || p.estado === 'escolhendo' || (p.estado === 'enviado' && (p.audio === 'esperando' || p.audio === 'cortando'))
}

// estadoDasFrases: rótulo curto, classe (cor) e o detalhe do pedido.
export function estadoDasFrases(p) {
  if (!p) return null
  switch (p.estado) {
    case 'na_fila':
      return { rotulo: 'Na fila', classe: 'fila', detalhe: '' }
    case 'escolhendo':
      return { rotulo: 'Escolhendo as frases', classe: 'indo', detalhe: p.etapa || '' }
    case 'falhou':
      return { rotulo: 'Não deu certo', classe: 'erro', detalhe: p.erro || '' }
  }
  const frases = `${p.frases} ${p.frases === 1 ? 'frase' : 'frases'} no Flashcards`
  const audio = {
    esperando: { rotulo: 'Áudios na fila', classe: 'fila' },
    cortando: { rotulo: 'Cortando os áudios', classe: 'indo' },
    pronto: { rotulo: p.com_audio === p.frases ? 'Pronto' : 'Pronto (alguns sem áudio)', classe: 'ok' },
    sem_video: { rotulo: 'Sem vídeo para o áudio', classe: 'erro' },
    falhou: { rotulo: 'Os áudios falharam', classe: 'erro' },
  }[p.audio] || { rotulo: 'Enviado', classe: 'ok' }
  const comAudio = p.frases ? ` · ${p.com_audio} com áudio` : ''
  return { rotulo: audio.rotulo, classe: audio.classe, detalhe: [`${frases}${comAudio}.`, p.audio_msg].filter(Boolean).join(' ') }
}

// podeRefazerAudios: o botão "Gerar os áudios que faltam".
export function podeRefazerAudios(p) {
  return p?.estado === 'enviado' && ['sem_video', 'falhou', 'pronto'].includes(p.audio) && p.com_audio < p.frases
}
