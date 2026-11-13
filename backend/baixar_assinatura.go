package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Baixar da assinatura (S28): filmes e episódios ficam no servidor, para assistir sem usar a
// conexão (a assinatura aceita só 1). O download usa a conexão por trás: só com ela livre, e
// para sozinho quando alguém começa a assistir pela assinatura; depois retoma de onde parou (Range).

const esquemaDosDownloadsDaAssinatura = `
CREATE TABLE IF NOT EXISTS downloads_da_assinatura (
	chave     TEXT PRIMARY KEY, -- filme:1, episodio:2
	titulo    TEXT NOT NULL,
	extensao  TEXT NOT NULL,
	tamanho   INTEGER NOT NULL DEFAULT 0,
	baixado   INTEGER NOT NULL DEFAULT 0,
	estado    TEXT NOT NULL DEFAULT 'na fila', -- na fila, baixando, esperando, baixado, erro
	mensagem  TEXT NOT NULL DEFAULT '',
	criado_em TEXT NOT NULL
);
`

type downloadDaAssinatura struct {
	Chave    string `json:"chave"`
	Titulo   string `json:"titulo"`
	Extensao string `json:"-"`
	Tamanho  int64  `json:"tamanho"`
	Baixado  int64  `json:"baixado"`
	Estado   string `json:"estado"`
	Mensagem string `json:"mensagem,omitempty"`
	CriadoEm string `json:"criado_em"`
	Capa     string `json:"capa,omitempty"`
	Pagina   string `json:"pagina,omitempty"`
}

// Quanto esperar para tentar de novo quando a assinatura está em uso.
var esperaPelaConexao = 30 * time.Second

func (a *app) pastaDaAssinatura() string {
	return filepath.Join(filepath.Dir(a.cfg.pastaDosTorrents), "assinatura")
}

func (a *app) caminhoDoDownload(d downloadDaAssinatura) string {
	return filepath.Join(a.pastaDaAssinatura(), strings.ReplaceAll(d.Chave, ":", "-")+"."+primeiro(d.Extensao, "mp4"))
}

// baixadoDaAssinatura: o arquivo do vídeo, se ele já foi baixado inteiro.
func (a *app) baixadoDaAssinatura(ctx context.Context, chave string) string {
	d, err := a.downloadDe(ctx, chave)
	if err != nil || d.Estado != "baixado" {
		return ""
	}
	c := a.caminhoDoDownload(d)
	if _, err := os.Stat(c); err != nil {
		return ""
	}
	return c
}

func (a *app) downloadDe(ctx context.Context, chave string) (downloadDaAssinatura, error) {
	var d downloadDaAssinatura
	err := a.db.QueryRowContext(ctx, `SELECT chave, titulo, extensao, tamanho, baixado, estado, mensagem, criado_em
		FROM downloads_da_assinatura WHERE chave = ?`, chave).
		Scan(&d.Chave, &d.Titulo, &d.Extensao, &d.Tamanho, &d.Baixado, &d.Estado, &d.Mensagem, &d.CriadoEm)
	return d, err
}

func (a *app) downloadsDaAssinatura(ctx context.Context) ([]downloadDaAssinatura, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT d.chave, d.titulo, d.extensao, d.tamanho, d.baixado, d.estado, d.mensagem, d.criado_em,
			COALESCE(i.capa, e.capa, s.capa, ''), COALESCE(e.serie_id, 0)
		FROM downloads_da_assinatura d
		LEFT JOIN itens i ON d.chave = 'filme:' || i.id AND i.tipo = 'filme'
		LEFT JOIN episodios e ON d.chave = 'episodio:' || e.id
		LEFT JOIN itens s ON s.tipo = 'serie' AND s.id = e.serie_id
		ORDER BY d.criado_em DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lista := []downloadDaAssinatura{}
	for rows.Next() {
		var d downloadDaAssinatura
		var serie int64
		if err := rows.Scan(&d.Chave, &d.Titulo, &d.Extensao, &d.Tamanho, &d.Baixado, &d.Estado, &d.Mensagem, &d.CriadoEm, &d.Capa, &serie); err != nil {
			return nil, err
		}
		tipo, id, _ := strings.Cut(d.Chave, ":")
		if tipo == "filme" {
			d.Pagina = "#/filme/" + id
		} else if serie > 0 {
			d.Pagina = fmt.Sprintf("#/serie/%d", serie)
		}
		lista = append(lista, d)
	}
	return lista, rows.Err()
}

// usaAConexao: tocar este vídeo ocupa a assinatura? Os de torrent e os já baixados, não; tocá-los
// não tira a conexão de quem está assistindo outra coisa pela assinatura.
func (a *app) usaAConexao(ctx context.Context, chave string) bool {
	if strings.HasPrefix(chave, "arquivo:") || strings.HasPrefix(chave, "proprio:") {
		return false
	}
	return a.baixadoDaAssinatura(ctx, chave) == ""
}

// ---------- Rotas ----------

