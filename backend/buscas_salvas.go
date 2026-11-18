package main

import (
	"net/http"
	"strings"
)

// Buscas salvas (S44): o Lucas guarda uma busca (achada ou não) para voltar a ela depois — tentar de
// novo, em outros indexadores, com outro nome. Vale para a busca do Início, a direta nos indexadores
// (Downloads) e a página de um título. Guarda só o texto e o endereço da tela onde ela roda.

const esquemaDasBuscasSalvas = `
CREATE TABLE IF NOT EXISTS buscas_salvas (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	texto     TEXT NOT NULL,
	onde      TEXT NOT NULL, -- inicio, torrents ou titulo
	endereco  TEXT NOT NULL, -- a rota do app (#/inicio?q=…), para refazer a busca
	criada_em TEXT NOT NULL,
	UNIQUE (onde, endereco)
);
`

var ondeSeBusca = map[string]bool{"inicio": true, "torrents": true, "titulo": true}

type buscaSalva struct {
	ID       int64  `json:"id"`
	Texto    string `json:"texto"`
	Onde     string `json:"onde"`
	Endereco string `json:"endereco"`
	CriadaEm string `json:"criada_em"`
}

func (a *app) handleBuscasSalvas(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT id, texto, onde, endereco, criada_em FROM buscas_salvas ORDER BY id DESC`)
	if err != nil {
		falha(w, err)
		return
	}
	defer rows.Close()
	lista := []buscaSalva{}
	for rows.Next() {
		var b buscaSalva
		if err := rows.Scan(&b.ID, &b.Texto, &b.Onde, &b.Endereco, &b.CriadaEm); err != nil {
			falha(w, err)
			return
		}
		lista = append(lista, b)
	}
	if err := rows.Err(); err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lista)
}

// handleSalvarBusca: salvar de novo a mesma busca a troca por uma nova, no topo.
func (a *app) handleSalvarBusca(w http.ResponseWriter, r *http.Request) {
	var corpo struct {
		Texto    string `json:"texto"`
		Onde     string `json:"onde"`
		Endereco string `json:"endereco"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	texto := strings.TrimSpace(corpo.Texto)
	if texto == "" || len(texto) > 300 || !ondeSeBusca[corpo.Onde] || !strings.HasPrefix(corpo.Endereco, "#/") || len(corpo.Endereco) > 600 {
		erroJSON(w, http.StatusBadRequest, "Diga o texto da busca, onde ela foi feita (inicio, torrents ou titulo) e o endereço (#/…).")
		return
	}
	var b buscaSalva
	err := a.db.QueryRowContext(r.Context(), `INSERT OR REPLACE INTO buscas_salvas (texto, onde, endereco, criada_em) VALUES (?, ?, ?, ?)
		RETURNING id, texto, onde, endereco, criada_em`,
		texto, corpo.Onde, corpo.Endereco, a.agoraEmTexto()).Scan(&b.ID, &b.Texto, &b.Onde, &b.Endereco, &b.CriadaEm)
	if err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (a *app) handleApagarBuscaSalva(w http.ResponseWriter, r *http.Request) {
	if _, err := a.db.ExecContext(r.Context(), `DELETE FROM buscas_salvas WHERE id = ?`, r.PathValue("id")); err != nil {
		falha(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
