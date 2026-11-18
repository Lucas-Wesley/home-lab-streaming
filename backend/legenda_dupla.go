package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
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

// Legenda dupla (S46): inglês e português gravados na imagem (o "modelo 6" que o Lucas escolheu em
// 18/11/2026: inglês branco em negrito, português menor e amarelo logo abaixo), num vídeo 1080p H.264
// que a TV e o projetor tocam (o projetor não mostra legenda externa, S43). Por ora, de vídeos de
// torrent (`arquivo:`), baixados ou não.
//
// O trabalho: 1) escolhe as legendas em inglês e português (a escolhida pelo Lucas, se houver, ou a
// melhor do OpenSubtitles); 2) acerta a sincronia do inglês pelo arquivo (S25/S26: legenda de dentro,
// impressão digital ou Whisper) e a do português pelos tempos do inglês já certo (`alinhar`), ou pelo
// áudio, se não bater; 3) grava em pedaços de 6 s (TS), a partir do ponto pedido e depois o começo, de
// modo que dá para assistir enquanto grava; 4) junta tudo num MP4 só, que se avança e volta à vontade.
//
// Enquanto grava, a tela recebe os pedaços prontos como um fluxo contínuo (como um canal ao vivo): o
// "ir para" manda o vídeo de novo daquele ponto (só até onde já está gravado).

const esquemaDaLegendaDupla = `
CREATE TABLE IF NOT EXISTS legendas_duplas (
	chave      TEXT PRIMARY KEY, -- arquivo:644 (o vídeo de origem)
	estado     TEXT NOT NULL,    -- na fila, sincronizando, gravando, juntando, pronta, erro
	mensagem   TEXT NOT NULL DEFAULT '',
	comeco     REAL NOT NULL DEFAULT 0,    -- onde a gravação começa (o ponto pedido para assistir)
	esperar    INTEGER NOT NULL DEFAULT 0, -- só depois de baixado (pedida junto com o download)
	duracao    REAL NOT NULL DEFAULT 0,
	sincronia  TEXT NOT NULL DEFAULT '',   -- como as duas legendas foram acertadas
	criada_em  TEXT NOT NULL,
	pronta_em  TEXT NOT NULL DEFAULT ''
);
`

// queimarLegenda: os testes rodam num ffmpeg sem libass (o do Mac); lá, o vídeo sai sem a legenda.
var queimarLegenda = env("LEGENDA_DUPLA_QUEIMAR", "1") != "0"

const pedacoDaDupla = 6.0 // segundos por pedaço gravado

type duplas struct {
	mu       sync.Mutex
	acordar  chan struct{}
	rodando  string
	cancelar context.CancelFunc
	leitores map[string]int // telas recebendo os pedaços: a pasta só é apagada sem ninguém lendo
}

type legendaDupla struct {
	Chave     string       `json:"chave"`
	Estado    string       `json:"estado"`
	Mensagem  string       `json:"mensagem,omitempty"`
	Sincronia string       `json:"sincronia,omitempty"`
	Comeco    float64      `json:"comeco"`
	Esperar   bool         `json:"esperar,omitempty"`
	Duracao   float64      `json:"duracao"`
	Gravado   float64      `json:"gravado"`          // segundos já gravados, somando os trechos
	Faixas    [][2]float64 `json:"faixas,omitempty"` // os trechos gravados, para a barra
	Tamanho   int64        `json:"tamanho,omitempty"`
	CriadaEm  string       `json:"criada_em"`
	ProntaEm  string       `json:"pronta_em,omitempty"`
}

var reChaveDaDupla = regexp.MustCompile(`^arquivo:(\d+)$`)

func (a *app) pastaDasDuplas() string {
	return filepath.Join(filepath.Dir(a.cfg.pastaDosTorrents), "duplas")
}

func (a *app) pastaDaDupla(chave string) string {
	return filepath.Join(a.pastaDasDuplas(), strings.ReplaceAll(chave, ":", "-"))
}

func (a *app) arquivoDaDupla(chave string) string { return a.pastaDaDupla(chave) + ".mp4" }

// duplaPronta devolve o MP4 final, se existir.
func (a *app) duplaPronta(chave string) string {
	if _, err := os.Stat(a.arquivoDaDupla(chave)); err == nil {
		return a.arquivoDaDupla(chave)
	}
	return ""
}

