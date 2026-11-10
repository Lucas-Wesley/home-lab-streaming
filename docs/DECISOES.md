# Streaming — Decisões

> Central de streaming do Lucas: canais ao vivo, filmes e séries de uma assinatura IPTV, num PWA para o celular e o
> computador, com o celular servindo também de **controle remoto da TV e do projetor**. Registro até **09/11/2026**.
> Parte do ecossistema: arquitetura e contrato comum ficam no repo **Hub**
> ([`hub-home-lab-web`](https://github.com/Lucas-Wesley/hub-home-lab-web), `docs/`).

---

## 1. Onde paramos

**Primeira versão pronta e testada no Mac (09/11/2026), ainda não publicada.** Tem:

- **Acesso colado em Ajustes** (S2): a mensagem do provedor como veio, ou só o link M3U. O servidor confere com o
  provedor e mostra situação, validade e conexões, nunca a senha.
- **Catálogo** baixado pela API do painel (S3) e guardado no SQLite, atualizado uma vez por dia ou em "Atualizar
  agora": canais, filmes e séries por categoria, com "Recentes".
- **Busca única no Início** (S28): canais, filmes e séries da assinatura e, com o TMDB, os títulos que só há por
  torrent, numa lista só; cada capa tem um selo (a TV da assinatura ou o T verde do torrent).
- **Filme** com sinopse, elenco e duração; **série** com temporadas, episódios e "próximo a ver".
- **Player** no aparelho: filmes e episódios com avançar e voltar; canais ao vivo em HLS (hls.js no Chrome).
- **Torrents** (S17): link magnet, arquivo .torrent ou item do Internet Archive (com busca nas coleções de filmes,
  animação, TV clássica e cinema mudo). Assistir sem baixar, baixar para depois, "visto" apaga; o motor de torrent só
  liga quando precisa.
- **Verificação das opções** (S23): duração × TMDB, formatos, impressão digital e quadros; as 3 melhores são verificadas
  sozinhas, ao mesmo tempo, e o botão de assistir avisa quando o aparelho não toca o som ou a imagem.
- **Página do título = tela de assistir** (S20, S28): a assinatura primeiro, depois as versões de torrent do **Prowlarr**
  organizadas (versões do filme, ou temporadas com o pacote completo e cada episódio), todas com **Assistir** e
  **Baixar**. A folha de assistir mostra de onde vem, o **idioma de cada áudio** (o Whisper reconhece quando o arquivo
  não diz) e o vídeo.
- **Downloads** (S28): da assinatura (baixa com a conexão livre, para quando alguém assiste e continua depois) e dos
  torrents; baixado, toca do disco sem ocupar a assinatura.
- **Legendas** (S21, S25): português e inglês pelo OpenSubtitles, escolhidas no player ou no controle da tela; valem no
  aparelho e na TV, com a sincronia acertada sozinha (referência, Whisper ou Claude) e ajuste manual de ±0,5 s.
- **Legenda gerada pelo áudio** (S29): o Parakeet transcreve minuto a minuto, a partir de onde se está, e a legenda
  cresce à frente de quem assiste; o Whisper e o Claude revisam (double check) um pouco adiante. Na assinatura, o
  servidor puxa antes só o áudio (~17 s por episódio) e o vídeo para e volta sozinho.
- **Favoritos** (S15): canais, filmes e séries, numa aba própria.
- **Continuar assistindo**, com o ponto em que parou, venha do player ou da tela (S22), e a opção de tirar da lista.
- **Telas** (S8): a TV e o projetor são achados na rede; **Assistir na tela** manda o vídeo do mesmo ponto, e o
  celular vira controle (pausar, avançar, voltar, ir para um ponto, volume e parar). Uma barra no pé da tela leva ao
  controle enquanto algo toca.
- **Uma reprodução por vez** (S5): começar em um aparelho para o outro sozinho.
- Ajustes com conteúdo adulto escondido por padrão (S10) e **Sugestões de melhoria**.

**Testado com os aparelhos de verdade** (09/11/2026):

| Aparelho | Endereço | Como | Resultado |
|----------|----------|------|-----------|
| TV Samsung UN50CU8000GXZD (Crystal UHD 2023, Tizen) | `192.168.1.140` | DLNA, `:9197/dmr` | Filme do provedor tocou do ponto pedido; pausar, avançar, voltar e parar funcionam. Torrent em MKV 1080p H.264 assistido sem baixar: tocou em 18 s e foi a 10:00 em 27 s. Arquivo baixado em 4K H.265 (MKV): tocou de 5:00 em 4 s. Volume e mudo funcionam (6 de 6; um pedido logo no início do vídeo foi ignorado, e o controle relê o volume da TV) |
| Projetor "Whale TV" (Android 13, SMART2KPJT, Whale OS da Zeasn) | `192.168.1.139` | DLNA, porta que muda ao reiniciar (`:1898/`, depois `:1880/`) | Filme do ponto pedido, canal ao vivo, pausar e avançar (S13, S14) |

**Próximos passos** (ordem sugerida):

1. **Publicar** (porta 8446, e 8447 para as telas) e instalar o PWA no celular.
2. Conferir no uso: formatos que o navegador do celular não toca (`mkv`, H.265) e o ao vivo na TV.
3. Ligar a TV pelo celular (S9: Wake-on-LAN e a API local da Samsung).
4. Guia de programação (EPG) nos canais.
5. Receptor próprio só se o DLNA não bastar (por exemplo, para uma interface na própria TV).

## 2. Decisões

| # | Decisão | Por quê |
|---|---------|---------|
| S1 | Mesma stack e infraestrutura dos outros apps: Go + SQLite + Vue PWA, um container na VM `apps`, Caddy na porta **8446**, `./deploy.sh` com testes e volta automática | Uma infraestrutura só para manter. |
| S2 | **O acesso entra pelo app, nunca pelo código**: em Ajustes, o Lucas cola a mensagem do provedor (ou o link M3U) e o servidor tira dela servidor, usuário e senha (`acesso.go`). Fica no banco, em `/srv/data/streaming` | A repo é pública. Trocar de provedor ou renovar não exige deploy. Os testes usam um provedor falso (`backend/provedorfalso`). |
| S3 | Catálogo pela **API Xtream** (categorias e itens), copiado inteiro no SQLite a cada sincronização (uma vez por dia e sob demanda). Episódios vêm quando a série é aberta e valem 6 horas | A M3U tem 79 MB e mistura tudo; a API vem por categoria, com capa, nota e ano. Com o banco local, a busca não depende do provedor. |
| S4 | **O servidor repassa o vídeo**: o aparelho pede a `/api/video/...` (app) ou `/tela/...` (telas) e nunca fala com o provedor | O provedor é só HTTP (o PWA em HTTPS não pode tocar vídeo HTTP), redireciona cada vídeo para um servidor com token (o DLNA da TV não segue o redirecionamento: erro 716) e assim o acesso fica escondido. O destino do redirecionamento fica guardado 10 minutos, para avançar sem voltar ao provedor. |
| S5 | **Uma reprodução por vez**: quem começa a assistir (o player chama `POST /api/reproducao`, ou o comando para uma tela) passa a ser o dono da assinatura; pedidos de outro vídeo recebem 409 e a tela que tocava recebe "parar" | A assinatura aceita 1 conexão; dois aparelhos ao mesmo tempo derrubariam os dois. |
| S6 | Ao vivo em **HLS** no app (lista reescrita para passar pelo servidor, com cada endereço assinado por HMAC) e em **TS contínuo** nas telas; filmes e episódios pelo arquivo, com Range | O navegador não toca TS; a TV toca TS e não precisa do HLS. A assinatura impede que o servidor vire um repasse aberto. |
| S7 | **O servidor é o centro do controle**: o celular pede "tocar X na tela Y" à API, e o servidor fala com a tela | O celular não precisa achar a TV nem saber o protocolo dela. Funciona do PWA, **sem app Flutter**. |
| S8 | **Telas por DLNA, sem instalar nada**: o servidor procura renderizadores por SSDP (no grupo e em cada endereço de `REDE_LOCAL`, porque o projetor só responde à busca dirigida a ele) e manda `SetAVTransportURI` + `Play`, com os cabeçalhos DLNA (`contentFeatures.dlna.org`) que dizem se dá para avançar | Os dois aparelhos da casa tocaram assim. Sem os cabeçalhos, a TV recusa avançar (erro 701). No XML, só entidades nomeadas: a Samsung recusa `&#34;` (erro 402). |
| S9 | Na Samsung, usar a **API local da TV** (WebSocket nas portas 8001/8002, `TokenAuthSupport`) para ligar e mandar teclas, quando precisar; ela pede permissão na TV na primeira vez | Para "Assistir na TV" com a TV desligada. Ainda não feito. |
| S10 | Esconder categorias adultas por padrão, com opção em Ajustes | Estão misturadas em canais, filmes e séries. |
| S11 | A porta das telas (**8447**) é publicada direto pelo container, em HTTP, e só tem `/tela/...` | O DLNA não confia na autoridade certificadora do homelab. A API fica só na porta do Caddy. |
| S13 | **Tela que não responde é achada de novo** antes de dar erro: busca SSDP no IP dela e, se não achar, na rede toda; o endereço novo fica guardado e o comando é repetido uma vez | O projetor Android muda a porta do DLNA quando reinicia (de 1898 para 1880, em 09/11/2026). Sem isso, ele aparecia como "não respondeu" estando ligado. |
| S14 | **Um endereço novo a cada envio** para a tela (`?envio=<hora>`), e o servidor só vai até o ponto depois que a duração aparece e o tempo anda, conferindo e repetindo até 3 vezes | O projetor lembra a posição de um endereço já tocado e abre pausado, num aviso próprio, sem pedir o vídeo; e diz PLAYING enquanto ainda carrega, ignorando a busca feita cedo. |
| S15 | **Favoritos** pelo id do provedor (`favoritos`), sem copiar o item: a aba mostra só o que ainda está no catálogo | Se o provedor tirar e devolver um filme, a marca volta junto. |
| S16 | A tela recebe também a **capa** (`upnp:albumArtURI`) junto com o título | É o que dá para mudar no player da TV e do projetor por DLNA: o player é o do aparelho; uma interface própria só com receptor (§3). |
| S17 | **Torrents com o motor desligado por padrão** (`anacrolix/torrent`, em Go, dentro do app): liga para ler a ficha de um magnet, para baixar (desliga ao terminar) e para assistir (desliga 2 minutos depois do último pedido do player); desligado, não compartilha nada. Assistir baixa só o trecho pedido; o que foi só assistido é apagado quando o motor desliga; "Baixar" guarda o arquivo inteiro; "Visto" apaga, e a pasta inteira sai quando nada mais do torrent está guardado | Pedido do Lucas: torrent ligado só no uso e nada guardado sem querer. Testado em 09/11/2026 com o Internet Archive: no Chrome do Mac, tocou em 7 s e avançou para 25:00 em 5 s com 36 de 461 MB baixados; no projetor, tocou de 25:00, voltou 30 s e tocou MPEG-2 baixado. |
| S18 | De cada vídeo de um torrent fica o melhor formato (MP4 antes de MKV, MPEG e OGV; a versão `_512kb` do Internet Archive só se não houver outra). O Internet Archive tem página própria (`Archive.vue`), com o torrent oficial de cada item | O Archive guarda cada filme em vários formatos. (Em 09/11/2026 o Lucas decidiu também ter busca em indexadores: S19.) |
| S19 | **Busca nos indexadores pelo Prowlarr**, que roda na VM ao lado (`homelab/prowlarr`, rede `proxy`, interface em `https://192.168.1.20:8448` só para cadastrar indexadores). O app usa a API (`/api/v1/search`); endereço e chave ficam no banco (`ajustes`, chave `prowlarr`); em produção o endereço vem de `PROWLARR_URL=http://prowlarr:9696`. O link de download vai ao navegador **sem a chave** (`semChave`) e o servidor só aceita de volta links `/{n}/download` do próprio Prowlarr; ele devolve um .torrent ou redireciona para um magnet | Pedido do Lucas: usar o buscador do Sonarr/Radarr sem as telas deles. Quem busca para os dois é o Prowlarr; Sonarr e Radarr exigiriam um cliente de download sempre ligado, contra S17. Os indexadores são escolha e responsabilidade do Lucas. |
| S20 | **Busca de títulos pelo TMDB** (chave em `ajustes`, `tmdb`; v3 no endereço ou token v4 no cabeçalho; respostas guardadas 1 hora em memória). Ao abrir um título, várias buscas juntas no Prowlarr (nome; nome e ano; nome e "complete"; ao abrir uma temporada, "Nome S02" e "Nome season 2"), com nome original, em inglês e em português. `lerLancamento` só aceita nomes que **começam** com o título e seguem com ano, temporada, episódio ou qualidade; filme exige o ano (±1). Cada opção é pontuada (semeadores, 1080p, "toca em tudo" = H.264 até 1080p, português) e "pesada" passa de 25 GB (filme) ou 6 GB por episódio; a melhor não pesada é a recomendada. Anime sem temporada ("Nome - 30") é convertido pela contagem de episódios do TMDB | Os indexadores procuram por palavra solta: "Dark" traz "Dark Matter" e "The Dark", e só "Dark 2017" ou "Dark S01 complete" trazem a série. O Knaben não aceita busca pelo código do IMDb. Testado em 09/11/2026 com Breaking Bad, Dark, Duna: Parte Dois e A Noite dos Mortos-Vivos. |
| S21 | **Legendas pelo OpenSubtitles** (chave em `ajustes`, `opensubtitles`; consumidor com "anonymous downloads" e "under dev": 100 por dia sem usuário e senha). Busca pelo código do TMDB que o provedor manda (`tmdb_id` do filme em `get_vod_info`; do episódio em `get_series_info`, guardado em `episodios.tmdb`) ou que a busca de torrents guarda (`torrents.tmdb`); sem código, pelo nome limpo e temporada/episódio. Arquivo baixado leva o hash do OpenSubtitles (a legenda "feita para este arquivo" vem primeiro). A escolhida fica em `legendas_escolhidas` por vídeo e em `DATA_DIR/legendas/os-<id>.srt` (UTF-8; as em Windows-1252 são convertidas), baixada uma vez só. O player recebe WebVTT (`/api/legenda/…vtt`); a tela, SRT na porta das telas (`/legenda/…srt`), anunciado no DIDL (`sec:CaptionInfoEx`, `pv:subtitleFileUri`, `<res>` text/srt) e no cabeçalho `CaptionInfo.sec` | Pedido do Lucas (PT e EN). A API exige endereço canônico (parâmetros em ordem alfabética), que `url.Values.Encode` já dá. Testado em 09/11/2026: a **TV buscou a legenda** ao abrir o vídeo; o **projetor não aceita legenda externa** por DLNA (o `GetProtocolInfo` dele não lista nenhum formato de legenda). |
| S22 | **A posição na tela é guardada pelo servidor**: enquanto uma tela toca, ele pergunta a posição a cada 15 s (`acompanhar`), e também antes de parar a tela ou de outro aparelho assumir. O player guarda também os vídeos de torrent, ao ir para o fundo e ao fechar (`keepalive`) | Antes, a posição da tela só era guardada com o controle aberto no celular, e o player não guardava torrents: o "Continuar" voltava a um ponto antigo. |
| S23 | **Verificar uma opção da busca antes de assistir**: o torrent entra **oculto** (`torrents.oculto`, fora da lista até ser adicionado; apagado em 7 dias), o motor lê só os pedaços que o **ffprobe** pede (por um servidor interno em 127.0.0.1), e sai: duração × TMDB (do filme, ou do episódio pela temporada), formatos de vídeo e áudio, legendas internas, impressão digital no OpenSubtitles (confirma o título quando a base conhece o arquivo) e 3 quadros (20%, 50%, 80%). Veredito: errado (< 75% da duração, outro título pela impressão digital, ou o vídeo é .exe/.zip), suspeito (75–90% ou > 130%, gravação de cinema, "WEB/Blu-ray" de filme que ainda não saiu em digital pelas datas do TMDB), ok, indefinido. A página do título verifica sozinha as 3 melhores opções (e as seguintes, se alguma der errado; até 5) e reordena: errada vai para o fim, suspeita nunca é recomendada. Os formatos ficam no arquivo (`torrent_arquivos.sondagem`) e o botão de assistir avisa **"Sem som / sem imagem neste aparelho"** pelo `canPlayType` do próprio navegador; arquivos baixados são lidos do disco ao abrir a página, os outros ao assistir pela primeira vez (o motor não liga só para listar, S17) | Pedido do Lucas em 09/11/2026: o "recomendado" da Odisseia era outro filme (1 h 26 × 2 h 52) com nome perfeito. Testado com A Odisseia (a errada foi para o fim; as de cinema marcadas; ainda não saiu em digital), Free Solo (documentário), Ruptura (pacote e episódio) e Oppenheimer: verificar 3 opções leva de 1 a 2 min. |
| S24 | **Torrents híbridos (v1 + v2) e a biblioteca**: `abrir` e `fichaDoMagnet` adicionam **sem o identificador v2** e uma vez só por torrent (trava `adicionando`, contador `adicoes`). Com os dois identificadores de antemão, a `anacrolix/torrent` v1.61.0 não registra o v2, e o anúncio aos rastreadores para ele entra em pânico (`panicif.False` em `updateTorrentInput`), derrubando o app | Caiu 3 vezes nos testes de 09/11/2026. `TestAbrirTorrentHibrido` e `TestMagnetHibrido` reproduzem o pânico sem a correção. Rever ao atualizar a biblioteca. |
| S25 | **Sincronia da legenda** (`legendas_escolhidas.atraso/escala/ajuste`): ao escolher uma legenda que não é "feita para este arquivo", o servidor acerta por trás o atraso (e a velocidade, 25 × 23,976 quadros) comparando os **tempos** das falas com uma referência sincronizada: 1) a legenda de dentro do arquivo baixado; 2) a legenda feita para o arquivo no OpenSubtitles (impressão digital), em qualquer idioma. A legenda sai ajustada no endereço (`?atraso=&escala=`), no player e na tela. Ajuste manual de ±0,5 s no player e no controle (a tela recebe o vídeo de novo, do mesmo ponto) | Pedido do Lucas: legendas de outra versão do mesmo filme quase sempre só estão deslocadas. Testado em 09/11/2026: a de A Noite dos Mortos-Vivos foi ajustada em +38,2 s em 15 s, sem baixar o filme. |
| S26 | **Whisper compartilhado** (`homelab/whisper`, speaches com faster-whisper "small" int8, só processador, na rede `proxy` em `http://whisper:8000`; o modelo sai da memória após 10 min). No Streaming, `WHISPER_URL`: 3. sem referência, ouve 3 trechos de 90 s e casa as **palavras** com as da legenda (mesmo idioma; votação entre os pares, ≥ 4 falas concordando); e, na verificação em dúvida, ouve 2 min e compara com a legenda do título (confirma ou desmente; diz o idioma do áudio). Vídeos da assinatura ficam de fora: ouvir o provedor ocupa a única conexão (S5) | No servidor (i7-8700T), 90 s de áudio levam 11–36 s. Com áudio real, 98% das falas casaram e o atraso saiu com 0,3 s de diferença; a legenda em outro idioma casou 0%. Os trechos curtos com ffsubsync ou detecção de silêncio erraram (descartados). |
| S27 | **Claude automático** (Claude Code, `claude -p`, modelo Haiku, só leitura dos quadros; login por `CLAUDE_CODE_OAUTH_TOKEN` no `.env`, como no Acervo): 1) legenda em **outro idioma**: casa pelo sentido as falas que o Whisper ouviu com as da legenda, e o atraso sai dos pares; 2) **parecer** quando a verificação termina "suspeito" ou "indefinido": recebe título, duração, nome, formatos, falas ouvidas e olha os 3 quadros; pode confirmar (indefinido → ok) ou desmentir (→ errado), e a explicação aparece na opção. Sem Claude ou sem login, essas etapas são puladas. Nos testes, `testdata/claude-falso` | Testado em 09/11/2026: legenda em português × áudio em inglês ajustada em +38,5 s em 16 s; parecer sobre a Odisseia "BluRray" (é o filme, imagem limpa, provável vazamento antes do digital). |
| S29 | **Legenda gerada pelo áudio**: o **Parakeet** (NVIDIA, serviço compartilhado do homelab em `parakeet/`, API da OpenAI) transcreve de minuto em minuto (`blocos_gerados`, 60 s + 3 s de sobra; a palavra repetida na emenda sai), começando pelo minuto em que o Lucas está (pelo `progresso`) e voltando ao começo no fim; cada minuto pronto reescreve o SRT (`gerada-{tipo}-{id}`), que vira a legenda escolhida. Inglês vai para o **v2**, os outros 24 idiomas europeus para o **v3**; o idioma vem do arquivo ou do **Whisper** (30 s), que também transcreve os idiomas fora do Parakeet. **Double check**: com 5 min de folga à frente de quem assiste, o Whisper ouve de novo e o **Claude** junta as duas transcrições linha a linha (mesmos tempos, mesmo número de linhas, senão fica o texto do Parakeet), começando 2 min adiante. **Assinatura**: puxa só a faixa de áudio (`-c:a copy`, `data/audio`) com a conexão livre; o player para e volta sozinho (`geracao.puxar`), e a tela para e volta pelo servidor (`liberar`). **Telas**: recebem a legenda junto com o vídeo; quando a que têm está acabando e já há mais pronta, o servidor manda o vídeo de novo do mesmo ponto | Pedido do Lucas (09–10/11/2026). Medido no servidor (10 min do piloto de Breaking Bad): Parakeet v2 ~95 s, v3 ~100 s (o v3 pulou mais frases em inglês), Whisper small 109 s. Juntando Parakeet e Whisper com o Claude, o erro contra o large-v3-turbo caiu de 19% (Parakeet) e 12% (Whisper) para 9% num minuto, e de 21%/23% para 19% no outro. Episódio real da assinatura: áudio puxado em 17 s, primeira legenda em 28 s, depois ~6 s por minuto. A revisão é mais lenta (~30 s por minuto com o processador dividido). |
| S28 | **Assinatura e torrent como uma coisa só**: 1) **busca do Início** junta o catálogo e o TMDB (`busca.go`): um título do TMDB que está na assinatura aparece uma vez (as cópias "Duna" e "Duna [L]" entram nele, pelo nome exato e ano, ou pelo nome com subtítulo e o mesmo ano); fora da assinatura, só com capa e ≥ 20 votos ou popularidade ≥ 5. 2) **página do título** (`#/titulo/{tipo}/{id}`) é a tela final: a assinatura primeiro, depois os torrents; "Assistir" num torrent prepara o torrent por trás e abre o vídeo certo (`POST /api/opcoes/preparar`); as páginas de filme e série da assinatura levam a ela quando o título é conhecido (`assinatura_tmdb`: o filme traz o código do TMDB nos detalhes; a série, pelo nome). 3) **formatos na folha de assistir** (`GET /api/formatos`), lidos na hora e guardados; idioma desconhecido ("unk") é reconhecido pelo Whisper em 30 s por faixa. 4) **baixar da assinatura** (`downloads_da_assinatura`, um por vez, retoma por Range). 5) a conexão tem um uso **por trás** (`entrarPorTras`): o servidor só a usa livre, e qualquer pedido de verdade corta o trabalho e devolve a conexão a quem a tinha. Torrents e vídeos baixados não tomam a conexão ao tocar | Pedido do Lucas (09/11/2026): "a mesma experiência para os dois". Sem classificação automática de qual é melhor: a assinatura vem primeiro, e a escolha é dele, com o idioma do áudio à vista. Testado com o catálogo real: Duna, Oppenheimer, 2001 e Breaking Bad juntos; o 2001 da assinatura é dublado (reconhecido em 18 s). |
| S12 | Cor do app: **âmbar** (`#b45309` / `#f5a65b`) | Não se confunde com o azul, o verde e o violeta dos outros apps, nem com o vermelho de erro. |

