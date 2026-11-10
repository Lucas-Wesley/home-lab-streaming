# Design system das apps do homelab

Vale para a Biblioteca Digital, o Acervo, o Flashcards, o Streaming, o Hub e as próximas apps.
Há uma cópia igual em `docs/DESIGN-SYSTEM.md` de cada app; os tokens ficam no começo do
`frontend/src/style.css` de cada uma, com os mesmos nomes e valores, menos a cor do app (`--marca`).
Definido pelo Lucas em 06/11/2026 e revisto em 09/11/2026 (segunda versão, abaixo) e em 10/11/2026
(hierarquia dos títulos e Ajustes em níveis).

A extensão do Chrome ainda usa a paleta da primeira versão (creme, Literata e terracota) no
`popup.css` e no botão; passa para esta quando for mexida.

## O que mudou na segunda versão

A primeira versão (fundo creme, títulos em serifa, destaque terracota, rótulos em maiúsculas
espaçadas e textos ligados por "·") é a combinação mais comum em telas geradas por IA e deixava as
três apps iguais e sem identidade. A segunda troca isso por neutros frios, a fonte do sistema na
interface e uma cor para cada app. Foi feita sem referências visuais escolhidas pelo Lucas; elas
entram numa terceira revisão.

## Princípios

1. **O texto vem primeiro.** Tudo o que não é conteúdo é menor, mais claro e mais discreto que ele.
2. **Uma cor por app.** Cada app tem a sua (`--marca`): Biblioteca em azul, Acervo em verde,
   Flashcards em violeta, Streaming em âmbar e Hub em ciano petróleo. Ela diz em qual app se está e aparece em poucos lugares: o ícone do app
   no menu, o ícone do item ativo, o botão principal, o foco, os links e o progresso.
3. **Neutro no resto.** Branco, cinzas frios e tinta escura. Azul só para informação e vermelho
   só para erro e atenção.
4. **Listas finas.** Linhas separadas por um fio, alinhadas à esquerda, sem caixas nem sombras.
   Imagens pequenas (miniatura de vídeo, capa de livro) e só quando ajudam a reconhecer o item.
5. **Vidro no que flutua.** Barras fixas, abas e painéis sobre o texto usam fundo translúcido com
   blur; o resto da página é chapado.
6. **Botões pequenos.** 32 px de altura no computador e 36 px no toque. Só um botão na cor do app
   por tela, para a ação principal.

## Cores

| Token | Claro | Escuro | Uso |
|---|---|---|---|
| `--fundo` | `#ffffff` | `#121418` | Fundo do conteúdo |
| `--lateral` | `#f7f8fa` | `#16181c` | Fundo do menu lateral |
| `--item-ativo` | `#ffffff` | `#23262c` | Item ativo do menu, com um fio em volta |
| `--superficie` | `#f2f3f6` | `#1c1f24` | Hover, blocos de apoio ("Continuar lendo") |
| `--superficie-forte` | `#e6e8ec` | `#282c32` | Chips, filtro ativo |
| `--texto` | `#17191e` | `#e9eaed` | Texto |
| `--texto-suave` | `#5c6270` | `#a2a7b0` | Texto de apoio, ícones, rótulos de grupo |
| `--texto-fraco` | `#858b96` | `#737983` | Metadados, versão |
| `--linha` | tinta 9% | branco 8% | Fios de separação, hover do menu |
| `--vidro` | branco 78% | fundo 72% | Barras e painéis com blur |
| `--azul` | `#2459c7` | `#8fb0f5` | Informação: "em andamento", "pronto", cartões novos, linhas acrescentadas num diff |
| `--vermelho` | `#cc3340` | `#f07a83` | Erro, perigo, contador que pede atenção, linhas removidas num diff |

Cada uma tem uma versão de fundo (`--azul-fundo`, `--vermelho-fundo`) para chips e avisos.
Sucesso não tem verde próprio: usa o azul.

### Cor do app

| App | `--marca` claro | `--marca` escuro | `--marca-fundo` claro / escuro |
|---|---|---|---|
| Biblioteca | `#2459c7` | `#8fb0f5` | `#e8effc` / `#1a2540` |
| Acervo | `#127a5a` | `#5ccf9f` | `#e3f3ec` / `#15302a` |
| Flashcards | `#6b47d1` | `#b19cff` | `#eee9fb` / `#2a2343` |
| Streaming | `#b45309` | `#f5a65b` | `#fdf0e1` / `#3a2814` |
| Hub | `#0e7490` | `#67cbe0` | `#e1f3f7` / `#12303a` |

