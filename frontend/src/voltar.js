// Volta para a tela anterior; aberto direto pelo endereço (sem histórico), vai para a lista.
export function voltar(destino) {
  if (history.length > 1) history.back()
  else location.hash = destino
}
