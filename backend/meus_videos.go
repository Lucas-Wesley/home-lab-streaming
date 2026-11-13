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

	"golang.org/x/text/unicode/norm"
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
	criado_em TEXT NOT NULL, -- a data da mensagem, quando veio do Telegram
	ordem     INTEGER NOT NULL DEFAULT 0 -- o número da mensagem: desempata a ordem
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
	videos    INTEGER NOT NULL DEFAULT 0, -- já guardados
	total     INTEGER NOT NULL DEFAULT 0, -- quantos o grupo tem para baixar
	criado_em TEXT NOT NULL,
	mensagens TEXT NOT NULL DEFAULT '', -- no grupo, as mensagens escolhidas ("4,5,40"); vazio: todas
	previsto  INTEGER NOT NULL DEFAULT 0 -- bytes que o Telegram diz que vêm
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
	ID         int64  `json:"id"`
	Link       string `json:"link"`
	Nome       string `json:"nome"`
	Pasta      string `json:"pasta"`
	Tudo       bool   `json:"tudo"`
	Estado     string `json:"estado"`
	Mensagem   string `json:"mensagem,omitempty"`
	Baixado    int64  `json:"baixado"`
	Videos     int    `json:"videos"`
	Total      int    `json:"total"`
	Previsto   int64  `json:"previsto"`
	CriadoEm   string `json:"criado_em"`
	escolhidas map[int64]bool
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
				// O macOS devolve os acentos decompostos (NFD); o banco guarda como foram escritos (NFC).
				noDisco[norm.NFC.String(p.Name()+"/"+f.Name())] = info
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
			noBanco[norm.NFC.String(arq)] = id
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
		ORDER BY v.pasta COLLATE NOCASE, v.criado_em, v.ordem, v.id`)
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
// e o registra. criadoEm vazio: agora.
func (a *app) guardarNaPasta(ctx context.Context, origemDoArquivo, pasta, nome, link, criadoEm string, ordem int64) (meuVideo, error) {
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
	res, err := a.db.ExecContext(ctx, `INSERT INTO meus_videos (pasta, nome, arquivo, tamanho, origem, criado_em, ordem) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (arquivo) DO UPDATE SET tamanho = excluded.tamanho, origem = excluded.origem`,
		pasta, final, arq, info.Size(), link, primeiro(criadoEm, a.carimbo()), ordem)
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

// O tdl guarda a sessão num banco que só um processo abre por vez: um tdl por vez com a sessão de
// verdade (tdlMu). O rastreio, se a fila está ocupada, usa uma cópia da sessão.
var tdlMu sync.Mutex

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

const colunasDoPedido = `id, link, nome, pasta, tudo, estado, mensagem, baixado, videos, total, previsto, criado_em, mensagens`

func lerPedido(s interface{ Scan(...any) error }) (pedidoDoTelegram, error) {
	var p pedidoDoTelegram
	var mensagens string
	err := s.Scan(&p.ID, &p.Link, &p.Nome, &p.Pasta, &p.Tudo, &p.Estado, &p.Mensagem, &p.Baixado, &p.Videos, &p.Total, &p.Previsto, &p.CriadoEm, &mensagens)
	for _, m := range strings.Split(mensagens, ",") {
		if id, e := strconv.ParseInt(m, 10, 64); e == nil {
			if p.escolhidas == nil {
				p.escolhidas = map[int64]bool{}
			}
			p.escolhidas[id] = true
		}
	}
	return p, err
}

func (a *app) pedidoDe(ctx context.Context, id int64) (pedidoDoTelegram, error) {
	return lerPedido(a.db.QueryRowContext(ctx, `SELECT `+colunasDoPedido+` FROM pedidos_do_telegram WHERE id = ?`, id))
}

func (a *app) pedidosDoTelegram(ctx context.Context) ([]pedidoDoTelegram, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT `+colunasDoPedido+` FROM pedidos_do_telegram ORDER BY id DESC LIMIT 30`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lista := []pedidoDoTelegram{}
	for rows.Next() {
		p, err := lerPedido(rows)
		if err != nil {
			return nil, err
		}
		lista = append(lista, p)
	}
	return lista, rows.Err()
}

