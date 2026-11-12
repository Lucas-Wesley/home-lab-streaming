package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// Legendas online pelo OpenSubtitles (S21): busca em português e inglês pelo código do TMDB que o
// provedor manda (filme e episódio) ou que a busca de torrents guardou; sem código, pelo nome. A
// escolhida fica guardada para aquele vídeo e vale no player e nas telas. Cada legenda é baixada
// uma vez (a cota é diária) e fica em DATA_DIR/legendas.

const esquemaDasLegendas = `
-- A legenda escolhida para cada vídeo (chave do progresso: filme:1, episodio:2, arquivo:3).
CREATE TABLE IF NOT EXISTS legendas_escolhidas (
	chave  TEXT PRIMARY KEY,
	id     TEXT NOT NULL, -- os-<file_id>
	idioma TEXT NOT NULL,
	nome   TEXT NOT NULL
);
`

const agenteDoOpenSubtitles = "homelabstreaming v1.0" // o OpenSubtitles exige o nome do app

func urlDoOpenSubtitles() string {
	return strings.TrimRight(env("OPENSUBTITLES_URL", "https://api.opensubtitles.com"), "/")
}

func (a *app) chaveDoOpenSubtitles() string {
	v, _ := a.ajuste("opensubtitles")
	return v
}

// A pasta das legendas fica ao lado da dos torrents, em DATA_DIR.
func (a *app) pastaDasLegendas() string {
	return filepath.Join(filepath.Dir(a.cfg.pastaDosTorrents), "legendas")
}

var reIDDaLegenda = regexp.MustCompile(`^os-(\d{1,12})$`)

func (a *app) arquivoDaLegenda(id string) string {
	if !reIDDaLegenda.MatchString(id) && !reIDGerada.MatchString(id) {
		return ""
	}
	return filepath.Join(a.pastaDasLegendas(), id+".srt")
}

func (a *app) pedirAoOpenSubtitles(ctx context.Context, chave, metodo, caminho string, corpo any, destino any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var leitor io.Reader
	if corpo != nil {
		b, _ := json.Marshal(corpo)
		leitor = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, metodo, urlDoOpenSubtitles()+"/api/v1"+caminho, leitor)
	if err != nil {
		return err
	}
	req.Header.Set("Api-Key", chave)
	req.Header.Set("User-Agent", agenteDoOpenSubtitles)
	req.Header.Set("Accept", "application/json")
	if corpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("O OpenSubtitles não respondeu (%v).", err)
	}
	defer resp.Body.Close()
	var erro struct {
		Message string `json:"message"`
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errors.New("O OpenSubtitles recusou a chave da API.")
	case resp.StatusCode == http.StatusNotAcceptable || resp.StatusCode == http.StatusTooManyRequests:
		json.NewDecoder(resp.Body).Decode(&erro)
		return fmt.Errorf("Limite de legendas do dia atingido no OpenSubtitles. %s", erro.Message)
	case resp.StatusCode != http.StatusOK:
		json.NewDecoder(resp.Body).Decode(&erro)
		return fmt.Errorf("O OpenSubtitles respondeu %d. %s", resp.StatusCode, erro.Message)
	}
	return json.NewDecoder(resp.Body).Decode(destino)
}

// ---------- Ajustes ----------

func (a *app) handleOpenSubtitles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"configurado": a.chaveDoOpenSubtitles() != ""})
}