## 3. Controle pelo celular

O fluxo: o Lucas busca no celular, toca no nome da tela (TV ou projetor), o servidor manda a tela tocar do mesmo
ponto e o celular abre o **controle**. "Assistir no aparelho" traz o vídeo de volta, e a tela para sozinha.

**Sobre o app Flutter e apps nas telas:** não foram precisos. Os dois aparelhos tocam por DLNA, o padrão que eles já
têm. Um receptor próprio (app Tizen na TV, app Android no projetor) só entra se faltar algo que o DLNA não faz: abrir
com a TV desligada, legenda escolhida, interface navegável pelo controle da TV.

O projetor também anuncia AirPlay (porta 7001), mas recusa tocar um link por ele (só espelhamento de tela).

## 4. Em aberto

- Formatos de torrent que o navegador não toca (MPEG-2, H.265 em MKV): hoje o player avisa e sugere uma tela. A TV
  toca MKV, H.264 e 4K H.265; o projetor toca MPEG-2.
- **Legenda gerada** (S29): conferir no uso o tempo da revisão (Whisper + Claude) e se vale manter ligada
  (`LEGENDA_REVISAO=0` desliga). O projetor não aceita legenda externa (ver abaixo), então a gerada só aparece na TV
  e no aparelho.
- Teste `TestTelaQueMudouDePorta` acusou uma corrida (race) 1 vez em 4 rodadas da suíte inteira; não se repetiu
  sozinho em 30 rodadas.