// tdl roda o programa com a sessão dada; sem barra de progresso (não há terminal). Devolve a saída
// padrão (o JSON do "chat ls"); no erro, a explicação.
func (a *app) tdl(ctx context.Context, sessao string, args ...string) (string, error) {
	todos := append([]string{"--storage", "type=bolt,path=" + sessao, "--disable-progress-ps"}, args...)
	cmd := exec.CommandContext(ctx, programaTdl(), todos...)
	var saida, erros bytes.Buffer
	cmd.Stdout, cmd.Stderr = &saida, &erros
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New(explicarErroDoTdl(saida.String()+"\n"+erros.String(), err))
	}
	return saida.String(), nil
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

// ---------- O que há no grupo ----------

// mensagemDoGrupo: o que o "chat export --with-content" do tdl diz de cada mensagem com arquivo.
type mensagemDoGrupo struct {
	id      int64
	arquivo string
	data    int64 // Unix
	texto   string
	tamanho int64   // bytes, do "raw" (a mensagem como o Telegram manda)
	duracao float64 // segundos
}

// listaDoGrupo guarda o JSON do tdl como veio ({"id": ..., "messages": [...]}) para devolvê-lo ao
// "tdl dl -f" só com as mensagens escolhidas, sem perder campo nenhum.
type listaDoGrupo struct {
	doc       map[string]json.RawMessage
	mensagens []map[string]any
}

func lerListaDoGrupo(caminho string) (listaDoGrupo, error) {
	var l listaDoGrupo
	dados, err := os.ReadFile(caminho)
	if err != nil {
		return l, err
	}
	if err := json.Unmarshal(dados, &l.doc); err != nil {
		return l, fmt.Errorf("a lista do tdl veio num formato estranho: %w", err)
	}
	if err := json.Unmarshal(l.doc["messages"], &l.mensagens); err != nil {
		return l, fmt.Errorf("a lista do tdl veio num formato estranho: %w", err)
	}
	return l, nil
}

func mensagemDe(m map[string]any) mensagemDoGrupo {
	id, _ := m["id"].(float64)
	data, _ := m["date"].(float64)
	arquivo, _ := m["file"].(string)
	texto, _ := m["text"].(string)
	v := mensagemDoGrupo{id: int64(id), arquivo: arquivo, data: int64(data), texto: texto}
	// raw.Media.Document: o tamanho e, nos atributos do vídeo, a duração.
	raw, _ := m["raw"].(map[string]any)
	media, _ := raw["Media"].(map[string]any)
	if doc, ok := media["Document"].(map[string]any); ok {
		tamanho, _ := doc["Size"].(float64)
		v.tamanho = int64(tamanho)
		atributos, _ := doc["Attributes"].([]any)
		for _, at := range atributos {
			if at, ok := at.(map[string]any); ok {
				if d, ok := at["Duration"].(float64); ok && d > 0 {
					v.duracao = d
				}
			}
		}
	}
	return v
}

// soVideos deixa na lista só os vídeos que passam no filtro, do mais antigo ao mais novo.
func (l *listaDoGrupo) soVideos(ficar func(mensagemDoGrupo) bool) []mensagemDoGrupo {
	ficam := []map[string]any{}
	videos := []mensagemDoGrupo{}
	for _, m := range l.mensagens {
		v := mensagemDe(m)
		if v.id > 0 && ehVideo(v.arquivo) && ficar(v) {
			ficam = append(ficam, m)
			videos = append(videos, v)
		}
	}
	l.mensagens = ficam
	sort.Slice(videos, func(i, j int) bool { return videos[i].id < videos[j].id })
	return videos
}

func (l listaDoGrupo) gravar(caminho string) error {
	b, err := json.Marshal(l.mensagens)
	if err != nil {
		return err
	}
	l.doc["messages"] = b
	novo, err := json.Marshal(l.doc)
	if err != nil {
		return err
	}
	return os.WriteFile(caminho, novo, 0o644)
}

// exportarGrupo: as mensagens com arquivo do grupo (todas, ou de "de" a "ate"), com o texto.
func (a *app) exportarGrupo(ctx context.Context, sessao, chat string, de, ate int64, caminho string) (listaDoGrupo, error) {
	args := []string{"chat", "export", "-c", chat, "--with-content", "--raw", "-o", caminho}
	if de > 0 {
		args = append(args, "-T", "id", "-i", fmt.Sprintf("%d,%d", de, ate))
	}
	if _, err := a.tdl(ctx, sessao, args...); err != nil {
		return listaDoGrupo{}, err
	}
	return lerListaDoGrupo(caminho)
}

