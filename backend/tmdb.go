package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// O TMDB (themoviedb.org) é o "catálogo" da busca de torrents (S20): primeiro o Lucas acha o filme
// ou a série pelo nome, com pôster; só ao abrir um título o app pergunta aos indexadores, e o que
// volta é organizado pelo que o TMDB sabe dele (nome, ano, temporadas). A chave fica no banco.

const imagensDoTMDB = "https://image.tmdb.org/t/p/"

// Chave v3 (32 caracteres) vai no endereço; o "token de leitura" (v4, um JWT) vai no cabeçalho.
var reChaveV3 = regexp.MustCompile(`^[0-9a-f]{32}$`)

func urlDoTMDB() string {
	return strings.TrimRight(env("TMDB_URL", "https://api.themoviedb.org"), "/")
}

func (a *app) chaveDoTMDB() string {
	v, _ := a.ajuste("tmdb")
	return v
}

// cacheDoTMDB guarda as respostas por uma hora: a página do título pede os mesmos dados várias vezes.
type cacheDoTMDB struct {
	mu    sync.Mutex
	itens map[string]respostaGuardada
}

type respostaGuardada struct {
	corpo  json.RawMessage
	expira time.Time
}

func (a *app) perguntarAoTMDB(ctx context.Context, chave, caminho string, params url.Values, destino any) error {
	if params == nil {
		params = url.Values{}
	}
	params.Set("language", "pt-BR")
	if reChaveV3.MatchString(chave) {
		params.Set("api_key", chave)
	}
	endereco := urlDoTMDB() + "/3" + caminho + "?" + params.Encode()
	a.tmdb.mu.Lock()
	if g, ok := a.tmdb.itens[endereco]; ok && a.agora().Before(g.expira) {
		a.tmdb.mu.Unlock()
		return json.Unmarshal(g.corpo, destino)
	}
	a.tmdb.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endereco, nil)
	if err != nil {
		return err
	}
	if !reChaveV3.MatchString(chave) {
		req.Header.Set("Authorization", "Bearer "+chave)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // o endereço tem a chave
		}
		return fmt.Errorf("O TMDB não respondeu (%v).", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return errors.New("O TMDB recusou a chave da API.")
	case resp.StatusCode == http.StatusNotFound:
		return errors.New("O TMDB não conhece esse título.")
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("O TMDB respondeu %d.", resp.StatusCode)
	}
	var corpo json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&corpo); err != nil {
		return errors.New("Resposta estranha do TMDB.")
	}
	a.tmdb.mu.Lock()
	if a.tmdb.itens == nil || len(a.tmdb.itens) > 500 {
		a.tmdb.itens = map[string]respostaGuardada{}
	}
	a.tmdb.itens[endereco] = respostaGuardada{corpo: corpo, expira: a.agora().Add(time.Hour)}
	a.tmdb.mu.Unlock()
	return json.Unmarshal(corpo, destino)
}

// ---------- Ajustes ----------

func (a *app) handleTMDB(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"configurado": a.chaveDoTMDB() != ""})
}

