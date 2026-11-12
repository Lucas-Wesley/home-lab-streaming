package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Documentários para o Acervo (S41). Na página de um documentário (gênero "Documentário" no TMDB), o
// filme, um episódio, a temporada ou a série inteira vão para o app Acervo, que tira deles o conteúdo
// para estudar. Cada filme ou episódio entra numa fila e vai um por vez, em ordem. O texto sai, nesta
// ordem de preferência:
//  1. da legenda no idioma original de dentro do arquivo baixado (é a do próprio vídeo);
//  2. da legenda do OpenSubtitles no idioma original, achada pelo código do TMDB (nunca pelo nome:
//     a busca por nome traz legenda de outro título) e com a duração conferida;
//  3. do áudio original, em mono e comprimido (o Acervo transcreve com o Whisper): do arquivo baixado,
//     da assinatura (só a faixa de áudio, com a conexão livre, S28) ou do torrent já aberto.
// A legenda é rápida e não ocupa a conexão; o áudio pega tudo o que é falado.

const esquemaDosEnviosAoAcervo = `
CREATE TABLE IF NOT EXISTS envios_ao_acervo (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	tipo          TEXT NOT NULL,              -- filme ou serie (TMDB)
	tmdb          INTEGER NOT NULL,
	temporada     INTEGER NOT NULL DEFAULT 0,
	episodio      INTEGER NOT NULL DEFAULT 0,
	nome          TEXT NOT NULL,
	estado        TEXT NOT NULL,              -- na_fila, enviando, enviado, falhou
	etapa         TEXT NOT NULL DEFAULT '',   -- o que está fazendo agora
	via           TEXT NOT NULL DEFAULT '',   -- de onde veio o texto: a legenda ou o áudio
	acervo_id     TEXT NOT NULL DEFAULT '',
	erro          TEXT NOT NULL DEFAULT '',
	criado_em     TEXT NOT NULL,
	atualizado_em TEXT NOT NULL,
	UNIQUE (tipo, tmdb, temporada, episodio)
);
`

func urlDoAcervo() string { return strings.TrimRight(env("ACERVO_URL", ""), "/") }
func linkDoAcervo() string {
	return strings.TrimRight(env("ACERVO_LINK", "https://acervo.internal"), "/")
}

var (
	// Variáveis só para os testes ficarem rápidos.
	esperaDoAcervo      = time.Minute
	tempoMaximoDoAudio  = 3 * time.Hour
	generosDocumentario = map[string]bool{"documentário": true, "documentary": true}
)

type envioAoAcervo struct {
	ID        int64  `json:"id"`
	Tipo      string `json:"tipo"`
	TMDB      int    `json:"tmdb"`
	Temporada int    `json:"temporada,omitempty"`
	Episodio  int    `json:"episodio,omitempty"`
	Nome      string `json:"nome"`
	Estado    string `json:"estado"`
	Etapa     string `json:"etapa,omitempty"`
	Via       string `json:"via,omitempty"`
	AcervoID  string `json:"acervo_id,omitempty"`
	Link      string `json:"link,omitempty"` // a Referência no Acervo
	Erro      string `json:"erro,omitempty"`
}

func ehDocumentario(t tituloDoTMDB) bool {
	for _, g := range t.Generos {
		if generosDocumentario[strings.ToLower(g)] {
			return true
		}
	}
	return false
}

