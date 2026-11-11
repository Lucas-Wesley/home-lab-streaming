package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// O catálogo vem da API do painel (S3) e fica inteiro no SQLite: a busca não depende do provedor.

type estadoDaSincronizacao struct {
	Rodando     bool   `json:"rodando"`
	Etapa       string `json:"etapa,omitempty"`
	Erro        string `json:"erro,omitempty"`
	TerminadaEm string `json:"terminada_em,omitempty"`
}

// tiposDoProvedor liga o tipo do app às ações da API do painel.
var tiposDoProvedor = []struct {
	tipo, nome, categorias, itens string
}{
	{"canal", "canais", "get_live_categories", "get_live_streams"},
	{"filme", "filmes", "get_vod_categories", "get_vod_streams"},
	{"serie", "séries", "get_series_categories", "get_series"},
}

// Categorias adultas ficam escondidas, a não ser que o Lucas mude em Ajustes (S10).
var reAdulto = regexp.MustCompile(`(?i)adult|xxx|\+ ?18|er[oó]tic`)

func (a *app) sincronizarSempre(ctx context.Context) {
	if a.cfg.intervaloDoCatalogo <= 0 {
		return
	}
	verificar := func() {
		ultima, _ := a.ajuste("catalogo_em")
		t, err := time.Parse(time.RFC3339, ultima)
		if err != nil || a.agora().Sub(t) >= a.cfg.intervaloDoCatalogo {
			a.sincronizar(ctx)
		}
	}
	verificar()
	relogio := time.NewTicker(time.Hour)
	defer relogio.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-relogio.C:
			verificar()
		}
	}
}

func (a *app) estadoDoCatalogo() estadoDaSincronizacao {
	a.sincMu.Lock()
	defer a.sincMu.Unlock()
	e := a.sincroni
	if !e.Rodando {
		e.TerminadaEm, _ = a.ajuste("catalogo_em")
	}
	return e
}

func (a *app) marcarEtapa(etapa string) {
	a.sincMu.Lock()
	a.sincroni.Etapa = etapa
	a.sincMu.Unlock()
}

// sincronizar baixa o catálogo inteiro e troca o do banco de uma vez. Uma sincronização por vez.
func (a *app) sincronizar(ctx context.Context) {
	a.sincMu.Lock()
	if a.sincroni.Rodando {
		a.sincMu.Unlock()
		return
	}
	a.sincroni = estadoDaSincronizacao{Rodando: true, Etapa: "Conferindo a assinatura"}
	a.sincMu.Unlock()

	err := a.baixarCatalogo(ctx)

	a.sincMu.Lock()
	a.sincroni = estadoDaSincronizacao{}
	if err != nil {
		a.sincroni.Erro = err.Error()
		log.Printf("catálogo: %v", err)
	}
	a.sincMu.Unlock()
}

