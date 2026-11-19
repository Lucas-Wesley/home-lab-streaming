package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Flashcards de um título (S49). Na página de um filme ou série em inglês, "Gerar flashcards" escolhe
// as melhores frases (40 por padrão, o Lucas muda na hora) e manda ao app Flashcards como uma
// proposta, que ele escuta, edita e aprova lá, num baralho só para filmes e séries ("Inglês · Filmes
// e séries", tema Inglês), com a etiqueta do título em cada cartão. Duas fases, numa fila só:
//
//  1. Pela legenda, sem ocupar a conexão: a legenda em inglês (de dentro do arquivo baixado ou do
//     OpenSubtitles, pelo código do TMDB); as frases candidatas e a nota de cada uma, sem IA
//     (frases.go), contra as palavras dos cartões que os baralhos do tema Inglês já têm; o Claude
//     escolhe entre as melhores as de inglês do dia a dia, traduz e explica a expressão. Se só
//     houver 25 boas, vão 25. A proposta vai ao Flashcards com os cartões esperando o áudio.
//  2. O áudio de cada frase, cortado do vídeo: do arquivo baixado, da assinatura (o áudio inteiro é
//     puxado de uma vez, só com a conexão livre: com alguém assistindo, espera e tenta depois) ou do
//     torrent aberto (baixa o que precisa). A legenda é acertada com o vídeo pelo Whisper antes de
//     cortar (menos quando veio de dentro do próprio arquivo). Cada áudio vai ao Flashcards, que o
//     põe no cartão, mesmo que já aprovado. Sem vídeo, o pedido diz e "Gerar os áudios" tenta de novo.

const esquemaDasFrases = `
CREATE TABLE IF NOT EXISTS frases_para_flashcards (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	tipo          TEXT NOT NULL,              -- filme ou serie (TMDB)
	tmdb          INTEGER NOT NULL,
	temporada     INTEGER NOT NULL DEFAULT 0,
	episodio      INTEGER NOT NULL DEFAULT 0,
	nome          TEXT NOT NULL DEFAULT '',
	limite        INTEGER NOT NULL,
	estado        TEXT NOT NULL,              -- na_fila, escolhendo, enviado, falhou
	etapa         TEXT NOT NULL DEFAULT '',
	erro          TEXT NOT NULL DEFAULT '',
	legenda       TEXT NOT NULL DEFAULT '',   -- de onde veio: "os-…" ou "dentro:<chave do vídeo>"
	candidatas    INTEGER NOT NULL DEFAULT 0,
	geracao       INTEGER NOT NULL DEFAULT 0, -- o pedido no Flashcards
	frases        TEXT NOT NULL DEFAULT '[]', -- JSON: []fraseEnviada
	audio         TEXT NOT NULL DEFAULT '',   -- esperando, cortando, pronto, sem_video, falhou
	audio_msg     TEXT NOT NULL DEFAULT '',
	criado_em     TEXT NOT NULL,
	atualizado_em TEXT NOT NULL,
	UNIQUE (tipo, tmdb, temporada, episodio)
);
`

const (
	limitePadraoDeFrases = 40
	limiteMaximoDeFrases = 100
	// Quantas candidatas (as de nota mais alta) o Claude lê para escolher.
	candidatasParaOClaude = 150
	// Cada palavra nova aparece em no máximo duas candidatas: variedade.
	frasesPorPalavraNova    = 2
	nomeDoBaralhoDeFilmes   = "Inglês · Filmes e séries"
	temaDeIngles            = "Inglês"
	audiosPorEnvio          = 10
	tempoMaximoDaEscolha    = 6 * time.Minute
	tempoMaximoDosAudios    = 90 * time.Minute
	esperaDaConexaoDasFrase = time.Minute
)

// modeloDasFrases: quem escolhe e traduz as frases (ver a comparação em DECISOES, S49).
func modeloDasFrases() string { return env("CLAUDE_MODELO_FRASES", "sonnet") }

func linkDoFlashcards() string {
	return strings.TrimRight(env("FLASHCARDS_LINK", "https://flashcards.internal"), "/")
}

type fraseEnviada struct {
	Texto    string  `json:"texto"`
	Inicio   float64 `json:"inicio"`
	Fim      float64 `json:"fim"`
	Fonte    string  `json:"fonte"`
	Audio    string  `json:"audio,omitempty"`     // o nome do áudio no Flashcards
	SemAudio bool    `json:"sem_audio,omitempty"` // não deu para cortar
}

type pedidoDeFrases struct {
	ID         int64  `json:"id"`
	Tipo       string `json:"tipo"`
	TMDB       int    `json:"tmdb"`
	Temporada  int    `json:"temporada,omitempty"`
	Episodio   int    `json:"episodio,omitempty"`
	Nome       string `json:"nome"`
	Limite     int    `json:"limite"`
	Estado     string `json:"estado"`
	Etapa      string `json:"etapa,omitempty"`
	Erro       string `json:"erro,omitempty"`
	Legenda    string `json:"-"`
	Candidatas int    `json:"candidatas,omitempty"`
	Geracao    int64  `json:"geracao,omitempty"`
	Link       string `json:"link,omitempty"` // a proposta no Flashcards
	Frases     int    `json:"frases"`
	ComAudio   int    `json:"com_audio"`
	Audio      string `json:"audio,omitempty"`
	AudioMsg   string `json:"audio_msg,omitempty"`
	lista      []fraseEnviada
}

