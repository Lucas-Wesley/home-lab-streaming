package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// O Prowlarr é o buscador de torrents (S19): o Lucas cadastra os indexadores na interface dele e o
// app só pergunta pela API (/api/v1/search). O endereço e a chave ficam no banco, como o acesso do
// IPTV; a chave nunca volta para o navegador nem aparece nas mensagens de erro.

type configDoProwlarr struct {
	URL   string `json:"url"`
	Chave string `json:"chave"`
}

func (a *app) prowlarr() (configDoProwlarr, bool) {
	var c configDoProwlarr
	if v, _ := a.ajuste("prowlarr"); v != "" {
		json.Unmarshal([]byte(v), &c)
	}
	if c.URL == "" {
		c.URL = env("PROWLARR_URL", "")
	}
	c.URL = strings.TrimRight(c.URL, "/")
	return c, c.URL != "" && c.Chave != ""
}

// pedirAoProwlarr faz um GET na API dele com a chave no cabeçalho.
func (a *app) pedirAoProwlarr(ctx context.Context, c configDoProwlarr, caminho string, destino any) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second) // a busca espera todos os indexadores
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+caminho, nil)
	if err != nil {
		return errors.New("Endereço do Prowlarr inválido.")
	}
	req.Header.Set("X-Api-Key", c.Chave)
	resp, err := a.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("O Prowlarr não respondeu (%v).", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return errors.New("O Prowlarr recusou a chave da API.")
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("O Prowlarr respondeu %d.", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(destino); err != nil {
		return errors.New("Resposta estranha do Prowlarr: o endereço é dele mesmo?")
	}
	return nil
}

type situacaoDoProwlarr struct {
	Configurado bool   `json:"configurado"`
	URL         string `json:"url"`
	Indexadores int    `json:"indexadores"`
	Erro        string `json:"erro,omitempty"`
}

func (a *app) situacaoDoProwlarr(ctx context.Context) situacaoDoProwlarr {
	c, ok := a.prowlarr()
	s := situacaoDoProwlarr{Configurado: ok, URL: c.URL}
	if !ok {
		return s
	}
	var indexadores []struct {
		Enable bool `json:"enable"`
	}
	if err := a.pedirAoProwlarr(ctx, c, "/api/v1/indexer", &indexadores); err != nil {
		s.Erro = err.Error()
		return s
	}
	for _, i := range indexadores {
		if i.Enable {
			s.Indexadores++
		}
	}
	return s
}

func (a *app) handleProwlarr(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.situacaoDoProwlarr(r.Context()))
}

