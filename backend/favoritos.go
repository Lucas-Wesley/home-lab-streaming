package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Favoritos: canais, filmes e séries marcados pelo Lucas, na aba Favoritos. Ficam pelo id do
// provedor; se o item sair do catálogo, some da lista (mas a marca fica, se ele voltar).
// Ao favoritar dá para escolher uma pasta (S50); a pasta 0 é "sem pasta". Apagar uma pasta não
// desfavorita nada: os itens dela voltam para "sem pasta".

const esquemaDasPastas = `
CREATE TABLE IF NOT EXISTS pastas (
	id        INTEGER PRIMARY KEY,
	nome      TEXT NOT NULL COLLATE NOCASE UNIQUE,
	criada_em TEXT NOT NULL
);
-- S58: títulos do TMDB favoritados sem estar no catálogo (tipo "tmdb-filme" ou "tmdb-serie", id do TMDB);
-- guardam o que a aba Favoritos precisa mostrar. A marca em si fica em favoritos, com a pasta.
CREATE TABLE IF NOT EXISTS favoritos_tmdb (
	tipo TEXT NOT NULL,
	id   INTEGER NOT NULL,
	nome TEXT NOT NULL,
	capa TEXT NOT NULL DEFAULT '',
	ano  TEXT NOT NULL DEFAULT '',
	nota REAL NOT NULL DEFAULT 0,
	PRIMARY KEY (tipo, id)
);
`

// Favorito do TMDB (S58): vale para quem não está na assinatura. A chave é "tmdb-filme:<id do TMDB>".
var tiposFavoritosTMDB = map[string]bool{"tmdb-filme": true, "tmdb-serie": true}

func tipoDeFavorito(tipo string) bool { return tiposValidos[tipo] || tiposFavoritosTMDB[tipo] }

type pasta struct {
	ID   int64  `json:"id"`
	Nome string `json:"nome"`
}

// Um grupo de favoritos, por tipo; vale para os sem pasta na página de favoritos.
type favoritosPorTipo struct {
	Canal []item `json:"canal"`
	Filme []item `json:"filme"`
	Serie []item `json:"serie"`
}

func (a *app) pastas(ctx context.Context) ([]pasta, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id, nome FROM pastas ORDER BY nome`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lista := []pasta{}
	for rows.Next() {
		var p pasta
		if err := rows.Scan(&p.ID, &p.Nome); err != nil {
			return nil, err
		}
		lista = append(lista, p)
	}
	return lista, rows.Err()
}

// handleFavoritos: as pastas com os itens delas (numa lista só, na ordem da marca) e, em "itens",
// os sem pasta por tipo.
func (a *app) handleFavoritos(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT `+colunasDoItem+`, f.pasta_id, f.marcado_em FROM favoritos f
		JOIN itens i ON i.tipo = f.tipo AND i.id = f.id
		ORDER BY f.marcado_em DESC`)
	if err != nil {
		falha(w, err)
		return
	}
	type marcado struct {
		it    item
		pasta int64
		em    string
	}
	marcados := []marcado{}
	for rows.Next() {
		var m marcado
		if err := rows.Scan(&m.it.Tipo, &m.it.ID, &m.it.Nome, &m.it.Capa, &m.it.Nota, &m.it.Ano, &m.it.Extensao, &m.pasta, &m.em); err != nil {
			rows.Close()
			falha(w, err)
			return
		}
		marcados = append(marcados, m)
	}
	rows.Close()

	// Os do TMDB (S58) entram como filme ou série, com a página do título; a chave guarda a marca.
	doTMDB, err := a.db.QueryContext(r.Context(), `SELECT t.tipo, t.id, t.nome, t.capa, t.nota, t.ano, f.pasta_id, f.marcado_em
		FROM favoritos f JOIN favoritos_tmdb t ON t.tipo = f.tipo AND t.id = f.id`)
	if err != nil {
		falha(w, err)
		return
	}
	for doTMDB.Next() {
		var m marcado
		var tipo string
		if err := doTMDB.Scan(&tipo, &m.it.ID, &m.it.Nome, &m.it.Capa, &m.it.Nota, &m.it.Ano, &m.pasta, &m.em); err != nil {
			doTMDB.Close()
			falha(w, err)
			return
		}
		m.it.Tipo = strings.TrimPrefix(tipo, "tmdb-")
		m.it.Chave = tipo + ":" + strconv.FormatInt(m.it.ID, 10)
		m.it.Pagina = "#/titulo/" + m.it.Tipo + "/" + strconv.FormatInt(m.it.ID, 10)
		marcados = append(marcados, m)
	}
	doTMDB.Close()
	sort.SliceStable(marcados, func(i, j int) bool { return marcados[i].em > marcados[j].em })

	pastas, err := a.pastas(r.Context())
	if err != nil {
		falha(w, err)
		return
	}
	comPasta := map[int64][]item{}
	for _, m := range marcados {
		comPasta[m.pasta] = append(comPasta[m.pasta], m.it)
	}
	tipo := func(itens []item, querido string) []item {
		doTipo := []item{}
		for _, it := range itens {
			if it.Tipo == querido {
				doTipo = append(doTipo, it)
			}
		}
		return doTipo
	}
	dasPastas := []map[string]any{}
	for _, p := range pastas {
		dasPastas = append(dasPastas, map[string]any{"id": p.ID, "nome": p.Nome, "itens": comPasta[p.ID]})
		delete(comPasta, p.ID)
	}
	semPasta := favoritosPorTipo{Canal: tipo(comPasta[0], "canal"), Filme: tipo(comPasta[0], "filme"), Serie: tipo(comPasta[0], "serie")}
	delete(comPasta, 0)
	// Pasta que sumiu do banco (não deveria): os itens caem em "sem pasta" em vez de sumir.
	for _, perdidos := range comPasta {
		for _, it := range perdidos {
			switch it.Tipo {
			case "canal":
				semPasta.Canal = append(semPasta.Canal, it)
			case "filme":
				semPasta.Filme = append(semPasta.Filme, it)
			case "serie":
				semPasta.Serie = append(semPasta.Serie, it)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"pastas": dasPastas, "itens": semPasta})
}