const colunasDasFrases = `id, tipo, tmdb, temporada, episodio, nome, limite, estado, etapa, erro, legenda, candidatas, geracao, frases, audio, audio_msg`

func lerPedidoDeFrases(l interface{ Scan(...any) error }) (*pedidoDeFrases, error) {
	var p pedidoDeFrases
	var frases string
	if err := l.Scan(&p.ID, &p.Tipo, &p.TMDB, &p.Temporada, &p.Episodio, &p.Nome, &p.Limite, &p.Estado, &p.Etapa, &p.Erro, &p.Legenda,
		&p.Candidatas, &p.Geracao, &frases, &p.Audio, &p.AudioMsg); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(frases), &p.lista)
	p.Frases = len(p.lista)
	for _, f := range p.lista {
		if f.Audio != "" {
			p.ComAudio++
		}
	}
	if p.Geracao > 0 {
		p.Link = fmt.Sprintf("%s/#/geracao/%d", linkDoFlashcards(), p.Geracao)
	}
	return &p, nil
}

func (a *app) pedidoDeFrasesDe(ctx context.Context, tipo string, tmdb, temporada, episodio int) (*pedidoDeFrases, error) {
	return lerPedidoDeFrases(a.db.QueryRowContext(ctx, `SELECT `+colunasDasFrases+` FROM frases_para_flashcards
		WHERE tipo = ? AND tmdb = ? AND temporada = ? AND episodio = ?`, tipo, tmdb, temporada, episodio))
}

func (a *app) guardarFrases(ctx context.Context, id int64, lista []fraseEnviada) {
	b, _ := json.Marshal(lista)
	a.db.ExecContext(ctx, `UPDATE frases_para_flashcards SET frases = ?, atualizado_em = ? WHERE id = ?`, string(b), a.carimbo(), id)
}

// ---------- Rotas ----------

func tituloDaRota(w http.ResponseWriter, r *http.Request) (string, int, bool) {
	tipo, id := r.PathValue("tipo"), atoi(r.PathValue("id"))
	if (tipo != "filme" && tipo != "serie") || id <= 0 {
		erroJSON(w, http.StatusBadRequest, "Título inválido.")
		return "", 0, false
	}
	return tipo, id, true
}

// handleFrasesDoTitulo: GET /api/titulos/{tipo}/{id}/flashcards?temporada=&episodio= — se dá para
// gerar e o que já foi pedido.
func (a *app) handleFrasesDoTitulo(w http.ResponseWriter, r *http.Request) {
	tipo, id, ok := tituloDaRota(w, r)
	if !ok {
		return
	}
	temporada, episodio := atoi(r.URL.Query().Get("temporada")), atoi(r.URL.Query().Get("episodio"))
	if tipo == "serie" && temporada == 0 {
		temporada, episodio = 1, 1
	}
	resposta := map[string]any{"disponivel": flashcardsDisponivel(), "limite": limitePadraoDeFrases}
	p, err := a.pedidoDeFrasesDe(r.Context(), tipo, id, temporada, episodio)
	if err == nil {
		resposta["pedido"] = p
	} else if !errors.Is(err, sql.ErrNoRows) {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resposta)
}

