package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Meus vídeos (S42): vídeos próprios (viagens, trabalho) guardados no servidor, em pastas. Chegam
// pelo link de uma mensagem do Telegram: o servidor baixa com o tdl, logado na conta do Lucas, e
// guarda com o nome e a pasta escolhidos. A pasta no disco é a fonte da verdade: o que for posto
// nela à mão também aparece.

const esquemaDosMeusVideos = `
CREATE TABLE IF NOT EXISTS meus_videos (
	id        INTEGER PRIMARY KEY,
	pasta     TEXT NOT NULL,
	nome      TEXT NOT NULL,
	arquivo   TEXT NOT NULL UNIQUE, -- caminho dentro da pasta dos meus vídeos
	tamanho   INTEGER NOT NULL DEFAULT 0,
	duracao   REAL NOT NULL DEFAULT 0,
	origem    TEXT NOT NULL DEFAULT '', -- o link da mensagem do Telegram, quando veio de lá
	criado_em TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS pedidos_do_telegram (
	id        INTEGER PRIMARY KEY,
	link      TEXT NOT NULL,
	nome      TEXT NOT NULL,
	pasta     TEXT NOT NULL,
	tudo      INTEGER NOT NULL DEFAULT 0, -- todos os vídeos do grupo, não só a mensagem
	estado    TEXT NOT NULL DEFAULT 'na fila', -- na fila, baixando, pronto, erro
	mensagem  TEXT NOT NULL DEFAULT '',
	baixado   INTEGER NOT NULL DEFAULT 0,
	videos    INTEGER NOT NULL DEFAULT 0,
	criado_em TEXT NOT NULL
);
`

type meuVideo struct {
	ID       int64   `json:"id"`
	Pasta    string  `json:"pasta"`
	Nome     string  `json:"nome"`
	Tamanho  int64   `json:"tamanho"`
	Duracao  float64 `json:"duracao"`
	Origem   string  `json:"origem,omitempty"`
	CriadoEm string  `json:"criado_em"`
	Capa     string  `json:"capa,omitempty"`
	Posicao  float64 `json:"posicao,omitempty"`
	arquivo  string
}

type pedidoDoTelegram struct {
	ID       int64  `json:"id"`
	Link     string `json:"link"`
	Nome     string `json:"nome"`
	Pasta    string `json:"pasta"`
	Tudo     bool   `json:"tudo"`
	Estado   string `json:"estado"`
	Mensagem string `json:"mensagem,omitempty"`
	Baixado  int64  `json:"baixado"`
	Videos   int    `json:"videos"`
	CriadoEm string `json:"criado_em"`
}

func programaTdl() string { return env("TDL_BIN", "tdl") }

func tdlDisponivel() bool {
	_, err := exec.LookPath(programaTdl())
	return err == nil
}

// ehVideo: as extensões dos torrents e o 3gp, de celular antigo.
func ehVideo(nome string) bool {
	ext := strings.ToLower(filepath.Ext(nome))
	return extensoesDeVideo[ext] > 0 || ext == ".3gp"
}

func (a *app) pastaDosMeusVideos() string {
	return filepath.Join(filepath.Dir(a.cfg.pastaDosTorrents), "meus-videos")
}

// A sessão do Telegram (login do tdl) fica junto dos dados, fora da imagem.
func (a *app) sessaoDoTelegram() string {
	return filepath.Join(filepath.Dir(a.cfg.pastaDosTorrents), "telegram")
}

func (a *app) capaDoMeuVideo(id int64) string {
	return filepath.Join(a.pastaDosMeusVideos(), ".capas", fmt.Sprintf("%d.jpg", id))
}

// limparNome tira do nome o que não pode ir num nome de arquivo (barras, controles) e os espaços
// sobrando. Pasta e vídeo usam a mesma regra: um nível de pasta só.
var reForaDoNome = regexp.MustCompile(`[/\\:*?"<>|\x00-\x1f]+`)

func limparNome(s string) string {
	s = reForaDoNome.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimLeft(s, ".")
	if r := []rune(s); len(r) > 120 {
		s = strings.TrimSpace(string(r[:120]))
	}
	return s
}