func (a *app) legendaDuplaDe(ctx context.Context, chave string) (*legendaDupla, error) {
	var d legendaDupla
	err := a.db.QueryRowContext(ctx, `SELECT chave, estado, mensagem, sincronia, comeco, esperar, duracao, criada_em, pronta_em FROM legendas_duplas WHERE chave = ?`, chave).
		Scan(&d.Chave, &d.Estado, &d.Mensagem, &d.Sincronia, &d.Comeco, &d.Esperar, &d.Duracao, &d.CriadaEm, &d.ProntaEm)
	if err != nil {
		return nil, err
	}
	if c := a.duplaPronta(chave); c != "" {
		if st, err := os.Stat(c); err == nil {
			d.Tamanho = st.Size()
		}
		if d.Duracao > 0 {
			d.Gravado, d.Faixas = d.Duracao, [][2]float64{{0, d.Duracao}}
		}
	} else {
		for _, f := range juntarTrechos(a.trechosDaDupla(chave, d.Comeco)) {
			d.Faixas = append(d.Faixas, [2]float64{f.inicio, f.fim})
			d.Gravado += f.fim - f.inicio
		}
	}
	return &d, nil
}

// ---------- Os pedaços gravados ----------

type trechoGravado struct {
	arquivo     string
	inicio, fim float64
}

// trechosDaDupla lê as listas do ffmpeg (uma por gravação: a, do ponto pedido ao fim; b, do começo
// ao ponto; p01, p02…, o que faltou depois de reiniciar), em ordem de tempo. O ffmpeg marca o começo
// do primeiro pedaço como 0: vale o ponto de onde aquela gravação partiu (guardado em <nome>.inicio).
func (a *app) trechosDaDupla(chave string, comeco float64) []trechoGravado {
	pasta := a.pastaDaDupla(chave)
	var todos []trechoGravado
	listas, _ := filepath.Glob(filepath.Join(pasta, "*.csv"))
	for _, caminho := range listas {
		nome := strings.TrimSuffix(filepath.Base(caminho), ".csv")
		inicio := map[string]float64{"a": comeco}[nome]
		if t, err := os.ReadFile(filepath.Join(pasta, nome+".inicio")); err == nil {
			inicio, _ = strconv.ParseFloat(strings.TrimSpace(string(t)), 64)
		}
		b, err := os.ReadFile(caminho)
		if err != nil {
			continue
		}
		for i, linha := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			partes := strings.Split(linha, ",")
			if len(partes) != 3 {
				continue
			}
			ini, _ := strconv.ParseFloat(partes[1], 64)
			fim, _ := strconv.ParseFloat(partes[2], 64)
			if i == 0 {
				ini = inicio
			}
			todos = append(todos, trechoGravado{filepath.Join(pasta, partes[0]), ini, fim})
		}
	}
	sort.Slice(todos, func(i, j int) bool { return todos[i].inicio < todos[j].inicio })
	return todos
}

// juntarTrechos: os trechos seguidos (sem buraco) viram um só.
func juntarTrechos(ts []trechoGravado) []trechoGravado {
	var out []trechoGravado
	for _, t := range ts {
		if n := len(out); n > 0 && t.inicio-out[n-1].fim < 0.5 {
			out[n-1].fim = math.Max(out[n-1].fim, t.fim)
			continue
		}
		out = append(out, trechoGravado{inicio: t.inicio, fim: t.fim})
	}
	return out
}

// gravadoAte: até onde há vídeo gravado sem buraco a partir de pos (pos, se nada).
func gravadoAte(faixas [][2]float64, pos float64) float64 {
	for _, f := range faixas {
		if pos >= f[0]-0.5 && pos < f[1] {
			return f[1]
		}
	}
	return pos
}

// ---------- Pedir, ver e apagar ----------