func (a *app) enviosDoTitulo(ctx context.Context, tipo string, tmdb int) ([]envioAoAcervo, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id, tipo, tmdb, temporada, episodio, nome, estado, etapa, via, acervo_id, erro
		FROM envios_ao_acervo WHERE tipo = ? AND tmdb = ? ORDER BY temporada, episodio`, tipo, tmdb)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lista := []envioAoAcervo{}
	for rows.Next() {
		var e envioAoAcervo
		if err := rows.Scan(&e.ID, &e.Tipo, &e.TMDB, &e.Temporada, &e.Episodio, &e.Nome, &e.Estado, &e.Etapa, &e.Via, &e.AcervoID, &e.Erro); err != nil {
			return nil, err
		}
		if e.AcervoID != "" {
			e.Link = linkDoAcervo() + "/#/referencia/" + e.AcervoID
		}
		lista = append(lista, e)
	}
	return lista, rows.Err()
}

// handleEnviosAoAcervo: GET /api/acervo?tipo=serie&tmdb=123 — se o Acervo está ligado e o que já foi.
func (a *app) handleEnviosAoAcervo(w http.ResponseWriter, r *http.Request) {
	tipo := r.URL.Query().Get("tipo")
	tmdb := atoi(r.URL.Query().Get("tmdb"))
	lista, err := a.enviosDoTitulo(r.Context(), tipo, tmdb)
	if err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"disponivel": urlDoAcervo() != "", "envios": lista})
}

// handleMandarAoAcervo: POST /api/acervo {tipo, tmdb, alcance, temporada, episodio}. alcance:
// "titulo" (o filme), "episodio", "temporada" ou "tudo". Entram na fila os que ainda não foram;
// os que falharam voltam para a fila.
func (a *app) handleMandarAoAcervo(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Tipo      string `json:"tipo"`
		TMDB      int    `json:"tmdb"`
		Alcance   string `json:"alcance"`
		Temporada int    `json:"temporada"`
		Episodio  int    `json:"episodio"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&p) != nil || (p.Tipo != "filme" && p.Tipo != "serie") || p.TMDB <= 0 {
		erroJSON(w, http.StatusBadRequest, "Pedido inválido.")
		return
	}
	if urlDoAcervo() == "" {
		erroJSON(w, http.StatusConflict, "O Acervo não está ligado neste servidor (ACERVO_URL).")
		return
	}
	chave, ok := a.exigirTMDB(w)
	if !ok {
		return
	}
	ctx := r.Context()
	t, err := a.tituloCompleto(ctx, chave, p.Tipo, p.TMDB)
	if err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	if !ehDocumentario(t) {
		erroJSON(w, http.StatusBadRequest, "Só documentários vão para o Acervo.")
		return
	}
	type alvo struct {
		temporada, episodio int
		nome                string
	}
	var alvos []alvo
	switch {
	case p.Tipo == "filme":
		alvos = []alvo{{0, 0, t.Titulo}}
	case p.Alcance == "episodio" && p.Temporada >= 0 && p.Episodio > 0:
		eps, err := a.episodiosDaTemporada(ctx, chave, p.TMDB, p.Temporada)
		if err != nil {
			erroJSON(w, http.StatusBadGateway, err.Error())
			return
		}
		nome := ""
		for _, e := range eps {
			if e.Numero == p.Episodio {
				nome = e.Nome
			}
		}
		alvos = []alvo{{p.Temporada, p.Episodio, nomeDoEnvio(t.Titulo, p.Temporada, p.Episodio, nome)}}
	case p.Alcance == "temporada" || p.Alcance == "tudo":
		temporadas := []int{p.Temporada}
		if p.Alcance == "tudo" {
			temporadas = nil
			for _, s := range t.Temporadas {
				if s.Numero > 0 && s.Episodios > 0 {
					temporadas = append(temporadas, s.Numero)
				}
			}
		}
		hoje := a.agora().Format("2006-01-02")
		for _, n := range temporadas {
			eps, err := a.episodiosDaTemporada(ctx, chave, p.TMDB, n)
			if err != nil {
				erroJSON(w, http.StatusBadGateway, err.Error())
				return
			}
			for _, e := range eps {
				if e.Data == "" || e.Data <= hoje { // os que ainda não saíram ficam de fora
					alvos = append(alvos, alvo{n, e.Numero, nomeDoEnvio(t.Titulo, n, e.Numero, e.Nome)})
				}
			}
		}
	default:
		erroJSON(w, http.StatusBadRequest, "Diga o episódio, a temporada ou tudo.")
		return
	}
	if len(alvos) == 0 {
		erroJSON(w, http.StatusBadRequest, "Nenhum episódio para mandar.")
		return
	}
	agora := a.carimbo()
	for _, x := range alvos {
		if _, err := a.db.ExecContext(ctx, `INSERT INTO envios_ao_acervo (tipo, tmdb, temporada, episodio, nome, estado, criado_em, atualizado_em)
			VALUES (?, ?, ?, ?, ?, 'na_fila', ?, ?)
			ON CONFLICT (tipo, tmdb, temporada, episodio) DO UPDATE SET estado = 'na_fila', etapa = '', erro = '', atualizado_em = excluded.atualizado_em
			WHERE envios_ao_acervo.estado = 'falhou'`, p.Tipo, p.TMDB, x.temporada, x.episodio, x.nome, agora, agora); err != nil {
			falha(w, err)
			return
		}
	}
	a.acordarEnvios()
	lista, _ := a.enviosDoTitulo(ctx, p.Tipo, p.TMDB)
	writeJSON(w, http.StatusAccepted, map[string]any{"disponivel": true, "envios": lista})
}