// Links aceitos: https://t.me/c/123/45 (grupo privado), https://t.me/nome/45 (público) e os de
// tópico (https://t.me/c/123/7/45). Com "tudo", basta o grupo.
var reLinkDoTelegram = regexp.MustCompile(`^(?:https?://)?(?:t|telegram)\.me/(?:c/(\d+)|([A-Za-z][A-Za-z0-9_]{3,}))((?:/\d+){0,2})/?(?:\?.*)?$`)

type linkDoTelegram struct {
	chat     string // id do grupo privado ou nome do público
	mensagem int64  // 0 quando o link é só do grupo
	limpo    string // o link sem o "?single" e afins, como o tdl espera
}

func lerLinkDoTelegram(s string) (linkDoTelegram, bool) {
	m := reLinkDoTelegram.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return linkDoTelegram{}, false
	}
	l := linkDoTelegram{chat: m[1]}
	base := "https://t.me/c/" + m[1]
	if m[1] == "" {
		l.chat, base = m[2], "https://t.me/"+m[2]
	}
	partes := strings.Split(strings.Trim(m[3], "/"), "/")
	if partes[0] != "" {
		l.mensagem, _ = strconv.ParseInt(partes[len(partes)-1], 10, 64)
	}
	l.limpo = base + m[3]
	return l, true
}

// ---------- A biblioteca ----------

// sincronizarMeusVideos acerta o banco com a pasta: arquivo novo entra, arquivo que sumiu sai.
func (a *app) sincronizarMeusVideos(ctx context.Context) error {
	raiz := a.pastaDosMeusVideos()
	noDisco := map[string]os.FileInfo{}
	pastas, _ := os.ReadDir(raiz)
	for _, p := range pastas {
		if !p.IsDir() || strings.HasPrefix(p.Name(), ".") {
			continue
		}
		arquivos, _ := os.ReadDir(filepath.Join(raiz, p.Name()))
		for _, f := range arquivos {
			if f.IsDir() || strings.HasPrefix(f.Name(), ".") || !ehVideo(f.Name()) {
				continue
			}
			if info, err := f.Info(); err == nil {
				noDisco[p.Name()+"/"+f.Name()] = info
			}
		}
	}
	rows, err := a.db.QueryContext(ctx, `SELECT id, arquivo FROM meus_videos`)
	if err != nil {
		return err
	}
	noBanco := map[string]int64{}
	for rows.Next() {
		var id int64
		var arq string
		if rows.Scan(&id, &arq) == nil {
			noBanco[arq] = id
		}
	}
	rows.Close()
	for arq, id := range noBanco {
		if _, ok := noDisco[arq]; !ok {
			a.db.ExecContext(ctx, `DELETE FROM meus_videos WHERE id = ?`, id)
			os.Remove(a.capaDoMeuVideo(id))
		}
	}
	novos := false
	for arq, info := range noDisco {
		if _, ok := noBanco[arq]; ok {
			continue
		}
		pasta, nome, _ := strings.Cut(arq, "/")
		nome = strings.TrimSuffix(nome, filepath.Ext(nome))
		if _, err := a.db.ExecContext(ctx, `INSERT INTO meus_videos (pasta, nome, arquivo, tamanho, criado_em) VALUES (?, ?, ?, ?, ?)`,
			pasta, nome, arq, info.Size(), info.ModTime().UTC().Truncate(time.Second).Format(time.RFC3339)); err != nil {
			return err
		}
		novos = true
	}
	if novos {
		a.emFundo(func() { a.completarMeusVideos(context.Background()) })
	}
	return nil
}

var completandoMeusVideos sync.Mutex

// completarMeusVideos lê a duração e tira a capa dos vídeos que ainda não têm (ffmpeg).
func (a *app) completarMeusVideos(ctx context.Context) {
	if !completandoMeusVideos.TryLock() {
		return
	}
	defer completandoMeusVideos.Unlock()
	lista, err := a.listarMeusVideos(ctx)
	if err != nil {
		return
	}
	for _, v := range lista {
		caminho := filepath.Join(a.pastaDosMeusVideos(), v.arquivo)
		if v.Duracao == 0 {
			if d := duracaoDoArquivo(ctx, caminho); d > 0 {
				a.db.ExecContext(ctx, `UPDATE meus_videos SET duracao = ? WHERE id = ?`, d, v.ID)
			}
		}
		if _, err := os.Stat(a.capaDoMeuVideo(v.ID)); err != nil {
			tirarCapa(ctx, caminho, a.capaDoMeuVideo(v.ID), v.Duracao)
		}
	}
}

