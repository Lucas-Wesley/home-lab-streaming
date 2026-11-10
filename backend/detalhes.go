package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Detalhes de filme e de série: sinopse, elenco e episódios vêm do provedor na hora em que o
// Lucas abre o item; os episódios ficam no banco, para tocar e para passar à TV.

type detalhes struct {
	Sinopse    string `json:"sinopse,omitempty"`
	Elenco     string `json:"elenco,omitempty"`
	Diretor    string `json:"diretor,omitempty"`
	Genero     string `json:"genero,omitempty"`
	Duracao    int64  `json:"duracao,omitempty"` // segundos
	Fundo      string `json:"fundo,omitempty"`
	Trailer    string `json:"trailer,omitempty"`
	Lancamento string `json:"lancamento,omitempty"`
	TMDB       int    `json:"tmdb,omitempty"` // o título no TMDB: a página dele junta assinatura e torrents (S28)
}

type infoDoProvedor struct {
	Plot         string          `json:"plot"`
	Description  string          `json:"description"`
	Cast         string          `json:"cast"`
	Actors       string          `json:"actors"`
	Director     string          `json:"director"`
	Genre        string          `json:"genre"`
	DurationSecs flex            `json:"duration_secs"`
	Backdrop     json.RawMessage `json:"backdrop_path"` // lista ou texto, conforme o painel
	Trailer      string          `json:"youtube_trailer"`
	Release      string          `json:"releasedate"`
	Release2     string          `json:"releaseDate"`
	MovieImage   string          `json:"movie_image"`
	TmdbID       flex            `json:"tmdb_id"`
}

func (i infoDoProvedor) detalhes() detalhes {
	d := detalhes{
		Sinopse:    primeiro(i.Plot, i.Description),
		Elenco:     primeiro(i.Cast, i.Actors),
		Diretor:    i.Director,
		Genero:     i.Genre,
		Duracao:    i.DurationSecs.int(),
		Trailer:    i.Trailer,
		Lancamento: primeiro(i.Release, i.Release2),
	}
	var lista []string
	if json.Unmarshal(i.Backdrop, &lista) == nil && len(lista) > 0 {
		d.Fundo = lista[0]
	} else {
		var um string
		if json.Unmarshal(i.Backdrop, &um) == nil {
			d.Fundo = um
		}
	}
	return d
}

