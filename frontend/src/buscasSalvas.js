import { api } from './api.js'

// Buscas salvas (S44): o texto e o endereço da tela onde a busca roda de novo.

export const ONDE = { inicio: 'Início', torrents: 'Indexadores', titulo: 'Título' }

export const enderecoDaBusca = (rota, q) => `#/${rota}?q=${encodeURIComponent(q.trim())}`

export const salvarBusca = ({ texto, onde, endereco }) => api('/api/buscas-salvas', { metodo: 'POST', corpo: { texto: texto.trim(), onde, endereco } })