func duracaoDoArquivo(ctx context.Context, caminho string) float64 {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(c, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", caminho).Output()
	if err != nil {
		return 0
	}
	d, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return d
}

func tirarCapa(ctx context.Context, video, capa string, duracao float64) {
	c, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	os.MkdirAll(filepath.Dir(capa), 0o755)
	ponto := "1"
	if duracao > 6 {
		ponto = "3"
	}
	if err := exec.CommandContext(c, "ffmpeg", "-y", "-v", "error", "-ss", ponto, "-i", video, "-frames:v", "1", "-vf", "scale=480:-2", capa).Run(); err != nil {
		os.Remove(capa)
	}
}

func (a *app) listarMeusVideos(ctx context.Context) ([]meuVideo, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT v.id, v.pasta, v.nome, v.arquivo, v.tamanho, v.duracao, v.origem, v.criado_em, COALESCE(p.posicao, 0)
		FROM meus_videos v LEFT JOIN progresso p ON p.chave = 'proprio:' || v.id
		ORDER BY v.pasta COLLATE NOCASE, v.criado_em, v.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lista := []meuVideo{}
	for rows.Next() {
		var v meuVideo
		if err := rows.Scan(&v.ID, &v.Pasta, &v.Nome, &v.arquivo, &v.Tamanho, &v.Duracao, &v.Origem, &v.CriadoEm, &v.Posicao); err != nil {
			return nil, err
		}
		if _, err := os.Stat(a.capaDoMeuVideo(v.ID)); err == nil {
			v.Capa = fmt.Sprintf("/api/meus-videos/%d/capa", v.ID)
		}
		lista = append(lista, v)
	}
	return lista, rows.Err()
}

func (a *app) meuVideo(ctx context.Context, id int64) (meuVideo, error) {
	var v meuVideo
	err := a.db.QueryRowContext(ctx, `SELECT id, pasta, nome, arquivo, tamanho, duracao, origem, criado_em FROM meus_videos WHERE id = ?`, id).
		Scan(&v.ID, &v.Pasta, &v.Nome, &v.arquivo, &v.Tamanho, &v.Duracao, &v.Origem, &v.CriadoEm)
	if err == nil {
		if _, e := os.Stat(a.capaDoMeuVideo(v.ID)); e == nil {
			v.Capa = fmt.Sprintf("/api/meus-videos/%d/capa", v.ID)
		}
	}
	return v, err
}

// guardarNaPasta move o arquivo baixado para pasta/nome.ext (com " 2", " 3" se o nome já existe)
// e o registra.
func (a *app) guardarNaPasta(ctx context.Context, origemDoArquivo, pasta, nome, link string) (meuVideo, error) {
	ext := strings.ToLower(filepath.Ext(origemDoArquivo))
	dir := filepath.Join(a.pastaDosMeusVideos(), pasta)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return meuVideo{}, err
	}
	final := nome
	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(dir, final+ext)); errors.Is(err, os.ErrNotExist) {
			break
		}
		final = fmt.Sprintf("%s %d", nome, i)
	}
	destino := filepath.Join(dir, final+ext)
	if err := moverArquivo(origemDoArquivo, destino); err != nil {
		return meuVideo{}, err
	}
	info, err := os.Stat(destino)
	if err != nil {
		return meuVideo{}, err
	}
	arq := pasta + "/" + final + ext
	res, err := a.db.ExecContext(ctx, `INSERT INTO meus_videos (pasta, nome, arquivo, tamanho, origem, criado_em) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (arquivo) DO UPDATE SET tamanho = excluded.tamanho, origem = excluded.origem`,
		pasta, final, arq, info.Size(), link, a.carimbo())
	if err != nil {
		return meuVideo{}, err
	}
	id, _ := res.LastInsertId()
	return meuVideo{ID: id, Pasta: pasta, Nome: final, Tamanho: info.Size(), arquivo: arq}, nil
}