func (a *app) handleGuardarTMDB(w http.ResponseWriter, r *http.Request) {
	var corpo struct {
		Chave string `json:"chave"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	chave := strings.TrimSpace(corpo.Chave)
	if chave == "" {
		erroJSON(w, http.StatusBadRequest, "Cole a chave da API do TMDB.")
		return
	}
	var teste json.RawMessage
	if err := a.perguntarAoTMDB(r.Context(), chave, "/configuration", nil, &teste); err != nil {
		erroJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.guardarAjuste("tmdb", chave); err != nil {
		falha(w, err)
		return
	}
	a.handleTMDB(w, r)
}

func (a *app) handleApagarTMDB(w http.ResponseWriter, r *http.Request) {
	if _, err := a.db.ExecContext(r.Context(), `DELETE FROM ajustes WHERE chave = 'tmdb'`); err != nil {
		falha(w, err)
		return
	}
	a.handleTMDB(w, r)
}

// ---------- Busca de títulos ----------

type tituloDoTMDB struct {
	Tipo       string            `json:"tipo"` // filme ou serie
	ID         int               `json:"id"`
	Titulo     string            `json:"titulo"`
	Original   string            `json:"original,omitempty"`
	Ano        string            `json:"ano,omitempty"`
	Capa       string            `json:"capa,omitempty"`
	Fundo      string            `json:"fundo,omitempty"`
	Sinopse    string            `json:"sinopse,omitempty"`
	Nota       float64           `json:"nota,omitempty"`
	Generos    []string          `json:"generos,omitempty"`
	Duracao    int               `json:"duracao,omitempty"` // minutos (filme)
	Temporadas []temporadaDoTMDB `json:"temporadas,omitempty"`
	ImdbID     string            `json:"imdb_id,omitempty"`
	// Filme: quando saiu (ou sai) em digital ou disco, a primeira data em qualquer país. Antes
	// dela, um "WEB-DL" ou "BluRay" do filme é falso ou é outro filme (S23).
	DigitalEm  string   `json:"digital_em,omitempty"`
	EstreiaEm  string   `json:"estreia_em,omitempty"`
	nomes      []string // como o título pode aparecer nos nomes dos torrents
	principais []string // os que viram busca: original, inglês e português (até dois)
	// Da busca (S28): títulos obscuros, sem votos nem procura, ficam fora da busca do Início.
	votos        int
	popularidade float64
}

type temporadaDoTMDB struct {
	Numero    int    `json:"numero"`
	Nome      string `json:"nome"`
	Episodios int    `json:"episodios"`
	Ano       string `json:"ano,omitempty"`
}

// resultadoDoTMDB cobre filme e série: um usa title/release_date, o outro name/first_air_date.
type resultadoDoTMDB struct {
	MediaType     string  `json:"media_type"`
	ID            int     `json:"id"`
	Title         string  `json:"title"`
	Name          string  `json:"name"`
	OriginalTitle string  `json:"original_title"`
	OriginalName  string  `json:"original_name"`
	ReleaseDate   string  `json:"release_date"`
	FirstAirDate  string  `json:"first_air_date"`
	PosterPath    string  `json:"poster_path"`
	BackdropPath  string  `json:"backdrop_path"`
	Overview      string  `json:"overview"`
	VoteAverage   float64 `json:"vote_average"`
	VoteCount     int     `json:"vote_count"`
	Popularity    float64 `json:"popularity"`
}

func (r resultadoDoTMDB) titulo(tipo string) tituloDoTMDB {
	t := tituloDoTMDB{Tipo: tipo, ID: r.ID, Titulo: primeiro(r.Title, r.Name), Original: primeiro(r.OriginalTitle, r.OriginalName),
		Ano: reAno.FindString(primeiro(r.ReleaseDate, r.FirstAirDate)), Sinopse: r.Overview, Nota: r.VoteAverage, EstreiaEm: primeiro(r.ReleaseDate, r.FirstAirDate),
		votos: r.VoteCount, popularidade: r.Popularity}
	if t.Original == t.Titulo {
		t.Original = ""
	}
	if r.PosterPath != "" {
		t.Capa = imagensDoTMDB + "w342" + r.PosterPath
	}
	if r.BackdropPath != "" {
		t.Fundo = imagensDoTMDB + "w780" + r.BackdropPath
	}
	return t
}

var tiposDoTMDB = map[string]string{"movie": "filme", "tv": "serie", "filme": "movie", "serie": "tv"}

func (a *app) exigirTMDB(w http.ResponseWriter) (string, bool) {
	chave := a.chaveDoTMDB()
	if chave == "" {
		erroJSON(w, http.StatusConflict, "Configure o TMDB em Ajustes para buscar títulos.")
	}
	return chave, chave != ""
}

func (a *app) handleBuscarTitulos(w http.ResponseWriter, r *http.Request) {
	chave, ok := a.exigirTMDB(w)
	if !ok {
		return
	}
	texto := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(texto)) < 2 {
		writeJSON(w, http.StatusOK, []tituloDoTMDB{})
		return
	}
	titulos, err := a.buscarNoTMDB(r.Context(), chave, texto)
	if err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, titulos)
}

// buscarNoTMDB: filmes e séries com o texto (pessoas ficam de fora), na ordem de relevância do TMDB.
func (a *app) buscarNoTMDB(ctx context.Context, chave, texto string) ([]tituloDoTMDB, error) {
	var resposta struct {
		Results []resultadoDoTMDB `json:"results"`
	}
	if err := a.perguntarAoTMDB(ctx, chave, "/search/multi", url.Values{"query": {texto}, "include_adult": {"false"}}, &resposta); err != nil {
		return nil, err
	}
	titulos := []tituloDoTMDB{}
	for _, res := range resposta.Results {
		if tipo, ok := tiposDoTMDB[res.MediaType]; ok && (res.MediaType == "movie" || res.MediaType == "tv") {
			t := res.titulo(tipo)
			t.Sinopse = ""
			titulos = append(titulos, t)
		}
	}
	return titulos, nil
}

// tituloCompleto pede ao TMDB os detalhes, os nomes alternativos (para reconhecer os torrents) e o
// código do IMDb.
func (a *app) tituloCompleto(ctx context.Context, chave, tipo string, id int) (tituloDoTMDB, error) {
	caminho, ok := tiposDoTMDB[tipo]
	if !ok || (caminho != "movie" && caminho != "tv") {
		return tituloDoTMDB{}, errors.New("Tipo de título inválido.")
	}
	var d struct {
		resultadoDoTMDB
		Runtime int `json:"runtime"`
		Genres  []struct {
			Name string `json:"name"`
		} `json:"genres"`
		Seasons []struct {
			SeasonNumber int    `json:"season_number"`
			Name         string `json:"name"`
			EpisodeCount int    `json:"episode_count"`
			AirDate      string `json:"air_date"`
		} `json:"seasons"`
		ExternalIDs struct {
			ImdbID string `json:"imdb_id"`
		} `json:"external_ids"`
		AlternativeTitles struct {
			Titles  []struct{ Title string } `json:"titles"`  // filme
			Results []struct{ Title string } `json:"results"` // série
		} `json:"alternative_titles"`
		ReleaseDates struct {
			Results []struct {
				Datas []struct {
					Tipo int    `json:"type"`
					Data string `json:"release_date"`
				} `json:"release_dates"`
			} `json:"results"`
		} `json:"release_dates"`
		Translations struct {
			Translations []struct {
				ISO  string `json:"iso_639_1"`
				Data struct {
					Title string `json:"title"`
					Name  string `json:"name"`
				} `json:"data"`
			} `json:"translations"`
		} `json:"translations"`
	}
	params := url.Values{"append_to_response": {"external_ids,alternative_titles,translations"}}
	if caminho == "movie" {
		params.Set("append_to_response", "external_ids,alternative_titles,translations,release_dates")
	}
	if err := a.perguntarAoTMDB(ctx, chave, "/"+caminho+"/"+strconv.Itoa(id), params, &d); err != nil {
		return tituloDoTMDB{}, err
	}
	t := d.titulo(tipo)
	t.Duracao, t.ImdbID = d.Runtime, d.ExternalIDs.ImdbID
	for _, pais := range d.ReleaseDates.Results {
		for _, l := range pais.Datas {
			// 4: digital, 5: disco. A data vem como "2026-11-17T00:00:00.000Z".
			if dia := strings.SplitN(l.Data, "T", 2)[0]; (l.Tipo == 4 || l.Tipo == 5) && (t.DigitalEm == "" || dia < t.DigitalEm) {
				t.DigitalEm = dia
			}
		}
	}
	for _, g := range d.Genres {
		t.Generos = append(t.Generos, g.Name)
	}
	for _, s := range d.Seasons {
		t.Temporadas = append(t.Temporadas, temporadaDoTMDB{Numero: s.SeasonNumber, Nome: s.Name, Episodios: s.EpisodeCount, Ano: reAno.FindString(s.AirDate)})
	}
	// Os torrents costumam usar o nome em inglês ou o original; os nacionais, o nome em português.
	nomes := []string{d.OriginalTitle, d.OriginalName, t.Titulo}
	for _, tr := range d.Translations.Translations {
		if tr.ISO == "en" || tr.ISO == "pt" {
			nomes = append(nomes, tr.Data.Title, tr.Data.Name)
		}
	}
	for _, alt := range append(d.AlternativeTitles.Titles, d.AlternativeTitles.Results...) {
		nomes = append(nomes, alt.Title)
	}
	ingles := ""
	for _, tr := range d.Translations.Translations {
		if tr.ISO == "en" {
			ingles = primeiro(tr.Data.Title, tr.Data.Name)
		}
	}
	for _, n := range []string{primeiro(d.OriginalTitle, d.OriginalName), ingles, t.Titulo} {
		if len(t.principais) < 2 && n != "" && ehLatino(n) && !contem(t.principais, comparavel(n)) {
			t.principais = append(t.principais, n)
		}
	}
	visto := map[string]bool{}
	for _, n := range nomes {
		if c := comparavel(n); c != "" && !visto[c] && ehLatino(n) {
			visto[c] = true
			t.nomes = append(t.nomes, n)
		}
	}
	return t, nil
}

// ehLatino: nomes em japonês, coreano etc. nunca aparecem nos torrents que o app entende.
func ehLatino(s string) bool {
	for _, r := range s {
		if r > 0x024F && r != '’' && r != '–' {
			return false
		}
	}
	return true
}

func (a *app) tituloDoPedido(w http.ResponseWriter, r *http.Request) (tituloDoTMDB, bool) {
	chave, ok := a.exigirTMDB(w)
	if !ok {
		return tituloDoTMDB{}, false
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		erroJSON(w, http.StatusBadRequest, "Título inválido.")
		return tituloDoTMDB{}, false
	}
	t, err := a.tituloCompleto(r.Context(), chave, r.PathValue("tipo"), id)
	if err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return t, false
	}
	return t, true
}

// itemDaAssinatura é o mesmo título no catálogo do IPTV, se houver.
type itemDaAssinatura struct {
	Tipo string `json:"tipo"`
	ID   int    `json:"id"`
	Nome string `json:"nome"`
	Ano  string `json:"ano,omitempty"`
}

func (a *app) naAssinatura(ctx context.Context, t tituloDoTMDB) []itemDaAssinatura {
	achados := []itemDaAssinatura{}
	vistos := map[int]bool{}
	// Primeiro os que já se sabe que são este título (S28): o nome do catálogo pode ser outro.
	if rows, err := a.db.QueryContext(ctx, `SELECT i.tipo, i.id, i.nome, i.ano FROM assinatura_tmdb m
		JOIN itens i ON i.tipo = m.tipo AND i.id = m.id WHERE m.tipo = ? AND m.tmdb = ? ORDER BY i.ordem`, t.Tipo, t.ID); err == nil {
		for rows.Next() {
			var it itemDaAssinatura
			if rows.Scan(&it.Tipo, &it.ID, &it.Nome, &it.Ano) == nil && !vistos[it.ID] {
				vistos[it.ID] = true
				achados = append(achados, it)
			}
		}
		rows.Close()
	}
	for _, nome := range t.nomes {
		// Os que já se sabe que são outro título ficam de fora ("Duna" de 1984 e o de 2021).
		rows, err := a.db.QueryContext(ctx, `SELECT tipo, id, nome, ano FROM itens WHERE tipo = ? AND busca LIKE ?
			AND NOT EXISTS (SELECT 1 FROM assinatura_tmdb m WHERE m.tipo = itens.tipo AND m.id = itens.id AND m.tmdb != ?)
			ORDER BY ordem LIMIT 20`, t.Tipo, normalizar(nome)+"%", t.ID)
		if err != nil {
			return achados
		}
		for rows.Next() {
			var it itemDaAssinatura
			if rows.Scan(&it.Tipo, &it.ID, &it.Nome, &it.Ano) != nil || vistos[it.ID] {
				continue
			}
			// O nome do catálogo tem de ser o título e, depois, só ano ou marcações ("[L]", "(4K)").
			resto := strings.TrimPrefix(comparavel(it.Nome), comparavel(nome))
			if resto != "" && !reRestoDoCatalogo.MatchString(resto) {
				continue
			}
			if it.Ano != "" && !anosBatem(it.Ano, t.Ano) {
				continue
			}
			vistos[it.ID] = true
			achados = append(achados, it)
		}
		rows.Close()
	}
	return achados
}

var reRestoDoCatalogo = regexp.MustCompile(`^\s+((19|20)\d{2}|4k|hd|fhd|uhd|l|leg|legendado|dublado|dub|nacional|hdr|hdr10|sdr|dual|imax|\s)*$`)

func (a *app) handleTitulo(w http.ResponseWriter, r *http.Request) {
	t, ok := a.tituloDoPedido(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"titulo": t, "assinatura": a.naAssinatura(r.Context(), t)})
}

type episodioDoTMDB struct {
	Numero  int    `json:"numero"`
	Duracao int    `json:"duracao,omitempty"` // minutos
	Nome    string `json:"nome"`
	Data    string `json:"data,omitempty"`
	Sinopse string `json:"sinopse,omitempty"`
	Imagem  string `json:"imagem,omitempty"`
}

func (a *app) handleTemporadaDoTitulo(w http.ResponseWriter, r *http.Request) {
	chave, ok := a.exigirTMDB(w)
	if !ok {
		return
	}
	id, err1 := strconv.Atoi(r.PathValue("id"))
	n, err2 := strconv.Atoi(r.PathValue("temporada"))
	if err1 != nil || err2 != nil {
		erroJSON(w, http.StatusBadRequest, "Temporada inválida.")
		return
	}
	var d struct {
		Episodes []struct {
			EpisodeNumber int    `json:"episode_number"`
			Name          string `json:"name"`
			AirDate       string `json:"air_date"`
			Overview      string `json:"overview"`
			StillPath     string `json:"still_path"`
			Runtime       int    `json:"runtime"`
		} `json:"episodes"`
	}
	if err := a.perguntarAoTMDB(r.Context(), chave, fmt.Sprintf("/tv/%d/season/%d", id, n), nil, &d); err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	episodios := []episodioDoTMDB{}
	for _, e := range d.Episodes {
		ep := episodioDoTMDB{Numero: e.EpisodeNumber, Nome: e.Name, Data: e.AirDate, Sinopse: e.Overview, Duracao: e.Runtime}
		if e.StillPath != "" {
			ep.Imagem = imagensDoTMDB + "w300" + e.StillPath
		}
		episodios = append(episodios, ep)
	}
	writeJSON(w, http.StatusOK, episodios)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// contem diz se algum dos nomes, comparável, é c.
func contem(nomes []string, c string) bool {
	for _, n := range nomes {
		if comparavel(n) == c {
			return true
		}
	}
	return false
}