func (a *app) handleLegendaDupla(w http.ResponseWriter, r *http.Request) {
	chave := r.PathValue("chave")
	if !reChaveDaDupla.MatchString(chave) {
		erroJSON(w, http.StatusBadRequest, "A legenda dupla é só para vídeos de torrent, por enquanto.")
		return
	}
	d, err := a.legendaDuplaDe(r.Context(), chave)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"chave": chave, "estado": ""})
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// handlePedirLegendaDupla: pedir de novo a mesma não refaz (só passa à frente na fila e, se ainda não
// começou, muda o ponto de começo); a que deu erro recomeça.
func (a *app) handlePedirLegendaDupla(w http.ResponseWriter, r *http.Request) {
	chave := r.PathValue("chave")
	m := reChaveDaDupla.FindStringSubmatch(chave)
	if m == nil {
		erroJSON(w, http.StatusBadRequest, "A legenda dupla é só para vídeos de torrent, por enquanto.")
		return
	}
	var corpo struct {
		Comeco  float64 `json:"comeco"`  // segundos: de onde assistir primeiro
		Esperar bool    `json:"esperar"` // só depois de baixado
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	id, _ := strconv.ParseInt(m[1], 10, 64)
	f, _, err := a.arquivo(r.Context(), id)
	if err != nil || f.Tipo != "video" {
		erroJSON(w, http.StatusNotFound, "Vídeo não encontrado.")
		return
	}
	comeco := math.Floor(math.Max(0, corpo.Comeco)/pedacoDaDupla) * pedacoDaDupla
	agora := a.agoraEmTexto()
	_, err = a.db.ExecContext(r.Context(), `INSERT INTO legendas_duplas (chave, estado, comeco, esperar, criada_em) VALUES (?, 'na fila', ?, ?, ?)
		ON CONFLICT (chave) DO UPDATE SET
			criada_em = excluded.criada_em,
			esperar = CASE WHEN legendas_duplas.estado IN ('na fila', 'erro') THEN excluded.esperar ELSE legendas_duplas.esperar END,
			comeco = CASE WHEN legendas_duplas.estado IN ('na fila', 'erro') THEN excluded.comeco ELSE legendas_duplas.comeco END,
			mensagem = CASE WHEN legendas_duplas.estado = 'erro' THEN '' ELSE legendas_duplas.mensagem END,
			estado = CASE WHEN legendas_duplas.estado = 'erro' THEN 'na fila' ELSE legendas_duplas.estado END`,
		chave, comeco, corpo.Esperar, agora)
	if err != nil {
		falha(w, err)
		return
	}
	a.acordarDuplas()
	d, err := a.legendaDuplaDe(r.Context(), chave)
	if err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (a *app) handleApagarLegendaDupla(w http.ResponseWriter, r *http.Request) {
	if err := a.apagarLegendaDupla(r.Context(), r.PathValue("chave")); err != nil {
		falha(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apagarLegendaDupla para a gravação (se for a dela) e apaga os arquivos. Também quando o vídeo de
// origem é apagado ou visto.
func (a *app) apagarLegendaDupla(ctx context.Context, chave string) error {
	a.duplas.mu.Lock()
	if a.duplas.rodando == chave && a.duplas.cancelar != nil {
		a.duplas.cancelar()
	}
	a.duplas.mu.Unlock()
	if _, err := a.db.ExecContext(ctx, `DELETE FROM legendas_duplas WHERE chave = ?`, chave); err != nil {
		return err
	}
	os.Remove(a.arquivoDaDupla(chave))
	a.limparPedacos(chave)
	return nil
}

// limparPedacos apaga a pasta dos pedaços, se nenhuma tela estiver lendo deles.
func (a *app) limparPedacos(chave string) {
	a.duplas.mu.Lock()
	lendo := a.duplas.leitores[chave] > 0
	a.duplas.mu.Unlock()
	if !lendo {
		os.RemoveAll(a.pastaDaDupla(chave))
	}
}

// ---------- O trabalhador ----------

func (a *app) acordarDuplas() {
	select {
	case a.duplas.acordar <- struct{}{}:
	default:
	}
}

// trabalharNasDuplas: uma gravação por vez (o processador é um só), a pedida por último antes.
// Depois de reiniciar o servidor, a que estava no meio recomeça do zero.
func (a *app) trabalharNasDuplas(ctx context.Context) {
	a.db.ExecContext(ctx, `UPDATE legendas_duplas SET estado = 'na fila' WHERE estado IN ('sincronizando', 'gravando', 'juntando')`)
	for {
		a.gravarDuplasPendentes(ctx)
		select {
		case <-ctx.Done():
			return
		case <-a.duplas.acordar:
		case <-time.After(time.Minute): // a que espera o download
		}
	}
}

func (a *app) gravarDuplasPendentes(ctx context.Context) {
	for ctx.Err() == nil {
		chave := a.proximaDupla(ctx)
		if chave == "" {
			return
		}
		c, cancel := context.WithCancel(ctx)
		a.duplas.mu.Lock()
		a.duplas.rodando, a.duplas.cancelar = chave, cancel
		a.duplas.mu.Unlock()
		err := a.gravarDupla(c, chave)
		a.duplas.mu.Lock()
		a.duplas.rodando, a.duplas.cancelar = "", nil
		a.duplas.mu.Unlock()
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("legenda dupla %s: %v", chave, semSegredos(err))
			a.db.Exec(`UPDATE legendas_duplas SET estado = 'erro', mensagem = ? WHERE chave = ?`, semSegredos(err).Error(), chave)
		}
	}
}

// proximaDupla: a pedida por último, menos as que esperam o download ainda não terminado.
func (a *app) proximaDupla(ctx context.Context) string {
	rows, err := a.db.QueryContext(ctx, `SELECT chave, esperar FROM legendas_duplas WHERE estado = 'na fila' ORDER BY criada_em DESC`)
	if err != nil {
		return ""
	}
	type pedido struct {
		chave   string
		esperar bool
	}
	var pedidos []pedido
	for rows.Next() {
		var p pedido
		rows.Scan(&p.chave, &p.esperar)
		pedidos = append(pedidos, p)
	}
	rows.Close()
	for _, p := range pedidos {
		if p.esperar {
			id, _ := strconv.ParseInt(reChaveDaDupla.FindStringSubmatch(p.chave)[1], 10, 64)
			if f, _, err := a.arquivo(ctx, id); err != nil || a.caminhoBaixado(f) == "" {
				continue
			}
		}
		return p.chave
	}
	return ""
}

func (a *app) gravarDupla(ctx context.Context, chave string) error {
	d, err := a.legendaDuplaDe(ctx, chave)
	if err != nil {
		return err
	}
	id, _ := strconv.ParseInt(reChaveDaDupla.FindStringSubmatch(chave)[1], 10, 64)
	f, t, err := a.arquivo(ctx, id)
	if err != nil {
		return errors.New("o vídeo de origem não existe mais")
	}
	entrada := a.caminhoBaixado(f)
	if entrada == "" {
		base, err := a.enderecoInterno()
		if err != nil {
			return err
		}
		entrada = fmt.Sprintf("%s/arquivo/%d", base, f.ID)
	}
	estado := func(e, msg string) {
		a.db.Exec(`UPDATE legendas_duplas SET estado = ?, mensagem = ? WHERE chave = ?`, e, msg, chave)
	}

	duracao := a.duracaoDoArquivo(ctx, f)
	if duracao <= 0 {
		return errors.New("não consegui ler a duração do vídeo")
	}
	a.db.Exec(`UPDATE legendas_duplas SET duracao = ? WHERE chave = ?`, duracao, chave)
	pasta := a.pastaDaDupla(chave)
	comeco := math.Min(d.Comeco, math.Max(0, duracao-pedacoDaDupla))
	// Reiniciou no meio da gravação (um deploy): as legendas já estão no ASS e os pedaços gravados
	// ficam; grava só o que falta.
	if _, err := os.Stat(filepath.Join(pasta, "dupla.ass")); err != nil || comeco != d.Comeco {
		if err := a.sincronizarDupla(ctx, chave, f, pasta); err != nil {
			return err
		}
	}

	estado("gravando", "")
	faixa := faixaEmIngles(a, f)
	a.db.Exec(`UPDATE legendas_duplas SET comeco = ? WHERE chave = ?`, comeco, chave)
	partes := [][2]float64{{comeco, duracao}}
	if comeco > 0 {
		partes = append(partes, [2]float64{0, comeco})
	}
	for _, parte := range partes {
		for _, falta := range faltando(parte, juntarTrechos(a.trechosDaDupla(chave, comeco))) {
			listas, _ := filepath.Glob(filepath.Join(pasta, "*.csv"))
			ate := falta[1] - 0.001 // para antes do quadro do ponto, que a gravação seguinte já tem
			if falta[1] >= duracao-0.5 {
				ate = 0 // até o fim
			}
			if err := gravarTrecho(ctx, pasta, entrada, fmt.Sprintf("p%02d", len(listas)+1), falta[0], ate, faixa); err != nil {
				return err
			}
		}
	}

	estado("juntando", "")
	if err := a.juntarDupla(ctx, chave, comeco); err != nil {
		return err
	}
	a.db.Exec(`UPDATE legendas_duplas SET estado = 'pronta', mensagem = '', pronta_em = ? WHERE chave = ?`, a.agoraEmTexto(), chave)
	a.limparPedacos(chave)
	a.avisos.avisar(aviso{Titulo: tituloDoArquivo(f, t), Texto: "Legenda dupla pronta: inglês e português na imagem.", Link: a.cfg.link + "/#/torrents"})
	return nil
}

// faltando: as partes de [de, ate) que nenhum trecho gravado cobre (menos de meio segundo não conta).
func faltando(intervalo [2]float64, gravados []trechoGravado) [][2]float64 {
	var out [][2]float64
	cursor := intervalo[0]
	for _, g := range gravados {
		if g.fim <= cursor || g.inicio >= intervalo[1] {
			continue
		}
		if g.inicio-cursor > 0.5 {
			out = append(out, [2]float64{cursor, g.inicio})
		}
		cursor = math.Max(cursor, g.fim)
	}
	if intervalo[1]-cursor > 0.5 {
		out = append(out, [2]float64{cursor, intervalo[1]})
	}
	return out
}

// sincronizarDupla escolhe e sincroniza as legendas e escreve o ASS numa pasta nova.
func (a *app) sincronizarDupla(ctx context.Context, chave string, f arquivoDeTorrent, pasta string) error {
	a.db.Exec(`UPDATE legendas_duplas SET estado = 'sincronizando', mensagem = ? WHERE chave = ?`, "Escolhendo e sincronizando as legendas em inglês e português…", chave)
	en, pt, err := a.legendasDaDupla(ctx, chave, f)
	if err != nil {
		return err
	}
	sincronia := strings.TrimSpace("Inglês: " + en.ajuste + " Português: " + pt.ajuste)
	a.db.Exec(`UPDATE legendas_duplas SET sincronia = ? WHERE chave = ?`, sincronia, chave)
	// No celular, a legenda que passa (S31) é a mesma inglesa que está na imagem, já sincronizada.
	a.db.Exec(`INSERT INTO legendas_escolhidas (chave, id, idioma, nome, atraso, escala, ajuste, alinhando) VALUES (?, ?, 'en', ?, ?, ?, ?, 0)
		ON CONFLICT (chave) DO UPDATE SET id = excluded.id, idioma = 'en', nome = excluded.nome, atraso = excluded.atraso,
			escala = excluded.escala, ajuste = excluded.ajuste, alinhando = 0`,
		chave, en.id, en.nome, en.al.Atraso, en.al.Escala, en.ajuste)

	os.RemoveAll(pasta)
	if err := os.MkdirAll(pasta, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(pasta, "dupla.ass"), []byte(assDaDupla(en, pt)), 0o644)
}

// faixaEmIngles: a faixa de áudio em inglês, se o arquivo disser; senão, a primeira.
func faixaEmIngles(a *app, f arquivoDeTorrent) int {
	var guardada string
	a.db.QueryRow(`SELECT sondagem FROM torrent_arquivos WHERE id = ?`, f.ID).Scan(&guardada)
	var s sondagem
	if json.Unmarshal([]byte(guardada), &s) == nil {
		for i, au := range s.Audios {
			if idioma := strings.ToLower(au.Idioma); idioma == "en" || idioma == "eng" {
				return i
			}
		}
	}
	return 0
}

// gravarTrecho grava [de, ate) (ate 0: até o fim) em pedaços de 6 s, com a legenda na imagem. Os
// tempos ficam os do vídeo de origem (-copyts): a legenda cai no lugar e os pedaços de antes e de
// depois do ponto pedido se encaixam.
func gravarTrecho(ctx context.Context, pasta, entrada, nome string, de, ate float64, faixa int) error {
	if err := os.WriteFile(filepath.Join(pasta, nome+".inicio"), []byte(strconv.FormatFloat(de, 'f', 3, 64)), 0o644); err != nil {
		return err
	}
	args := []string{"-hide_banner", "-nostdin", "-v", "error"}
	if de > 0 {
		args = append(args, "-ss", strconv.FormatFloat(de, 'f', 3, 64))
	}
	if ate > 0 {
		args = append(args, "-to", strconv.FormatFloat(ate, 'f', 3, 64))
	}
	filtro := "scale='min(1920,iw)':-2,format=yuv420p"
	if queimarLegenda {
		filtro += ",subtitles=dupla.ass"
	}
	args = append(args, "-copyts", "-i", entrada,
		"-map", "0:v:0", "-map", fmt.Sprintf("0:a:%d?", faixa),
		"-vf", filtro,
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "20",
		"-force_key_frames", fmt.Sprintf("expr:if(isnan(prev_forced_t),1,gte(t,prev_forced_t+%g))", pedacoDaDupla),
		"-c:a", "aac", "-b:a", "192k", "-ac", "2",
		"-f", "segment", "-segment_time", strconv.FormatFloat(pedacoDaDupla, 'f', 0, 64), "-segment_format", "mpegts",
		"-segment_list", nome+".csv", "-segment_list_type", "csv", nome+"_%05d.ts")
	cmd := comPrioridadeBaixa(ctx, programaFFmpeg, args...)
	cmd.Dir = pasta
	if saida, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("o ffmpeg falhou ao gravar: %s", ultimasLinhas(string(saida), 3))
	}
	return nil
}

// comPrioridadeBaixa: a gravação ocupa o processador inteiro por quase uma hora; com nice, os outros
// apps do servidor (e o vídeo que a tela recebe) passam na frente.
func comPrioridadeBaixa(ctx context.Context, programa string, args ...string) *exec.Cmd {
	if nice, err := exec.LookPath("nice"); err == nil {
		return exec.CommandContext(ctx, nice, append([]string{"-n", "10", programa}, args...)...)
	}
	return exec.CommandContext(ctx, programa, args...)
}

func ultimasLinhas(s string, n int) string {
	linhas := strings.Split(strings.TrimSpace(s), "\n")
	if len(linhas) > n {
		linhas = linhas[len(linhas)-n:]
	}
	return strings.Join(linhas, " ")
}

// juntarDupla junta os pedaços, em ordem, num MP4 que se avança e volta (moov no começo). Os pedaços
// vão um atrás do outro, como um TS só (os tempos são os do original, contínuos): o concat do ffmpeg,
// que soma a duração de cada arquivo, deixava ~0,1 s de buraco a cada 6 s (um engasgo).
func (a *app) juntarDupla(ctx context.Context, chave string, comeco float64) error {
	pasta := a.pastaDaDupla(chave)
	trechos := a.trechosDaDupla(chave, comeco)
	temporario := a.arquivoDaDupla(chave) + ".juntando"
	cmd := exec.CommandContext(ctx, programaFFmpeg, "-hide_banner", "-nostdin", "-v", "error", "-y", "-f", "mpegts", "-i", "pipe:0",
		"-map", "0:v:0", "-map", "0:a:0?", "-c", "copy", "-movflags", "+faststart", "-f", "mp4", temporario)
	cmd.Dir = pasta
	entrada, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var saida strings.Builder
	cmd.Stderr = &saida
	if err := cmd.Start(); err != nil {
		return err
	}
	var errCopia error
	for _, t := range trechos {
		arq, err := os.Open(t.arquivo)
		if err != nil {
			errCopia = err
			break
		}
		_, err = io.Copy(entrada, arq)
		arq.Close()
		if err != nil {
			errCopia = err
			break
		}
	}
	entrada.Close()
	if err := cmd.Wait(); err != nil || errCopia != nil {
		os.Remove(temporario)
		return fmt.Errorf("o ffmpeg falhou ao juntar: %s %v", ultimasLinhas(saida.String(), 3), errCopia)
	}
	return os.Rename(temporario, a.arquivoDaDupla(chave))
}

// ---------- As duas legendas ----------

type legendaDaDupla struct {
	id, idioma, nome, ajuste string
	texto                    []byte
	al                       alinhamento
}

// legendasDaDupla escolhe e sincroniza as duas legendas.
func (a *app) legendasDaDupla(ctx context.Context, chave string, f arquivoDeTorrent) (en, pt legendaDaDupla, err error) {
	escolhida := a.legendaDe(ctx, chave)
	var opcoes []opcaoDeLegenda
	if a.chaveDoOpenSubtitles() != "" {
		if v, e := a.videoParaLegenda(ctx, "arquivo", f.ID); e == nil {
			opcoes, _ = a.buscarLegendas(ctx, a.chaveDoOpenSubtitles(), v)
		}
	}
	escolher := func(idiomas ...string) (legendaDaDupla, bool) {
		for _, idioma := range idiomas {
			// A que o Lucas escolheu, se já sincronizada.
			if escolhida != nil && escolhida.Geracao == nil && !escolhida.Alinhando && escolhida.Idioma == idioma {
				return legendaDaDupla{id: escolhida.ID, idioma: idioma, nome: escolhida.Nome, ajuste: "a que você escolheu.",
					al: alinhamento{Atraso: escolhida.Atraso, Escala: escolhida.Escala}}, true
			}
		}
		for _, maquina := range []bool{false, true} {
			for _, idioma := range idiomas {
				for _, o := range opcoes {
					if o.Idioma == idioma && o.Maquina == maquina {
						return legendaDaDupla{id: o.ID, idioma: idioma, nome: o.Nome}, true
					}
				}
			}
		}
		return legendaDaDupla{}, false
	}
	var ok bool
	if en, ok = escolher("en"); !ok {
		return en, pt, errors.New("não achei legenda em inglês para este vídeo (o OpenSubtitles está configurado em Ajustes?)")
	}
	if pt, ok = escolher("pt-BR", "pt-PT"); !ok {
		return en, pt, errors.New("não achei legenda em português para este vídeo")
	}
	for _, l := range []*legendaDaDupla{&en, &pt} {
		if err := a.baixarLegenda(ctx, l.id, a.arquivoDaLegenda(l.id)); err != nil {
			return en, pt, err
		}
		if l.texto, err = os.ReadFile(a.arquivoDaLegenda(l.id)); err != nil {
			return en, pt, err
		}
	}
	if en.ajuste == "" {
		en.ajuste, en.al = a.acharSincronia(ctx, "arquivo", f.ID, en.id, "en")
	}
	if pt.ajuste == "" {
		// O português pelos tempos do inglês já certo; se não bater, pelo próprio arquivo.
		ref := lerFalas([]byte(ajustarTempos(string(en.texto), en.al.Atraso, en.al.Escala)))
		if al := alinhar(lerFalas(pt.texto), ref); al.Acerto >= 0.3 {
			pt.al, pt.ajuste = al, fmt.Sprintf("Ajustada em %s pelos tempos da inglesa.", segundosComSinal(al.Atraso))
		} else {
			pt.ajuste, pt.al = a.acharSincronia(ctx, "arquivo", f.ID, pt.id, pt.idioma)
		}
	}
	return en, pt, nil
}

// ---------- O arquivo ASS ----------

type falaComTexto struct {
	inicio, fim float64
	texto       string
}

var reMarcasDoTexto = regexp.MustCompile(`<[^>]*>|\{[^}]*\}`)

// falasDoSRT lê as falas com tempo de fim e texto (uma fala vira uma linha; duas pessoas, duas).
func falasDoSRT(srt []byte, al alinhamento) []falaComTexto {
	var out []falaComTexto
	for _, bloco := range strings.Split(strings.ReplaceAll(string(srt), "\r\n", "\n"), "\n\n") {
		linhas := strings.Split(strings.TrimSpace(bloco), "\n")
		for i, l := range linhas {
			m := reTempoDaFala.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			var partes []string
			for _, t := range linhas[i+1:] {
				t = strings.TrimSpace(reMarcasDoTexto.ReplaceAllString(t, ""))
				t = strings.NewReplacer(`\`, "", "{", "", "}", "").Replace(t)
				if t == "" {
					continue
				}
				// Fala de outra pessoa ("- Não!") começa linha nova; o resto da mesma frase, não.
				if len(partes) > 0 && !strings.HasPrefix(t, "-") {
					partes[len(partes)-1] += " " + t
				} else {
					partes = append(partes, t)
				}
			}
			if len(partes) > 0 {
				out = append(out, falaComTexto{
					inicio: segundosDoSRT(m[1], m[2], m[3], m[4])*al.Escala + al.Atraso,
					fim:    segundosDoSRT(m[5], m[6], m[7], m[8])*al.Escala + al.Atraso,
					texto:  strings.Join(partes, `\N`),
				})
			}
			break
		}
	}
	return out
}

func tempoDoASS(t float64) string {
	cs := int(math.Round(math.Max(0, t) * 100))
	return fmt.Sprintf("%d:%02d:%02d.%02d", cs/360000, cs/6000%60, cs/100%60, cs%100)
}

// Posição das linhas (em pixels de 1080): o português fica embaixo; o inglês, logo acima dele.
const (
	margemDeBaixo     = 46
	alturaDaLinhaPT   = 58 // fonte 46 com contorno
	letrasPorLinhaPT  = 72 // cabem numa linha de 1760 px, na fonte 46
	folgaEntreLinguas = 4
)

// linhasNaTela: quantas linhas a fala ocupa (as quebradas pela largura também).
func linhasNaTela(texto string, porLinha int) int {
	n := 0
	for _, parte := range strings.Split(texto, `\N`) {
		n += max(1, (len([]rune(parte))+porLinha-1)/porLinha)
	}
	return n
}

// assDaDupla: o modelo aprovado pelo Lucas (inglês branco em negrito, português menor e amarelo logo
// abaixo). Sem depender do empilhamento automático do libass (que muda com a ordem e o tempo das
// falas): o português fica fixo embaixo, numa camada; o inglês, noutra, sobe o tanto de linhas do
// português que aparece junto com ele (pelo menos uma, para não pular de altura).
func assDaDupla(en, pt legendaDaDupla) string {
	var b strings.Builder
	b.WriteString("[Script Info]\nScriptType: v4.00+\nPlayResX: 1920\nPlayResY: 1080\nScaledBorderAndShadow: yes\nWrapStyle: 0\n\n")
	b.WriteString("[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n")
	fmt.Fprintf(&b, "Style: EN,Liberation Sans,60,&H00FFFFFF,&H000000FF,&H00000000,&H64000000,-1,0,0,0,100,100,0,0,1,3,0,2,80,80,%d,1\n", margemDeBaixo)
	fmt.Fprintf(&b, "Style: PT,Liberation Sans,46,&H0060E0FF,&H000000FF,&H00000000,&H64000000,0,0,0,0,100,100,0,0,1,2.5,0,2,80,80,%d,1\n\n", margemDeBaixo)
	b.WriteString("[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
	falasPT, falasEN := falasDoSRT(pt.texto, pt.al), falasDoSRT(en.texto, en.al)
	for _, f := range falasPT {
		if f.fim > 0 {
			fmt.Fprintf(&b, "Dialogue: 0,%s,%s,PT,,0,0,0,,%s\n", tempoDoASS(f.inicio), tempoDoASS(f.fim), f.texto)
		}
	}
	for _, f := range falasEN {
		if f.fim <= 0 {
			continue
		}
		linhas := 0
		for _, p := range falasPT {
			if p.inicio < f.fim && p.fim > f.inicio {
				linhas = max(linhas, linhasNaTela(p.texto, letrasPorLinhaPT))
			}
		}
		// Sempre o espaço de uma linha de português, para o inglês não pular de altura entre as falas.
		margem := margemDeBaixo + max(1, linhas)*alturaDaLinhaPT + folgaEntreLinguas
		fmt.Fprintf(&b, "Dialogue: 1,%s,%s,EN,,0,0,%d,,%s\n", tempoDoASS(f.inicio), tempoDoASS(f.fim), margem, f.texto)
	}
	return b.String()
}

// ---------- Para a tela, enquanto grava ----------

// inicioDoFluxo: o começo do pedaço gravado onde pos cai (a tela conta o tempo a partir dele).
func (a *app) inicioDoFluxo(chave string, comeco, pos float64) (float64, bool) {
	if t := trechoEm(a.trechosDaDupla(chave, comeco), pos); t != nil {
		return t.inicio, true
	}
	return 0, false
}

// trechoEm: o pedaço onde pos cai. No encontro das duas gravações, o último pedaço de antes do ponto
// passa um quadro dele: vale o que começa mais perto.
func trechoEm(ts []trechoGravado, pos float64) *trechoGravado {
	var achado *trechoGravado
	for i := range ts {
		if pos >= ts[i].inicio-0.01 && pos < ts[i].fim {
			achado = &ts[i]
		}
	}
	return achado
}

// servirDuplaCrescendo manda à tela os pedaços prontos, um atrás do outro, a partir de desde, como
// um fluxo contínuo; na frente do que já foi gravado, espera o próximo pedaço.
func (a *app) servirDuplaCrescendo(w http.ResponseWriter, r *http.Request, chave string, desde float64) {
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	cabecalhosDLNA(w, fonte{aoVivo: true})
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	a.duplas.mu.Lock()
	a.duplas.leitores[chave]++
	a.duplas.mu.Unlock()
	defer func() {
		a.duplas.mu.Lock()
		a.duplas.leitores[chave]--
		a.duplas.mu.Unlock()
		if a.duplaPronta(chave) != "" {
			a.limparPedacos(chave)
		}
	}()
	rc := http.NewResponseController(w)
	fim := -1.0
	for r.Context().Err() == nil {
		d, err := a.legendaDuplaDe(r.Context(), chave)
		if err != nil {
			return
		}
		ts := a.trechosDaDupla(chave, d.Comeco)
		var proximo *trechoGravado
		if fim < 0 {
			proximo = trechoEm(ts, desde)
		} else {
			for i := range ts {
				if ts[i].inicio > fim-1 && math.Abs(ts[i].inicio-fim) < 0.5 {
					proximo = &ts[i]
					break
				}
			}
		}
		if proximo == nil {
			if d.Estado != "gravando" && d.Estado != "sincronizando" && d.Estado != "na fila" {
				return // acabou de gravar (ou deu erro) e não há mais nada à frente
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		arq, err := os.Open(proximo.arquivo)
		if err != nil {
			return
		}
		_, err = io.Copy(w, arq)
		arq.Close()
		if err != nil {
			return
		}
		_ = rc.Flush()
		fim = proximo.fim
	}
}