func moverArquivo(de, para string) error {
	if err := os.Rename(de, para); err == nil {
		return nil
	}
	// Discos diferentes: copia e apaga.
	entrada, err := os.Open(de)
	if err != nil {
		return err
	}
	defer entrada.Close()
	saida, err := os.Create(para)
	if err != nil {
		return err
	}
	if _, err := saida.ReadFrom(entrada); err != nil {
		saida.Close()
		os.Remove(para)
		return err
	}
	if err := saida.Close(); err != nil {
		return err
	}
	return os.Remove(de)
}

// ---------- A fila do Telegram ----------

// O tdl guarda a sessão num banco que só um processo abre por vez: um pedido por vez.
func (a *app) trabalharNoTelegram(ctx context.Context) {
	a.db.ExecContext(ctx, `UPDATE pedidos_do_telegram SET estado = 'na fila' WHERE estado = 'baixando'`)
	for {
		for ctx.Err() == nil {
			var id int64
			if a.db.QueryRowContext(ctx, `SELECT id FROM pedidos_do_telegram WHERE estado = 'na fila' ORDER BY id LIMIT 1`).Scan(&id) != nil {
				break
			}
			a.baixarDoTelegram(ctx, id)
		}
		select {
		case <-ctx.Done():
			return
		case <-a.telegramAcordar:
		}
	}
}

func (a *app) acordarTelegram() {
	select {
	case a.telegramAcordar <- struct{}{}:
	default:
	}
}

func (a *app) pedidoDe(ctx context.Context, id int64) (pedidoDoTelegram, error) {
	var p pedidoDoTelegram
	err := a.db.QueryRowContext(ctx, `SELECT id, link, nome, pasta, tudo, estado, mensagem, baixado, videos, criado_em FROM pedidos_do_telegram WHERE id = ?`, id).
		Scan(&p.ID, &p.Link, &p.Nome, &p.Pasta, &p.Tudo, &p.Estado, &p.Mensagem, &p.Baixado, &p.Videos, &p.CriadoEm)
	return p, err
}