func (a *app) baixarCatalogo(ctx context.Context) error {
	ac, err := a.acesso()
	if err != nil {
		return err
	}
	if !ac.completo() {
		return nil
	}
	conta, err := a.consultarConta(ctx, ac)
	if err != nil {
		return err
	}
	if err := a.guardarConta(conta); err != nil {
		return err
	}

	type baixado struct {
		categorias []categoriaDoProvedor
		itens      []itemDoProvedor
	}
	dados := map[string]baixado{}
	for _, t := range tiposDoProvedor {
		a.marcarEtapa("Baixando " + t.nome)
		var b baixado
		if err := a.buscarJSON(ctx, ac.urlDaApi(t.categorias, nil), &b.categorias); err != nil {
			return fmt.Errorf("categorias de %s: %w", t.nome, err)
		}
		if err := a.buscarJSON(ctx, ac.urlDaApi(t.itens, nil), &b.itens); err != nil {
			return fmt.Errorf("%s: %w", t.nome, err)
		}
		dados[t.tipo] = b
	}

	a.marcarEtapa("Guardando o catálogo")
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, apagar := range []string{`DELETE FROM categorias`, `DELETE FROM itens`} {
		if _, err := tx.Exec(apagar); err != nil {
			return err
		}
	}
	cat, err := tx.Prepare(`INSERT OR REPLACE INTO categorias (tipo, id, nome, adulto, ordem) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer cat.Close()
	item, err := tx.Prepare(`INSERT OR REPLACE INTO itens (tipo, id, nome, busca, categoria_id, capa, nota, ano, extensao, adicionado, ordem)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer item.Close()
	for tipo, b := range dados {
		for i, c := range b.categorias {
			if _, err := cat.Exec(tipo, string(c.ID), nomeDaCategoria(c.Nome), reAdulto.MatchString(c.Nome), i); err != nil {
				return err
			}
		}
		for i, it := range b.itens {
			id, capa, adicionado := it.StreamID.int(), it.Icone, it.Adicionado.int()
			if tipo == "serie" {
				id, capa, adicionado = it.SeriesID.int(), it.Capa, it.Modificado.int()
			}
			if id == 0 {
				continue
			}
			nome := strings.TrimSpace(it.Nome)
			if _, err := item.Exec(tipo, id, nome, normalizar(nome), string(it.Categoria), capa, it.Nota.float(),
				anoDe(it), it.Extensao, adicionado, i); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`INSERT INTO ajustes (chave, valor) VALUES ('catalogo_em', ?)
		ON CONFLICT (chave) DO UPDATE SET valor = excluded.valor`, a.carimbo()); err != nil {
		return err
	}
	return tx.Commit()
}

// Os painéis escrevem "FILMES | AÇÃO" ou "CANAIS | +PLUTOTV BR": o tipo sai da frente e as
// maiúsculas viram caixa normal, menos siglas e marcas.
var (
	rePrefixoDoTipo    = regexp.MustCompile(`(?i)^\s*(canais|filmes|s[ée]ries|vod|live)\s*[|:-]\s*`)
	palavrasMinusculas = map[string]bool{"e": true, "de": true, "da": true, "do": true, "das": true, "dos": true, "em": true, "a": true, "o": true}
	marcas             = map[string]string{"hbomax": "HBO Max", "plutotv": "Pluto TV", "sportv": "SporTV", "espn": "ESPN", "hbo": "HBO",
		"sbt": "SBT", "sbt+": "SBT+", "dazn": "DAZN", "mgm+": "MGM+", "uhd": "UHD", "xxx": "XXX", "us": "EUA", "br": "Brasil",
		"tv": "TV", "apple+": "Apple+", "disney+": "Disney+", "discovery+": "Discovery+", "history+": "History+",
		"record+": "Record+", "+fast": "Fast", "+sbt": "SBT", "+disney": "Disney", "+hbomax": "HBO Max",
		"+paramount": "Paramount", "+plutotv": "Pluto TV", "standup": "Stand-up"}
)

func nomeDaCategoria(bruto string) string {
	nome := rePrefixoDoTipo.ReplaceAllString(strings.TrimSpace(bruto), "")
	if nome == "" {
		nome = strings.TrimSpace(bruto)
	}
	if nome != strings.ToUpper(nome) {
		return nome // já vem em caixa normal
	}
	palavras := strings.Fields(nome)
	for i, p := range palavras {
		baixa := strings.ToLower(p)
		switch {
		case marcas[baixa] != "":
			palavras[i] = marcas[baixa]
		case strings.IndexFunc(p, unicode.IsDigit) >= 0 || p == "|" || strings.Contains(p, "/"):
			palavras[i] = p
		case i > 0 && palavrasMinusculas[baixa]:
			palavras[i] = baixa
		case i == 0:
			r := []rune(baixa)
			palavras[i] = strings.ToUpper(string(r[0])) + string(r[1:])
		default:
			palavras[i] = baixa
		}
	}
	return strings.Join(palavras, " ")
}

var (
	reAno       = regexp.MustCompile(`(19|20)\d\d`)
	reAnoNoNome = regexp.MustCompile(`\(((?:19|20)\d\d)\)\s*$`) // "Filme (2024)"
)

func anoDe(it itemDoProvedor) string {
	for _, s := range []string{string(it.Ano), it.Lancamento, it.Lancamento2} {
		if m := reAno.FindString(s); m != "" {
			return m
		}
	}
	if m := reAnoNoNome.FindStringSubmatch(it.Nome); m != nil {
		return m[1]
	}
	return ""
}

// normalizar deixa o texto em minúsculas e sem acento, para a busca achar "acao" em "Ação". O
// transformador guarda estado: um por chamada, porque dois pedidos ao mesmo tempo o quebravam.
func normalizar(s string) string {
	t, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), s)
	if err != nil {
		t = s
	}
	return strings.ToLower(t)
}

