# Títulos para verificar

Durante o uso, o Lucas encontra títulos com algo estranho e não para para investigar na hora: a capa não aparece,
um episódio não acha torrent, a opção recomendada é ruim. Na página do título, o menu **⋯** (canto de cima, à
direita) → **Mandar para verificação** guarda o título com o que a página mostrava naquele momento. Nada muda no
app: o registro só fica guardado até alguém investigar (decisão S37).

A lista aparece em **Ajustes → Títulos para verificar**, com o estado e a resposta de cada título.

## O que fica guardado

Tabela `relatos`, API `/api/relatos`:

| Campo | O que é |
|---|---|
| `tipo`, `tmdb`, `titulo`, `ano` | O título do TMDB que a página mostrava. A página fica em `#/titulo/{tipo}/{tmdb}`. |
| `temporada`, `episodio` | O último episódio aberto na página (0 se nenhum). |
| `texto` | O que o Lucas notou. É opcional e pode vir vazio. |
| `contexto` | JSON com o que a página via (detalhado abaixo). |
| `versao` | Versão do app no momento do envio. Serve para saber se uma correção posterior já cobre o caso. |
| `estado`, `resposta` | `nova`, `feita` ou `descartada`, mais a resposta que aparece no app. |

O `contexto` traz:

- `endereco`: o endereço da página.
- `assinatura`: os itens do catálogo do IPTV ligados ao título (`id`, `nome`, `ano`). Uma lista vazia, num título
  que está na assinatura, indica associação errada.
- `temporadas`: as temporadas do TMDB e o número de episódios de cada uma. `episodios_do_tmdb` e
  `episodios_na_assinatura` dão as contagens.
- `tambem_em`: os outros serviços onde o título está.
- `busca`: a busca de torrents do filme ou do episódio aberto. Traz `feitas`/`total`, os indexadores que
  falharam (`falharam`), as buscas feitas nos indexadores (`buscas`), a quantidade de opções e as 5 melhores
  (`melhores`: nome, indexador, tamanho, semeadores, nota, recomendada e resultado da verificação).
- `erro`: a mensagem de erro que a página mostrava, se havia alguma.

## Como verificar (para o Claude Code)

Quando o Lucas pedir para "verificar os títulos reportados":

1. **Ler os pendentes.** Rodar a partir da pasta `streaming`:
   `curl -s --cacert ../certs/homelab-ca.crt "https://192.168.1.20:8446/api/relatos?estado=nova"`.
2. **Investigar cada um.** Pontos a conferir:
   - **Associação com o TMDB.** No servidor, a tabela `assinatura_tmdb` (`sqlite3 -readonly
     /srv/data/streaming/streaming.db`) diz a que título do TMDB cada item do catálogo foi ligado. Comparar com
     `GET /api/titulos/{tipo}/{tmdb}`: o título certo tem ano, capa e temporadas. Um título do TMDB sem ano ou
     sem capa costuma ser outro título.
   - **Busca de torrents.** Conferir se os nomes em `buscas` estão certos e se `falharam` traz algum
     indexador (Prowlarr: `indexerstatus`). Se `melhores` estiver vazio, a busca pode estar procurando o
     título errado. Se só houver pacotes, a busca acertou, mas não existem episódios avulsos.
   - **Ranking.** Se a recomendada estiver ruim, ver a nota e a verificação dela. Os critérios estão em
     `avaliar` (`backend/organizar.go`).
3. **Corrigir.** Corrigir a regra, não só o caso, com um teste do caso como exemplo. Se uma associação errada
   ficou guardada, apagar a linha de `assinatura_tmdb` depois do deploy. Seguir o fluxo normal: testes,
   commit, push e `./deploy.sh`.
4. **Marcar.** Fazer `PATCH /api/relatos/{id}` com `{"estado":"feita","resposta":"<o que estava errado e o que
   mudou>"}`. Se não houver nada errado, usar `descartada` e explicar o motivo, por exemplo: "não existem
   episódios avulsos nos indexadores, só a temporada inteira". A resposta aparece em Ajustes.

## Exemplo: Terror (2018)

Este caso, de 11/11/2026, motivou o recurso. Ele foi resolvido antes de o recurso existir.

- **O que o Lucas viu.** A série "Terror", aberta pelo catálogo do IPTV (Amazon Prime), tinha capa na lista,
  mas a página do título aparecia sem capa e com gêneros estranhos ("Crime, Action & Adventure", 1 temporada).
  Os episódios só apareciam porque vinham da assinatura, sem imagens, e nenhum episódio achava torrent.
- **O que estava errado.** O item "Terror" (2018) do catálogo foi ligado ao TMDB 320207, outra série chamada
  "Terror", ainda sem estreia (sem ano, sem capa, sem episódios). A regra `casaComOTitulo` pulava a conferência
  do ano quando o TMDB não tinha ano. A busca de torrents procurava essa outra série, por isso não achava nada.
- **A correção.** Item com ano agora exige que o título do TMDB também tenha ano, com no máximo 1 de diferença.
  Se o ano bate, o catálogo pode omitir o "The" do começo ("Terror" de 2018 é "The Terror"). Foi adicionado o
  teste `TestCasaComOTituloPeloAno`, e a linha errada de `assinatura_tmdb` foi apagada. As outras 33
  associações guardadas foram conferidas uma a uma e estavam certas.
- **Como teria sido com o recurso.** O relato traria `assinatura: [{"id": 8719, "nome": "Terror", "ano":
  "2018"}]`, `tmdb: 320207` sem `ano` e `busca.opcoes: 0`. Só com isso já dava para ver a série errada, sem
  precisar reproduzir o caso.