func (a *app) handleGuardarOpenSubtitles(w http.ResponseWriter, r *http.Request) {
	var corpo struct {
		Chave string `json:"chave"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	chave := strings.TrimSpace(corpo.Chave)
	if chave == "" {
		erroJSON(w, http.StatusBadRequest, "Cole a chave da API do OpenSubtitles.")
		return
	}
	var teste json.RawMessage
	if err := a.pedirAoOpenSubtitles(r.Context(), chave, http.MethodGet, "/infos/languages", nil, &teste); err != nil {
		erroJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.guardarAjuste("opensubtitles", chave); err != nil {
		falha(w, err)
		return
	}
	a.handleOpenSubtitles(w, r)
}

func (a *app) handleApagarOpenSubtitles(w http.ResponseWriter, r *http.Request) {
	if _, err := a.db.ExecContext(r.Context(), `DELETE FROM ajustes WHERE chave = 'opensubtitles'`); err != nil {
		falha(w, err)
		return
	}
	a.handleOpenSubtitles(w, r)
}

// ---------- Qual vídeo é ----------

// videoParaLegenda é o que se sabe de um vídeo para achar a legenda dele.
type videoParaLegenda struct {
	tmdbFilme    int64  // filme no TMDB
	tmdbEpisodio int64  // episódio no TMDB (o provedor manda este)
	tmdbSerie    int64  // série no TMDB (a busca de torrents guarda este) + temporada e episódio
	nome         string // sem código: busca pelo nome
	ano          string
	temporada    int
	episodio     int
	release      string // nome do arquivo, para preferir a legenda da mesma versão
	hash         string // hash do OpenSubtitles, quando o arquivo está inteiro no disco
	idiomas      string // os idiomas da busca; vazio: português e inglês (D50: o original do documentário)
}

var (
	// "Duna [L]", "Duna (2021) 4K", "Filme - Dublado": o que o catálogo põe depois do nome.
	reMarcasDoCatalogo = regexp.MustCompile(`(?i)\s*(\[[^\]]*\]|\((19|20)\d{2}\)|\b(4k|fhd|hd|uhd|legendado|dublado|dual|nacional)\b|-\s*$)`)
	reTMDBGuardado     = regexp.MustCompile(`^(filme|serie)/(\d+)$`)
)

func nomeDoCatalogo(nome string) string {
	return strings.TrimSpace(strings.Trim(reMarcasDoCatalogo.ReplaceAllString(nome, " "), " -"))
}

func (a *app) videoParaLegenda(ctx context.Context, tipo string, id int64) (videoParaLegenda, error) {
	var v videoParaLegenda
	switch tipo {
	case "filme":
		it, err := a.item(ctx, "filme", id)
		if err != nil {
			return v, err
		}
		v.nome, v.ano, v.release = nomeDoCatalogo(it.Nome), it.Ano, it.Nome
		if ac, err := a.acesso(); err == nil && ac.completo() {
			var info struct {
				Info infoDoProvedor `json:"info"`
			}
			if a.buscarJSON(ctx, ac.urlDaApi("get_vod_info", url.Values{"vod_id": {strconv.FormatInt(id, 10)}}), &info) == nil {
				v.tmdbFilme = info.Info.TmdbID.int()
			}
		}
	case "episodio":
		var serieID int64
		var nomeDaSerie, ano string
		err := a.db.QueryRowContext(ctx, `SELECT e.serie_id, e.temporada, e.numero, e.tmdb, i.nome, i.ano
			FROM episodios e JOIN itens i ON i.tipo = 'serie' AND i.id = e.serie_id WHERE e.id = ?`, id).
			Scan(&serieID, &v.temporada, &v.episodio, &v.tmdbEpisodio, &nomeDaSerie, &ano)
		if err != nil {
			return v, err
		}
		v.nome, v.ano = nomeDoCatalogo(nomeDaSerie), ano
	case "arquivo":
		f, t, err := a.arquivo(ctx, id)
		if err != nil {
			return v, err
		}
		v.release, v.temporada, v.episodio = strings.TrimSuffix(filepath.Base(f.Nome), filepath.Ext(f.Nome)), f.Temporada, f.Episodio
		var tmdb string
		a.db.QueryRowContext(ctx, `SELECT tmdb FROM torrents WHERE hash = ?`, f.Hash).Scan(&tmdb)
		if m := reTMDBGuardado.FindStringSubmatch(tmdb); m != nil {
			n, _ := strconv.ParseInt(m[2], 10, 64)
			if m[1] == "filme" {
				v.tmdbFilme = n
			} else {
				v.tmdbSerie = n
			}
		}
		v.nome, v.ano = nomeDoLancamento(v.release, t.Titulo)
		if f.Download == "baixado" {
			if caminho := a.caminhoBaixado(f); caminho != "" {
				v.hash, _ = hashDoOpenSubtitles(caminho)
			}
		}
	default:
		return v, errors.New("Canais ao vivo não têm legenda.")
	}
	return v, nil
}

var reFimDoNome = regexp.MustCompile(`(?i)\s-\s\d{1,4}\b|[ ._-]+(\(?(19|20)\d{2}\)?|s\d{1,2}(e\d{1,3})?|\d{1,2}x\d{1,3}|season|temporada|\d{3,4}p|4k|web|bluray|brrip|hdtv|dvdrip|complete)\b`)

// nomeDoLancamento tira do nome de um arquivo de torrent o título e o ano: "Dark.S01E01.720p" → "Dark".
func nomeDoLancamento(arquivo, torrent string) (string, string) {
	for _, n := range []string{arquivo, torrent} {
		n = reAntesDoNome.ReplaceAllString(n, "")
		if loc := reFimDoNome.FindStringIndex(n); loc != nil && loc[0] > 0 {
			nome := strings.TrimSpace(strings.NewReplacer(".", " ", "_", " ").Replace(n[:loc[0]]))
			return nome, reAnoSolto.FindString(n[loc[0]:])
		}
	}
	return strings.NewReplacer(".", " ", "_", " ").Replace(reAntesDoNome.ReplaceAllString(arquivo, "")), ""
}

// hashDoOpenSubtitles: tamanho do arquivo mais a soma, em blocos de 8 bytes, dos primeiros e dos
// últimos 64 KB. Uma legenda com o mesmo hash foi feita para este arquivo exato.
func hashDoOpenSubtitles(caminho string) (string, error) {
	const bloco = 64 << 10
	f, err := os.Open(caminho)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() < bloco {
		return "", errors.New("arquivo pequeno demais")
	}
	soma := uint64(st.Size())
	buf := make([]byte, bloco)
	for _, inicio := range []int64{0, st.Size() - bloco} {
		if _, err := f.ReadAt(buf, inicio); err != nil {
			return "", err
		}
		for i := 0; i < bloco; i += 8 {
			soma += binary.LittleEndian.Uint64(buf[i:])
		}
	}
	return fmt.Sprintf("%016x", soma), nil
}

// ---------- Busca ----------

type opcaoDeLegenda struct {
	ID       string `json:"id"` // os-<file_id>
	Idioma   string `json:"idioma"`
	Nome     string `json:"nome"`
	Baixadas int    `json:"baixadas"`
	Surdos   bool   `json:"surdos,omitempty"`  // com descrição de sons
	Maquina  bool   `json:"maquina,omitempty"` // traduzida por máquina ou IA
	Exata    bool   `json:"exata,omitempty"`   // feita para este arquivo (mesmo hash)
	pontos   float64
}

type legendaDoOpenSubtitles struct {
	Attributes struct {
		Language          string `json:"language"`
		DownloadCount     int    `json:"download_count"`
		HearingImpaired   bool   `json:"hearing_impaired"`
		AITranslated      bool   `json:"ai_translated"`
		MachineTranslated bool   `json:"machine_translated"`
		MoviehashMatch    bool   `json:"moviehash_match"`
		Release           string `json:"release"`
		Files             []struct {
			FileID   int64  `json:"file_id"`
			FileName string `json:"file_name"`
		} `json:"files"`
	} `json:"attributes"`
}

var preferenciaDoIdioma = map[string]float64{"pt-BR": 300, "pt-PT": 200, "en": 100}

// parametrosDaBusca monta a busca do OpenSubtitles. Ele quer os parâmetros em ordem alfabética,
// em minúsculas e números sem zeros à esquerda; url.Values.Encode já ordena.
func (v videoParaLegenda) parametrosDaBusca() url.Values {
	p := url.Values{"languages": {primeiro(v.idiomas, "en,pt-br,pt-pt")}}
	switch {
	case v.tmdbEpisodio > 0:
		p.Set("tmdb_id", strconv.FormatInt(v.tmdbEpisodio, 10))
	case v.tmdbSerie > 0 && v.episodio > 0:
		p.Set("parent_tmdb_id", strconv.FormatInt(v.tmdbSerie, 10))
		p.Set("season_number", strconv.Itoa(max(v.temporada, 1)))
		p.Set("episode_number", strconv.Itoa(v.episodio))
	case v.tmdbFilme > 0:
		p.Set("tmdb_id", strconv.FormatInt(v.tmdbFilme, 10))
	default:
		p.Set("query", strings.ToLower(v.nome))
		if v.episodio > 0 {
			p.Set("season_number", strconv.Itoa(max(v.temporada, 1)))
			p.Set("episode_number", strconv.Itoa(v.episodio))
		} else if v.ano != "" {
			p.Set("year", v.ano)
		}
	}
	if v.hash != "" {
		p.Set("moviehash", v.hash)
	}
	return p
}

func (a *app) buscarLegendas(ctx context.Context, chave string, v videoParaLegenda) ([]opcaoDeLegenda, error) {
	var resposta struct {
		Data []legendaDoOpenSubtitles `json:"data"`
	}
	if err := a.pedirAoOpenSubtitles(ctx, chave, http.MethodGet, "/subtitles?"+v.parametrosDaBusca().Encode(), nil, &resposta); err != nil {
		return nil, err
	}
	palavras := map[string]bool{}
	for _, p := range strings.Fields(comparavel(v.release)) {
		palavras[p] = true
	}
	opcoes := []opcaoDeLegenda{}
	for _, l := range resposta.Data {
		at := l.Attributes
		idioma := map[string]string{"pt-br": "pt-BR", "pt-pt": "pt-PT", "en": "en"}[strings.ToLower(at.Language)]
		if idioma == "" && v.idiomas != "" {
			idioma = strings.ToLower(at.Language) // pedida em outro idioma (D50)
		}
		if idioma == "" || len(at.Files) == 0 {
			continue
		}
		o := opcaoDeLegenda{
			ID: "os-" + strconv.FormatInt(at.Files[0].FileID, 10), Idioma: idioma, Nome: primeiro(at.Release, at.Files[0].FileName),
			Baixadas: at.DownloadCount, Surdos: at.HearingImpaired, Maquina: at.AITranslated || at.MachineTranslated, Exata: at.MoviehashMatch,
		}
		o.pontos = preferenciaDoIdioma[idioma] + math.Log2(1+float64(o.Baixadas))*3
		// Mesma versão (grupo, origem, resolução) costuma ter o mesmo tempo.
		comuns := 0
		for _, p := range strings.Fields(comparavel(o.Nome)) {
			if palavras[p] && len(p) > 2 {
				comuns++
			}
		}
		o.pontos += float64(min(comuns, 8)) * 6
		if o.Exata {
			o.pontos += 500
		}
		if o.Maquina {
			o.pontos -= 80
		}
		if o.Surdos {
			o.pontos -= 5
		}
		opcoes = append(opcoes, o)
	}
	sort.SliceStable(opcoes, func(i, j int) bool { return opcoes[i].pontos > opcoes[j].pontos })
	return opcoes, nil
}

// ---------- Rotas ----------

var reChaveComLegenda = regexp.MustCompile(`^(filme|episodio|arquivo):(\d+)$`)

func chaveDaLegenda(w http.ResponseWriter, r *http.Request) (string, string, int64, bool) {
	chave := r.PathValue("chave")
	m := reChaveComLegenda.FindStringSubmatch(chave)
	if m == nil {
		erroJSON(w, http.StatusBadRequest, "Este vídeo não tem legenda.")
		return "", "", 0, false
	}
	id, _ := strconv.ParseInt(m[2], 10, 64)
	return chave, m[1], id, true
}

type legendaEscolhida struct {
	ID     string `json:"id"`
	Idioma string `json:"idioma"`
	Nome   string `json:"nome"`
	Exata  bool   `json:"exata,omitempty"` // no pedido: feita para este arquivo, não precisa de ajuste
	// Sincronia (S25): t' = t × escala + atraso. O ajuste automático roda por trás (alinhando).
	Atraso    float64 `json:"atraso"`
	Escala    float64 `json:"escala"`
	Ajuste    string  `json:"ajuste,omitempty"`
	Alinhando bool    `json:"alinhando,omitempty"`
	// Legenda gerada pelo áudio (S29): como vai a geração.
	Geracao *geracao `json:"geracao,omitempty"`
}

func (a *app) legendaDe(ctx context.Context, chave string) *legendaEscolhida {
	var l legendaEscolhida
	if a.db.QueryRowContext(ctx, `SELECT id, idioma, nome, atraso, escala, ajuste, alinhando FROM legendas_escolhidas WHERE chave = ?`, chave).
		Scan(&l.ID, &l.Idioma, &l.Nome, &l.Atraso, &l.Escala, &l.Ajuste, &l.Alinhando) != nil {
		return nil
	}
	if _, err := os.Stat(a.arquivoDaLegenda(l.ID)); err != nil {
		return nil
	}
	if reIDGerada.MatchString(l.ID) {
		l.Geracao = a.geracaoDe(ctx, chave)
	}
	return &l
}

func (a *app) handleLegendas(w http.ResponseWriter, r *http.Request) {
	chave, tipo, id, ok := chaveDaLegenda(w, r)
	if !ok {
		return
	}
	resposta := map[string]any{"escolhida": a.legendaDe(r.Context(), chave), "configurado": a.chaveDoOpenSubtitles() != "", "gerar": a.opcoesDeGerar(r.Context(), chave)}
	if !r.URL.Query().Has("buscar") || a.chaveDoOpenSubtitles() == "" {
		writeJSON(w, http.StatusOK, resposta)
		return
	}
	v, err := a.videoParaLegenda(r.Context(), tipo, id)
	if errors.Is(err, sql.ErrNoRows) {
		erroJSON(w, http.StatusNotFound, "Vídeo não encontrado.")
		return
	}
	if err != nil {
		erroJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	opcoes, err := a.buscarLegendas(r.Context(), a.chaveDoOpenSubtitles(), v)
	if err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	resposta["opcoes"] = opcoes
	writeJSON(w, http.StatusOK, resposta)
}

func (a *app) handleEscolherLegenda(w http.ResponseWriter, r *http.Request) {
	chave, tipo, idDoVideo, ok := chaveDaLegenda(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPatch {
		// Ajuste manual da sincronia (botões −0,5 s / +0,5 s no player e no controle).
		var corpo struct {
			Atraso float64 `json:"atraso"`
		}
		if !lerCorpo(w, r, &corpo) {
			return
		}
		corpo.Atraso = math.Max(-600, math.Min(600, math.Round(corpo.Atraso*10)/10))
		if _, err := a.db.ExecContext(r.Context(), `UPDATE legendas_escolhidas SET atraso = ?, ajuste = 'manual', alinhando = 0 WHERE chave = ?`, corpo.Atraso, chave); err != nil {
			falha(w, err)
			return
		}
		writeJSON(w, http.StatusOK, a.legendaDe(r.Context(), chave))
		return
	}
	if r.Method == http.MethodDelete {
		if _, err := a.db.ExecContext(r.Context(), `DELETE FROM legendas_escolhidas WHERE chave = ?`, chave); err != nil {
			falha(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var corpo legendaEscolhida
	if !lerCorpo(w, r, &corpo) {
		return
	}
	caminho := a.arquivoDaLegenda(corpo.ID)
	if caminho == "" {
		erroJSON(w, http.StatusBadRequest, "Legenda inválida.")
		return
	}
	if err := a.baixarLegenda(r.Context(), corpo.ID, caminho); err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	ajuste, alinhando := "", !corpo.Exata
	if corpo.Exata {
		ajuste = "Feita para este arquivo: não precisa de ajuste."
	}
	if _, err := a.db.ExecContext(r.Context(), `INSERT INTO legendas_escolhidas (chave, id, idioma, nome, atraso, escala, ajuste, alinhando) VALUES (?, ?, ?, ?, 0, 1, ?, ?)
		ON CONFLICT (chave) DO UPDATE SET id = excluded.id, idioma = excluded.idioma, nome = excluded.nome,
			atraso = 0, escala = 1, ajuste = excluded.ajuste, alinhando = excluded.alinhando`,
		chave, corpo.ID, corpo.Idioma, corpo.Nome, ajuste, alinhando); err != nil {
		falha(w, err)
		return
	}
	if alinhando {
		a.emFundo(func() { a.alinharSozinho(chave, tipo, idDoVideo, corpo.ID) })
	}
	writeJSON(w, http.StatusOK, a.legendaDe(r.Context(), chave))
}

// baixarLegenda traz a legenda do OpenSubtitles, se ainda não estiver no disco, em UTF-8.
func (a *app) baixarLegenda(ctx context.Context, id, caminho string) error {
	if _, err := os.Stat(caminho); err == nil {
		return nil
	}
	if !reIDDaLegenda.MatchString(id) {
		return errors.New("Legenda não encontrada.")
	}
	chave := a.chaveDoOpenSubtitles()
	if chave == "" {
		return errors.New("Configure o OpenSubtitles em Ajustes.")
	}
	fileID, _ := strconv.ParseInt(reIDDaLegenda.FindStringSubmatch(id)[1], 10, 64)
	var d struct {
		Link string `json:"link"`
	}
	if err := a.pedirAoOpenSubtitles(ctx, chave, http.MethodPost, "/download", map[string]any{"file_id": fileID}, &d); err != nil {
		return err
	}
	if d.Link == "" {
		return errors.New("O OpenSubtitles não mandou o link da legenda.")
	}
	texto, err := a.baixarPequeno(ctx, d.Link, 5<<20)
	if err != nil {
		return fmt.Errorf("baixando a legenda: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		return err
	}
	temporario := caminho + ".baixando"
	if err := os.WriteFile(temporario, emUTF8(texto), 0o644); err != nil {
		return err
	}
	return os.Rename(temporario, caminho)
}

// emUTF8: legendas antigas vêm em Windows-1252 (Latin-1); a TV e o navegador querem UTF-8.
func emUTF8(b []byte) []byte {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if utf8.Valid(b) {
		return b
	}
	if t, err := charmap.Windows1252.NewDecoder().Bytes(b); err == nil {
		return t
	}
	return b
}

var reTempoDoSRT = regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`)

