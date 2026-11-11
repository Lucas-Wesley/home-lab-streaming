package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Títulos para verificar (S37): na página do título, o menu "⋯" → "Mandar para verificação" guarda o
// título e o que a página via (assinatura, busca, episódio aberto). Não faz nada sozinho: o Claude
// Code lê pela API quando o Lucas pede, investiga e marca como feito, com uma resposta. Roteiro em
// docs/PARA-VERIFICAR.md.

const esquemaDosRelatos = `
CREATE TABLE IF NOT EXISTS relatos (
	id            TEXT PRIMARY KEY,
	tipo          TEXT NOT NULL, -- filme ou serie
	tmdb          INTEGER NOT NULL,
	titulo        TEXT NOT NULL,
	ano           TEXT NOT NULL DEFAULT '',
	temporada     INTEGER NOT NULL DEFAULT 0, -- o último episódio aberto, se houver
	episodio      INTEGER NOT NULL DEFAULT 0,
	texto         TEXT NOT NULL DEFAULT '', -- o que o Lucas notou (opcional)
	contexto      TEXT NOT NULL DEFAULT '{}', -- JSON: o que a página via
	versao        TEXT NOT NULL DEFAULT '',
	estado        TEXT NOT NULL DEFAULT 'nova', -- nova, feita ou descartada
	resposta      TEXT NOT NULL DEFAULT '',
	criada_em     TEXT NOT NULL,
	atualizada_em TEXT NOT NULL
);
`

const limiteDoContexto = 64 << 10

type relato struct {
	ID           string          `json:"id"`
	Tipo         string          `json:"tipo"`
	TMDB         int             `json:"tmdb"`
	Titulo       string          `json:"titulo"`
	Ano          string          `json:"ano"`
	Temporada    int             `json:"temporada"`
	Episodio     int             `json:"episodio"`
	Texto        string          `json:"texto"`
	Contexto     json.RawMessage `json:"contexto"`
	Versao       string          `json:"versao"`
	Estado       string          `json:"estado"`
	Resposta     string          `json:"resposta"`
	CriadaEm     string          `json:"criada_em"`
	AtualizadaEm string          `json:"atualizada_em"`
}

const colunasDoRelato = `id, tipo, tmdb, titulo, ano, temporada, episodio, texto, contexto, versao, estado, resposta, criada_em, atualizada_em`

func lerRelato(s interface{ Scan(...any) error }) (relato, error) {
	var r relato
	var contexto string
	err := s.Scan(&r.ID, &r.Tipo, &r.TMDB, &r.Titulo, &r.Ano, &r.Temporada, &r.Episodio, &r.Texto, &contexto, &r.Versao, &r.Estado, &r.Resposta, &r.CriadaEm, &r.AtualizadaEm)
	r.Contexto = json.RawMessage(contexto)
	return r, err
}

// handleRelatos: os novos primeiro; ?estado=nova traz só os que faltam verificar.
func (a *app) handleRelatos(w http.ResponseWriter, r *http.Request) {
	filtro, args := "", []any{}
	if e := r.URL.Query().Get("estado"); e != "" {
		filtro, args = "WHERE estado = ?", append(args, e)
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT `+colunasDoRelato+` FROM relatos `+filtro+`
		ORDER BY estado != 'nova', criada_em DESC`, args...)
	if err != nil {
		falha(w, err)
		return
	}
	defer rows.Close()
	lista := []relato{}
	for rows.Next() {
		rel, err := lerRelato(rows)
		if err != nil {
			falha(w, err)
			return
		}
		lista = append(lista, rel)
	}
	if err := rows.Err(); err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lista)
}

// handleGuardarRelato: o id vem do aparelho, para o envio repetido não duplicar.
func (a *app) handleGuardarRelato(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idDeSugestao.MatchString(id) {
		erroJSON(w, http.StatusBadRequest, "Id inválido.")
		return
	}
	var corpo struct {
		Tipo      string          `json:"tipo"`
		TMDB      int             `json:"tmdb"`
		Titulo    string          `json:"titulo"`
		Ano       string          `json:"ano"`
		Temporada int             `json:"temporada"`
		Episodio  int             `json:"episodio"`
		Texto     string          `json:"texto"`
		Contexto  json.RawMessage `json:"contexto"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	if (corpo.Tipo != "filme" && corpo.Tipo != "serie") || corpo.TMDB <= 0 || strings.TrimSpace(corpo.Titulo) == "" {
		erroJSON(w, http.StatusBadRequest, "Diga o título: tipo (filme ou serie), tmdb e nome.")
		return
	}
	contexto := "{}"
	if len(corpo.Contexto) > 0 {
		if len(corpo.Contexto) > limiteDoContexto || !json.Valid(corpo.Contexto) {
			erroJSON(w, http.StatusBadRequest, "Contexto inválido.")
			return
		}
		contexto = string(corpo.Contexto)
	}
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO relatos (id, tipo, tmdb, titulo, ano, temporada, episodio, texto, contexto, versao, estado, criada_em, atualizada_em)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'nova', ?, ?)
		ON CONFLICT (id) DO UPDATE SET texto = excluded.texto, contexto = excluded.contexto, atualizada_em = excluded.atualizada_em
		WHERE estado = 'nova'`,
		id, corpo.Tipo, corpo.TMDB, strings.TrimSpace(corpo.Titulo), corpo.Ano, corpo.Temporada, corpo.Episodio,
		strings.TrimSpace(corpo.Texto), contexto, version, a.agoraEmTexto(), a.agoraEmTexto())
	if err != nil {
		falha(w, err)
		return
	}
	a.responderRelato(w, r, id)
}

// handleMarcarRelato: o Claude Code marca como feito (ou descartado) e diz o que achou.
func (a *app) handleMarcarRelato(w http.ResponseWriter, r *http.Request) {
	var corpo struct {
		Estado   string `json:"estado"`
		Resposta string `json:"resposta"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	if !estadosDeSugestao[corpo.Estado] {
		erroJSON(w, http.StatusBadRequest, "Estado inválido: use nova, feita ou descartada.")
		return
	}
	res, err := a.db.ExecContext(r.Context(), `UPDATE relatos SET estado = ?, resposta = ?, atualizada_em = ? WHERE id = ?`,
		corpo.Estado, strings.TrimSpace(corpo.Resposta), a.agoraEmTexto(), r.PathValue("id"))
	if err != nil {
		falha(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		erroJSON(w, http.StatusNotFound, "Título para verificar não encontrado.")
		return
	}
	a.responderRelato(w, r, r.PathValue("id"))
}

func (a *app) handleApagarRelato(w http.ResponseWriter, r *http.Request) {
	if _, err := a.db.ExecContext(r.Context(), `DELETE FROM relatos WHERE id = ?`, r.PathValue("id")); err != nil {
		falha(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) responderRelato(w http.ResponseWriter, r *http.Request, id string) {
	rel, err := lerRelato(a.db.QueryRowContext(r.Context(), `SELECT `+colunasDoRelato+` FROM relatos WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		erroJSON(w, http.StatusNotFound, "Título para verificar não encontrado.")
		return
	}
	if err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rel)
}
