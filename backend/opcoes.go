package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Opções de assistir (S28): na página do título, a assinatura e as versões de torrent são opções do
// mesmo jeito. "Assistir" numa versão de torrent prepara o torrent por trás e devolve o vídeo certo
// (o episódio, num pacote); a folha de assistir mostra os formatos (o idioma do áudio, sobretudo),
// lidos na hora em que se abre a opção.

const esquemaDasOpcoes = `
-- O título do TMDB de cada item da assinatura: o filme traz o código nos detalhes; a série, pelo nome.
CREATE TABLE IF NOT EXISTS assinatura_tmdb (
	tipo TEXT NOT NULL,
	id   INTEGER NOT NULL,
	tmdb INTEGER NOT NULL,
	PRIMARY KEY (tipo, id)
);
CREATE INDEX IF NOT EXISTS assinatura_tmdb_titulo ON assinatura_tmdb (tipo, tmdb);
-- Formatos dos vídeos da assinatura (ffprobe), por chave: filme:1, episodio:2.
CREATE TABLE IF NOT EXISTS formatos_da_assinatura (
	chave  TEXT PRIMARY KEY,
	dados  TEXT NOT NULL,
	quando TEXT NOT NULL
);
`

// ---------- Assistir uma versão de torrent ----------

func (a *app) handlePrepararOpcao(w http.ResponseWriter, r *http.Request) {
	var p pedidoDeVerificacao
	if !lerCorpo(w, r, &p) {
		return
	}
	if p.Magnet == "" && p.Link == "" {
		erroJSON(w, http.StatusBadRequest, "Falta o link do torrent.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()
	hash, err := a.fichaDoPedido(ctx, p)
	if err != nil {
		erroJSON(w, http.StatusBadGateway, "Não consegui abrir o torrent: "+err.Error())
		return
	}
	// Quem assiste ou baixa uma versão a traz para a lista (um candidato só verificado fica oculto).
	a.db.ExecContext(r.Context(), `UPDATE torrents SET oculto = 0 WHERE hash = ?`, hash)
	if reTMDBGuardado.MatchString(p.TMDB) {
		a.db.ExecContext(r.Context(), `UPDATE torrents SET tmdb = ? WHERE hash = ?`, p.TMDB, hash)
	}
	f, err := a.videoPrincipal(r.Context(), hash, p.Temporada, p.Episodio)
	if err != nil {
		erroJSON(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hash": hash, "arquivo": f})
}

// ---------- Formatos de um vídeo ----------

var reChaveDeVideo = regexp.MustCompile(`^(arquivo|filme|episodio):(\d+)$`)

func (a *app) handleFormatos(w http.ResponseWriter, r *http.Request) {
	m := reChaveDeVideo.FindStringSubmatch(r.URL.Query().Get("chave"))
	if m == nil {
		erroJSON(w, http.StatusBadRequest, "Vídeo inválido.")
		return
	}
	id, _ := strconv.ParseInt(m[2], 10, 64)
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	s, err := a.formatosDe(ctx, m[1], id)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"erro": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"formatos": s})
}

func (a *app) formatosDe(ctx context.Context, tipo string, id int64) (*sondagem, error) {
	if tipo == "arquivo" {
		f, _, err := a.arquivo(ctx, id)
		if err != nil {
			return nil, errors.New("Vídeo não encontrado.")
		}
		s := f.Formatos
		if s == nil {
			lido, err := a.sondar(ctx, f)
			if err != nil {
				return nil, err
			}
			s = &lido
		}
		if base, err := a.enderecoInterno(); err == nil && a.ouvirIdiomas(ctx, fmt.Sprintf("%s/arquivo/%d", base, f.ID), s) {
			b, _ := json.Marshal(s)
			a.db.ExecContext(ctx, `UPDATE torrent_arquivos SET sondagem = ? WHERE id = ?`, string(b), f.ID)
		}
		return s, nil
	}
	chave := fmt.Sprintf("%s:%d", tipo, id)
	var dados string
	if a.db.QueryRowContext(ctx, `SELECT dados FROM formatos_da_assinatura WHERE chave = ?`, chave).Scan(&dados) == nil {
		var s sondagem
		if json.Unmarshal([]byte(dados), &s) == nil {
			return &s, nil
		}
	}
	endereco, err := a.enderecoDaAssinatura(ctx, tipo, id)
	if err != nil {
		return nil, err
	}
	// A assinatura aceita 1 conexão: ler os formatos só quando ninguém está assistindo outra coisa.
	c, sair, err := a.conexao.entrarPorTras(ctx, chave)
	if err != nil {
		return nil, errors.New("A assinatura está em uso agora; os detalhes aparecem quando ela ficar livre.")
	}
	defer sair()
	s, err := lerFormatos(c, endereco, "-user_agent", agenteDoPlayer)
	if err != nil {
		if c.Err() != nil && ctx.Err() == nil {
			return nil, errors.New("A assinatura começou a ser usada; os detalhes ficam para depois.")
		}
		return nil, semSegredos(err)
	}
	a.ouvirIdiomas(c, endereco, &s, "-user_agent", agenteDoPlayer)
	b, _ := json.Marshal(s)
	a.db.ExecContext(ctx, `INSERT OR REPLACE INTO formatos_da_assinatura (chave, dados, quando) VALUES (?, ?, ?)`,
		chave, string(b), a.agora().UTC().Format(time.RFC3339))
	return &s, nil
}

// semIdioma: códigos que não dizem o idioma (muitos arquivos da assinatura vêm assim).
var semIdioma = map[string]bool{"": true, "und": true, "unk": true, "mis": true, "zxx": true, "?": true}

// ouvirIdiomas: para as faixas de áudio (até 3) que o arquivo não diz o idioma, o Whisper ouve 30 s
// e reconhece. Devolve true se descobriu algum. Sem Whisper, nada muda.
func (a *app) ouvirIdiomas(ctx context.Context, endereco string, s *sondagem, extras ...string) bool {
	if urlDoWhisper() == "" {
		return false
	}
	achou := false
	for i := range s.Audios {
		if i >= 3 || !semIdioma[strings.ToLower(s.Audios[i].Idioma)] {
			continue
		}
		inicio := s.Duracao * 0.3 // longe da abertura, que costuma ser só música
		wav, err := audioDaFaixa(ctx, endereco, i, inicio, 30, extras...)
		if err != nil || len(wav) < 1000 {
			continue
		}
		t, err := a.transcrever(ctx, wav, inicio)
		if err != nil || t.Idioma == "" || len(t.Falas) == 0 {
			continue
		}
		s.Audios[i].Idioma, s.Audios[i].Ouvido = t.Idioma, true
		achou = true
	}
	return achou
}

// enderecoDaAssinatura: o vídeo no provedor (com a senha: nunca vai ao navegador nem ao registro).
func (a *app) enderecoDaAssinatura(ctx context.Context, tipo string, id int64) (string, error) {
	f, err := a.fonteDe(ctx, tipo, id, "")
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("Vídeo não encontrado.")
	}
	if err != nil {
		return "", semSegredos(err)
	}
	return f.url, nil
}

// ---------- A assinatura e o TMDB ----------

func (a *app) guardarTMDBDaAssinatura(ctx context.Context, tipo string, id int64, tmdb int) {
	if tmdb > 0 {
		a.db.ExecContext(ctx, `INSERT OR REPLACE INTO assinatura_tmdb (tipo, id, tmdb) VALUES (?, ?, ?)`, tipo, id, tmdb)
	}
}

func (a *app) tmdbGuardado(ctx context.Context, tipo string, id int64) int {
	var tmdb int
	a.db.QueryRowContext(ctx, `SELECT tmdb FROM assinatura_tmdb WHERE tipo = ? AND id = ?`, tipo, id).Scan(&tmdb)
	return tmdb
}

var reMarcasDoNome = regexp.MustCompile(`\[[^\]]*\]|\([^)]*\)|\b(19|20)\d{2}\b`)

// tmdbDaSerie acha a série no TMDB pelo nome do catálogo (os detalhes da série não trazem o código).
func (a *app) tmdbDaSerie(ctx context.Context, it item) int {
	if tmdb := a.tmdbGuardado(ctx, "serie", it.ID); tmdb > 0 {
		return tmdb
	}
	chave := a.chaveDoTMDB()
	nome := strings.TrimSpace(reMarcasDoNome.ReplaceAllString(it.Nome, " "))
	if chave == "" || nome == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	titulos, err := a.buscarNoTMDB(ctx, chave, nome)
	if err != nil {
		return 0
	}
	for _, t := range titulos {
		if t.Tipo == "serie" && (casaComOTitulo(t, it) || casaComSubtitulo(t, it)) {
			a.guardarTMDBDaAssinatura(ctx, "serie", it.ID, t.ID)
			return t.ID
		}
	}
	return 0
}

// tmdbDosItens: os códigos já conhecidos de uma lista do catálogo (para a busca do Início).
func (a *app) tmdbDosItens(ctx context.Context, itens []item) {
	if len(itens) == 0 {
		return
	}
	marcas := make([]string, len(itens))
	args := []any{itens[0].Tipo}
	for i, it := range itens {
		marcas[i] = "?"
		args = append(args, it.ID)
	}
	rows, err := a.db.QueryContext(ctx, `SELECT id, tmdb FROM assinatura_tmdb WHERE tipo = ? AND id IN (`+strings.Join(marcas, ",")+`)`, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	conhecidos := map[int64]int{}
	for rows.Next() {
		var id int64
		var tmdb int
		if rows.Scan(&id, &tmdb) == nil {
			conhecidos[id] = tmdb
		}
	}
	for i := range itens {
		itens[i].tmdb = conhecidos[itens[i].ID]
	}
}