func (a *app) pedidosDoTelegram(ctx context.Context) ([]pedidoDoTelegram, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id, link, nome, pasta, tudo, estado, mensagem, baixado, videos, criado_em
		FROM pedidos_do_telegram ORDER BY id DESC LIMIT 30`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lista := []pedidoDoTelegram{}
	for rows.Next() {
		var p pedidoDoTelegram
		if err := rows.Scan(&p.ID, &p.Link, &p.Nome, &p.Pasta, &p.Tudo, &p.Estado, &p.Mensagem, &p.Baixado, &p.Videos, &p.CriadoEm); err != nil {
			return nil, err
		}
		lista = append(lista, p)
	}
	return lista, rows.Err()
}

// argumentosDoTdl: a sessão fica na pasta de dados; sem barra de progresso (não há terminal).
func (a *app) argumentosDoTdl(resto ...string) []string {
	return append([]string{"--storage", "type=bolt,path=" + a.sessaoDoTelegram(), "--disable-progress-ps"}, resto...)
}

func (a *app) rodarTdl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, programaTdl(), a.argumentosDoTdl(args...)...)
	var saida bytes.Buffer
	cmd.Stdout, cmd.Stderr = &saida, &saida
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New(explicarErroDoTdl(saida.String(), err))
	}
	return nil
}

// explicarErroDoTdl traduz os erros que o Lucas pode resolver; o resto vai como veio (o fim).
func explicarErroDoTdl(saida string, err error) string {
	baixo := strings.ToLower(saida)
	switch {
	case strings.Contains(baixo, "not authorized") || strings.Contains(baixo, "auth_key_unregistered") || strings.Contains(baixo, "session_revoked"):
		return "O Telegram não está conectado no servidor: falta fazer o login (veja Meus vídeos em docs/DECISOES.md, S42)."
	case strings.Contains(baixo, "channel_private") || strings.Contains(baixo, "chat_id_invalid") || strings.Contains(baixo, "peer_id_invalid") || strings.Contains(baixo, "channel_invalid"):
		return "A conta não tem acesso a esse grupo (ou o link está errado)."
	case strings.Contains(baixo, "message not found") || strings.Contains(baixo, "msg_id_invalid"):
		return "Mensagem não encontrada: ela pode ter sido apagada."
	case strings.Contains(baixo, "timeout") && strings.Contains(baixo, "database"):
		return "A sessão do Telegram está aberta em outro lugar (outro tdl rodando no servidor)."
	}
	linhas := strings.Split(strings.TrimSpace(saida), "\n")
	if n := len(linhas); n > 0 && linhas[n-1] != "" {
		ultima := linhas[n-1]
		for i := n - 1; i >= 0 && i >= n-4; i-- {
			if strings.Contains(strings.ToLower(linhas[i]), "error") {
				ultima = linhas[i]
				break
			}
		}
		if len(ultima) > 300 {
			ultima = ultima[:300]
		}
		return "O tdl falhou: " + strings.TrimSpace(ultima)
	}
	return "O tdl falhou: " + err.Error()
}

var (
	pedidosMu       sync.Mutex
	cancelarPedidos = map[int64]context.CancelFunc{}
)

func (a *app) baixarDoTelegram(ctx context.Context, id int64) {
	p, err := a.pedidoDe(ctx, id)
	if err != nil {
		return
	}
	falhar := func(msg string) {
		a.db.ExecContext(ctx, `UPDATE pedidos_do_telegram SET estado = 'erro', mensagem = ? WHERE id = ?`, msg, id)
		a.avisos.avisar(aviso{Titulo: primeiro(p.Nome, p.Pasta), Texto: "Não baixou do Telegram: " + msg, Link: a.cfg.link + "/#/meus-videos", Urgente: true})
	}
	link, ok := lerLinkDoTelegram(p.Link)
	if !ok {
		falhar("Link do Telegram inválido.")
		return
	}
	if !tdlDisponivel() {
		falhar("O tdl não está instalado no servidor.")
		return
	}
	c, cancelar := context.WithCancel(ctx)
	defer cancelar()
	pedidosMu.Lock()
	cancelarPedidos[id] = cancelar
	pedidosMu.Unlock()
	defer func() {
		pedidosMu.Lock()
		delete(cancelarPedidos, id)
		pedidosMu.Unlock()
	}()

	tmp := filepath.Join(a.pastaDosMeusVideos(), ".baixando", strconv.FormatInt(id, 10))
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		falhar(err.Error())
		return
	}
	defer os.RemoveAll(tmp)
	a.db.ExecContext(ctx, `UPDATE pedidos_do_telegram SET estado = 'baixando', mensagem = '', baixado = 0 WHERE id = ?`, id)

	// Enquanto o tdl baixa, o tamanho da pasta é o andamento.
	parar := make(chan struct{})
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-parar:
				return
			case <-t.C:
				a.db.ExecContext(ctx, `UPDATE pedidos_do_telegram SET baixado = ? WHERE id = ?`, tamanhoDaPasta(tmp), id)
			}
		}
	}()
	modelo := "{{ .MessageID }}_{{ filenamify .FileName }}"
	if p.Tudo {
		lista := filepath.Join(tmp, ".lista.json")
		err = a.rodarTdl(c, "chat", "export", "-c", link.chat, "-o", lista)
		if err == nil {
			a.tirarOsJaBaixados(ctx, lista, link)
			err = a.rodarTdl(c, "dl", "-f", lista, "-d", tmp, "--restart", "--template", modelo)
		}
	} else {
		err = a.rodarTdl(c, "dl", "-u", link.limpo, "-d", tmp, "--group", "--restart", "--template", modelo)
	}
	close(parar)
	if err != nil {
		if _, e := a.pedidoDe(ctx, id); e != nil {
			return // cancelado e apagado
		}
		if c.Err() != nil && ctx.Err() == nil {
			falhar("Cancelado.")
			return
		}
		falhar(err.Error())
		return
	}

	baixados := videosBaixados(tmp)
	if len(baixados) == 0 {
		msg := "A mensagem não tem vídeo."
		if p.Tudo {
			msg = "Nenhum vídeo novo no grupo."
		}
		a.db.ExecContext(ctx, `UPDATE pedidos_do_telegram SET estado = 'pronto', mensagem = ?, baixado = 0 WHERE id = ?`, msg, id)
		return
	}
	var total int64
	for i, b := range baixados {
		nome := p.Nome
		switch {
		case nome == "":
			nome = primeiro(limparNome(strings.TrimSuffix(b.nome, filepath.Ext(b.nome))), fmt.Sprintf("Vídeo %d", b.mensagem))
		case len(baixados) > 1:
			nome = fmt.Sprintf("%s %02d", nome, i+1)
		}
		origem := link.limpo
		if b.mensagem > 0 {
			origem = linkDaMensagem(link, b.mensagem)
		}
		v, err := a.guardarNaPasta(ctx, b.caminho, p.Pasta, nome, origem)
		if err != nil {
			falhar("Não consegui guardar o vídeo: " + err.Error())
			return
		}
		total += v.Tamanho
	}
	a.db.ExecContext(ctx, `UPDATE pedidos_do_telegram SET estado = 'pronto', mensagem = '', baixado = ?, videos = ? WHERE id = ?`, total, len(baixados), id)
	a.emFundo(func() { a.completarMeusVideos(context.Background()) })
	texto := "Baixado em Meus vídeos › " + p.Pasta + "."
	if len(baixados) > 1 {
		texto = fmt.Sprintf("%d vídeos baixados em Meus vídeos › %s.", len(baixados), p.Pasta)
	}
	a.avisos.avisar(aviso{Titulo: primeiro(p.Nome, p.Pasta), Texto: texto, Link: a.cfg.link + "/#/meus-videos"})
}

func linkDaMensagem(l linkDoTelegram, mensagem int64) string {
	if _, err := strconv.ParseInt(l.chat, 10, 64); err == nil {
		return fmt.Sprintf("https://t.me/c/%s/%d", l.chat, mensagem)
	}
	return fmt.Sprintf("https://t.me/%s/%d", l.chat, mensagem)
}

// tirarOsJaBaixados: "todos os vídeos do grupo" de novo só baixa o que ainda não veio. A lista
// do tdl é {"id": ..., "messages": [{"id": 4, ...}]}; se o formato mudar, baixa tudo.
func (a *app) tirarOsJaBaixados(ctx context.Context, lista string, l linkDoTelegram) {
	dados, err := os.ReadFile(lista)
	if err != nil {
		return
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(dados, &doc) != nil {
		return
	}
	var mensagens []map[string]any
	if json.Unmarshal(doc["messages"], &mensagens) != nil {
		return
	}
	rows, err := a.db.QueryContext(ctx, `SELECT origem FROM meus_videos WHERE origem != ''`)
	if err != nil {
		return
	}
	ja := map[string]bool{}
	for rows.Next() {
		var o string
		if rows.Scan(&o) == nil {
			ja[o] = true
		}
	}
	rows.Close()
	ficam := mensagens[:0]
	for _, m := range mensagens {
		id, _ := m["id"].(float64)
		if id > 0 && ja[linkDaMensagem(l, int64(id))] {
			continue
		}
		ficam = append(ficam, m)
	}
	b, err := json.Marshal(ficam)
	if err != nil {
		return
	}
	doc["messages"] = b
	if novo, err := json.Marshal(doc); err == nil {
		os.WriteFile(lista, novo, 0o644)
	}
}

type baixadoDoTelegram struct {
	caminho  string
	nome     string // o nome original, sem o número da mensagem
	mensagem int64
}

var reArquivoDoTdl = regexp.MustCompile(`^(\d+)_(.*)$`)

// videosBaixados: os vídeos da pasta, na ordem das mensagens. Fotos e outros arquivos ficam de fora.
func videosBaixados(dir string) []baixadoDoTelegram {
	entradas, _ := os.ReadDir(dir)
	lista := []baixadoDoTelegram{}
	for _, e := range entradas {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !ehVideo(e.Name()) || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		b := baixadoDoTelegram{caminho: filepath.Join(dir, e.Name()), nome: e.Name()}
		if m := reArquivoDoTdl.FindStringSubmatch(e.Name()); m != nil {
			b.mensagem, _ = strconv.ParseInt(m[1], 10, 64)
			b.nome = m[2]
		}
		lista = append(lista, b)
	}
	sort.Slice(lista, func(i, j int) bool {
		if lista[i].mensagem != lista[j].mensagem {
			return lista[i].mensagem < lista[j].mensagem
		}
		return lista[i].nome < lista[j].nome
	})
	return lista
}

func tamanhoDaPasta(dir string) int64 {
	var total int64
	filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, e := d.Info(); e == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// ---------- Rotas ----------

func (a *app) handleMeusVideos(w http.ResponseWriter, r *http.Request) {
	if err := a.sincronizarMeusVideos(r.Context()); err != nil {
		falha(w, err)
		return
	}
	videos, err := a.listarMeusVideos(r.Context())
	if err != nil {
		falha(w, err)
		return
	}
	pedidos, err := a.pedidosDoTelegram(r.Context())
	if err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"videos":   videos,
		"pedidos":  pedidos,
		"telegram": map[string]bool{"instalado": tdlDisponivel()},
	})
}

func (a *app) handlePedirAoTelegram(w http.ResponseWriter, r *http.Request) {
	var corpo struct {
		Link  string `json:"link"`
		Nome  string `json:"nome"`
		Pasta string `json:"pasta"`
		Tudo  bool   `json:"tudo"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	l, ok := lerLinkDoTelegram(corpo.Link)
	if !ok {
		erroJSON(w, http.StatusBadRequest, "Isso não parece um link de mensagem do Telegram (https://t.me/…).")
		return
	}
	if !corpo.Tudo && l.mensagem == 0 {
		erroJSON(w, http.StatusBadRequest, "O link é do grupo, não de uma mensagem: copie o link da mensagem ou marque \"todos os vídeos do grupo\".")
		return
	}
	pasta := limparNome(corpo.Pasta)
	if pasta == "" {
		erroJSON(w, http.StatusBadRequest, "Escolha uma pasta.")
		return
	}
	nome := limparNome(corpo.Nome)
	if nome == "" && !corpo.Tudo {
		erroJSON(w, http.StatusBadRequest, "Dê um nome ao vídeo.")
		return
	}
	res, err := a.db.ExecContext(r.Context(), `INSERT INTO pedidos_do_telegram (link, nome, pasta, tudo, criado_em) VALUES (?, ?, ?, ?, ?)`,
		l.limpo, nome, pasta, corpo.Tudo, a.carimbo())
	if err != nil {
		falha(w, err)
		return
	}
	id, _ := res.LastInsertId()
	a.acordarTelegram()
	p, _ := a.pedidoDe(r.Context(), id)
	writeJSON(w, http.StatusAccepted, p)
}