// ---------- Consultas ----------

type categoria struct {
	ID    string `json:"id"`
	Nome  string `json:"nome"`
	Itens int    `json:"itens"`
}

type item struct {
	Tipo     string  `json:"tipo"`
	ID       int64   `json:"id"`
	Nome     string  `json:"nome"`
	Capa     string  `json:"capa"`
	Nota     float64 `json:"nota,omitempty"`
	Ano      string  `json:"ano,omitempty"`
	Extensao string  `json:"extensao,omitempty"`
	// Só na busca do Início (S28): de onde vem e, para os títulos do TMDB, a página deles.
	Origem string `json:"origem,omitempty"` // assinatura ou torrent
	Pagina string `json:"pagina,omitempty"`
	tmdb   int    // o título do TMDB, quando já se sabe (assinatura_tmdb)
}

var tiposValidos = map[string]bool{"canal": true, "filme": true, "serie": true}

func (a *app) handleCategorias(w http.ResponseWriter, r *http.Request) {
	tipo := r.PathValue("tipo")
	if !tiposValidos[tipo] {
		erroJSON(w, http.StatusNotFound, "Tipo desconhecido.")
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT c.id, c.nome, COUNT(i.id)
		FROM categorias c JOIN itens i ON i.tipo = c.tipo AND i.categoria_id = c.id
		WHERE c.tipo = ? AND (c.adulto = 0 OR ?)
		GROUP BY c.id ORDER BY c.ordem`, tipo, a.mostrarAdulto())
	if err != nil {
		falha(w, err)
		return
	}
	defer rows.Close()
	lista := []categoria{}
	for rows.Next() {
		var c categoria
		if err := rows.Scan(&c.ID, &c.Nome, &c.Itens); err != nil {
			falha(w, err)
			return
		}
		lista = append(lista, c)
	}
	if err := rows.Err(); err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lista)
}

const colunasDoItem = `i.tipo, i.id, i.nome, i.capa, i.nota, i.ano, i.extensao`

// Sem as categorias adultas, a não ser que o Lucas tenha pedido para mostrar.
const filtroAdulto = `NOT EXISTS (SELECT 1 FROM categorias c WHERE c.tipo = i.tipo AND c.id = i.categoria_id AND c.adulto = 1 AND NOT ?)`

func lerItens(rows *sql.Rows) ([]item, error) {
	defer rows.Close()
	lista := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.Tipo, &it.ID, &it.Nome, &it.Capa, &it.Nota, &it.Ano, &it.Extensao); err != nil {
			return nil, err
		}
		lista = append(lista, it)
	}
	return lista, rows.Err()
}

// handleItens lista uma categoria, ou os mais recentes do tipo quando a categoria vem vazia.
func (a *app) handleItens(w http.ResponseWriter, r *http.Request) {
	tipo := r.PathValue("tipo")
	if !tiposValidos[tipo] {
		erroJSON(w, http.StatusNotFound, "Tipo desconhecido.")
		return
	}
	q := r.URL.Query()
	limite := numero(q.Get("limite"), 60, 1, 200)
	pular := numero(q.Get("pular"), 0, 0, 1<<30)
	var rows *sql.Rows
	var err error
	if cat := q.Get("categoria"); cat != "" {
		rows, err = a.db.QueryContext(r.Context(), `SELECT `+colunasDoItem+` FROM itens i
			WHERE i.tipo = ? AND i.categoria_id = ? AND `+filtroAdulto+` ORDER BY i.ordem LIMIT ? OFFSET ?`,
			tipo, cat, a.mostrarAdulto(), limite, pular)
	} else {
		rows, err = a.db.QueryContext(r.Context(), `SELECT `+colunasDoItem+` FROM itens i
			WHERE i.tipo = ? AND `+filtroAdulto+` ORDER BY i.adicionado DESC, i.ordem LIMIT ? OFFSET ?`,
			tipo, a.mostrarAdulto(), limite, pular)
	}
	if err != nil {
		falha(w, err)
		return
	}
	lista, err := lerItens(rows)
	if err != nil {
		falha(w, err)
		return
	}
	// Já conhecido no TMDB: a capa abre direto a página do título (S28), sem passar pela do catálogo.
	if tipo != "canal" {
		a.tmdbDosItens(r.Context(), lista)
		for i, it := range lista {
			if it.tmdb > 0 {
				lista[i].Pagina = fmt.Sprintf("#/titulo/%s/%d", tipo, it.tmdb)
			}
		}
	}
	writeJSON(w, http.StatusOK, lista)
}

// handleBuscar procura no nome, com todas as palavras, sem ligar para acento ou caixa.
func (a *app) handleBuscar(w http.ResponseWriter, r *http.Request) {
	palavras := strings.Fields(normalizar(r.URL.Query().Get("q")))
	resultado := map[string][]item{"canal": {}, "filme": {}, "serie": {}}
	if len(palavras) == 0 {
		writeJSON(w, http.StatusOK, resultado)
		return
	}
	if len(palavras) > 8 {
		palavras = palavras[:8]
	}
	onde := make([]string, len(palavras))
	args := []any{}
	for i, p := range palavras {
		onde[i] = `i.busca LIKE ? ESCAPE '\'`
		args = append(args, "%"+escaparLike(p)+"%")
	}
	limite := numero(r.URL.Query().Get("limite"), 40, 1, 200)
	for tipo := range resultado {
		// Primeiro o que começa com a busca, depois o resto, cada parte na ordem do provedor.
		consulta := `SELECT ` + colunasDoItem + ` FROM itens i WHERE i.tipo = ? AND ` + strings.Join(onde, " AND ") +
			` AND ` + filtroAdulto + ` ORDER BY i.busca NOT LIKE ? ESCAPE '\', i.ordem LIMIT ?`
		todos := append([]any{tipo}, args...)
		todos = append(todos, a.mostrarAdulto(), escaparLike(strings.Join(palavras, " "))+"%", limite)
		rows, err := a.db.QueryContext(r.Context(), consulta, todos...)
		if err != nil {
			falha(w, err)
			return
		}
		lista, err := lerItens(rows)
		if err != nil {
			falha(w, err)
			return
		}
		resultado[tipo] = lista
	}
	a.juntarComOTMDB(r.Context(), r.URL.Query().Get("q"), resultado)
	writeJSON(w, http.StatusOK, resultado)
}

func escaparLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func numero(s string, padrao, minimo, maximo int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return padrao
	}
	return max(minimo, min(n, maximo))
}

func (a *app) item(ctx context.Context, tipo string, id int64) (item, error) {
	var it item
	err := a.db.QueryRowContext(ctx, `SELECT `+colunasDoItem+` FROM itens i WHERE i.tipo = ? AND i.id = ?`, tipo, id).
		Scan(&it.Tipo, &it.ID, &it.Nome, &it.Capa, &it.Nota, &it.Ano, &it.Extensao)
	return it, err
}

func (a *app) handleCatalogo(w http.ResponseWriter, r *http.Request) {
	contagem := map[string]int{}
	rows, err := a.db.QueryContext(r.Context(), `SELECT tipo, COUNT(*) FROM itens GROUP BY tipo`)
	if err != nil {
		falha(w, err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			falha(w, err)
			return
		}
		contagem[t] = n
	}
	writeJSON(w, http.StatusOK, map[string]any{"sincronizacao": a.estadoDoCatalogo(), "itens": contagem})
}

func (a *app) handleSincronizar(w http.ResponseWriter, r *http.Request) {
	a.emFundo(func() { a.sincronizar(context.Background()) })
	w.WriteHeader(http.StatusAccepted)
}