// handleCancelarEnvio: DELETE /api/acervo/{id} tira da fila (ou da lista, se falhou).
func (a *app) handleCancelarEnvio(w http.ResponseWriter, r *http.Request) {
	id := atoi(r.PathValue("id"))
	res, err := a.db.ExecContext(r.Context(), `DELETE FROM envios_ao_acervo WHERE id = ? AND estado IN ('na_fila', 'falhou')`, id)
	if err != nil {
		falha(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		erroJSON(w, http.StatusConflict, "Só sai da lista o que ainda não foi ou falhou.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func nomeDoEnvio(titulo string, temporada, episodio int, nome string) string {
	s := fmt.Sprintf("%s · T%d E%d", titulo, temporada, episodio)
	if nome != "" {
		s += " · " + nome
	}
	return s
}

func (a *app) episodiosDaTemporada(ctx context.Context, chave string, serie, temporada int) ([]episodioDoTMDB, error) {
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
	if err := a.perguntarAoTMDB(ctx, chave, fmt.Sprintf("/tv/%d/season/%d", serie, temporada), nil, &d); err != nil {
		return nil, err
	}
	eps := []episodioDoTMDB{}
	for _, e := range d.Episodes {
		ep := episodioDoTMDB{Numero: e.EpisodeNumber, Nome: e.Name, Data: e.AirDate, Sinopse: e.Overview, Duracao: e.Runtime}
		if e.StillPath != "" {
			ep.Imagem = imagensDoTMDB + "w300" + e.StillPath
		}
		eps = append(eps, ep)
	}
	return eps, nil
}

// ---------- A fila ----------

func (a *app) acordarEnvios() {
	select {
	case a.enviosAcordar <- struct{}{}:
	default:
	}
}

// trabalharNosEnvios manda um por vez, na ordem em que foram pedidos.
func (a *app) trabalharNosEnvios(ctx context.Context) {
	a.db.Exec(`UPDATE envios_ao_acervo SET estado = 'na_fila', etapa = '' WHERE estado = 'enviando'`) // o servidor reiniciou no meio
	for ctx.Err() == nil {
		switch a.enviarProximo(ctx) {
		case semEnvios:
			select {
			case <-ctx.Done():
			case <-a.enviosAcordar:
			case <-time.After(esperaDoAcervo):
			}
		case esperarAssinatura:
			select {
			case <-ctx.Done():
			case <-time.After(esperaDoAcervo):
			}
		}
	}
}

type resultadoDoEnvio int

const (
	enviouUm resultadoDoEnvio = iota
	semEnvios
	esperarAssinatura
)

// enviarProximo faz o próximo da fila (enviado ou falhou). Com a assinatura em uso, ele volta para a
// fila e espera.
func (a *app) enviarProximo(ctx context.Context) resultadoDoEnvio {
	var e envioAoAcervo
	err := a.db.QueryRowContext(ctx, `SELECT id, tipo, tmdb, temporada, episodio, nome FROM envios_ao_acervo WHERE estado = 'na_fila' ORDER BY id LIMIT 1`).
		Scan(&e.ID, &e.Tipo, &e.TMDB, &e.Temporada, &e.Episodio, &e.Nome)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("envios ao Acervo: %v", err)
		}
		return semEnvios
	}
	a.db.ExecContext(ctx, `UPDATE envios_ao_acervo SET estado = 'enviando', etapa = '', atualizado_em = ? WHERE id = ?`, a.carimbo(), e.ID)
	etapa := func(texto string) {
		a.db.ExecContext(ctx, `UPDATE envios_ao_acervo SET etapa = ?, atualizado_em = ? WHERE id = ?`, texto, a.carimbo(), e.ID)
	}
	via, acervoID, err := a.enviarAoAcervo(ctx, e, etapa)
	switch {
	case ctx.Err() != nil:
		return semEnvios
	case errors.Is(err, errAssinaturaOcupada):
		// Alguém está assistindo pela assinatura: volta para a fila e tenta daqui a pouco.
		a.db.ExecContext(ctx, `UPDATE envios_ao_acervo SET estado = 'na_fila', etapa = 'Esperando a assinatura ficar livre…', atualizado_em = ? WHERE id = ?`, a.carimbo(), e.ID)
		return esperarAssinatura
	case err != nil:
		msg := semSegredos(err).Error()
		log.Printf("envio ao Acervo de %q: %s", e.Nome, msg)
		a.db.ExecContext(ctx, `UPDATE envios_ao_acervo SET estado = 'falhou', etapa = '', erro = ?, via = ?, atualizado_em = ? WHERE id = ?`, msg, via, a.carimbo(), e.ID)
		a.avisos.avisar(aviso{Titulo: e.Nome, Texto: "Não foi para o Acervo: " + msg, Link: fmt.Sprintf("%s/#/titulo/%s/%d", a.cfg.link, e.Tipo, e.TMDB), Urgente: true})
	default:
		a.db.ExecContext(ctx, `UPDATE envios_ao_acervo SET estado = 'enviado', etapa = '', erro = '', via = ?, acervo_id = ?, atualizado_em = ? WHERE id = ?`,
			via, acervoID, a.carimbo(), e.ID)
	}
	return enviouUm
}

// documentarioParaOAcervo é o que o Acervo recebe junto com o texto (D50 de lá).
type documentarioParaOAcervo struct {
	Chave           string `json:"chave"`
	Nome            string `json:"nome"`
	Original        string `json:"original,omitempty"`
	Ano             string `json:"ano,omitempty"`
	Sinopse         string `json:"sinopse,omitempty"`
	Idioma          string `json:"idioma,omitempty"`
	Temporada       int    `json:"temporada,omitempty"`
	Episodio        int    `json:"episodio,omitempty"`
	Episodios       int    `json:"episodios,omitempty"`
	TituloEpisodio  string `json:"titulo_episodio,omitempty"`
	SinopseEpisodio string `json:"sinopse_episodio,omitempty"`
	Duracao         int    `json:"duracao,omitempty"` // segundos
	Capa            string `json:"capa,omitempty"`
	Link            string `json:"link,omitempty"`
	TMDB            string `json:"tmdb"`
	Origem          string `json:"origem,omitempty"`
	Texto           string `json:"texto"`
	Legenda         string `json:"legenda,omitempty"`
	IdiomaDoTexto   string `json:"idioma_texto,omitempty"`
}

// enviarAoAcervo faz um filme ou episódio: acha o texto (legenda ou áudio) e manda. Devolve por onde
// o texto veio e o id da Referência no Acervo.
func (a *app) enviarAoAcervo(ctx context.Context, e envioAoAcervo, etapa func(string)) (string, string, error) {
	chave := a.chaveDoTMDB()
	if chave == "" {
		return "", "", errors.New("Falta a chave do TMDB em Ajustes.")
	}
	etapa("Lendo o título no TMDB…")
	t, err := a.tituloCompleto(ctx, chave, e.Tipo, e.TMDB)
	if err != nil {
		return "", "", err
	}
	d := documentarioParaOAcervo{
		Chave: fmt.Sprintf("%s/%d", e.Tipo, e.TMDB), Nome: t.Titulo, Original: t.Original, Ano: t.Ano, Sinopse: t.Sinopse,
		Idioma: t.Idioma, Duracao: t.Duracao * 60, Capa: strings.Replace(t.Capa, "/w342/", "/w500/", 1),
		Link: fmt.Sprintf("%s/#/titulo/%s/%d", a.cfg.link, e.Tipo, e.TMDB),
		TMDB: fmt.Sprintf("https://www.themoviedb.org/movie/%d", e.TMDB),
	}
	if e.Tipo == "serie" {
		d.Temporada, d.Episodio, d.Duracao = e.Temporada, e.Episodio, 0
		d.TMDB = fmt.Sprintf("https://www.themoviedb.org/tv/%d/season/%d/episode/%d", e.TMDB, e.Temporada, e.Episodio)
		eps, err := a.episodiosDaTemporada(ctx, chave, e.TMDB, e.Temporada)
		if err != nil {
			return "", "", err
		}
		d.Episodios = len(eps)
		for _, ep := range eps {
			if ep.Numero == e.Episodio {
				d.TituloEpisodio, d.SinopseEpisodio, d.Duracao = ep.Nome, ep.Sinopse, ep.Duracao*60
			}
		}
	}
	fontes := a.videosDoDocumentario(ctx, t, e)

	// 1 e 2: a legenda no idioma original.
	etapa("Procurando a legenda no idioma original…")
	if srt, qual, origem := a.legendaParaOAcervo(ctx, t, e, fontes, float64(d.Duracao)); srt != nil {
		d.Texto, d.Legenda, d.IdiomaDoTexto, d.Origem = "legenda", qual, t.Idioma, origem
		etapa("Mandando a legenda ao Acervo…")
		id, err := a.mandarAoAcervo(ctx, d, "legenda", "legenda.srt", bytes.NewReader(srt))
		return "Legenda: " + qual, id, err
	}

	// 3: o áudio original.
	if len(fontes) == 0 {
		return "", "", errors.New("Sem legenda no idioma original e sem vídeo para ouvir: abra um torrent deste " +
			map[bool]string{true: "episódio", false: "filme"}[e.Tipo == "serie"] + " e mande de novo.")
	}
	tmp, err := os.MkdirTemp("", "acervo-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)
	audio := filepath.Join(tmp, "audio.ogg")
	var ultimoErro error
	for _, f := range fontes {
		var idioma string
		idioma, ultimoErro = a.audioParaOAcervo(ctx, f, t.Idioma, audio, etapa)
		if ultimoErro == nil {
			d.Texto, d.Origem, d.IdiomaDoTexto = "audio", f.origem, idioma
			break
		}
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		if errors.Is(ultimoErro, errAssinaturaOcupada) {
			return "Áudio: " + f.descricao, "", ultimoErro // espera a conexão, em vez de baixar o torrent inteiro
		}
		log.Printf("áudio de %q por %s: %v", e.Nome, f.descricao, semSegredos(ultimoErro))
	}
	via := "Áudio: " + fontes[0].descricao
	if ultimoErro != nil {
		return via, "", ultimoErro
	}
	for _, f := range fontes {
		if f.origem == d.Origem {
			via = "Áudio: " + f.descricao
			break
		}
	}
	arq, err := os.Open(audio)
	if err != nil {
		return via, "", err
	}
	defer arq.Close()
	etapa("Mandando o áudio ao Acervo…")
	id, err := a.mandarAoAcervo(ctx, d, "audio", "audio.ogg", arq)
	return via, id, err
}

// ---------- De onde vem ----------

// videoDoDocumentario é um lugar de onde o vídeo pode ser lido.
type videoDoDocumentario struct {
	chave     string // filme:1, episodio:2 ou arquivo:3, como no resto do app
	tipo      string
	id        int64
	baixado   string // o caminho, quando está inteiro no disco
	origem    string // assinatura ou torrent
	descricao string
}

// videosDoDocumentario: os vídeos deste filme ou episódio, do mais barato de ler ao mais caro:
// baixados (torrent ou assinatura), a assinatura (só o áudio, com a conexão livre) e o torrent aberto
// (baixa o que faltar enquanto lê).
func (a *app) videosDoDocumentario(ctx context.Context, t tituloDoTMDB, e envioAoAcervo) []videoDoDocumentario {
	var baixados, assinatura, torrents []videoDoDocumentario
	// Torrents já abertos deste título.
	rows, err := a.db.QueryContext(ctx, `SELECT f.id FROM torrent_arquivos f JOIN torrents t ON t.hash = f.hash
		WHERE t.tmdb = ? AND f.tipo = 'video' AND (? = 0 OR (f.temporada = ? AND f.episodio = ?)) ORDER BY f.tamanho DESC`,
		fmt.Sprintf("%s/%d", e.Tipo, e.TMDB), e.Episodio, e.Temporada, e.Episodio)
	if err == nil {
		var ids []int64
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				ids = append(ids, id)
			}
		}
		rows.Close()
		for _, id := range ids {
			f, _, err := a.arquivo(ctx, id)
			if err != nil {
				continue
			}
			v := videoDoDocumentario{chave: fmt.Sprintf("arquivo:%d", id), tipo: "arquivo", id: id, origem: "torrent", descricao: "torrent (" + f.Nome + ")"}
			if c := a.caminhoBaixado(f); c != "" {
				v.baixado, v.descricao = c, "torrent baixado ("+f.Nome+")"
				baixados = append(baixados, v)
			} else {
				torrents = append(torrents, v)
			}
		}
	}
	// A assinatura.
	for _, it := range a.naAssinatura(ctx, t) {
		if e.Tipo == "filme" {
			v := videoDoDocumentario{chave: fmt.Sprintf("filme:%d", it.ID), tipo: "filme", id: int64(it.ID), origem: "assinatura", descricao: "assinatura"}
			if c := a.baixadoDaAssinatura(ctx, v.chave); c != "" {
				v.baixado, v.descricao = c, "download da assinatura"
				baixados = append(baixados, v)
			} else {
				assinatura = append(assinatura, v)
			}
			continue
		}
		if _, err := a.lerSerie(ctx, int64(it.ID), false); err != nil {
			continue
		}
		var ep int64
		if a.db.QueryRowContext(ctx, `SELECT id FROM episodios WHERE serie_id = ? AND temporada = ? AND numero = ?`, it.ID, e.Temporada, e.Episodio).Scan(&ep) != nil {
			continue
		}
		v := videoDoDocumentario{chave: fmt.Sprintf("episodio:%d", ep), tipo: "episodio", id: ep, origem: "assinatura", descricao: "assinatura"}
		if c := a.baixadoDaAssinatura(ctx, v.chave); c != "" {
			v.baixado, v.descricao = c, "download da assinatura"
			baixados = append(baixados, v)
		} else {
			assinatura = append(assinatura, v)
		}
	}
	return append(append(baixados, assinatura...), torrents...)
}