// handleApagarPedido: cancela (se está baixando) e tira da lista.
func (a *app) handleApagarPedido(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		erroJSON(w, http.StatusBadRequest, "Pedido inválido.")
		return
	}
	a.db.ExecContext(r.Context(), `DELETE FROM pedidos_do_telegram WHERE id = ?`, id)
	pedidosMu.Lock()
	if cancelar := cancelarPedidos[id]; cancelar != nil {
		cancelar()
	}
	pedidosMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// handleRetomarPedido: tenta de novo um pedido que deu erro.
func (a *app) handleRetomarPedido(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		erroJSON(w, http.StatusBadRequest, "Pedido inválido.")
		return
	}
	a.db.ExecContext(r.Context(), `UPDATE pedidos_do_telegram SET estado = 'na fila', mensagem = '' WHERE id = ? AND estado = 'erro'`, id)
	a.acordarTelegram()
	p, err := a.pedidoDe(r.Context(), id)
	if err != nil {
		erroJSON(w, http.StatusNotFound, "Pedido não encontrado.")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *app) meuVideoDoCaminho(w http.ResponseWriter, r *http.Request) (meuVideo, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		v, err := a.meuVideo(r.Context(), id)
		if err == nil {
			return v, true
		}
	}
	erroJSON(w, http.StatusNotFound, "Vídeo não encontrado.")
	return meuVideo{}, false
}