func primeiro(valores ...string) string {
	for _, v := range valores {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func idDoCaminho(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		erroJSON(w, http.StatusNotFound, "Não encontrado.")
		return 0, false
	}
	return id, true
}

func (a *app) acessoOuErro(w http.ResponseWriter) (acesso, bool) {
	ac, err := a.acesso()
	if err != nil {
		falha(w, err)
		return ac, false
	}
	if !ac.completo() {
		erroJSON(w, http.StatusConflict, "Configure o acesso à assinatura em Ajustes.")
		return ac, false
	}
	return ac, true
}

func (a *app) handleFilme(w http.ResponseWriter, r *http.Request) {
	id, ok := idDoCaminho(w, r)
	if !ok {
		return
	}
	it, err := a.item(r.Context(), "filme", id)
	if errors.Is(err, sql.ErrNoRows) {
		erroJSON(w, http.StatusNotFound, "Filme não encontrado.")
		return
	}
	if err != nil {
		falha(w, err)
		return
	}
	resposta := map[string]any{"item": it, "progresso": a.progressoDe(r.Context(), "filme:"+strconv.FormatInt(id, 10))}
	ac, ok := a.acessoOuErro(w)
	if !ok {
		return
	}
	var info struct {
		Info infoDoProvedor `json:"info"`
	}
	// Sem os detalhes, o filme ainda toca: o erro só tira a sinopse da tela.
	if err := a.buscarJSON(r.Context(), ac.urlDaApi("get_vod_info", url.Values{"vod_id": {strconv.FormatInt(id, 10)}}), &info); err == nil {
		d := info.Info.detalhes()
		if tmdb := int(info.Info.TmdbID.int()); tmdb > 0 {
			a.guardarTMDBDaAssinatura(r.Context(), "filme", id, tmdb)
			if a.chaveDoTMDB() != "" { // sem a chave, a página do título não abre
				d.TMDB = tmdb
			}
		}
		resposta["detalhes"] = d
	}
	writeJSON(w, http.StatusOK, resposta)
}

type episodio struct {
	ID        int64   `json:"id"`
	SerieID   int64   `json:"serie_id"`
	Temporada int     `json:"temporada"`
	Numero    int     `json:"numero"`
	Titulo    string  `json:"titulo"`
	Extensao  string  `json:"extensao"`
	Duracao   int64   `json:"duracao,omitempty"`
	Capa      string  `json:"capa,omitempty"`
	Sinopse   string  `json:"sinopse,omitempty"`
	Posicao   float64 `json:"posicao,omitempty"` // onde o Lucas parou, em segundos
}

type serieDoProvedor struct {
	Info     infoDoProvedor  `json:"info"`
	Episodes json.RawMessage `json:"episodes"` // {"1": [...]}; alguns painéis mandam [] quando não há
}

type episodioDoProvedor struct {
	ID       flex   `json:"id"`
	Num      flex   `json:"episode_num"`
	Titulo   string `json:"title"`
	Extensao string `json:"container_extension"`
	Season   flex   `json:"season"`
	Info     struct {
		DurationSecs flex   `json:"duration_secs"`
		MovieImage   string `json:"movie_image"`
		Plot         string `json:"plot"`
		TmdbID       flex   `json:"tmdb_id"`
	} `json:"info"`
}

// Os episódios de uma série valem por algumas horas; depois, o provedor é consultado de novo.
const validadeDosEpisodios = 6 * time.Hour

func (a *app) handleSerie(w http.ResponseWriter, r *http.Request) {
	id, ok := idDoCaminho(w, r)
	if !ok {
		return
	}
	it, err := a.item(r.Context(), "serie", id)
	if errors.Is(err, sql.ErrNoRows) {
		erroJSON(w, http.StatusNotFound, "Série não encontrada.")
		return
	}
	if err != nil {
		falha(w, err)
		return
	}
	d, err := a.lerSerie(r.Context(), id, r.URL.Query().Has("atualizar"))
	if err != nil {
		erroJSON(w, http.StatusBadGateway, "Não consegui buscar os episódios no provedor: "+err.Error())
		return
	}
	eps, err := a.episodios(r.Context(), id)
	if err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": it, "detalhes": d, "episodios": eps, "tmdb": a.tmdbDaSerie(r.Context(), it)})
}

// lerSerie devolve os detalhes guardados ou, se estiverem velhos, busca de novo no provedor.
func (a *app) lerSerie(ctx context.Context, id int64, forcar bool) (detalhes, error) {
	var d detalhes
	var info, lida string
	err := a.db.QueryRowContext(ctx, `SELECT info, lida_em FROM series_lidas WHERE serie_id = ?`, id).Scan(&info, &lida)
	if err == nil && !forcar {
		if t, e := time.Parse(time.RFC3339, lida); e == nil && a.agora().Sub(t) < validadeDosEpisodios {
			return d, json.Unmarshal([]byte(info), &d)
		}
	}
	ac, err := a.acesso()
	if err != nil {
		return d, err
	}
	if !ac.completo() {
		return d, errors.New("acesso não configurado")
	}
	var s serieDoProvedor
	if err := a.buscarJSON(ctx, ac.urlDaApi("get_series_info", url.Values{"series_id": {strconv.FormatInt(id, 10)}}), &s); err != nil {
		return d, err
	}
	d = s.Info.detalhes()
	porTemporada := map[string][]episodioDoProvedor{}
	if len(s.Episodes) > 0 && s.Episodes[0] == '{' {
		if err := json.Unmarshal(s.Episodes, &porTemporada); err != nil {
			return d, err
		}
	}

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return d, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM episodios WHERE serie_id = ?`, id); err != nil {
		return d, err
	}
	for chave, lista := range porTemporada {
		temporadaDaChave, _ := strconv.Atoi(chave)
		for _, e := range lista {
			temporada := int(e.Season.int())
			if temporada == 0 {
				temporada = temporadaDaChave
			}
			if e.ID.int() == 0 {
				continue
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO episodios (id, serie_id, temporada, numero, titulo, extensao, duracao, capa, sinopse, tmdb)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, e.ID.int(), id, temporada, e.Num.int(), tituloDoEpisodio(e.Titulo),
				primeiro(e.Extensao, "mp4"), e.Info.DurationSecs.int(), e.Info.MovieImage, e.Info.Plot, e.Info.TmdbID.int()); err != nil {
				return d, err
			}
		}
	}
	b, _ := json.Marshal(d)
	if _, err := tx.Exec(`INSERT INTO series_lidas (serie_id, info, lida_em) VALUES (?, ?, ?)
		ON CONFLICT (serie_id) DO UPDATE SET info = excluded.info, lida_em = excluded.lida_em`, id, string(b), a.carimbo()); err != nil {
		return d, err
	}
	return d, tx.Commit()
}

// O painel escreve "Nome da Série - S01E01 - Título": fica só o título.
var reCodigoDoEpisodio = regexp.MustCompile(`(?i)^.*?\bS\d+\s*E\d+\b\s*[-:–]?\s*`)

func tituloDoEpisodio(bruto string) string {
	t := strings.TrimSpace(reCodigoDoEpisodio.ReplaceAllString(strings.TrimSpace(bruto), ""))
	if t == "" {
		return strings.TrimSpace(bruto)
	}
	return t
}

func (a *app) episodios(ctx context.Context, serieID int64) ([]episodio, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT e.id, e.serie_id, e.temporada, e.numero, e.titulo, e.extensao, e.duracao, e.capa, e.sinopse,
			COALESCE(p.posicao, 0)
		FROM episodios e LEFT JOIN progresso p ON p.chave = 'episodio:' || e.id
		WHERE e.serie_id = ?`, serieID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lista := []episodio{}
	for rows.Next() {
		var e episodio
		if err := rows.Scan(&e.ID, &e.SerieID, &e.Temporada, &e.Numero, &e.Titulo, &e.Extensao, &e.Duracao, &e.Capa, &e.Sinopse, &e.Posicao); err != nil {
			return nil, err
		}
		e.Titulo = tituloDoEpisodio(e.Titulo)
		lista = append(lista, e)
	}
	sort.Slice(lista, func(i, j int) bool {
		if lista[i].Temporada != lista[j].Temporada {
			return lista[i].Temporada < lista[j].Temporada
		}
		return lista[i].Numero < lista[j].Numero
	})
	return lista, rows.Err()
}

func (a *app) episodio(ctx context.Context, id int64) (episodio, error) {
	var e episodio
	err := a.db.QueryRowContext(ctx, `SELECT id, serie_id, temporada, numero, titulo, extensao, duracao, capa, sinopse FROM episodios WHERE id = ?`, id).
		Scan(&e.ID, &e.SerieID, &e.Temporada, &e.Numero, &e.Titulo, &e.Extensao, &e.Duracao, &e.Capa, &e.Sinopse)
	e.Titulo = tituloDoEpisodio(e.Titulo)
	return e, err
}
