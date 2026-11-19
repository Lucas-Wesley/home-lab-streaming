// Regras do player (S51), sem o Vue: versões do canal, quem pegou a conexão e as tentativas.
import { hora } from './jogos.js'

// "FHD", "HD · reserva", "FHD · H265"; sem nada no nome, "Padrão".
export function rotuloDaVersao(v) {
  const partes = [v.qualidade || 'Padrão']
  if (v.h265) partes.push('H265')
  if (v.reserva) partes.push('reserva')
  return partes.join(' · ')
}

// A próxima versão para tentar quando a atual trava: a seguinte na lista (que vem da melhor para a
// pior), voltando ao começo; nenhuma, se só há uma.
export function proximaVersao(versoes, atual) {
  if (!versoes?.length || versoes.length < 2) return null
  const i = versoes.findIndex((v) => v.id === atual)
  return versoes[(i + 1) % versoes.length]
}

// Espera antes de cada nova tentativa de abrir o vídeo: 1, 3, 6 e 10 s.
export const ESPERAS = [1000, 3000, 6000, 10000]

// Outro aparelho pegou a conexão? Só quando o servidor diz um dono diferente deste aparelho. O
// servidor trabalhando por trás (S28) devolve a conexão sozinho; "app" sem código é a versão antiga
// do app ou o servidor recém-ligado: só conta como outro se o vídeo também for outro.
export function perdeuAConexao(quem, meuDono, chave) {
  if (!quem?.dono || quem.dono === meuDono || quem.tipo === 'servidor') return false
  if (quem.dono === 'app') return quem.chave !== chave
  return true
}

// O aviso: "O computador Windows começou a assistir Premiere 3 às 21:04."
export function textoDeQuemPegou(quem, chave) {
  const quando = quem.desde ? ` às ${hora(quem.desde)}` : ''
  const oque = quem.chave === chave ? 'este mesmo vídeo' : quem.titulo || 'outro vídeo'
  if (quem.tipo === 'tela') return `${quem.nome || 'Uma tela'} começou a tocar ${oque}${quando}.`
  const nome = quem.nome || 'Outro aparelho'
  return `${nome.charAt(0).toUpperCase()}${nome.slice(1)} começou a assistir ${oque}${quando}.`
}

// Segundos de vídeo já carregados à frente do ponto atual.
export function folgaCarregada(buffered, agora) {
  for (let i = 0; i < (buffered?.length || 0); i++) {
    if (buffered.start(i) <= agora + 0.5 && agora < buffered.end(i)) return buffered.end(i) - agora
  }
  return 0
}

// Segundos até o fim do vídeo (nunca negativo).
export function faltaParaOFim(agora, duracao) {
  return Number.isFinite(duracao) && duracao > 0 ? Math.max(0, duracao - agora) : Infinity
}

// Passa de um player para o próximo (o do episódio seguinte): manter a tela cheia.
export const passagem = { telaCheia: false }
