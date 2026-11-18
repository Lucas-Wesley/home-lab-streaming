package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"
)

// Downloads (S45): a página mostra só o que está no servidor ou baixando, vídeo por vídeo, com o nome
// do título (do TMDB) em vez do nome do torrent. Os torrents assistidos sem baixar não aparecem.

type videoBaixado struct {
	ID        int64   `json:"id"`
	Hash      string  `json:"hash"`
	Titulo    string  `json:"titulo"`  // o nome do TMDB, ou o do torrent
	Torrent   string  `json:"torrent"` // o nome do torrent (ou do arquivo, num pacote)
	Capa      string  `json:"capa,omitempty"`
	TMDB      string  `json:"tmdb,omitempty"` // filme/123: leva à página do título
	Temporada int     `json:"temporada,omitempty"`
	Episodio  int     `json:"episodio,omitempty"`
	Tamanho   int64   `json:"tamanho"`
	Baixado   int64   `json:"baixado"`
	Estado    string  `json:"estado"` // baixando ou baixado
	Posicao   float64 `json:"posicao,omitempty"`
	// Legenda dupla (S46): estado da gravação e quanto já foi gravado (0 a 1).
	Dupla      string  `json:"dupla,omitempty"`
	DuplaParte float64 `json:"dupla_parte,omitempty"`
}

func (a *app) handleDownloadsDeTorrent(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT f.id, f.hash, f.nome, f.tamanho, f.temporada, f.episodio, f.download,
			COALESCE(p.posicao, 0), t.titulo, t.nome_do_titulo, t.capa, t.tmdb,
			(SELECT COUNT(*) FROM torrent_arquivos v WHERE v.hash = f.hash AND v.tipo = 'video')
		FROM torrent_arquivos f JOIN torrents t ON t.hash = f.hash
		LEFT JOIN progresso p ON p.chave = 'arquivo:' || f.id
		WHERE f.tipo = 'video' AND f.download != ''
		ORDER BY f.download = 'baixado', f.id DESC`)
	if err != nil {
		falha(w, err)
		return
	}
	defer rows.Close()
	lista := []videoBaixado{}
	semNome := map[string]string{}
	for rows.Next() {
		var v videoBaixado
		var nomeDoArquivo, nomeDoTorrent, nomeDoTitulo string
		var videos int
		if err := rows.Scan(&v.ID, &v.Hash, &nomeDoArquivo, &v.Tamanho, &v.Temporada, &v.Episodio, &v.Estado,
			&v.Posicao, &nomeDoTorrent, &nomeDoTitulo, &v.Capa, &v.TMDB, &videos); err != nil {
			falha(w, err)
			return
		}
		v.Titulo, v.Torrent = primeiro(nomeDoTitulo, nomeDoTorrent), nomeDoTorrent
		if videos > 1 {
			v.Torrent = nomeDoArquivo
		}
		if v.Estado == "baixando" {
			v.Baixado = a.downloads.progresso(v.ID)
		} else {
			v.Baixado = v.Tamanho
		}
		if nomeDoTitulo == "" && v.TMDB != "" {
			semNome[v.Hash] = v.TMDB
		}
		if d, err := a.legendaDuplaDe(r.Context(), fmt.Sprintf("arquivo:%d", v.ID)); err == nil {
			v.Dupla = d.Estado
			if d.Duracao > 0 {
				v.DuplaParte = math.Min(1, d.Gravado/d.Duracao)
			}
		}
		lista = append(lista, v)
	}
	if err := rows.Err(); err != nil {
		falha(w, err)
		return
	}
	// Torrents baixados antes do nome do TMDB: nomeia por trás; a página pega na próxima leitura.
	for hash, tmdb := range semNome {
		go a.nomearPeloTMDB(hash, tmdb)
	}
	writeJSON(w, http.StatusOK, lista)
}

var nomeando sync.Map // hash → em andamento, para a leitura a cada 4 s não repetir o pedido

// nomearPeloTMDB guarda o nome (e a capa e o ano, se faltarem) do título a que o torrent pertence.
func (a *app) nomearPeloTMDB(hash, tmdb string) {
	if _, ja := nomeando.LoadOrStore(hash, true); ja {
		return
	}
	defer nomeando.Delete(hash)
	m := reTMDBGuardado.FindStringSubmatch(tmdb)
	chave := a.chaveDoTMDB()
	if m == nil || chave == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	t, err := a.tituloCompleto(ctx, chave, m[1], atoi(m[2]))
	if err != nil || t.Titulo == "" {
		return
	}
	a.db.ExecContext(ctx, `UPDATE torrents SET nome_do_titulo = ?,
		capa = CASE WHEN capa = '' THEN ? ELSE capa END, ano = CASE WHEN ano = '' THEN ? ELSE ano END WHERE hash = ?`,
		t.Titulo, t.Capa, t.Ano, hash)
}