// handleChavesFavoritas devolve só as chaves ("filme:12"), para o app marcar o coração em qualquer tela.
func (a *app) handleChavesFavoritas(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT tipo || ':' || id FROM favoritos`)
	if err != nil {
		falha(w, err)
		return
	}
	defer rows.Close()
	chaves := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			falha(w, err)
			return
		}
		chaves = append(chaves, c)
	}
	writeJSON(w, http.StatusOK, chaves)
}

// handleEstadoFavorito: se está marcado e em qual pasta, para a folha de escolha da pasta.
func (a *app) handleEstadoFavorito(w http.ResponseWriter, r *http.Request) {
	tipo := r.PathValue("tipo")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !tipoDeFavorito(tipo) || err != nil {
		erroJSON(w, http.StatusNotFound, "Item inválido.")
		return
	}
	var pasta int64
	err = a.db.QueryRowContext(r.Context(), `SELECT pasta_id FROM favoritos WHERE tipo = ? AND id = ?`, tipo, id).Scan(&pasta)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"marcado": err == nil, "pasta": pasta})
}

// O corpo do PUT: a pasta e, só nos títulos do TMDB (S58), o que a aba Favoritos mostra.
type corpoDoFavorito struct {
	Pasta int64   `json:"pasta"`
	Nome  string  `json:"nome"`
	Capa  string  `json:"capa"`
	Ano   string  `json:"ano"`
	Nota  float64 `json:"nota"`
}

// handleMarcarFavorito: PUT marca (a pasta vem no corpo, 0 = sem pasta) e DELETE desmarca. Um PUT
// de novo no mesmo item só muda a pasta: a data da marca fica.
func (a *app) handleMarcarFavorito(w http.ResponseWriter, r *http.Request) {
	tipo := r.PathValue("tipo")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !tipoDeFavorito(tipo) || err != nil {
		erroJSON(w, http.StatusNotFound, "Item inválido.")
		return
	}
	if r.Method == http.MethodDelete {
		_, err = a.db.ExecContext(r.Context(), `DELETE FROM favoritos WHERE tipo = ? AND id = ?`, tipo, id)
		if err == nil && tiposFavoritosTMDB[tipo] {
			_, err = a.db.ExecContext(r.Context(), `DELETE FROM favoritos_tmdb WHERE tipo = ? AND id = ?`, tipo, id)
		}
	} else {
		var pasta int64
		var dados corpoDoFavorito
		if r.ContentLength != 0 {
			if !lerCorpo(w, r, &dados) {
				return
			}
			pasta = dados.Pasta
		}
		if pasta != 0 {
			var n int
			if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM pastas WHERE id = ?`, pasta).Scan(&n); err != nil {
				falha(w, err)
				return
			}
			if n == 0 {
				erroJSON(w, http.StatusNotFound, "Pasta não encontrada.")
				return
			}
		}
		if tiposFavoritosTMDB[tipo] {
			nome := strings.TrimSpace(dados.Nome)
			if nome == "" {
				var n int
				a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM favoritos_tmdb WHERE tipo = ? AND id = ?`, tipo, id).Scan(&n)
				if n == 0 {
					erroJSON(w, http.StatusBadRequest, "Faltou o nome do título.")
					return
				}
			} else if _, err := a.db.ExecContext(r.Context(), `INSERT INTO favoritos_tmdb (tipo, id, nome, capa, ano, nota) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT (tipo, id) DO UPDATE SET nome = excluded.nome, capa = excluded.capa, ano = excluded.ano, nota = excluded.nota`,
				tipo, id, nome, dados.Capa, dados.Ano, dados.Nota); err != nil {
				falha(w, err)
				return
			}
		}
		_, err = a.db.ExecContext(r.Context(), `INSERT INTO favoritos (tipo, id, marcado_em, pasta_id) VALUES (?, ?, ?, ?)
			ON CONFLICT (tipo, id) DO UPDATE SET pasta_id = excluded.pasta_id`, tipo, id, a.carimbo(), pasta)
	}
	if err != nil {
		falha(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) handlePastas(w http.ResponseWriter, r *http.Request) {
	lista, err := a.pastas(r.Context())
	if err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lista)
}

// nomeDaPasta aceito: sem vazio e sem exagero de tamanho; os espaços duplos viram um.
func nomeDaPasta(nome string) (string, bool) {
	nome = strings.Join(strings.Fields(nome), " ")
	return nome, nome != "" && len([]rune(nome)) <= 60
}

func (a *app) handleCriarPasta(w http.ResponseWriter, r *http.Request) {
	var corpo struct {
		Nome string `json:"nome"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	nome, ok := nomeDaPasta(corpo.Nome)
	if !ok {
		erroJSON(w, http.StatusBadRequest, "Diga um nome para a pasta.")
		return
	}
	var p pasta
	err := a.db.QueryRowContext(r.Context(), `INSERT INTO pastas (nome, criada_em) VALUES (?, ?) RETURNING id, nome`,
		nome, a.carimbo()).Scan(&p.ID, &p.Nome)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			erroJSON(w, http.StatusConflict, "Já existe uma pasta com esse nome.")
			return
		}
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *app) handleMudarPasta(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		erroJSON(w, http.StatusNotFound, "Pasta não encontrada.")
		return
	}
	var corpo struct {
		Nome string `json:"nome"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	nome, ok := nomeDaPasta(corpo.Nome)
	if !ok {
		erroJSON(w, http.StatusBadRequest, "Diga um nome para a pasta.")
		return
	}
	res, err := a.db.ExecContext(r.Context(), `UPDATE pastas SET nome = ? WHERE id = ?`, nome, id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			erroJSON(w, http.StatusConflict, "Já existe uma pasta com esse nome.")
			return
		}
		falha(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		erroJSON(w, http.StatusNotFound, "Pasta não encontrada.")
		return
	}
	writeJSON(w, http.StatusOK, pasta{ID: id, Nome: nome})
}

// handleApagarPasta: os favoritos da pasta voltam para "sem pasta"; nenhum se perde.
func (a *app) handleApagarPasta(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		erroJSON(w, http.StatusNotFound, "Pasta não encontrada.")
		return
	}
	if _, err := a.db.ExecContext(r.Context(), `UPDATE favoritos SET pasta_id = 0 WHERE pasta_id = ?`, id); err != nil {
		falha(w, err)
		return
	}
	res, err := a.db.ExecContext(r.Context(), `DELETE FROM pastas WHERE id = ?`, id)
	if err != nil {
		falha(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		erroJSON(w, http.StatusNotFound, "Pasta não encontrada.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
