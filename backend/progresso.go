package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// Onde o Lucas parou em cada filme e episódio, venha do player do app ou do que a tela informa.
// Alimenta "Continuar assistindo" e o "Assistir na TV" a partir do mesmo ponto.

type progresso struct {
	Chave        string  `json:"chave"`
	Titulo       string  `json:"titulo"`
	Subtitulo    string  `json:"subtitulo,omitempty"`
	Capa         string  `json:"capa,omitempty"`
	SerieID      int64   `json:"serie_id,omitempty"`
	Posicao      float64 `json:"posicao"`
	Duracao      float64 `json:"duracao"`
	AtualizadoEm string  `json:"atualizado_em"`
	Pagina       string  `json:"pagina,omitempty"` // onde ver o item: a página do filme, da série ou do torrent
}

var reChaveGuardavel = regexp.MustCompile(`^(filme|episodio|arquivo):\d+$`)

func (a *app) progressoDe(ctx context.Context, chave string) *progresso {
	var p progresso
	err := a.db.QueryRowContext(ctx, `SELECT chave, titulo, subtitulo, capa, serie_id, posicao, duracao, atualizado_em
		FROM progresso WHERE chave = ?`, chave).
		Scan(&p.Chave, &p.Titulo, &p.Subtitulo, &p.Capa, &p.SerieID, &p.Posicao, &p.Duracao, &p.AtualizadoEm)
	if err != nil {
		return nil
	}
	return &p
}

// guardarProgresso monta título e capa a partir do catálogo, para a lista não depender do aparelho.
func (a *app) guardarProgresso(ctx context.Context, chave string, posicao, duracao float64) error {
	tipo, idTexto, _ := strings.Cut(chave, ":")
	id, _ := strconv.ParseInt(idTexto, 10, 64)
	p := progresso{Chave: chave, Posicao: posicao, Duracao: duracao}
	switch tipo {
	case "filme":
		it, err := a.item(ctx, "filme", id)
		if err != nil {
			return err
		}
		p.Titulo, p.Capa = it.Nome, it.Capa
		if p.Subtitulo = it.Ano; p.Subtitulo == "" {
			p.Subtitulo = "Filme"
		}
	case "episodio":
		e, err := a.episodio(ctx, id)
		if err != nil {
			return err
		}
		s, err := a.item(ctx, "serie", e.SerieID)
		if err != nil {
			return err
		}
		p.Titulo, p.Capa, p.SerieID = s.Nome, s.Capa, s.ID
		p.Subtitulo = fmt.Sprintf("T%d E%d · %s", e.Temporada, e.Numero, e.Titulo)
	case "arquivo":
		f, t, err := a.arquivo(ctx, id)
		if err != nil {
			return err
		}
		p.Titulo, p.Capa = tituloDoArquivo(f, t), t.Capa
		p.Subtitulo = "Torrent"
		if t.Ano != "" {
			p.Subtitulo = t.Ano + " · torrent"
		}
	default:
		return sql.ErrNoRows
	}
	_, err := a.db.ExecContext(ctx, `INSERT INTO progresso (chave, titulo, subtitulo, capa, serie_id, posicao, duracao, atualizado_em)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (chave) DO UPDATE SET titulo = excluded.titulo, subtitulo = excluded.subtitulo, capa = excluded.capa,
			serie_id = excluded.serie_id, posicao = excluded.posicao, duracao = excluded.duracao, atualizado_em = excluded.atualizado_em`,
		p.Chave, p.Titulo, p.Subtitulo, p.Capa, p.SerieID, p.Posicao, p.Duracao, a.carimbo())
	return err
}

func (a *app) guardarProgressoDaTela(s sessao, posicao, duracao float64) {
	if reChaveGuardavel.MatchString(s.Chave) {
		_ = a.guardarProgresso(context.Background(), s.Chave, posicao, duracao)
	}
}

// handleContinuar lista o que está pela metade, do mais recente ao mais antigo; de cada série,
// só o último episódio.
func (a *app) handleContinuar(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT chave, titulo, subtitulo, capa, serie_id, posicao, duracao, atualizado_em
		FROM progresso WHERE posicao > 30 AND (duracao = 0 OR posicao < duracao * 0.95)
		ORDER BY atualizado_em DESC LIMIT 60`)
	if err != nil {
		falha(w, err)
		return
	}
	defer rows.Close()
	lista := []progresso{}
	series := map[int64]bool{}
	for rows.Next() {
		var p progresso
		if err := rows.Scan(&p.Chave, &p.Titulo, &p.Subtitulo, &p.Capa, &p.SerieID, &p.Posicao, &p.Duracao, &p.AtualizadoEm); err != nil {
			falha(w, err)
			return
		}
		p.Pagina = a.paginaDe(r.Context(), p)
		if p.SerieID != 0 {
			if series[p.SerieID] {
				continue
			}
			series[p.SerieID] = true
		}
		lista = append(lista, p)
		if len(lista) == 20 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lista)
}

func (a *app) handleGuardarProgresso(w http.ResponseWriter, r *http.Request) {
	chave := r.PathValue("chave")
	if !reChaveGuardavel.MatchString(chave) {
		erroJSON(w, http.StatusBadRequest, "Vídeo inválido.")
		return
	}
	var corpo struct {
		Posicao float64 `json:"posicao"`
		Duracao float64 `json:"duracao"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	err := a.guardarProgresso(r.Context(), chave, max(0, corpo.Posicao), max(0, corpo.Duracao))
	if errors.Is(err, sql.ErrNoRows) {
		erroJSON(w, http.StatusNotFound, "Vídeo não encontrado.")
		return
	}
	if err != nil {
		falha(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) handleApagarProgresso(w http.ResponseWriter, r *http.Request) {
	if _, err := a.db.ExecContext(r.Context(), `DELETE FROM progresso WHERE chave = ?`, r.PathValue("chave")); err != nil {
		falha(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) paginaDe(ctx context.Context, p progresso) string {
	tipo, id, _ := strings.Cut(p.Chave, ":")
	switch {
	case p.SerieID != 0:
		return fmt.Sprintf("#/serie/%d", p.SerieID)
	case tipo == "filme":
		return "#/filme/" + id
	case tipo == "arquivo":
		var hash string
		if a.db.QueryRowContext(ctx, `SELECT hash FROM torrent_arquivos WHERE id = ?`, id).Scan(&hash) == nil {
			return "#/torrent/" + hash
		}
	}
	return ""
}
