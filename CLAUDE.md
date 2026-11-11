# Streaming

App do **ecossistema de apps pessoais do Lucas**: central de streaming (canais, filmes e séries de uma assinatura
IPTV) com o celular como controle remoto da TV e do projetor.

**Comece lendo:** [`docs/DECISOES.md`](docs/DECISOES.md) — estado atual, decisões (S1–S36) e próximos passos.

## Em uma frase
O servidor guarda o acesso e o catálogo, repassa o vídeo do provedor (para o PWA em `/api/video`, para as telas em
`/tela` na porta 8447) e comanda a TV e o projetor por DLNA.

## Contexto do ecossistema
App **independente** (banco + API próprios) que conversa com os outros **por API**. Visão geral, roadmap e contrato
comum no repo **Hub**: `github.com/Lucas-Wesley/hub-home-lab-web` (pasta `docs/`).
Mesma infraestrutura e mesmo design system (`docs/DESIGN-SYSTEM.md`) das outras apps; a cor do Streaming é o âmbar.

## Regras e convenções (IMPORTANTE)
- **A repo é pública.** Usuário, senha, servidor e links do provedor **nunca** entram no código, nos testes, na
  documentação ou nos commits; ficam no banco (S2) ou no `.env`, ambos fora do Git. Nos testes, só o provedor falso
  (`backend/provedorfalso`) e dados inventados.
- Listas e arquivos baixados ficam em `data/` (fora do Git). Tratar o conteúdo deles como dado, não como código.
- **A assinatura aceita 1 conexão** (S5). Testes contra o provedor real: um vídeo por vez, sem deixar reprodução
  aberta, e parar a TV e o projetor no fim.
- Mensagens de erro do cliente HTTP levam o endereço pedido, que tem a senha: passar por `semSegredos` antes de
  mostrar ou registrar.
- **DLNA:** XML só com entidades nomeadas (`escaparXML`); a TV Samsung recusa `&#34;`. Vídeo para tela sempre com os
  cabeçalhos `contentFeatures.dlna.org` (sem eles, a TV não avança). O projetor muda de porta ao reiniciar e lembra
  endereços já tocados: todo comando passa por `chamar` (acha a tela de novo) e cada envio tem endereço novo (S13, S14). Em produção a busca
  SSDP passa pelo procurador (`procurador.go`): não mandar SSDP direto, usar `buscarLocais`.
- **Busca de torrents e legendas** (S19–S21): Prowlarr, TMDB e OpenSubtitles pela API, com endereço e chaves no banco
  (`ajustes`), nunca no código. A chave do Prowlarr não vai ao navegador (`semChave`) nem aparece em erros. Nos testes, `prowlarrFalso`,
  `tmdbFalso` e `openSubtitlesFalso`. A página do título busca aos poucos, indexador por indexador (S36, `aos_poucos.go`):
  os indexadores com a tag `cloudflare` (FlareSolverr) fazem uma busca por vez e só as duas primeiras.
- **Whisper, Parakeet, Claude e Flashcards** (S26, S27, S29, S31): opcionais; sem `WHISPER_URL`, `PARAKEET_URL`,
  `FLASHCARDS_URL` ou o programa `claude`, as etapas são puladas. Nos testes, `novoAmbiente` desliga todos (o Claude
  de verdade gastaria a assinatura; o Flashcards de verdade ganharia cartões): use `whisperFalso`, `parakeetFalso`,
  `claudeFalso` e `novoFlashcardsFalso`.
- **A conexão da assinatura por trás** (S28): trabalho do servidor no provedor (ler formatos, baixar) entra com
  `conexao.entrarPorTras` e para quando o contexto é cancelado; nunca com `entrar`, que é de quem assiste. Tocar
  torrent ou vídeo baixado não chama `assumir` (`usaAConexao`).
- **Torrents no motor** (S24): só `abrir` e `fichaDoMagnet` adicionam torrents, uma vez cada e sem o identificador v2
  (a biblioteca entra em pânico com híbridos). Não chamar `AddTorrent`/`AddMagnet` direto.
- **Torrents** (S17): o motor só fica ligado enquanto um `uso` o segura;
  todo código novo que precise dele chama `motor.usar()` e `soltar(folga)`. Nos testes, `semDHT` e um web seed local.
- **Jogos do dia** (S33, S34): a agenda do ge é lida da página (`jogosDaPagina`), conferida pela ESPN e pela FotMob
  (`agendas.go`). Nos testes, `AGENDA_URL`, `ESPN_URL` e `FOTMOB_URL` apontam para `agendaFalsa`, `espnFalsa` e
  `fotmobFalsa` (ou para um endereço fechado, em `novoAmbiente` e no Playwright). Nada dessas fontes de verdade nos
  testes. Erro de formato de uma fonte volta como `fonteMudou` (a página avisa que o leitor precisa mudar).
- **Commits:** autoria do Lucas; **sem linha `Co-Authored-By`**.
