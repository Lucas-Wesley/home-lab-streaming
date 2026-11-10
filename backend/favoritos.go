package main

import (
	"net/http"
	"strconv"
)

// Favoritos: canais, filmes e séries marcados pelo Lucas, na aba Favoritos.
// Ficam pelo id do provedor; se o item sair do catálogo, some da lista (mas a marca fica, se ele voltar).

func (a *app) handleFavoritos(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT `+colunasDoItem+` FROM favoritos f
		JOIN itens i ON i.tipo = f.tipo AND i.id = f.id
		ORDER BY f.marcado_em DESC`)
	if err != nil {
		falha(w, err)
		return
	}
	lista, err := lerItens(rows)
	if err != nil {
		falha(w, err)
		return
	}
	resultado := map[string][]item{"canal": {}, "filme": {}, "serie": {}}
	for _, it := range lista {
		resultado[it.Tipo] = append(resultado[it.Tipo], it)
	}
	writeJSON(w, http.StatusOK, resultado)
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

func (a *app) handleMarcarFavorito(w http.ResponseWriter, r *http.Request) {
	tipo := r.PathValue("tipo")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if !tiposValidos[tipo] || err != nil {
		erroJSON(w, http.StatusNotFound, "Item inválido.")
		return
	}
	if r.Method == http.MethodDelete {
		_, err = a.db.ExecContext(r.Context(), `DELETE FROM favoritos WHERE tipo = ? AND id = ?`, tipo, id)
	} else {
		_, err = a.db.ExecContext(r.Context(), `INSERT INTO favoritos (tipo, id, marcado_em) VALUES (?, ?, ?)
			ON CONFLICT (tipo, id) DO NOTHING`, tipo, id, a.carimbo())
	}
	if err != nil {
		falha(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
