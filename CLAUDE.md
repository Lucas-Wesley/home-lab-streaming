# Streaming

App do **ecossistema de apps pessoais do Lucas**: central de streaming (canais, filmes e séries de uma assinatura
IPTV) com o celular como controle remoto da TV e do projetor.

**Comece lendo:** [`docs/DECISOES.md`](docs/DECISOES.md) — estado atual, decisões (S1–S53) e próximos passos.

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
- **Documentários para o Acervo** (S41, `acervo.go`): só com `ACERVO_URL`; nos testes, `acervoFalso`,
  `tmdbDeDocumentarios` e `openSubtitlesDeDocumentarios` (`novoAmbiente` desliga o Acervo de verdade). A legenda só é
  buscada pelo código do TMDB, nunca pelo nome; o áudio da assinatura só com a conexão livre (`fonteDoAudio`).
- **Meus vídeos** (S42, `meus_videos.go`): o tdl roda com a conta do Telegram do Lucas (sessão em `/data/telegram`,
  nunca no Git). Nos testes, só `testdata/tdl-falso` (`TDL_BIN`; `novoAmbiente` aponta para um tdl que não existe).
  Um tdl por vez: a sessão é um banco que só um processo abre. A **Trilha** toca esses vídeos direto pela
  `GET /api/video/proprio/<id>.mp4` (videoaula, T30 de lá): mudar essa rota é mudar a Trilha também.
- **Legenda dupla** (S46, `legenda_dupla.go`): uma gravação por vez, com `nice`. Nos testes, o ffmpeg do Mac não tem
  libass: `queimarLegenda = false` (o vídeo sai sem a legenda). A fonte da imagem é Liberation Sans (`font-liberation`
  no Dockerfile).
- **Flashcards de um título** (S49, `frases_para_flashcards.go`): a proposta vai ao Flashcards por `POST /api/propostas` e os
  áudios por `POST /api/geracoes/{id}/audios` (F45 de lá): mudar um lado é mudar o outro. A fonte de cada cartão (o link da
  fala) identifica a frase nos dois apps. O áudio da assinatura só com a conexão livre (`fonteDoAudio`). Nos testes,
  `novoFlashcardsFalso` e `openSubtitlesDasFrases`.
- **Player** (S51): controles próprios em `Player.vue`; regras sem Vue em `player.js` (com teste). O dono da conexão de um
  aparelho é `app:<código>` (`aparelho.js`); "app" sozinho é versão antiga do app ou o servidor recém-ligado. O player vive fora da rota (S53, mini player e
  Picture-in-Picture): `emReproducao` no `App.vue`. Gavetas do
  player ficam dentro dele (a `Folha` some em tela cheia). Teste de navegador com WebM do ffmpeg (`e2e/player.spec.js`).
- **Títulos para verificar** (S37): o Lucas manda títulos estranhos pelo menu ⋯ da página do título. Quando ele
  pedir para "verificar os títulos reportados", seguir [`docs/PARA-VERIFICAR.md`](docs/PARA-VERIFICAR.md).
- **Commits:** autoria do Lucas; **sem linha `Co-Authored-By`**.