O texto sobre a cor do app usa `--marca-texto`: branco no claro e `--fundo` no escuro. Uma app nova
escolhe uma cor que não se confunda com as outras nem com o vermelho.

O tema **sépia** da Biblioteca é um tema de leitura, com papel bege e azul acinzentado no lugar da
cor do app.

## Tipografia

- **Interface:** fonte do sistema (SF no Apple, Roboto no Android). Corpo de 15 px. Títulos de
  lista, nome do app e títulos de página também ficam nela.
- **Leitura:** Literata, só no texto que se lê de verdade: o leitor do Acervo, os livros da
  Biblioteca, trechos de resultado de busca e o verso dos cartões.
- **Hierarquia** (revista em 10/11/2026): **um título é sempre maior que o que vem abaixo dele.**

  | Nível | Tamanho e peso | Cor | Classe | Uso |
  |---|---|---|---|---|
  | Título da página | 28 px, 700, −0,022em | `--texto` | `.pagina h1` | Um por tela |
  | Título de seção | 20 px, 650, −0,012em | `--texto` | `.rotulo` | O que agrupa uma lista ou um bloco ("Continuar assistindo", "Contêineres") |
  | Título de item | 17 px, 650 | `--texto` | — | A linha de uma lista |
  | Descrição | 14,5 px, 400 | `--texto-suave` | `.descricao`, `.descricao-da-pagina` | O resumo do item ou da página |
  | Metadado | 12 px | `--texto-fraco` | — | Data, versão, contagens pequenas |
  | Rótulo de campo ou de número | 13 px, 600 | `--texto-suave` | `.rotulo-campo`, `.rotulo-numero` | Em cima de um campo de formulário ou de um número grande, nunca de uma lista |

  O erro que isso corrige: um título de seção pequeno e cinza (13 px) em cima de itens maiores
  (17 px) fazia a seção parecer menos importante que o que ela contém.
- **Leitura:** 17,5 px com entrelinha 1,72, coluna de no máximo 720 px.

## Forma e espaço

- **Raios:** 8 px (botão, campo, item de menu), 12 px (aviso, cartão) e 18 px (painel, folha,
  bloco de destaque); a pílula usa 999 px.
- **Margem lateral:** 20 px no computador e 16 px no celular. Coluna de 720 px.
- **Sombra:** só em painéis que flutuam (`--sombra`) e, bem leve, no item ativo do menu.
- **Blur:** `blur(20px) saturate(1.6)` (`--blur`); o véu atrás de uma folha usa 4 px.
- **Foco:** contorno de 2 px na cor do app; o campo em foco ganha também um halo de 3 px dela.

## Componentes

- **Navegação:** no computador, menu lateral de 216 px (240 px na Biblioteca) em `--lateral`. No
  topo, o ícone do app num quadrado de 28 px na cor do app, com o nome ao lado em 16 px e peso 700.
  Itens de 34 px com ícone de 20 px; o ativo fica em `--item-ativo` com um fio em volta e o ícone na
  cor do app. No celular, **abas de vidro** no pé da tela, com a aba ativa na cor do app; no leitor
  elas somem e o voltar fica na barra do topo.
- **Barra do leitor:** fixa, de vidro, com voltar, onde estou e ações em ícones redondos.
- **Lista:** `.lista` com um fio no topo e um em cada linha, linhas de 14 a 18 px de respiro e
  hover que muda só a cor do título para a cor do app.
- **Números com nome:** contagens aparecem como chips com o número e o que ele conta ("3 novos",
  "2 para revisar"), nunca números soltos explicados por uma legenda. Os zerados não aparecem.
- **Filtros:** pílulas sem borda; a ativa ganha `--superficie-forte` e negrito.
- **Chips de estado:** neutro (cinza), azul (andando/pronto) ou vermelho (erro).
- **Bloco de destaque:** o atalho mais usado da tela (o "Continuar lendo" da Biblioteca) fica num
  bloco em `--superficie` com raio de 18 px e rótulo na cor do app. Um por tela, no máximo.