func (a *app) handleDownloadsDaAssinatura(w http.ResponseWriter, r *http.Request) {
	lista, err := a.downloadsDaAssinatura(r.Context())
	if err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, lista)
}

func (a *app) handleBaixarDaAssinatura(w http.ResponseWriter, r *http.Request) {
	chave := r.PathValue("chave")
	m := reChaveDeVideo.FindStringSubmatch(chave)
	if m == nil || m[1] == "arquivo" {
		erroJSON(w, http.StatusBadRequest, "Só filmes e episódios da assinatura podem ser baixados.")
		return
	}
	id, _ := strconv.ParseInt(m[2], 10, 64)
	if r.Method == http.MethodDelete {
		if d, err := a.downloadDe(r.Context(), chave); err == nil {
			a.db.ExecContext(r.Context(), `DELETE FROM downloads_da_assinatura WHERE chave = ?`, chave)
			a.pararDownload(chave)
			os.Remove(a.caminhoDoDownload(d))
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if d, err := a.downloadDe(r.Context(), chave); err == nil {
		writeJSON(w, http.StatusOK, d) // já está na fila ou baixado
		return
	}
	f, err := a.fonteDe(r.Context(), m[1], id, "")
	if errors.Is(err, sql.ErrNoRows) {
		erroJSON(w, http.StatusNotFound, "Vídeo não encontrado.")
		return
	}
	if err != nil {
		erroJSON(w, http.StatusConflict, semSegredos(err).Error())
		return
	}
	ext := strings.TrimPrefix(filepath.Ext(strings.SplitN(f.url, "?", 2)[0]), ".")
	d := downloadDaAssinatura{Chave: chave, Titulo: f.titulo, Extensao: primeiro(ext, "mp4"), Estado: "na fila", CriadoEm: a.agora().UTC().Format(time.RFC3339)}
	if _, err := a.db.ExecContext(r.Context(), `INSERT INTO downloads_da_assinatura (chave, titulo, extensao, estado, criado_em) VALUES (?, ?, ?, ?, ?)`,
		d.Chave, d.Titulo, d.Extensao, d.Estado, d.CriadoEm); err != nil {
		falha(w, err)
		return
	}
	a.acordarDownloads()
	writeJSON(w, http.StatusAccepted, d)
}

// ---------- O trabalho ----------

// acordarDownloads avisa o trabalhador de que há algo novo (sem esperar a próxima tentativa).
func (a *app) acordarDownloads() {
	select {
	case a.downloadsDaAssinaturaAcordar <- struct{}{}:
	default:
	}
}

func (a *app) pararDownload(chave string) {
	a.baixandoMu.Lock()
	if a.baixandoAgora == chave && a.cancelarDownload != nil {
		a.cancelarDownload()
	}
	a.baixandoMu.Unlock()
}

// trabalharNosDownloads roda enquanto o servidor estiver no ar.
func (a *app) trabalharNosDownloads(ctx context.Context) {
	a.db.ExecContext(ctx, `UPDATE downloads_da_assinatura SET estado = 'na fila' WHERE estado IN ('baixando', 'esperando')`)
	for {
		esperar := a.baixarPendentes(ctx)
		var espera <-chan time.Time
		if esperar {
			espera = time.After(esperaPelaConexao)
		}
		select {
		case <-ctx.Done():
			return
		case <-a.downloadsDaAssinaturaAcordar:
		case <-espera:
		}
	}
}

// baixarPendentes baixa a fila, um por vez. Devolve true se algum ficou esperando a conexão.
func (a *app) baixarPendentes(ctx context.Context) (esperando bool) {
	for ctx.Err() == nil {
		var chave string
		err := a.db.QueryRowContext(ctx, `SELECT chave FROM downloads_da_assinatura WHERE estado IN ('na fila', 'baixando', 'esperando')
			ORDER BY criado_em LIMIT 1`).Scan(&chave)
		if err != nil {
			return false
		}
		switch a.baixarUm(ctx, chave) {
		case errOutraReproducao:
			a.db.ExecContext(ctx, `UPDATE downloads_da_assinatura SET estado = 'esperando',
				mensagem = 'A assinatura está em uso: continua quando ela ficar livre.' WHERE chave = ?`, chave)
			return true
		}
	}
	return false
}

// baixarUm continua o download de um vídeo de onde ele parou. Erros definitivos ficam no banco.
func (a *app) baixarUm(ctx context.Context, chave string) error {
	d, err := a.downloadDe(ctx, chave)
	if err != nil {
		return nil
	}
	tipo, idTexto, _ := strings.Cut(chave, ":")
	id, _ := strconv.ParseInt(idTexto, 10, 64)
	falhar := func(msg string) error {
		a.db.ExecContext(ctx, `UPDATE downloads_da_assinatura SET estado = 'erro', mensagem = ? WHERE chave = ?`, msg, chave)
		a.avisos.avisar(aviso{Titulo: d.Titulo, Texto: "O download falhou: " + msg, Link: a.cfg.link + "/#/torrents", Urgente: true})
		return nil
	}
	f, err := a.fonteDe(ctx, tipo, id, "")
	if err != nil {
		return falhar(semSegredos(err).Error())
	}
	c, sair, err := a.conexao.entrarPorTras(ctx, chave)
	if err != nil {
		return errOutraReproducao
	}
	defer sair()
	c, cancelar := context.WithCancel(c)
	defer cancelar()
	a.baixandoMu.Lock()
	a.baixandoAgora, a.cancelarDownload = chave, cancelar
	a.baixandoMu.Unlock()
	defer func() {
		a.baixandoMu.Lock()
		a.baixandoAgora, a.cancelarDownload = "", nil
		a.baixandoMu.Unlock()
	}()

	os.MkdirAll(a.pastaDaAssinatura(), 0o755)
	caminho := a.caminhoDoDownload(d)
	arq, err := os.OpenFile(caminho, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return falhar(err.Error())
	}
	defer arq.Close()
	// Continua do tamanho do arquivo (o que foi gravado de verdade, não o que o banco diz).
	ja, _ := arq.Seek(0, io.SeekEnd)
	if d.Tamanho > 0 && ja >= d.Tamanho {
		a.db.ExecContext(ctx, `UPDATE downloads_da_assinatura SET estado = 'baixado', baixado = tamanho, mensagem = '' WHERE chave = ?`, chave)
		return nil
	}
	a.db.ExecContext(ctx, `UPDATE downloads_da_assinatura SET estado = 'baixando', mensagem = '' WHERE chave = ?`, chave)
	resp, err := a.pedirAoProvedor(c, f.url, fmt.Sprintf("bytes=%d-", ja))
	if err != nil {
		if c.Err() != nil {
			return a.depoisDeParar(ctx, chave)
		}
		return falhar("O provedor não respondeu: " + semSegredos(err).Error())
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusPartialContent:
		if _, total, ok := strings.Cut(resp.Header.Get("Content-Range"), "/"); ok {
			if n, err := strconv.ParseInt(total, 10, 64); err == nil {
				d.Tamanho = n
			}
		}
	case resp.StatusCode == http.StatusOK:
		// Sem Range: recomeça do zero.
		arq.Truncate(0)
		arq.Seek(0, io.SeekStart)
		ja = 0
		d.Tamanho = resp.ContentLength
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && ja > 0:
		a.db.ExecContext(ctx, `UPDATE downloads_da_assinatura SET estado = 'baixado', tamanho = ?, baixado = ?, mensagem = '' WHERE chave = ?`, ja, ja, chave)
		return nil
	default:
		return falhar(fmt.Sprintf("O provedor respondeu %d.", resp.StatusCode))
	}
	a.db.ExecContext(ctx, `UPDATE downloads_da_assinatura SET tamanho = ? WHERE chave = ?`, d.Tamanho, chave)

	baixado := ja
	buf := make([]byte, 256<<10)
	ultimo := time.Now()
	for {
		n, errLer := resp.Body.Read(buf)
		if n > 0 {
			if _, err := arq.Write(buf[:n]); err != nil {
				return falhar("Não consegui gravar no disco: " + err.Error())
			}
			baixado += int64(n)
			if time.Since(ultimo) > time.Second {
				ultimo = time.Now()
				a.db.ExecContext(ctx, `UPDATE downloads_da_assinatura SET baixado = ? WHERE chave = ?`, baixado, chave)
			}
		}
		if errLer == io.EOF {
			break
		}
		if errLer != nil {
			a.db.ExecContext(ctx, `UPDATE downloads_da_assinatura SET baixado = ? WHERE chave = ?`, baixado, chave)
			if c.Err() != nil {
				return a.depoisDeParar(ctx, chave)
			}
			log.Printf("download de %s parou: %v", chave, errLer)
			return errOutraReproducao // tenta de novo daqui a pouco, de onde parou
		}
	}
	if d.Tamanho > 0 && baixado < d.Tamanho {
		a.db.ExecContext(ctx, `UPDATE downloads_da_assinatura SET baixado = ? WHERE chave = ?`, baixado, chave)
		return errOutraReproducao
	}
	a.db.ExecContext(ctx, `UPDATE downloads_da_assinatura SET estado = 'baixado', tamanho = ?, baixado = ?, mensagem = '' WHERE chave = ?`, baixado, baixado, chave)
	a.avisos.avisar(aviso{Titulo: d.Titulo, Texto: "Baixado: dá para assistir sem usar a assinatura.", Link: a.cfg.link + "/#/torrents"})
	return nil
}

// depoisDeParar: o download foi cortado (alguém começou a assistir, ou o Lucas apagou).
func (a *app) depoisDeParar(ctx context.Context, chave string) error {
	if _, err := a.downloadDe(ctx, chave); err != nil {
		return nil // apagado
	}
	return errOutraReproducao
}