// opensubtitlesDoIdioma: o código que o OpenSubtitles usa para o idioma do TMDB.
func opensubtitlesDoIdioma(i string) string {
	switch i {
	case "pt":
		return "pt-br,pt-pt"
	case "zh", "cn":
		return "zh-cn,zh-tw"
	}
	return i
}

// legendaParaOAcervo: a legenda no idioma original, de dentro de um arquivo baixado ou do
// OpenSubtitles (pelo código do TMDB, com a duração conferida). Sem idioma original conhecido, nada.
func (a *app) legendaParaOAcervo(ctx context.Context, t tituloDoTMDB, e envioAoAcervo, fontes []videoDoDocumentario, esperada float64) (srt []byte, qual, origem string) {
	if t.Idioma == "" {
		return nil, "", ""
	}
	for _, f := range fontes {
		if f.baixado == "" {
			continue
		}
		if b := legendaDeDentroNoIdioma(ctx, f.baixado, t.Idioma); b != nil && legendaConfere(b, esperada) {
			return b, fmt.Sprintf("de dentro do arquivo, %s", t.Idioma), f.origem
		}
	}
	chave := a.chaveDoOpenSubtitles()
	if chave == "" {
		return nil, "", ""
	}
	v := videoParaLegenda{tmdbFilme: int64(e.TMDB), idiomas: opensubtitlesDoIdioma(t.Idioma)}
	if e.Tipo == "serie" {
		v = videoParaLegenda{tmdbSerie: int64(e.TMDB), temporada: e.Temporada, episodio: e.Episodio, idiomas: opensubtitlesDoIdioma(t.Idioma)}
	}
	opcoes, err := a.buscarLegendas(ctx, chave, v)
	if err != nil {
		log.Printf("legenda para o Acervo (%s): %v", e.Nome, err)
		return nil, "", ""
	}
	tentadas := 0
	for _, o := range opcoes {
		if idiomaDaLegenda(o.Idioma) != t.Idioma || o.Maquina || tentadas >= 3 {
			continue
		}
		tentadas++
		caminho := a.arquivoDaLegenda(o.ID)
		if a.baixarLegenda(ctx, o.ID, caminho) != nil {
			continue
		}
		b, err := os.ReadFile(caminho)
		if err == nil && legendaConfere(b, esperada) {
			return b, fmt.Sprintf("OpenSubtitles, %s: %s", o.Idioma, o.Nome), "opensubtitles"
		}
	}
	return nil, "", ""
}