// nomeDoGrupo: o nome que aparece no Telegram, para sugerir a pasta.
func (a *app) nomeDoGrupo(ctx context.Context, sessao, chat string) string {
	if _, err := strconv.ParseInt(chat, 10, 64); err != nil {
		return chat // grupo público: o nome do link
	}
	saida, err := a.tdl(ctx, sessao, "chat", "ls", "-o", "json", "-f", "ID == "+chat)
	if err != nil {
		return ""
	}
	var grupos []struct {
		Nome string `json:"visible_name"`
	}
	if json.Unmarshal([]byte(saida), &grupos) != nil || len(grupos) == 0 {
		return ""
	}
	return limparNome(strings.ReplaceAll(grupos[0].Nome, "_", " "))
}

var (
	reEtiquetas    = regexp.MustCompile(`^(?:#\S+\s*)+`)
	reNomeDeCamera = regexp.MustCompile(`(?i)^(?:\d+|(?:vid|img|video|pxl|mov|trim|signal|whatsapp video|screen[ _-]?recording|gravacao)[\w .-]*)$`)
	fusoDeCasa     = func() *time.Location {
		if l, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
			return l
		}
		return time.Local
	}()
)

// nomeDoVideo dá um nome ao vídeo sem o Lucas escrever: a primeira linha do texto da mensagem (sem
// as #etiquetas); sem texto, o nome do arquivo; se o nome é de câmera ("VID_2026…", só números),
// a data da mensagem.
func nomeDoVideo(texto, arquivo string, data int64) string {
	for _, linha := range strings.Split(texto, "\n") {
		if n := limparNome(reEtiquetas.ReplaceAllString(strings.TrimSpace(linha), "")); n != "" {
			if r := []rune(n); len(r) > 80 {
				n = string(r[:80])
				if i := strings.LastIndex(n, " "); i > 40 {
					n = n[:i]
				}
				n = strings.TrimRight(n, " ,.;:-") + "…"
			}
			return n
		}
	}
	base := limparNome(strings.TrimSuffix(arquivo, filepath.Ext(arquivo)))
	if base != "" && !reNomeDeCamera.MatchString(base) {
		return base
	}
	if data > 0 {
		return "Vídeo de " + time.Unix(data, 0).In(fusoDeCasa).Format("02-01-2006 às 15h04")
	}
	return primeiro(base, "Vídeo")
}

// jaBaixadas: as mensagens do grupo que já viraram vídeo aqui (pela origem).
func (a *app) jaBaixadas(ctx context.Context, l linkDoTelegram) map[int64]bool {
	ja := map[int64]bool{}
	rows, err := a.db.QueryContext(ctx, `SELECT origem FROM meus_videos WHERE origem != ''`)
	if err != nil {
		return ja
	}
	defer rows.Close()
	prefixo := strings.TrimSuffix(linkDaMensagem(l, 0), "0")
	for rows.Next() {
		var o string
		if rows.Scan(&o) == nil && strings.HasPrefix(o, prefixo) {
			if id, err := strconv.ParseInt(strings.TrimPrefix(o, prefixo), 10, 64); err == nil {
				ja[id] = true
			}
		}
	}
	return ja
}

func linkDaMensagem(l linkDoTelegram, mensagem int64) string {
	if _, err := strconv.ParseInt(l.chat, 10, 64); err == nil {
		return fmt.Sprintf("https://t.me/c/%s/%d", l.chat, mensagem)
	}
	return fmt.Sprintf("https://t.me/%s/%d", l.chat, mensagem)
}