// handlePedirFrases: POST /api/titulos/{tipo}/{id}/flashcards {limite, temporada, episodio}. Pedir de
// novo refaz (o Flashcards troca a proposta, se ainda estiver em revisão).
func (a *app) handlePedirFrases(w http.ResponseWriter, r *http.Request) {
	tipo, id, ok := tituloDaRota(w, r)
	if !ok {
		return
	}
	var corpo struct {
		Limite    int `json:"limite"`
		Temporada int `json:"temporada"`
		Episodio  int `json:"episodio"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	if corpo.Limite == 0 {
		corpo.Limite = limitePadraoDeFrases
	}
	if tipo == "filme" {
		corpo.Temporada, corpo.Episodio = 0, 0
	} else if corpo.Temporada <= 0 || corpo.Episodio <= 0 {
		corpo.Temporada, corpo.Episodio = 1, 1
	}
	switch {
	case !flashcardsDisponivel():
		erroJSON(w, http.StatusConflict, "O Flashcards não está ligado neste servidor (FLASHCARDS_URL e Claude).")
		return
	case corpo.Limite < 1 || corpo.Limite > limiteMaximoDeFrases:
		erroJSON(w, http.StatusBadRequest, fmt.Sprintf("Escolha de 1 a %d frases.", limiteMaximoDeFrases))
		return
	}
	ctx := r.Context()
	if p, err := a.pedidoDeFrasesDe(ctx, tipo, id, corpo.Temporada, corpo.Episodio); err == nil && (p.Estado == "na_fila" || p.Estado == "escolhendo" || p.Audio == "cortando") {
		erroJSON(w, http.StatusConflict, "Já está gerando os flashcards deste título.")
		return
	}
	agora := a.carimbo()
	if _, err := a.db.ExecContext(ctx, `INSERT INTO frases_para_flashcards (tipo, tmdb, temporada, episodio, limite, estado, criado_em, atualizado_em)
		VALUES (?, ?, ?, ?, ?, 'na_fila', ?, ?)
		ON CONFLICT (tipo, tmdb, temporada, episodio) DO UPDATE SET limite = excluded.limite, estado = 'na_fila', etapa = '', erro = '',
			candidatas = 0, frases = '[]', audio = '', audio_msg = '', atualizado_em = excluded.atualizado_em`,
		tipo, id, corpo.Temporada, corpo.Episodio, corpo.Limite, agora, agora); err != nil {
		falha(w, err)
		return
	}
	a.acordarFrases()
	p, err := a.pedidoDeFrasesDe(ctx, tipo, id, corpo.Temporada, corpo.Episodio)
	if err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleRefazerAudios: POST /api/titulos/{tipo}/{id}/flashcards/audios — tenta de novo os áudios que faltam.
func (a *app) handleRefazerAudios(w http.ResponseWriter, r *http.Request) {
	tipo, id, ok := tituloDaRota(w, r)
	if !ok {
		return
	}
	var corpo struct {
		Temporada int `json:"temporada"`
		Episodio  int `json:"episodio"`
	}
	if r.ContentLength != 0 && !lerCorpo(w, r, &corpo) {
		return
	}
	if tipo == "filme" {
		corpo.Temporada, corpo.Episodio = 0, 0
	} else if corpo.Temporada <= 0 {
		corpo.Temporada, corpo.Episodio = 1, 1
	}
	ctx := r.Context()
	p, err := a.pedidoDeFrasesDe(ctx, tipo, id, corpo.Temporada, corpo.Episodio)
	if err != nil || p.Estado != "enviado" {
		erroJSON(w, http.StatusConflict, "Gere os flashcards primeiro.")
		return
	}
	if p.Audio == "cortando" {
		erroJSON(w, http.StatusConflict, "Os áudios já estão sendo cortados.")
		return
	}
	for i := range p.lista {
		p.lista[i].SemAudio = false
	}
	a.guardarFrases(ctx, p.ID, p.lista)
	a.db.ExecContext(ctx, `UPDATE frases_para_flashcards SET audio = 'esperando', audio_msg = '', atualizado_em = ? WHERE id = ?`, a.carimbo(), p.ID)
	a.acordarFrases()
	p, _ = a.pedidoDeFrasesDe(ctx, tipo, id, corpo.Temporada, corpo.Episodio)
	writeJSON(w, http.StatusOK, p)
}

// ---------- A fila ----------

func (a *app) acordarFrases() {
	select {
	case a.frasesAcordar <- struct{}{}:
	default:
	}
}

// trabalharNasFrases faz um pedido por vez: primeiro escolher frases (rápido, não ocupa a conexão),
// depois os áudios.
func (a *app) trabalharNasFrases(ctx context.Context) {
	a.db.Exec(`UPDATE frases_para_flashcards SET estado = 'na_fila', etapa = '' WHERE estado = 'escolhendo'`)
	a.db.Exec(`UPDATE frases_para_flashcards SET audio = 'esperando' WHERE audio = 'cortando'`)
	for ctx.Err() == nil {
		fez, esperar := a.proximasFrases(ctx)
		if fez {
			continue
		}
		espera := time.Hour
		if esperar {
			espera = esperaDaConexaoDasFrase
		}
		select {
		case <-ctx.Done():
		case <-a.frasesAcordar:
		case <-time.After(espera):
		}
	}
}

// proximasFrases: fez algo, ou deve tentar de novo daqui a pouco (a conexão estava ocupada).
func (a *app) proximasFrases(ctx context.Context) (fez, esperar bool) {
	p, err := lerPedidoDeFrases(a.db.QueryRowContext(ctx, `SELECT `+colunasDasFrases+` FROM frases_para_flashcards WHERE estado = 'na_fila' ORDER BY id LIMIT 1`))
	if err == nil {
		a.escolherFrases(ctx, p)
		return true, false
	}
	p, err = lerPedidoDeFrases(a.db.QueryRowContext(ctx, `SELECT `+colunasDasFrases+` FROM frases_para_flashcards WHERE estado = 'enviado' AND audio = 'esperando' ORDER BY atualizado_em LIMIT 1`))
	if err != nil {
		return false, false
	}
	err = a.cortarAudios(ctx, p)
	switch {
	case ctx.Err() != nil:
		return false, false
	case errors.Is(err, errAssinaturaOcupada):
		a.db.ExecContext(ctx, `UPDATE frases_para_flashcards SET audio = 'esperando', audio_msg = 'Esperando a conexão da assinatura ficar livre.', atualizado_em = ? WHERE id = ?`, a.carimbo(), p.ID)
		return false, true
	case err != nil:
		msg := semSegredos(err).Error()
		log.Printf("frases de %q: áudio: %s", p.Nome, msg)
		a.db.ExecContext(ctx, `UPDATE frases_para_flashcards SET audio = 'falhou', audio_msg = ?, atualizado_em = ? WHERE id = ?`, msg, a.carimbo(), p.ID)
		a.desistirDosAudios(ctx, p.ID)
	}
	return true, false
}

// ---------- Fase 1: as frases ----------

func (a *app) escolherFrases(ctx context.Context, p *pedidoDeFrases) {
	a.db.ExecContext(ctx, `UPDATE frases_para_flashcards SET estado = 'escolhendo', etapa = '', erro = '', atualizado_em = ? WHERE id = ?`, a.carimbo(), p.ID)
	etapa := func(texto string) {
		a.db.ExecContext(ctx, `UPDATE frases_para_flashcards SET etapa = ?, atualizado_em = ? WHERE id = ?`, texto, a.carimbo(), p.ID)
	}
	c, cancel := context.WithTimeout(ctx, tempoMaximoDaEscolha)
	defer cancel()
	err := a.fazerFrases(c, p, etapa)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		msg := semSegredos(err).Error()
		log.Printf("frases de %q: %s", p.Nome, msg)
		a.db.ExecContext(ctx, `UPDATE frases_para_flashcards SET estado = 'falhou', etapa = '', erro = ?, atualizado_em = ? WHERE id = ?`, msg, a.carimbo(), p.ID)
		a.avisos.avisar(aviso{Titulo: primeiro(p.Nome, "Flashcards"), Texto: "Os flashcards não saíram: " + msg,
			Link: fmt.Sprintf("%s/#/titulo/%s/%d", a.cfg.link, p.Tipo, p.TMDB), Urgente: true})
	}
}

// escolhaDoClaude: as frases escolhidas, pelo número, com a tradução e a expressão.
type escolhaDoClaude struct {
	Frases []struct {
		Numero     int    `json:"numero"`
		Traducao   string `json:"traducao"`
		Expressao  string `json:"expressao"`
		Explicacao string `json:"explicacao"`
	} `json:"frases"`
	Observacao string `json:"observacao"`
}

const esquemaDaEscolha = `{"type":"object","properties":{"frases":{"type":"array","items":{"type":"object","properties":{` +
	`"numero":{"type":"integer"},"traducao":{"type":"string"},"expressao":{"type":"string"},"explicacao":{"type":"string"}},` +
	`"required":["numero","traducao","expressao","explicacao"]}},"observacao":{"type":"string"}},"required":["frases","observacao"]}`

func (a *app) fazerFrases(ctx context.Context, p *pedidoDeFrases, etapa func(string)) error {
	chave := a.chaveDoTMDB()
	if chave == "" {
		return errors.New("Falta a chave do TMDB em Ajustes.")
	}
	etapa("Lendo o título no TMDB…")
	t, err := a.tituloCompleto(ctx, chave, p.Tipo, p.TMDB)
	if err != nil {
		return err
	}
	nome := t.Titulo
	if p.Tipo == "serie" {
		nome = nomeDoEnvio(t.Titulo, p.Temporada, p.Episodio, "")
	}
	a.db.ExecContext(ctx, `UPDATE frases_para_flashcards SET nome = ? WHERE id = ?`, nome, p.ID)
	p.Nome = nome
	if t.Idioma != "" && t.Idioma != "en" {
		return errors.New("O título não é falado em inglês.")
	}

	etapa("Procurando a legenda em inglês…")
	srt, legenda, err := a.legendaDasFrases(ctx, t, p)
	if err != nil {
		return err
	}

	etapa("Lendo os seus baralhos de inglês…")
	frentes, err := a.frentesDeIngles(ctx)
	if err != nil {
		return err
	}
	candidatas := variadas(pontuarFrases(frasesDaLegenda(srt), frentes), candidatasParaOClaude)
	if len(candidatas) == 0 {
		return errors.New("A legenda não tem frases que sirvam (ou todas já estão nos seus baralhos).")
	}

	etapa(fmt.Sprintf("O Claude está escolhendo entre %d frases…", len(candidatas)))
	escolha, err := a.escolherComClaude(ctx, t, nome, candidatas, p.Limite, modeloDasFrases())
	if err != nil {
		return err
	}

	etapa("Mandando ao Flashcards…")
	baralho, err := a.baralhoDeFilmes(ctx)
	if err != nil {
		return err
	}
	linkDoTitulo := fmt.Sprintf("%s/#/titulo/%s/%d", a.cfg.link, p.Tipo, p.TMDB)
	tag := "streaming " + etiquetaDoTitulo(t.Titulo)
	var cartoes []map[string]any
	var lista []fraseEnviada
	usadas := map[int]bool{}
	for _, e := range escolha.Frases {
		i := e.Numero - 1
		if i < 0 || i >= len(candidatas) || usadas[i] || strings.TrimSpace(e.Traducao) == "" || len(cartoes) >= p.Limite {
			continue
		}
		usadas[i] = true
		f := candidatas[i]
		fonte := fmt.Sprintf("%s?fala=%.1f", linkDoTitulo, f.Inicio)
		if p.Tipo == "serie" {
			fonte = fmt.Sprintf("%s?t=%d&e=%d&fala=%.1f", linkDoTitulo, p.Temporada, p.Episodio, f.Inicio)
		}
		verso := strings.TrimSpace(e.Traducao)
		if ex, expl := strings.TrimSpace(e.Expressao), strings.TrimSpace(e.Explicacao); ex != "" && expl != "" {
			verso += "\n\n" + ex + ": " + expl
		}
		cartoes = append(cartoes, map[string]any{"frente": f.Texto, "verso": verso, "tags": tag, "fonte": fonte, "audio_pendente": true})
		lista = append(lista, fraseEnviada{Texto: f.Texto, Inicio: f.Inicio, Fim: f.Fim, Fonte: fonte})
	}
	if len(cartoes) == 0 {
		return errors.New("O Claude não achou frases boas para estudar neste título." + entreParenteses(escolha.Observacao))
	}
	contexto := fmt.Sprintf("%d frases escolhidas entre %d candidatas da legenda em inglês.", len(cartoes), len(candidatas))
	if len(cartoes) < p.Limite {
		contexto = fmt.Sprintf("Só %d frases boas (você pediu %d), escolhidas entre %d candidatas da legenda em inglês.", len(cartoes), p.Limite, len(candidatas))
	}
	var g struct {
		ID int64 `json:"id"`
	}
	if err := a.pedirAoFlashcards(ctx, http.MethodPost, "/api/propostas", map[string]any{
		"origem": "streaming", "titulo": "Frases de " + nome, "link": linkDoTitulo, "baralho_id": baralho,
		"contexto": contexto, "observacoes": strings.TrimSpace(escolha.Observacao), "cartoes": cartoes,
	}, &g); err != nil {
		return err
	}
	b, _ := json.Marshal(lista)
	a.db.ExecContext(ctx, `UPDATE frases_para_flashcards SET estado = 'enviado', etapa = '', legenda = ?, candidatas = ?, geracao = ?, frases = ?,
		audio = 'esperando', audio_msg = '', atualizado_em = ? WHERE id = ?`, legenda, len(candidatas), g.ID, string(b), a.carimbo(), p.ID)
	return nil
}

// variadas: as n melhores, com cada palavra nova em no máximo duas frases.
func variadas(frases []fraseDoFilme, n int) []fraseDoFilme {
	vezes := map[string]int{}
	var out []fraseDoFilme
	for _, f := range frases {
		repetida := false
		for _, p := range f.Desconhecidas {
			if vezes[p] >= frasesPorPalavraNova {
				repetida = true
			}
		}
		if repetida {
			continue
		}
		for _, p := range f.Desconhecidas {
			vezes[p]++
		}
		out = append(out, f)
		if len(out) == n {
			break
		}
	}
	// Na ordem do filme: o Claude lê melhor a história em sequência.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Inicio < out[j].Inicio })
	return out
}

var reNaoEtiqueta = regexp.MustCompile(`[^a-z0-9]+`)

// etiquetaDoTitulo: "O Convite" vira "o-convite".
func etiquetaDoTitulo(titulo string) string {
	return strings.Trim(reNaoEtiqueta.ReplaceAllString(comparavel(titulo), "-"), "-")
}

// legendaDasFrases: a legenda em inglês de dentro de um arquivo baixado (é a do próprio vídeo) ou a
// do OpenSubtitles, pelo código do TMDB, com a duração conferida. Devolve também de onde veio.
func (a *app) legendaDasFrases(ctx context.Context, t tituloDoTMDB, p *pedidoDeFrases) ([]byte, string, error) {
	e := envioAoAcervo{Tipo: p.Tipo, TMDB: p.TMDB, Temporada: p.Temporada, Episodio: p.Episodio, Nome: p.Nome}
	for _, f := range a.videosDoDocumentario(ctx, t, e) {
		if f.baixado == "" {
			continue
		}
		if b := legendaDeDentroNoIdioma(ctx, f.baixado, "en"); b != nil {
			return b, "dentro:" + f.chave, nil
		}
	}
	chave := a.chaveDoOpenSubtitles()
	if chave == "" {
		return nil, "", errors.New("Configure o OpenSubtitles em Ajustes.")
	}
	v := videoParaLegenda{tmdbFilme: int64(p.TMDB), idiomas: "en"}
	esperada := float64(t.Duracao * 60)
	if p.Tipo == "serie" {
		v = videoParaLegenda{tmdbSerie: int64(p.TMDB), temporada: p.Temporada, episodio: p.Episodio, idiomas: "en"}
		esperada = 0
	}
	opcoes, err := a.buscarLegendas(ctx, chave, v)
	if err != nil {
		return nil, "", err
	}
	tentadas := 0
	for _, maquina := range []bool{false, true} {
		for _, o := range opcoes {
			if idiomaDaLegenda(o.Idioma) != "en" || o.Maquina != maquina || tentadas >= 3 {
				continue
			}
			tentadas++
			caminho := a.arquivoDaLegenda(o.ID)
			if a.baixarLegenda(ctx, o.ID, caminho) != nil {
				continue
			}
			if b, err := os.ReadFile(caminho); err == nil && legendaConfere(b, esperada) {
				return b, o.ID, nil
			}
		}
	}
	return nil, "", errors.New("Não achei legenda em inglês para este título.")
}

// frentesDeIngles: as frentes dos cartões de todos os baralhos do tema Inglês.
func (a *app) frentesDeIngles(ctx context.Context) ([]string, error) {
	var baralhos []struct {
		ID   string `json:"id"`
		Nome string `json:"nome"`
		Tema string `json:"tema"`
	}
	if err := a.pedirAoFlashcards(ctx, http.MethodGet, "/api/baralhos", nil, &baralhos); err != nil {
		return nil, err
	}
	var frentes []string
	for _, b := range baralhos {
		if comparavel(b.Tema) != comparavel(temaDeIngles) {
			continue
		}
		var notas []struct {
			Frente string `json:"frente"`
		}
		if err := a.pedirAoFlashcards(ctx, http.MethodGet, "/api/notas?limite=1000&baralho="+url.QueryEscape(b.ID), nil, &notas); err != nil {
			return nil, err
		}
		for _, n := range notas {
			frentes = append(frentes, n.Frente)
		}
	}
	return frentes, nil
}

// baralhoDeFilmes acha o baralho dos filmes e séries pelo nome; se não houver, cria, no tema Inglês.
func (a *app) baralhoDeFilmes(ctx context.Context) (string, error) {
	var baralhos []struct {
		ID   string `json:"id"`
		Nome string `json:"nome"`
	}
	if err := a.pedirAoFlashcards(ctx, http.MethodGet, "/api/baralhos", nil, &baralhos); err != nil {
		return "", err
	}
	for _, b := range baralhos {
		if comparavel(b.Nome) == comparavel(nomeDoBaralhoDeFilmes) {
			return b.ID, nil
		}
	}
	var novo struct {
		ID string `json:"id"`
	}
	err := a.pedirAoFlashcards(ctx, http.MethodPost, "/api/baralhos", map[string]string{"nome": nomeDoBaralhoDeFilmes, "tema": temaDeIngles}, &novo)
	return novo.ID, err
}

// escolherComClaude: o Claude lê as candidatas (na ordem do filme, com a palavra nova marcada) e
// escolhe até limite, traduzindo e explicando a expressão de cada uma.
func (a *app) escolherComClaude(ctx context.Context, t tituloDoTMDB, nome string, candidatas []fraseDoFilme, limite int, modelo string) (escolhaDoClaude, error) {
	var lista strings.Builder
	for i, f := range candidatas {
		novas := ""
		if len(f.Desconhecidas) > 0 {
			novas = " [nova: " + strings.Join(f.Desconhecidas, ", ") + "]"
		}
		fmt.Fprintf(&lista, "%d. %s%s\n", i+1, f.Texto, novas)
	}
	sinopse := ""
	if t.Sinopse != "" {
		sinopse = "\nSinopse (só para entender o contexto): " + t.Sinopse + "\n"
	}
	pedido := fmt.Sprintf(`O Lucas aprende inglês vendo filmes e séries. Ele vai estudar frases de %q em flashcards: a frase em inglês na frente (com o áudio do filme, para ouvir e repetir) e, no verso, a tradução e, quando houver, a expressão explicada.
%s
Abaixo estão %d frases candidatas, tiradas da legenda em inglês, na ordem do filme. Elas já passaram por um filtro: tamanho bom, sem nomes de personagens, sem frases parecidas com os cartões que ele já tem. Entre colchetes, a palavra que ele provavelmente ainda não conhece (sem colchetes: ele já conhece todas as palavras, e a frase só vale se a construção for muito útil).

Escolha até %d frases, as melhores para aprender o inglês do dia a dia. Prefira:
- construções e expressões que um nativo usa em qualquer conversa (phrasal verbs, expressões idiomáticas, jeitos naturais de pedir, reclamar, concordar, convidar, se desculpar);
- frases que fazem sentido sozinhas, fora da cena;
- frases com uma coisa nova para aprender (a palavra entre colchetes, ou uma construção).
Deixe de fora:
- o que é específico da história do filme (só faz sentido na trama, fala de um objeto ou acontecimento do enredo);
- respostas triviais, frases sem nada a aprender, repetições de uma ideia já escolhida;
- frases quebradas ou que a legenda juntou de falas diferentes.
Se houver menos frases boas que %d, devolva só as boas: é melhor 25 boas que 40 com fracas.

Para cada escolhida, devolva:
- numero: o número da frase na lista;
- traducao: uma tradução natural em português do Brasil, fiel ao sentido (não palavra por palavra);
- expressao: a expressão, phrasal verb ou palavra que vale aprender na frase, como está nela (vazio se não houver);
- explicacao: o que ela quer dizer e quando se usa, em uma frase curta em português (vazio se expressao for vazia).
Em observacao, uma frase sobre o que você priorizou ou por que vieram menos frases.

Frases:
%s`, nome, sinopse, len(candidatas), limite, limite, lista.String())
	var e escolhaDoClaude
	err := a.consultarClaude(ctx, pedido, esquemaDaEscolha, "", modelo, false, &e)
	return e, err
}

// ---------- Fase 2: os áudios ----------

// desistirDosAudios: as frases sem áudio deixam de esperar no Flashcards.
func (a *app) desistirDosAudios(ctx context.Context, id int64) {
	p, err := lerPedidoDeFrases(a.db.QueryRowContext(ctx, `SELECT `+colunasDasFrases+` FROM frases_para_flashcards WHERE id = ?`, id))
	if err != nil || p.Geracao == 0 {
		return
	}
	var audios []map[string]string
	for i, f := range p.lista {
		if f.Audio == "" {
			audios = append(audios, map[string]string{"fonte": f.Fonte, "audio": ""})
			p.lista[i].SemAudio = true
		}
	}
	if len(audios) > 0 {
		a.pedirAoFlashcards(ctx, http.MethodPost, fmt.Sprintf("/api/geracoes/%d/audios", p.Geracao), map[string]any{"audios": audios}, nil)
		a.guardarFrases(ctx, p.ID, p.lista)
	}
}

func (a *app) cortarAudios(ctx context.Context, p *pedidoDeFrases) error {
	faltam := 0
	for _, f := range p.lista {
		if f.Audio == "" && !f.SemAudio {
			faltam++
		}
	}
	if faltam == 0 {
		a.db.ExecContext(ctx, `UPDATE frases_para_flashcards SET audio = 'pronto', audio_msg = '', atualizado_em = ? WHERE id = ?`, a.carimbo(), p.ID)
		return nil
	}
	a.db.ExecContext(ctx, `UPDATE frases_para_flashcards SET audio = 'cortando', audio_msg = 'Procurando o vídeo…', atualizado_em = ? WHERE id = ?`, a.carimbo(), p.ID)
	msg := func(texto string) {
		a.db.ExecContext(ctx, `UPDATE frases_para_flashcards SET audio_msg = ?, atualizado_em = ? WHERE id = ?`, texto, a.carimbo(), p.ID)
	}
	c, cancel := context.WithTimeout(ctx, tempoMaximoDosAudios)
	defer cancel()

	chave := a.chaveDoTMDB()
	if chave == "" {
		return errors.New("Falta a chave do TMDB em Ajustes.")
	}
	t, err := a.tituloCompleto(c, chave, p.Tipo, p.TMDB)
	if err != nil {
		return err
	}
	fontes := a.videosDoDocumentario(c, t, envioAoAcervo{Tipo: p.Tipo, TMDB: p.TMDB, Temporada: p.Temporada, Episodio: p.Episodio, Nome: p.Nome})
	if len(fontes) == 0 {
		a.db.ExecContext(ctx, `UPDATE frases_para_flashcards SET audio = 'sem_video', audio_msg = ?, atualizado_em = ? WHERE id = ?`,
			"Sem vídeo deste título para cortar o áudio: abra um torrent dele (ou espere aparecer na assinatura) e toque em Gerar os áudios.", a.carimbo(), p.ID)
		a.desistirDosAudios(ctx, p.ID)
		return nil
	}
	var ultimo error
	for _, f := range fontes {
		ultimo = a.cortarAudiosDe(c, p, f, msg)
		if ultimo == nil || c.Err() != nil || errors.Is(ultimo, errAssinaturaOcupada) {
			break
		}
		log.Printf("frases de %q: áudio por %s: %v", p.Nome, f.descricao, semSegredos(ultimo))
	}
	if ultimo != nil {
		return ultimo
	}
	p, _ = lerPedidoDeFrases(a.db.QueryRowContext(ctx, `SELECT `+colunasDasFrases+` FROM frases_para_flashcards WHERE id = ?`, p.ID))
	texto := fmt.Sprintf("%d de %d frases com áudio.", p.ComAudio, p.Frases)
	a.db.ExecContext(ctx, `UPDATE frases_para_flashcards SET audio = 'pronto', audio_msg = ?, atualizado_em = ? WHERE id = ?`, texto, a.carimbo(), p.ID)
	a.desistirDosAudios(ctx, p.ID)
	return nil
}

// cortarAudiosDe corta as frases que faltam de um vídeo: acha a faixa em inglês, acerta a legenda
// com o áudio e manda cada trecho ao Flashcards, de 10 em 10.
func (a *app) cortarAudiosDe(ctx context.Context, p *pedidoDeFrases, f videoDoDocumentario, msg func(string)) error {
	var s *sondagem
	switch {
	case f.tipo == "arquivo":
		s, _ = a.formatosDe(ctx, f.tipo, f.id)
	case f.baixado != "":
		if lida, err := lerFormatos(ctx, f.baixado); err == nil {
			s = &lida
		}
	default:
		msg("Lendo as faixas de áudio da assinatura…")
		var err error
		if s, err = a.formatosDe(ctx, f.tipo, f.id); err != nil {
			if strings.Contains(err.Error(), "em uso") || strings.Contains(err.Error(), "começou a ser usada") {
				return errAssinaturaOcupada
			}
			return err
		}
	}
	faixa, idioma := faixaNoIdioma(s, "en")
	if idioma != "" && idioma != "en" {
		return fmt.Errorf("O vídeo (%s) não tem o áudio em inglês.", f.descricao)
	}

	puxado := a.audioPuxado(f.chave)
	_, jaPuxado := os.Stat(puxado)
	if f.origem == "assinatura" && f.baixado == "" {
		msg("Puxando o áudio da assinatura (a conexão estava livre)…")
	}
	fonte, faixaNaFonte, _, err := a.fonteDoAudio(ctx, f.chave, f.tipo, f.id, faixa, func(string, string) {})
	if err != nil {
		return err
	}
	if jaPuxado != nil && strings.HasSuffix(fonte, ".mka") {
		defer os.Remove(puxado) // puxado só para isto
	}

	// A legenda de dentro deste mesmo arquivo já está no tempo dele; a do OpenSubtitles é acertada pelo Whisper.
	al := alinhamento{Escala: 1}
	if p.Legenda != "dentro:"+f.chave {
		msg("Acertando a legenda com o áudio (Whisper)…")
		var ok bool
		if al, ok = a.sincroniaDasFrases(ctx, p, fonte, faixaNaFonte, duracaoDoAudio(ctx, fonte)); !ok {
			if urlDoWhisper() != "" {
				return fmt.Errorf("A legenda não bateu com o áudio do vídeo (%s).", f.descricao)
			}
			log.Printf("frases de %q: sem o Whisper, os áudios saem nos tempos da legenda", p.Nome)
		}
	}

	var lote []map[string]string
	enviar := func() error {
		if len(lote) == 0 {
			return nil
		}
		err := a.pedirAoFlashcards(ctx, http.MethodPost, fmt.Sprintf("/api/geracoes/%d/audios", p.Geracao), map[string]any{"audios": lote}, nil)
		lote = nil
		return err
	}
	feitas := 0
	for i, fr := range p.lista {
		if fr.Audio != "" || fr.SemAudio {
			continue
		}
		msg(fmt.Sprintf("Cortando os áudios: %d de %d…", p.ComAudio+feitas+1, p.Frases))
		inicio, fim := fr.Inicio*al.Escala+al.Atraso, fr.Fim*al.Escala+al.Atraso
		if fim-inicio > duracaoMaximaDaFala {
			fim = inicio + duracaoMaximaDaFala
		}
		c, cancel := context.WithTimeout(ctx, 3*time.Minute)
		nome, err := a.mandarAudioDaFala(c, fonte, faixaNaFonte, math.Max(0, inicio), math.Max(0, fim))
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			log.Printf("frases de %q: áudio de %q: %v", p.Nome, fr.Texto, semSegredos(err))
			p.lista[i].SemAudio = true
			lote = append(lote, map[string]string{"fonte": fr.Fonte, "audio": ""})
		} else {
			p.lista[i].Audio = nome
			lote = append(lote, map[string]string{"fonte": fr.Fonte, "audio": nome})
			feitas++
		}
		if len(lote) >= audiosPorEnvio {
			if err := enviar(); err != nil {
				return err
			}
			a.guardarFrases(ctx, p.ID, p.lista)
		}
	}
	if err := enviar(); err != nil {
		return err
	}
	a.guardarFrases(ctx, p.ID, p.lista)
	return nil
}

// duracaoDoAudio: a duração (ffprobe); 0 se não der.
func duracaoDoAudio(ctx context.Context, fonte string) float64 {
	s, err := lerFormatos(ctx, fonte)
	if err != nil {
		return 0
	}
	return s.Duracao
}

// sincroniaDasFrases: o Whisper ouve 3 trechos de 90 s do áudio e as falas são casadas com as da
// legenda usada nas frases.
func (a *app) sincroniaDasFrases(ctx context.Context, p *pedidoDeFrases, fonte string, faixa int, duracao float64) (alinhamento, bool) {
	nada := alinhamento{Escala: 1}
	if urlDoWhisper() == "" || duracao <= 0 {
		return nada, false
	}
	srt, err := os.ReadFile(a.arquivoDaLegenda(p.Legenda))
	if err != nil {
		return nada, false
	}
	var ouvidas []trechoFalado
	for _, ponto := range []float64{0.25, 0.5, 0.75} {
		inicio := math.Max(0, duracao*ponto-45)
		wav, err := audioDaFaixa(ctx, fonte, faixa, inicio, 90)
		if err != nil || len(wav) < 1000 {
			continue
		}
		t, err := a.transcrever(ctx, wav, inicio)
		if err != nil {
			return nada, false
		}
		ouvidas = append(ouvidas, t.Falas...)
	}
	pares, _ := casarFalas(ouvidas, falasComTexto(srt))
	return sincroniaPelasFalas(pares)
}
