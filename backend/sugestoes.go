package main

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Sugestões de melhoria que o Lucas anota no próprio app (Ajustes). O Claude Code lê pela API,
// faz o que foi pedido e marca como feita, com uma resposta. A API é igual em todas as apps.
type melhoria struct {
	ID           string `json:"id"`
	Texto        string `json:"texto"`
	Tela         string `json:"tela"`
	Estado       string `json:"estado"` // nova, feita ou descartada
	Resposta     string `json:"resposta"`
	CriadaEm     string `json:"criada_em"`
	AtualizadaEm string `json:"atualizada_em"`
}

// O id vem do aparelho, para o envio repetido não duplicar a sugestão.
var idDeSugestao = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

var estadosDeSugestao = map[string]bool{"nova": true, "feita": true, "descartada": true}

func (a *app) handleSugestoes(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT id, texto, tela, estado, resposta, criada_em, atualizada_em
		FROM sugestoes ORDER BY estado != 'nova', criada_em DESC`)
	if err != nil {
		falha(w, err)
		return
	}
	defer rows.Close()
	lista := []melhoria{}
	for rows.Next() {
		var s melhoria
		if err := rows.Scan(&s.ID, &s.Texto, &s.Tela, &s.Estado, &s.Resposta, &s.CriadaEm, &s.AtualizadaEm); err != nil {
			falha(w, err)
			return
		}
		lista = append(lista, s)
	}
	if err := rows.Err(); err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lista)
}

// handleGuardarSugestao cria a sugestão ou corrige o texto de uma que ainda não foi feita.
func (a *app) handleGuardarSugestao(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !idDeSugestao.MatchString(id) {
		erroJSON(w, http.StatusBadRequest, "Id inválido.")
		return
	}
	var corpo struct {
		Texto string `json:"texto"`
		Tela  string `json:"tela"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	texto := strings.TrimSpace(corpo.Texto)
	if texto == "" {
		erroJSON(w, http.StatusBadRequest, "Escreva a sugestão.")
		return
	}
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO sugestoes (id, texto, tela, estado, criada_em, atualizada_em)
		VALUES (?, ?, ?, 'nova', ?, ?)
		ON CONFLICT (id) DO UPDATE SET texto = excluded.texto, atualizada_em = excluded.atualizada_em
		WHERE estado = 'nova'`, id, texto, strings.TrimSpace(corpo.Tela), a.agoraEmTexto(), a.agoraEmTexto())
	if err != nil {
		falha(w, err)
		return
	}
	a.responderSugestao(w, r, id)
}

// handleMarcarSugestao muda o estado e deixa uma resposta; é o que o Claude Code usa ao terminar.
func (a *app) handleMarcarSugestao(w http.ResponseWriter, r *http.Request) {
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
	res, err := a.db.ExecContext(r.Context(), `UPDATE sugestoes SET estado = ?, resposta = ?, atualizada_em = ? WHERE id = ?`,
		corpo.Estado, strings.TrimSpace(corpo.Resposta), a.agoraEmTexto(), r.PathValue("id"))
	if err != nil {
		falha(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		erroJSON(w, http.StatusNotFound, "Sugestão não encontrada.")
		return
	}
	a.responderSugestao(w, r, r.PathValue("id"))
}

func (a *app) handleApagarSugestao(w http.ResponseWriter, r *http.Request) {
	if _, err := a.db.ExecContext(r.Context(), `DELETE FROM sugestoes WHERE id = ?`, r.PathValue("id")); err != nil {
		falha(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// agoraEmTexto: a data como o navegador lê (o time.Time direto no SQLite vira um texto que o
// Date do JavaScript não entende).
func (a *app) agoraEmTexto() string {
	return a.agora().UTC().Format(time.RFC3339)
}

func (a *app) responderSugestao(w http.ResponseWriter, r *http.Request, id string) {
	var s melhoria
	err := a.db.QueryRowContext(r.Context(), `SELECT id, texto, tela, estado, resposta, criada_em, atualizada_em
		FROM sugestoes WHERE id = ?`, id).Scan(&s.ID, &s.Texto, &s.Tela, &s.Estado, &s.Resposta, &s.CriadaEm, &s.AtualizadaEm)
	if errors.Is(err, sql.ErrNoRows) {
		erroJSON(w, http.StatusNotFound, "Sugestão não encontrada.")
		return
	}
	if err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}