// sessaoParaRastrear: a sessão de verdade, se a fila está parada; se não, uma cópia (o banco do
// tdl só abre num processo por vez). Devolve também o que fazer no fim.
func (a *app) sessaoParaRastrear() (string, func(), error) {
	if tdlMu.TryLock() {
		return a.sessaoDoTelegram(), tdlMu.Unlock, nil
	}
	dir, err := os.MkdirTemp("", "sessao-tdl-")
	if err != nil {
		return "", nil, err
	}
	copia := filepath.Join(dir, "sessao")
	if err := os.MkdirAll(copia, 0o700); err != nil {
		return "", nil, err
	}
	arquivos, _ := os.ReadDir(a.sessaoDoTelegram())
	for _, f := range arquivos {
		if dados, err := os.ReadFile(filepath.Join(a.sessaoDoTelegram(), f.Name())); err == nil {
			os.WriteFile(filepath.Join(copia, f.Name()), dados, 0o600)
		}
	}
	return copia, func() { os.RemoveAll(dir) }, nil
}

type videoDoGrupo struct {
	Mensagem int64   `json:"mensagem"`
	Nome     string  `json:"nome"`
	Data     string  `json:"data,omitempty"`
	Tamanho  int64   `json:"tamanho"`
	Duracao  float64 `json:"duracao,omitempty"`
	Ja       bool    `json:"ja"`
}

