// Package provedorfalso imita um painel Xtream Codes, para os testes não dependerem da
// assinatura de verdade (a repo é pública: nenhum dado do provedor real entra nela).
//
// Como o real, ele redireciona cada vídeo para um "servidor de entrega" com um token e entrega
// o ao vivo em HLS (lista com endereços relativos) ou em TS contínuo.
package provedorfalso

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	Usuario = "usuario-teste"
	Senha   = "senha-teste"
)

// Video tem 1 MB de bytes previsíveis: o teste confere o pedaço que pediu pelo Range.
var Video = func() []byte {
	b := make([]byte, 1<<20)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}()

type Provedor struct {
	// Pedidos conta quantos vídeos foram pedidos ao "provedor" (sem contar a entrega).
	Pedidos atomic.Int64
	// Arquivo, se houver, é entregue no lugar de Video: um vídeo de verdade, para o ffprobe.
	Arquivo []byte
	// Lento faz a entrega demorar (para cortar uma leitura no meio), em nanossegundos.
	Lento atomic.Int64
	// CanaisExtras entram na categoria "ESPORTES" (12), e Guia é a programação de cada canal
	// (get_short_epg): os testes dos jogos do dia. Ficam vazios no resto.
	CanaisExtras []map[string]any
	Guia         map[int64][]Programa
}

// Programa é uma linha do guia; o painel manda título e descrição em base64.
type Programa struct {
	Titulo      string
	Inicio, Fim time.Time
}

func (p *Provedor) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/player_api.php", p.api)
	mux.HandleFunc("/movie/{u}/{s}/{arquivo}", p.redirecionar("movie"))
	mux.HandleFunc("/series/{u}/{s}/{arquivo}", p.redirecionar("series"))
	mux.HandleFunc("/live/{u}/{s}/{arquivo}", p.redirecionar("live"))
	mux.HandleFunc("/entrega/{tipo}/{arquivo}", p.entregar)
	mux.HandleFunc("/hls/{arquivo}", p.segmento)
	return mux
}

func autorizado(r *http.Request, u, s string) bool {
	return u == Usuario && s == Senha
}