// legendaConfere: falas suficientes e, sabendo a duração, a última fala perto do fim (uma legenda
// de outra versão ou de outro título termina muito antes ou depois).
func legendaConfere(srt []byte, esperada float64) bool {
	falas := lerFalas(srt)
	if len(falas) < 40 {
		return false
	}
	if esperada <= 0 {
		return true
	}
	fim := falas[len(falas)-1].fim
	return fim >= esperada*0.7 && fim <= esperada*1.08
}

// legendaDeDentroNoIdioma extrai do arquivo a legenda em texto no idioma pedido (as de imagem não servem).
func legendaDeDentroNoIdioma(ctx context.Context, caminho, idioma string) []byte {
	s, err := lerFormatos(ctx, caminho)
	if err != nil {
		return nil
	}
	for i, l := range s.Legendas {
		if idiomaCurto(strings.ToLower(l)) != idioma {
			continue
		}
		c, cancel := context.WithTimeout(ctx, 3*time.Minute)
		saida, err := exec.CommandContext(c, programaFFmpeg, "-v", "error", "-i", caminho, "-map", fmt.Sprintf("0:s:%d", i), "-f", "srt", "-").Output()
		cancel()
		if err == nil && len(lerFalas(saida)) >= 40 {
			return saida
		}
	}
	return nil
}