// rastrearGrupo: os vídeos do grupo, com o nome que cada um vai ganhar, e o nome do grupo.
func (a *app) rastrearGrupo(ctx context.Context, l linkDoTelegram) (string, []videoDoGrupo, error) {
	sessao, liberar, err := a.sessaoParaRastrear()
	if err != nil {
		return "", nil, err
	}
	defer liberar()
	dir, err := os.MkdirTemp("", "rastreio-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(dir)
	lista, err := a.exportarGrupo(ctx, sessao, l.chat, 0, 0, filepath.Join(dir, "lista.json"))
	if err != nil {
		return "", nil, err
	}
	ja := a.jaBaixadas(ctx, l)
	videos := []videoDoGrupo{}
	for _, m := range lista.soVideos(func(mensagemDoGrupo) bool { return true }) {
		v := videoDoGrupo{Mensagem: m.id, Nome: nomeDoVideo(m.texto, m.arquivo, m.data), Tamanho: m.tamanho, Duracao: m.duracao, Ja: ja[m.id]}
		if m.data > 0 {
			v.Data = time.Unix(m.data, 0).UTC().Format(time.RFC3339)
		}
		videos = append(videos, v)
	}
	return a.nomeDoGrupo(ctx, sessao, l.chat), videos, nil
}

// ---------- Baixar ----------

var (
	pedidosMu       sync.Mutex
	cancelarPedidos = map[int64]context.CancelFunc{}
)

func (a *app) baixarDoTelegram(ctx context.Context, id int64) {
	p, err := a.pedidoDe(ctx, id)
	if err != nil {
		return
	}
	titulo := primeiro(p.Nome, p.Pasta)
	falhar := func(msg string) {
		a.db.ExecContext(ctx, `UPDATE pedidos_do_telegram SET estado = 'erro', mensagem = ? WHERE id = ?`, msg, id)
		a.avisos.avisar(aviso{Titulo: titulo, Texto: "Não baixou do Telegram: " + msg, Link: a.cfg.link + "/#/meus-videos", Urgente: true})
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
	tdlMu.Lock()
	defer tdlMu.Unlock()
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
	a.db.ExecContext(ctx, `UPDATE pedidos_do_telegram SET estado = 'baixando', mensagem = '', baixado = 0, videos = 0, total = 0 WHERE id = ?`, id)

	sessao := a.sessaoDoTelegram()
	modelo := "{{ .MessageID }}_{{ filenamify .FileName }}"
	g := &guardaDoPedido{a: a, p: p, link: link, tmp: tmp, info: map[int64]mensagemDoGrupo{}}

	if p.Tudo {
		caminho := filepath.Join(tmp, ".lista.json")
		lista, err := a.exportarGrupo(c, sessao, link.chat, 0, 0, caminho)
		if err != nil {
			a.depoisDoErro(ctx, c, id, err, falhar)
			return
		}
		ja := a.jaBaixadas(ctx, link)
		// Os escolhidos na lista do rastreio (sem escolha, todos), menos os que já estão aqui.
		var previsto int64
		for _, m := range lista.soVideos(func(m mensagemDoGrupo) bool {
			return !ja[m.id] && (p.escolhidas == nil || p.escolhidas[m.id])
		}) {
			g.info[m.id] = m
			previsto += m.tamanho
		}
		if len(g.info) == 0 {
			a.db.ExecContext(ctx, `UPDATE pedidos_do_telegram SET estado = 'pronto', mensagem = 'Nenhum vídeo novo no grupo.' WHERE id = ?`, id)
			return
		}
		if err := lista.gravar(caminho); err != nil {
			falhar(err.Error())
			return
		}
		a.db.ExecContext(ctx, `UPDATE pedidos_do_telegram SET total = ?, previsto = ? WHERE id = ?`, len(g.info), previsto, id)
		// Cada vídeo vai para a pasta assim que termina: dá para assistir antes do fim, e o que
		// já veio fica mesmo se o pedido for cancelado.
		err = g.enquanto(ctx, func() error {
			_, err := a.tdl(c, sessao, "dl", "-f", caminho, "-d", tmp, "--restart", "--template", modelo)
			return err
		})
		if err != nil {
			a.depoisDoErro(ctx, c, id, err, falhar)
			return
		}
	} else {
		err = g.enquanto(ctx, func() error {
			_, err := a.tdl(c, sessao, "dl", "-u", link.limpo, "-d", tmp, "--group", "--restart", "--template", modelo)
			return err
		})
		if err != nil {
			a.depoisDoErro(ctx, c, id, err, falhar)
			return
		}
		// Sem nome escolhido: o texto das mensagens dá o nome (o álbum são mensagens seguidas).
		if baixados := videosBaixados(tmp); len(baixados) > 0 {
			de, ate := baixados[0].mensagem, baixados[len(baixados)-1].mensagem
			if de > 0 {
				if lista, err := a.exportarGrupo(c, sessao, link.chat, de, ate, filepath.Join(tmp, ".lista.json")); err == nil {
					for _, m := range lista.soVideos(func(mensagemDoGrupo) bool { return true }) {
						g.info[m.id] = m
					}
				}
			}
			g.album = len(baixados) > 1
		}
	}
	if err := g.guardarProntos(ctx); err != nil {
		falhar("Não consegui guardar o vídeo: " + err.Error())
		return
	}
	if g.guardados == 0 {
		a.db.ExecContext(ctx, `UPDATE pedidos_do_telegram SET estado = 'pronto', mensagem = 'A mensagem não tem vídeo.', baixado = 0 WHERE id = ?`, id)
		return
	}
	a.db.ExecContext(ctx, `UPDATE pedidos_do_telegram SET estado = 'pronto', mensagem = '', baixado = ?, videos = ? WHERE id = ?`, g.bytes, g.guardados, id)
	a.emFundo(func() { a.completarMeusVideos(context.Background()) })
	texto := "Baixado em Meus vídeos › " + p.Pasta + "."
	if g.guardados > 1 {
		texto = fmt.Sprintf("%d vídeos baixados em Meus vídeos › %s.", g.guardados, p.Pasta)
	}
	a.avisos.avisar(aviso{Titulo: titulo, Texto: texto, Link: a.cfg.link + "/#/meus-videos"})
}

func (a *app) depoisDoErro(ctx, c context.Context, id int64, err error, falhar func(string)) {
	if _, e := a.pedidoDe(ctx, id); e != nil {
		return // cancelado e apagado
	}
	if c.Err() != nil && ctx.Err() == nil {
		falhar("Cancelado.")
		return
	}
	falhar(err.Error())
}

// guardaDoPedido leva os vídeos prontos da pasta temporária para Meus vídeos, com o nome certo.
type guardaDoPedido struct {
	a         *app
	p         pedidoDoTelegram
	link      linkDoTelegram
	tmp       string
	info      map[int64]mensagemDoGrupo // o texto e a data de cada mensagem, quando se sabe
	album     bool                      // vários vídeos com o nome escolhido: "Nome 01", "Nome 02"
	guardados int
	bytes     int64
	ordem     int
}

// enquanto roda o tdl e, a cada 2 s, mostra o andamento (e, no grupo, guarda o que já terminou).
func (g *guardaDoPedido) enquanto(ctx context.Context, rodar func() error) error {
	parar, parou := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(parou)
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-parar:
				return
			case <-t.C:
				if g.p.Tudo {
					g.guardarProntos(ctx)
				}
				g.a.db.ExecContext(ctx, `UPDATE pedidos_do_telegram SET baixado = ?, videos = ? WHERE id = ?`, g.bytes+tamanhoDaPasta(g.tmp), g.guardados, g.p.ID)
			}
		}
	}()
	err := rodar()
	close(parar)
	<-parou
	return err
}