func escrever(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (p *Provedor) api(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !autorizado(r, q.Get("username"), q.Get("password")) {
		escrever(w, map[string]any{"user_info": map[string]any{"auth": 0}})
		return
	}
	switch q.Get("action") {
	case "":
		// Os painéis misturam texto e número nos mesmos campos; o falso também.
		escrever(w, map[string]any{"user_info": map[string]any{
			"auth": 1, "status": "Active", "exp_date": fmt.Sprint(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC).Unix()),
			"max_connections": "1", "active_cons": 0, "is_trial": "0",
		}})
	case "get_live_categories":
		escrever(w, []map[string]any{
			{"category_id": "10", "category_name": "CANAIS | NOTICIAS"},
			{"category_id": "11", "category_name": "CANAIS | XXX ADULTOS"},
			{"category_id": "12", "category_name": "CANAIS | ESPORTES"},
		})
	case "get_vod_categories":
		escrever(w, []map[string]any{{"category_id": "20", "category_name": "FILMES | AÇÃO"}})
	case "get_series_categories":
		escrever(w, []map[string]any{{"category_id": "30", "category_name": "SÉRIES | NETFLIX"}})
	case "get_live_streams":
		escrever(w, append([]map[string]any{
			{"num": 1, "name": "Jornal 24h", "stream_id": 100, "stream_icon": "", "category_id": "10", "added": "1700000000"},
			{"num": 2, "name": "Canal Adulto", "stream_id": 101, "stream_icon": "", "category_id": "11", "added": "1700000000"},
		}, p.CanaisExtras...))
	case "get_short_epg":
		var id int64
		fmt.Sscan(q.Get("stream_id"), &id)
		lista := []map[string]any{}
		for _, pr := range p.Guia[id] {
			lista = append(lista, map[string]any{
				"title":           base64.StdEncoding.EncodeToString([]byte(pr.Titulo)),
				"description":     "",
				"start":           pr.Inicio.UTC().Format("2006-01-02 15:04:05"),
				"start_timestamp": fmt.Sprint(pr.Inicio.Unix()),
				"stop_timestamp":  fmt.Sprint(pr.Fim.Unix()),
			})
		}
		escrever(w, map[string]any{"epg_listings": lista})
	case "get_vod_streams":
		escrever(w, []map[string]any{
			{"num": 1, "name": "Missão Explosiva", "stream_id": 200, "stream_icon": "", "rating": "7.5", "category_id": "20", "container_extension": "mp4", "added": "1700000100"},
			{"num": 2, "name": "Ação Total (2024)", "stream_id": 201, "stream_icon": "", "rating": 6, "category_id": "20", "container_extension": "mkv", "added": "1700000200"},
		})
	case "get_series":
		escrever(w, []map[string]any{
			{"num": 1, "name": "Série de Teste", "series_id": 300, "cover": "", "rating": "8", "category_id": "30", "releaseDate": "2026-10-30", "last_modified": "1700000300"},
		})
	case "get_vod_info":
		escrever(w, map[string]any{"info": map[string]any{"plot": "Um filme de teste.", "duration_secs": 5400, "tmdb_id": 841, "genre": "Ação", "backdrop_path": []string{"https://exemplo.invalid/fundo.jpg"}}})
	case "get_series_info":
		escrever(w, map[string]any{
			"info": map[string]any{"plot": "Uma série de teste.", "backdrop_path": "https://exemplo.invalid/fundo.jpg"},
			"episodes": map[string]any{
				"1": []map[string]any{
					{"id": "301", "episode_num": 1, "title": "Série de Teste - S01E01 - O começo", "container_extension": "mp4", "season": 1, "info": map[string]any{"duration_secs": 2700, "tmdb_id": "62085"}},
					{"id": "302", "episode_num": "2", "title": "Série de Teste - S01E02 - O meio", "container_extension": "mp4", "season": "1", "info": map[string]any{}},
				},
			},
		})
	default:
		escrever(w, []any{})
	}
}

// redirecionar faz o que o painel real faz: manda o player para outro servidor, com um token.
func (p *Provedor) redirecionar(tipo string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !autorizado(r, r.PathValue("u"), r.PathValue("s")) {
			http.Error(w, "proibido", http.StatusForbidden)
			return
		}
		p.Pedidos.Add(1)
		http.Redirect(w, r, fmt.Sprintf("/entrega/%s/%s?token=abc", tipo, r.PathValue("arquivo")), http.StatusFound)
	}
}

func (p *Provedor) entregar(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("token") != "abc" {
		http.Error(w, "token", http.StatusForbidden)
		return
	}
	arquivo := r.PathValue("arquivo")
	switch {
	case strings.HasSuffix(arquivo, ".m3u8"):
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:7\n#EXTINF:6.0,\n/hls/7.ts\n#EXTINF:6.0,\n/hls/8.ts\n")
	case strings.HasSuffix(arquivo, ".ts") && r.PathValue("tipo") == "live":
		// Ao vivo em TS: manda pedaços até o cliente desistir.
		w.Header().Set("Content-Type", "video/mp2t")
		for i := 0; i < 50 && r.Context().Err() == nil; i++ {
			w.Write(bytes.Repeat([]byte{0x47}, 188*10))
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
		conteudo := Video
		if p.Arquivo != nil {
			conteudo = p.Arquivo
		}
		if lento := time.Duration(p.Lento.Load()); lento > 0 {
			select {
			case <-time.After(lento):
			case <-r.Context().Done():
				return
			}
		}
		http.ServeContent(w, r, arquivo, time.Unix(1700000000, 0), bytes.NewReader(conteudo))
	}
}

func (p *Provedor) segmento(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "video/mp2t")
	w.Write(bytes.Repeat([]byte{0x47}, 188))
}