// handleMudarMeuVideo renomeia e/ou muda de pasta (o arquivo também).
func (a *app) handleMudarMeuVideo(w http.ResponseWriter, r *http.Request) {
	v, ok := a.meuVideoDoCaminho(w, r)
	if !ok {
		return
	}
	var corpo struct {
		Nome  *string `json:"nome"`
		Pasta *string `json:"pasta"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	nome, pasta := v.Nome, v.Pasta
	if corpo.Nome != nil {
		nome = limparNome(*corpo.Nome)
	}
	if corpo.Pasta != nil {
		pasta = limparNome(*corpo.Pasta)
	}
	if nome == "" || pasta == "" {
		erroJSON(w, http.StatusBadRequest, "Nome e pasta não podem ficar vazios.")
		return
	}
	ext := filepath.Ext(v.arquivo)
	novo := pasta + "/" + nome + ext
	if novo != v.arquivo {
		destino := filepath.Join(a.pastaDosMeusVideos(), novo)
		if _, err := os.Stat(destino); err == nil && !strings.EqualFold(novo, v.arquivo) {
			erroJSON(w, http.StatusConflict, "Já há um vídeo com esse nome nessa pasta.")
			return
		}
		os.MkdirAll(filepath.Dir(destino), 0o755)
		if err := moverArquivo(filepath.Join(a.pastaDosMeusVideos(), v.arquivo), destino); err != nil {
			falha(w, err)
			return
		}
		os.Remove(filepath.Dir(filepath.Join(a.pastaDosMeusVideos(), v.arquivo))) // a pasta antiga, se ficou vazia
	}
	if _, err := a.db.ExecContext(r.Context(), `UPDATE meus_videos SET nome = ?, pasta = ?, arquivo = ? WHERE id = ?`, nome, pasta, novo, v.ID); err != nil {
		falha(w, err)
		return
	}
	a.db.ExecContext(r.Context(), `UPDATE progresso SET titulo = ?, subtitulo = ? WHERE chave = ?`, nome, pasta, fmt.Sprintf("proprio:%d", v.ID))
	v, _ = a.meuVideo(r.Context(), v.ID)
	writeJSON(w, http.StatusOK, v)
}

func (a *app) handleApagarMeuVideo(w http.ResponseWriter, r *http.Request) {
	v, ok := a.meuVideoDoCaminho(w, r)
	if !ok {
		return
	}
	caminho := filepath.Join(a.pastaDosMeusVideos(), v.arquivo)
	if err := os.Remove(caminho); err != nil && !errors.Is(err, os.ErrNotExist) {
		falha(w, err)
		return
	}
	os.Remove(filepath.Dir(caminho))
	os.Remove(a.capaDoMeuVideo(v.ID))
	a.db.ExecContext(r.Context(), `DELETE FROM meus_videos WHERE id = ?`, v.ID)
	a.db.ExecContext(r.Context(), `DELETE FROM progresso WHERE chave = ?`, fmt.Sprintf("proprio:%d", v.ID))
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) handleCapaDoMeuVideo(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "max-age=86400")
	http.ServeFile(w, r, a.capaDoMeuVideo(id))
}

// servirMeuVideo entrega o arquivo, para o player do app e para as telas.
func (a *app) servirMeuVideo(w http.ResponseWriter, r *http.Request, id int64, paraTela bool) {
	v, err := a.meuVideo(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	caminho := filepath.Join(a.pastaDosMeusVideos(), v.arquivo)
	w.Header().Set("Content-Type", mimeDe(strings.TrimPrefix(filepath.Ext(caminho), ".")))
	w.Header().Set("Cache-Control", "no-store")
	if paraTela {
		cabecalhosDLNA(w, fonte{})
	}
	http.ServeFile(w, r, caminho)
}

func (a *app) fonteDoMeuVideo(ctx context.Context, id int64) (fonte, error) {
	v, err := a.meuVideo(ctx, id)
	if err != nil {
		return fonte{}, err
	}
	ext := strings.TrimPrefix(filepath.Ext(v.arquivo), ".")
	// O endereço é só para a extensão: quem entrega é servirMeuVideo.
	return fonte{fmt.Sprintf("proprio:%d", id), v.Nome, fmt.Sprintf("proprio://%d.%s", id, ext), mimeDe(ext), false}, nil
}