// faixaNoIdioma: a faixa de áudio no idioma original; sem saber, a primeira.
func faixaNoIdioma(s *sondagem, idioma string) (int, string) {
	if s == nil {
		return 0, ""
	}
	for i, f := range s.Audios {
		if idioma != "" && idiomaCurto(strings.ToLower(f.Idioma)) == idioma {
			return i, idioma
		}
	}
	if len(s.Audios) > 0 && !semIdioma[strings.ToLower(s.Audios[0].Idioma)] {
		return 0, idiomaCurto(strings.ToLower(s.Audios[0].Idioma))
	}
	return 0, ""
}

// audioParaOAcervo tira o áudio original do vídeo, em mono a 16 kHz e Opus (~11 MB por hora).
// Devolve o idioma da faixa escolhida (vazio se o arquivo não diz: o Whisper reconhece).
func (a *app) audioParaOAcervo(ctx context.Context, f videoDoDocumentario, idioma, destino string, etapa func(string)) (string, error) {
	var s *sondagem
	if f.tipo == "arquivo" {
		arq, _, err := a.arquivo(ctx, f.id)
		if err != nil {
			return "", err
		}
		var guardada string
		a.db.QueryRowContext(ctx, `SELECT sondagem FROM torrent_arquivos WHERE id = ?`, f.id).Scan(&guardada)
		var lida sondagem
		if json.Unmarshal([]byte(guardada), &lida) == nil && len(lida.Audios) > 0 {
			s = &lida
		} else if lida, err := a.sondar(ctx, arq); err == nil {
			s = &lida
		}
	} else if f.baixado != "" {
		if lida, err := lerFormatos(ctx, f.baixado); err == nil {
			s = &lida
		}
	} else {
		etapa("Lendo as faixas de áudio da assinatura…")
		s, _ = a.formatosDe(ctx, f.tipo, f.id)
	}
	faixa, idiomaDaFaixa := faixaNoIdioma(s, idioma)

	puxado := a.audioPuxado(f.chave)
	_, jaPuxado := os.Stat(puxado)
	fonte, faixaNaFonte, extras, err := a.fonteDoAudio(ctx, f.chave, f.tipo, f.id, faixa, func(_, msg string) {
		if msg != "" {
			etapa(strings.Replace(msg, " (uns 15 s)", "", 1)) // um filme inteiro leva mais de um minuto
		}
	})
	if err != nil {
		return "", err
	}
	if jaPuxado != nil {
		defer os.Remove(puxado) // puxado só para isto: não fica ocupando o disco
	}
	if f.origem == "torrent" && f.baixado == "" {
		etapa("Lendo o áudio pelo torrent (baixa o que faltar)…")
	} else {
		etapa("Tirando o áudio original…")
	}
	c, cancel := context.WithTimeout(ctx, tempoMaximoDoAudio)
	defer cancel()
	args := append([]string{"-v", "error", "-y"}, extras...)
	args = append(args, "-i", fonte, "-map", fmt.Sprintf("0:a:%d", faixaNaFonte), "-vn", "-ac", "1", "-ar", "16000",
		"-c:a", "libopus", "-b:a", "24k", "-application", "voip", destino)
	if out, err := exec.CommandContext(c, programaFFmpeg, args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("Não consegui tirar o áudio: %v %s", err, strings.TrimSpace(string(out)))
	}
	if st, err := os.Stat(destino); err != nil || st.Size() < 10_000 {
		return "", errors.New("O áudio saiu vazio.")
	}
	return idiomaDaFaixa, nil
}

// mandarAoAcervo faz o POST /api/documentarios do Acervo. Já estar lá (409) também serve.
func (a *app) mandarAoAcervo(ctx context.Context, d documentarioParaOAcervo, campo, nome string, conteudo io.Reader) (string, error) {
	leitor, escritor := io.Pipe()
	mw := multipart.NewWriter(escritor)
	go func() {
		dados, _ := json.Marshal(d)
		err := mw.WriteField("dados", string(dados))
		if err == nil {
			var parte io.Writer
			if parte, err = mw.CreateFormFile(campo, nome); err == nil {
				_, err = io.Copy(parte, conteudo)
			}
		}
		if err == nil {
			err = mw.Close()
		}
		escritor.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlDoAcervo()+"/api/documentarios", leitor)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := a.http.Do(req)
	if err != nil {
		return "", errors.New("O Acervo não respondeu.")
	}
	defer resp.Body.Close()
	var r struct {
		ID   string `json:"id"`
		Erro string `json:"erro"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r)
	if (resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusConflict) && r.ID != "" {
		return r.ID, nil
	}
	return "", fmt.Errorf("O Acervo recusou (%d): %s", resp.StatusCode, primeiro(r.Erro, resp.Status))
}