func (a *app) handleGuardarProwlarr(w http.ResponseWriter, r *http.Request) {
	var corpo configDoProwlarr
	if !lerCorpo(w, r, &corpo) {
		return
	}
	atual, _ := a.prowlarr()
	c := configDoProwlarr{URL: strings.TrimRight(strings.TrimSpace(corpo.URL), "/"), Chave: strings.TrimSpace(corpo.Chave)}
	if c.URL == "" {
		c.URL = atual.URL
	}
	if c.Chave == "" {
		c.Chave = atual.Chave // mudar só o endereço não pede a chave de novo
	}
	if u, err := url.Parse(c.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		erroJSON(w, http.StatusBadRequest, "Endereço do Prowlarr inválido (ex.: http://prowlarr:9696).")
		return
	}
	if c.Chave == "" {
		erroJSON(w, http.StatusBadRequest, "Cole a chave da API do Prowlarr (Settings → General → API Key).")
		return
	}
	var teste []json.RawMessage
	if err := a.pedirAoProwlarr(r.Context(), c, "/api/v1/indexer", &teste); err != nil {
		erroJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	b, _ := json.Marshal(c)
	if err := a.guardarAjuste("prowlarr", string(b)); err != nil {
		falha(w, err)
		return
	}
	a.handleProwlarr(w, r)
}

func (a *app) handleApagarProwlarr(w http.ResponseWriter, r *http.Request) {
	if _, err := a.db.ExecContext(r.Context(), `DELETE FROM ajustes WHERE chave = 'prowlarr'`); err != nil {
		falha(w, err)
		return
	}
	a.handleProwlarr(w, r)
}

// ---------- Busca ----------

// Categorias do padrão Newznab, que o Prowlarr usa: 2000 filmes, 5000 TV (inclui anime, 5070).
var categoriasDaBusca = map[string][]string{
	"filme": {"2000"},
	"serie": {"5000"},
	"":      {"2000", "5000"},
}

type resultadoDaBusca struct {
	Titulo     string   `json:"titulo"`
	Indexador  string   `json:"indexador"`
	Tamanho    int64    `json:"tamanho"`
	Semeadores int      `json:"semeadores"`
	Baixando   int      `json:"baixando"`
	Data       string   `json:"data"`
	Tipo       string   `json:"tipo"` // filme, serie ou ""
	Qualidade  []string `json:"qualidade"`
	// Para adicionar: o magnet, quando vem pronto, ou o link de download do Prowlarr sem a chave.
	Magnet string `json:"magnet,omitempty"`
	Link   string `json:"link,omitempty"`
	// Chave identifica a opção entre buscas (para a verificação, S23): o infohash, quando se sabe.
	Chave string `json:"chave"`
}

type lancamentoDoProwlarr struct {
	Title       string `json:"title"`
	Indexer     string `json:"indexer"`
	Size        int64  `json:"size"`
	Seeders     *int   `json:"seeders"`
	Leechers    *int   `json:"leechers"`
	PublishDate string `json:"publishDate"`
	Protocol    string `json:"protocol"`
	DownloadURL string `json:"downloadUrl"`
	MagnetURL   string `json:"magnetUrl"`
	InfoHash    string `json:"infoHash"`
	Categories  []struct {
		ID int `json:"id"`
	} `json:"categories"`
}

var reQualidade = []struct {
	re   *regexp.Regexp
	nome string
}{
	{regexp.MustCompile(`(?i)\b(2160p|4k|uhd)\b`), "4K"},
	{regexp.MustCompile(`(?i)\b1080p\b`), "1080p"},
	{regexp.MustCompile(`(?i)\b720p\b`), "720p"},
	{regexp.MustCompile(`(?i)\b480p\b`), "480p"},
	{regexp.MustCompile(`(?i)\b(x265|h[ .]?265|hevc)\b`), "H.265"},
	{regexp.MustCompile(`(?i)\b(hdr|hdr10|dv|dolby\.?vision)\b`), "HDR"},
	{reAudioSemNavegador, "Áudio Dolby/DTS"},
	{reCinema, "Gravado no cinema"},
	{regexp.MustCompile(`(?i)\b(dual|dual\.?audio|dual\.?áudio)\b`), "Dual áudio"},
	{regexp.MustCompile(`(?i)\b(dublado|dub|pt-?br)\b`), "Português"},
}

func qualidadeDe(titulo string) []string {
	q := []string{}
	for _, r := range reQualidade {
		if r.re.MatchString(titulo) {
			q = append(q, r.nome)
		}
	}
	return q
}

func (a *app) handleProcurarTorrents(w http.ResponseWriter, r *http.Request) {
	c, ok := a.prowlarr()
	if !ok {
		erroJSON(w, http.StatusConflict, "Configure o Prowlarr em Ajustes para buscar torrents.")
		return
	}
	texto := strings.TrimSpace(r.URL.Query().Get("q"))
	if texto == "" {
		writeJSON(w, http.StatusOK, []resultadoDaBusca{})
		return
	}
	tipo := r.URL.Query().Get("tipo")
	if _, valido := categoriasDaBusca[tipo]; !valido {
		erroJSON(w, http.StatusBadRequest, "Tipo de busca inválido.")
		return
	}
	resultados, err := a.procurarNoProwlarr(r.Context(), c, texto, tipo)
	if err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	sort.SliceStable(resultados, func(i, j int) bool { return resultados[i].Semeadores > resultados[j].Semeadores })
	writeJSON(w, http.StatusOK, resultados)
}

// resultadosDoProwlarr converte a resposta dele; usenet fica de fora (o app só fala torrent).
func resultadosDoProwlarr(lancamentos []lancamentoDoProwlarr) []resultadoDaBusca {
	resultados := []resultadoDaBusca{}
	for _, l := range lancamentos {
		if l.Protocol != "" && l.Protocol != "torrent" {
			continue
		}
		titulo := html.UnescapeString(l.Title) // alguns indexadores mandam "1&ordf; Temporada"
		res := resultadoDaBusca{
			Titulo: titulo, Indexador: l.Indexer, Tamanho: l.Size, Data: l.PublishDate,
			Tipo: tipoDasCategorias(l.Categories), Qualidade: qualidadeDe(titulo),
		}
		if l.Seeders != nil {
			res.Semeadores = *l.Seeders
		}
		if l.Leechers != nil {
			res.Baixando = *l.Leechers
		}
		switch {
		case strings.HasPrefix(l.MagnetURL, "magnet:"):
			res.Magnet = l.MagnetURL
		case strings.HasPrefix(l.DownloadURL, "magnet:"):
			res.Magnet = l.DownloadURL
		case l.DownloadURL != "":
			res.Link = semChave(l.DownloadURL)
		case l.MagnetURL != "":
			res.Link = semChave(l.MagnetURL)
		case l.InfoHash != "":
			res.Magnet = "magnet:?xt=urn:btih:" + l.InfoHash + "&dn=" + url.QueryEscape(l.Title)
		default:
			continue
		}
		res.Chave = chaveDoResultado(res, l.InfoHash)
		resultados = append(resultados, res)
	}
	return resultados
}
func tipoDasCategorias(cats []struct {
	ID int `json:"id"`
}) string {
	for _, c := range cats {
		switch {
		case c.ID >= 2000 && c.ID < 3000:
			return "filme"
		case c.ID >= 5000 && c.ID < 6000:
			return "serie"
		}
	}
	return ""
}

// semChave tira a chave da API do link de download, que vai para o navegador.
func semChave(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Del("apikey")
	u.RawQuery = q.Encode()
	return u.String()
}

// ---------- Adicionar um resultado ----------

var reDownloadDoProwlarr = regexp.MustCompile(`/\d+/download$`)

// clienteSemRedirecionarMagnet para quando o Prowlarr redireciona para um magnet:, que não é HTTP.
var clienteSemRedirecionarMagnet = &http.Client{
	Timeout: time.Minute,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme == "magnet" || len(via) >= 10 {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

// baixarDoProwlarr pega a ficha pelo link do Prowlarr: ou vem um .torrent, ou um redirecionamento
// para um magnet. O link só é aceito se for de download do próprio Prowlarr.
func (a *app) baixarDoProwlarr(ctx context.Context, link string) (magnet string, mi *metainfo.MetaInfo, err error) {
	c, ok := a.prowlarr()
	if !ok {
		return "", nil, errors.New("Configure o Prowlarr em Ajustes.")
	}
	u, err := url.Parse(link)
	base, _ := url.Parse(c.URL)
	if err != nil || base == nil || u.Scheme != base.Scheme || u.Host != base.Host || !reDownloadDoProwlarr.MatchString(u.Path) {
		return "", nil, errors.New("Link de download inválido.")
	}
	q := u.Query()
	q.Set("apikey", c.Chave)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", nil, err
	}
	resp, err := clienteSemRedirecionarMagnet.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return "", nil, fmt.Errorf("O Prowlarr não entregou o torrent (%v).", err)
	}
	defer resp.Body.Close()
	if loc := resp.Header.Get("Location"); strings.HasPrefix(loc, "magnet:") {
		return loc, nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("O Prowlarr não entregou o torrent (%d). O indexador pode estar fora do ar.", resp.StatusCode)
	}
	ficha, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return "", nil, err
	}
	if m := reMagnet.Find(ficha); m != nil && bytes.HasPrefix(bytes.TrimSpace(ficha), []byte("magnet:")) {
		return string(m), nil, nil
	}
	mi, err = metainfo.Load(bytes.NewReader(ficha))
	if err != nil {
		return "", nil, errors.New("O indexador mandou um torrent inválido.")
	}
	return "", mi, nil
}

func (a *app) adicionarDoProwlarr(ctx context.Context, link string) (string, error) {
	magnet, mi, err := a.baixarDoProwlarr(ctx, link)
	if err != nil {
		return "", err
	}
	if magnet != "" {
		return a.adicionarMagnet(ctx, magnet)
	}
	return a.adicionarDaFicha(ctx, mi, "arquivo", "", torrentGuardado{})
}