- **Aviso rápido:** pílula de vidro no pé da tela, que some em 4 s.
- **Folha de ações:** sobe do pé da tela, com raio de 18 px e véu com blur leve.
- **Ajustes:** último item do menu, discreto. **Em níveis** (10/11/2026): a primeira tela só tem o
  título "Ajustes" e a lista das seções (`.lista-de-ajustes`: nome em 17 px, descrição de uma linha
  e uma seta); cada seção abre numa página própria (`#/ajustes/<seção>`) com "‹ Ajustes" para
  voltar, o nome da seção como título e uma descrição (`.descricao-da-pagina`). Nada de listar o
  conteúdo de todas as seções numa tela só. Uma lista dentro de uma seção (as instruções da IA)
  segue o mesmo padrão, e o item abre a sua página. Toda app tem a seção **Sugestões de melhoria**
  (campo de texto, botão "Enviar sugestão" e a lista com estado e a resposta do Claude), sobre a
  API comum `/api/sugestoes` (ver ARQUITETURA do Hub); as que usam IA têm também **Instruções da
  IA**.
- **Barra de seleção:** pílula escura (`--destaque`) com ícones de 20 px, que aparece ao
  selecionar texto; no celular fica abaixo da seleção, porque em cima vem o menu do sistema.
- **Trecho com explicação:** sublinhado como link (pontilhado e cinza enquanto a IA trabalha); o
  toque abre uma folha com a explicação e as fontes, sem sair da página.
- **Citação:** fio de 2 px na cor do app à esquerda, texto em `--texto-suave` e itálico.
- **Números e gráficos:** número grande (28 px, peso 600) com rótulo de grupo em cima e o detalhe
  em `--texto-suave` embaixo, sem cartão. Gráfico de uma série em `--azul`: colunas de até 24 px
  com a ponta arredondada (4 px), fios do eixo em `--linha` e o valor do dia escolhido numa linha
  acima, ao tocar ou passar o mouse. Intensidade (calendário) em tons do mesmo azul.
- **Barra de uso (Hub):** nome e "X de Y" numa linha, barra de 8 px em `--azul` sobre
  `--azul-fundo`; passa para `--vermelho` acima de 85%, com o chip "Quase cheio". Partes de um
  todo (o disco da VM) vão numa barra empilhada de 14 px, com 2 px entre as partes, e uma legenda
  com o valor de cada uma.
- **Resultado de busca no texto:** linha da lista com o título (peso 500), onde está (página e
  capítulo) em `--texto-suave` e o trecho em Literata, com as palavras achadas em `<mark>` de
  fundo `--azul-fundo`. Tocar na linha leva ao ponto achado.
- **Botão flutuante (extensão do Chrome):** círculo de vidro de 36 px no canto de baixo à direita,
  com o ícone do app de destino em `--texto`; ao abrir a página (5 s) e ao passar o mouse, abre numa
  pílula com o nome da ação. Depois de enviar, mostra o resultado (✓ em azul, erro em vermelho) e
  o link **Abrir** em azul, e recolhe em 6 s.
- **Ícone da app:** um desenho de linha preta (traço de 12 px em 512) sobre fundo branco, centrado,
  diferente em cada app: livro aberto na Biblioteca, livros na estante no Acervo, cartões sobrepostos
  no Flashcards, tela com um triângulo de "tocar" no Streaming, dois servidores empilhados no Hub, bandeja com uma seta saindo na extensão do Chrome (traço mais grosso, porque vive em
  16 px na barra). Dentro da app, o mesmo desenho aparece em branco sobre a cor do app, no menu.

## Evitar

São os sinais mais comuns de tela genérica e ficaram de fora de propósito:

- Rótulos em maiúsculas espaçadas acima dos grupos.
- Serifa em títulos de interface (a serifa é para o texto de leitura).
- Fundo creme com destaque terracota.
- Números soltos explicados por uma legenda separada.
- Destacar uma palavra só de um título com outra cor ou peso.

## Referências que inspiraram

- Apple HIG, *Materials*: vidro e blur só nas camadas que flutuam.
- Readwise Reader, iA Writer e Apple Books: texto em primeiro lugar e serifa na leitura.
- Linear e Things: listas finas, interface neutra e uma cor só para ação.
- Microsoft 365 e os apps da Apple: uma cor por app de uma mesma família, para se saber onde se
  está.
- Referências visuais escolhidas pelo Lucas: pendentes, para a terceira revisão.