- **Sincronia dos vídeos da assinatura**: só manual, porque ouvir o provedor ocupa a única conexão. Dá para ouvir
  quando nada está tocando.
- **Legenda no projetor**: o DLNA dele não aceita legenda externa. Caminho possível: o servidor junta a legenda ao
  vídeo com o ffmpeg (`-c copy`, sem recodificar) antes de mandar. Nas MKV, as legendas que vêm dentro do arquivo
  aparecem pelo controle do projetor.
- **Áudio Dolby e DTS no navegador**: o Chrome mostra o vídeo sem som (E-AC-3, AC-3, DTS). O player avisa
  (`webkitAudioDecodedByteCount`) e sugere a tela; a busca não marca esses como "toca em tudo". A TV toca com som.
- **FlareSolverr**, se o Lucas quiser indexadores protegidos pelo Cloudflare (o Prowlarr avisa no cadastro).
- Leitura dos nomes de torrent: pacotes de várias temporadas sem "S" ("Temporadas 1-3") e numeração de anime com
  temporadas que o TMDB divide diferente; o que não se entende vai para "Outros deste título".
- Usar as legendas `.srt` que vêm no torrent como opção, sem gastar a cota do OpenSubtitles; o **SubDL** como segunda
  fonte de legendas.

- Guia de programação (EPG): o provedor entrega (`xmltv.php`, ~61 mil programas), falta decidir quanto mostrar.
- Vídeos `mkv` no navegador do celular: o Chrome toca parte deles; o que não tocar, vai para a tela.
- Interface própria para TV (navegação pelo controle remoto), se um dia houver receptor.