func (g *guardaDoPedido) guardarProntos(ctx context.Context) error {
	baixados := videosBaixados(g.tmp)
	for _, b := range baixados {
		m, sabe := g.info[b.mensagem]
		nome := g.p.Nome
		switch {
		case g.p.Tudo || nome == "":
			if !sabe {
				m = mensagemDoGrupo{arquivo: b.nome}
			}
			nome = nomeDoVideo(m.texto, primeiro(m.arquivo, b.nome), m.data)
		case g.album:
			g.ordem++
			nome = fmt.Sprintf("%s %02d", nome, g.ordem)
		}
		origem := g.link.limpo
		if b.mensagem > 0 {
			origem = linkDaMensagem(g.link, b.mensagem)
		}
		criadoEm := ""
		if m.data > 0 {
			criadoEm = time.Unix(m.data, 0).UTC().Format(time.RFC3339)
		}
		v, err := g.a.guardarNaPasta(ctx, b.caminho, g.p.Pasta, nome, origem, criadoEm, b.mensagem)
		if err != nil {
			return err
		}
		g.guardados++
		g.bytes += v.Tamanho
	}
	return nil
}

type baixadoDoTelegram struct {
	caminho  string
	nome     string // o nome original, sem o número da mensagem
	mensagem int64
}

var reArquivoDoTdl = regexp.MustCompile(`^(\d+)_(.*)$`)

// videosBaixados: os vídeos prontos da pasta, na ordem das mensagens. O tdl grava em ".tmp" e só
// renomeia no fim; fotos e outros arquivos ficam de fora.
func videosBaixados(dir string) []baixadoDoTelegram {
	entradas, _ := os.ReadDir(dir)
	lista := []baixadoDoTelegram{}
	for _, e := range entradas {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !ehVideo(e.Name()) {
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
		// No grupo, só estas mensagens (as marcadas na lista do rastreio); vazio: todas.
		Mensagens []int64 `json:"mensagens"`
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
	nome := limparNome(corpo.Nome) // vazio: o nome sai da mensagem (nomeDoVideo)
	mensagens := []string{}
	if corpo.Tudo {
		nome = "" // no grupo, cada vídeo ganha o seu
		for _, m := range corpo.Mensagens {
			if m > 0 {
				mensagens = append(mensagens, strconv.FormatInt(m, 10))
			}
		}
	}
	res, err := a.db.ExecContext(r.Context(), `INSERT INTO pedidos_do_telegram (link, nome, pasta, tudo, criado_em, mensagens) VALUES (?, ?, ?, ?, ?, ?)`,
		l.limpo, nome, pasta, corpo.Tudo, a.carimbo(), strings.Join(mensagens, ","))
	if err != nil {
		falha(w, err)
		return
	}
	id, _ := res.LastInsertId()
	a.acordarTelegram()
	p, _ := a.pedidoDe(r.Context(), id)
	writeJSON(w, http.StatusAccepted, p)
}

// handleRastrear: os vídeos do grupo do link, com o nome de cada um, antes de baixar.
func (a *app) handleRastrear(w http.ResponseWriter, r *http.Request) {
	var corpo struct {
		Link string `json:"link"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	l, ok := lerLinkDoTelegram(corpo.Link)
	if !ok {
		erroJSON(w, http.StatusBadRequest, "Isso não parece um link do Telegram (https://t.me/…).")
		return
	}
	if !tdlDisponivel() {
		erroJSON(w, http.StatusServiceUnavailable, "O tdl não está instalado no servidor.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	grupo, videos, err := a.rastrearGrupo(ctx, l)
	if err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	novos := 0
	for _, v := range videos {
		if !v.Ja {
			novos++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"grupo": grupo, "pasta": primeiro(grupo, l.chat), "link": l.limpo, "videos": videos, "novos": novos,
		"livre": espacoLivre(a.pastaDosMeusVideos())})
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
