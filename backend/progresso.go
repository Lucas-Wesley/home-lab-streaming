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
	tmdb         string  // "filme/62" ou "serie/1396", quando se sabe
}

// Quantos itens "Continuar assistindo" mostra: o resto continua guardado, para retomar pela página.
const itensParaContinuar = 12

// nomeDoTorrent tira do nome do torrent o site, a qualidade e o ano: "www.UIndex.org - Breaking.Bad.S01E01.Pilot.1080p"
// vira "Breaking Bad".
var (
	reSiteDoTorrent = regexp.MustCompile(`(?i)^\s*(\[[^\]]*\]\s*|www\.\S+\s*-\s*)`)
	reFimDoTorrent  = regexp.MustCompile(`(?i)[\s(\[](19|20)\d{2}([\s)\]]|$)|\s(S\d{1,2}\s*E\d{1,3}|S\d{1,2}|season\s*\d+|temporada\s*\d+|complete|2160p|1080p|720p|480p|4k|web-?dl|webrip|bluray|brrip|hdtv|x26[45]|hevc)\b`)
)

func nomeDoTorrent(s string) string {
	s = reSiteDoTorrent.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(strings.NewReplacer(".", " ", "_", " ").Replace(s)), " ")
	if loc := reFimDoTorrent.FindStringIndex(s); loc != nil && loc[0] > 0 {
		s = s[:loc[0]]
	}
	return strings.Trim(s, " -")
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
		p.tmdb = a.tmdbDaAssinatura(ctx, "filme", id)
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
		p.tmdb = a.tmdbDaAssinatura(ctx, "serie", e.SerieID)
	case "arquivo":
		f, t, err := a.arquivo(ctx, id)
		if err != nil {
			return err
		}
		p.Titulo, p.Capa = nomeDoTorrent(t.Titulo), t.Capa
		if f.Episodio == 0 && t.Videos > 1 {
			p.Titulo = tituloDoArquivo(f, t)
		}
		p.Subtitulo = "Torrent"
		if f.Episodio > 0 {
			p.Subtitulo = fmt.Sprintf("T%d E%d · torrent", f.Temporada, f.Episodio)
		} else if t.Ano != "" {
			p.Subtitulo = t.Ano + " · torrent"
		}
		// Veio da página de um título: o nome e a imagem do TMDB, no lugar do nome do torrent.
		a.db.QueryRowContext(ctx, `SELECT tmdb FROM torrents WHERE hash = ?`, t.Hash).Scan(&p.tmdb)
		if m := reTMDBGuardado.FindStringSubmatch(p.tmdb); m != nil {
			if chave := a.chaveDoTMDB(); chave != "" {
				var r resultadoDoTMDB
				if a.perguntarAoTMDB(ctx, chave, "/"+tiposDoTMDB[m[1]]+"/"+m[2], nil, &r) == nil {
					tt := r.titulo(m[1])
					p.Titulo, p.Capa = tt.Titulo, primeiro(tt.Fundo, tt.Capa)
				}
			}
		}
	default:
		return sql.ErrNoRows
	}
	_, err := a.db.ExecContext(ctx, `INSERT INTO progresso (chave, titulo, subtitulo, capa, serie_id, posicao, duracao, atualizado_em, tmdb)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (chave) DO UPDATE SET titulo = excluded.titulo, subtitulo = excluded.subtitulo, capa = excluded.capa,
			serie_id = excluded.serie_id, posicao = excluded.posicao, duracao = excluded.duracao, atualizado_em = excluded.atualizado_em,
			tmdb = excluded.tmdb`,
		p.Chave, p.Titulo, p.Subtitulo, p.Capa, p.SerieID, p.Posicao, p.Duracao, a.carimbo(), p.tmdb)
	return err
}

// tmdbDaAssinatura: o título do TMDB ligado ao filme ou à série da assinatura, se já foi ligado.
func (a *app) tmdbDaAssinatura(ctx context.Context, tipo string, id int64) string {
	var n int
	if a.db.QueryRowContext(ctx, `SELECT tmdb FROM assinatura_tmdb WHERE tipo = ? AND id = ?`, tipo, id).Scan(&n) != nil || n == 0 {
		return ""
	}
	return fmt.Sprintf("%s/%d", tipo, n)
}

func (a *app) guardarProgressoDaTela(s sessao, posicao, duracao float64) {
	if reChaveGuardavel.MatchString(s.Chave) {
		_ = a.guardarProgresso(context.Background(), s.Chave, posicao, duracao)
	}
}

// handleContinuar lista o que está pela metade, do mais recente ao mais antigo; de cada série
// (da assinatura ou do torrent), só o último episódio, e de cada título, uma vez.
func (a *app) handleContinuar(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT chave, titulo, subtitulo, capa, serie_id, posicao, duracao, atualizado_em, tmdb
		FROM progresso WHERE posicao > 30 AND (duracao = 0 OR posicao < duracao * 0.95)
		ORDER BY atualizado_em DESC LIMIT 60`)
	if err != nil {
		falha(w, err)
		return
	}
	defer rows.Close()
	lista := []progresso{}
	vistos := map[string]bool{}
	for rows.Next() {
		var p progresso
		if err := rows.Scan(&p.Chave, &p.Titulo, &p.Subtitulo, &p.Capa, &p.SerieID, &p.Posicao, &p.Duracao, &p.AtualizadoEm, &p.tmdb); err != nil {
			falha(w, err)
			return
		}
		p.Pagina = a.paginaDe(r.Context(), p)
		if strings.HasPrefix(p.Chave, "arquivo:") && p.tmdb == "" {
			p.Titulo = nomeDoTorrent(p.Titulo) // os guardados antes de o nome ser limpo
		}
		// O mesmo título pode vir da assinatura e de vários torrents: fica o mais recente. Junta pelo
		// TMDB, pela série da assinatura e pelo nome limpo (os guardados antes de saber o TMDB).
		grupos := []string{"nome/" + comparavel(nomeDoTorrent(p.Titulo))}
		if p.tmdb != "" {
			grupos = append(grupos, p.tmdb)
		}
		if p.SerieID != 0 {
			grupos = append(grupos, a.tmdbDaAssinatura(r.Context(), "serie", p.SerieID), fmt.Sprint("assinatura/", p.SerieID))
		}
		repetido := false
		for _, g := range grupos {
			repetido = repetido || (g != "" && vistos[g])
		}
		if repetido {
			continue
		}
		for _, g := range grupos {
			if g != "" {
				vistos[g] = true
			}
		}
		lista = append(lista, p)
		if len(lista) == itensParaContinuar {
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
	case tipo == "arquivo" && p.tmdb != "":
		return "#/titulo/" + p.tmdb
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
