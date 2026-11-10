// "1,4 GB", "350 MB": o tamanho de um arquivo, como se lê num gerenciador de arquivos.
export function tamanho(bytes) {
  if (!bytes || bytes < 0) return ''
  const gb = bytes / 2 ** 30
  if (gb >= 1) return `${gb.toLocaleString('pt-BR', { maximumFractionDigits: 1 })} GB`
  return `${Math.max(1, Math.round(bytes / 2 ** 20))} MB`
}