// paraWebVTT converte SRT no formato que o elemento <track> do navegador entende.
func paraWebVTT(srt []byte) []byte {
	texto := strings.ReplaceAll(string(srt), "\r\n", "\n")
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for _, linha := range strings.Split(texto, "\n") {
		if strings.Contains(linha, "-->") {
			linha = reTempoDoSRT.ReplaceAllString(linha, "$1.$2")
		}
		b.WriteString(linha)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// servirLegenda entrega a legenda guardada: em WebVTT para o navegador, em SRT para as telas.
func (a *app) servirLegenda(vtt bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(strings.TrimSuffix(r.PathValue("arquivo"), ".vtt"), ".srt")
		caminho := a.arquivoDaLegenda(id)
		if caminho == "" {
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile(caminho)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "max-age=86400")
		if reIDGerada.MatchString(id) {
			w.Header().Set("Cache-Control", "no-cache") // cresce enquanto é gerada
		}
		// A sincronia vai no endereço (o player e a tela pedem com o atraso guardado): cada ajuste
		// é um endereço novo, sem cache velho.
		atraso, _ := strconv.ParseFloat(r.URL.Query().Get("atraso"), 64)
		escala, err := strconv.ParseFloat(r.URL.Query().Get("escala"), 64)
		if err != nil || escala < 0.9 || escala > 1.1 {
			escala = 1
		}
		texto := ajustarTempos(string(b), atraso, escala)
		if vtt {
			w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
			w.Write(paraWebVTT([]byte(texto)))
			return
		}
		log.Printf("tela %s buscou a legenda %s", r.RemoteAddr, id)
		w.Header().Set("Content-Type", "application/x-subrip; charset=utf-8")
		w.Write([]byte(texto))
	}
}

// legendaNaTela diz à tela onde buscar a legenda: o cabeçalho CaptionInfo.sec da Samsung,
// pedido junto com o vídeo (getCaptionInfo.sec), aponta para o arquivo na porta das telas.
func (a *app) legendaNaTela(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("legenda")
	if a.arquivoDaLegenda(id) == "" {
		return
	}
	endereco := "http://" + r.Host + "/legenda/" + id + ".srt"
	sincronia := url.Values{}
	for _, p := range []string{"atraso", "escala"} {
		if v := r.URL.Query().Get(p); v != "" {
			sincronia.Set(p, v)
		}
	}
	if len(sincronia) > 0 {
		endereco += "?" + sincronia.Encode()
	}
	w.Header().Set("CaptionInfo.sec", endereco)
}
