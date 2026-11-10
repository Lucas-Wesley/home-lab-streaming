package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

const chaveDeLegendaFalsa = "chave-do-opensubtitles"

// openSubtitlesFalso responde como a API: busca, download (que dá um link) e o arquivo, em
// Windows-1252 como muitas legendas antigas.
func openSubtitlesFalso(t *testing.T) (*httptest.Server, *[]string, *int) {
	t.Helper()
	var (
		mu        sync.Mutex
		buscas    []string
		downloads int
		srv       *httptest.Server
	)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/arquivo/123.srt" {
			w.Write([]byte("1\r\n00:00:01,500 --> 00:00:03,000\r\nOl\xe1, cora\xe7\xe3o\r\n"))
			return
		}
		if r.Header.Get("Api-Key") != chaveDeLegendaFalsa || r.Header.Get("User-Agent") == "" {
			http.Error(w, `{"message":"invalid key"}`, http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/infos/languages":
			fmt.Fprint(w, `{"data":[]}`)
		case "/api/v1/subtitles":
			buscas = append(buscas, r.URL.RawQuery)
			fmt.Fprint(w, `{"data":[
				{"attributes":{"language":"en","download_count":90000,"release":"Missao.Explosiva.2024.1080p.WEB-DL","files":[{"file_id":7}]}},
				{"attributes":{"language":"pt-BR","download_count":10,"ai_translated":true,"release":"Outra versao","files":[{"file_id":8}]}},
				{"attributes":{"language":"pt-BR","download_count":500,"release":"Missao Explosiva 2024 WEB-DL","files":[{"file_id":123}]}},
				{"attributes":{"language":"es","download_count":999999,"release":"Espanhol","files":[{"file_id":9}]}}]}`)
		case "/api/v1/download":
			var corpo struct {
				FileID int64 `json:"file_id"`
			}
			json.NewDecoder(r.Body).Decode(&corpo)
			if corpo.FileID != 123 {
				http.Error(w, `{"message":"arquivo errado"}`, http.StatusBadRequest)
				return
			}
			downloads++
			fmt.Fprintf(w, `{"link":"%s/arquivo/123.srt","remaining":99}`, srv.URL)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &buscas, &downloads
}

func TestLegendaDoFilmeNoPlayerENaTela(t *testing.T) {
	e := novoAmbiente(t)
	falso, buscas, downloads := openSubtitlesFalso(t)
	t.Setenv("OPENSUBTITLES_URL", falso.URL)

	if status, corpo := e.pedir(t, "PUT", "/api/opensubtitles", map[string]string{"chave": "errada"}); status != 400 || !strings.Contains(corpo, "recusou") {
		t.Fatalf("chave errada: %d %s", status, corpo)
	}
	if status, corpo := e.pedir(t, "PUT", "/api/opensubtitles", map[string]string{"chave": chaveDeLegendaFalsa}); status != 200 {
		t.Fatalf("guardar chave: %d %s", status, corpo)
	}

	var r struct {
		Opcoes    []opcaoDeLegenda  `json:"opcoes"`
		Escolhida *legendaEscolhida `json:"escolhida"`
	}
	e.json(t, "/api/legendas/filme:200?buscar", &r)
	// O provedor mandou tmdb_id 841: a busca vai pelo código, em ordem alfabética.
	if len(*buscas) != 1 || (*buscas)[0] != "languages=en%2Cpt-br%2Cpt-pt&tmdb_id=841" {
		t.Fatalf("busca: %v", *buscas)
	}
	// Português primeiro (a traduzida por máquina por último entre elas); espanhol fica de fora.
	if len(r.Opcoes) != 3 || r.Opcoes[0].ID != "os-123" || r.Opcoes[1].ID != "os-8" || r.Opcoes[2].Idioma != "en" || r.Escolhida != nil {
		t.Fatalf("opções: %+v", r.Opcoes)
	}

	for range 2 { // a segunda escolha não baixa de novo
		if status, corpo := e.pedir(t, "PUT", "/api/legendas/filme:200", r.Opcoes[0]); status != 200 || !strings.Contains(corpo, `"os-123"`) {
			t.Fatalf("escolher: %d %s", status, corpo)
		}
	}
	if *downloads != 1 {
		t.Fatalf("baixou %d vezes", *downloads)
	}
	status, vtt := e.pedir(t, "GET", "/api/legenda/os-123.vtt", nil)
	if status != 200 || !strings.HasPrefix(vtt, "WEBVTT") || !strings.Contains(vtt, "00:00:01.500 --> 00:00:03.000") || !strings.Contains(vtt, "Olá, coração") {
		t.Fatalf("WebVTT (em UTF-8): %d %q", status, vtt)
	}

	// Na tela: a legenda vai no DIDL e no cabeçalho que a Samsung pede junto com o vídeo.
	tv := novaTVFalsa(t)
	te := registrarTV(t, e, tv)
	if status, corpo := e.pedir(t, "POST", "/api/telas/"+url.PathEscape(te.ID)+"/tocar", map[string]any{"chave": "filme:200"}); status != 200 {
		t.Fatalf("tocar: %d %s", status, corpo)
	}
	envio := tv.corpo("SetAVTransportURI")
	srt := e.telas.URL + "/legenda/os-123.srt"
	if !strings.Contains(envio, "sec:CaptionInfoEx sec:type=&quot;srt&quot;&gt;"+srt) || !strings.Contains(envio, "pv:subtitleFileUri=&quot;"+srt) ||
		!strings.Contains(envio, "&amp;legenda=os-123") || strings.Contains(envio, "&#") {
		t.Fatalf("DIDL com legenda:\n%s", envio)
	}
	req, _ := http.NewRequest("HEAD", e.telas.URL+"/tela/filme/200.mp4?legenda=os-123", nil)
	req.Header.Set("getCaptionInfo.sec", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("CaptionInfo.sec") != srt {
		t.Fatalf("CaptionInfo.sec: %q", resp.Header.Get("CaptionInfo.sec"))
	}
	resp, err = http.Get(srt)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("SRT na porta das telas: %v %v", err, resp)
	}
	resp.Body.Close()

	// Tirar a legenda: o próximo envio vai sem.
	e.pedir(t, "DELETE", "/api/legendas/filme:200", nil)
	e.pedir(t, "POST", "/api/telas/"+url.PathEscape(te.ID)+"/tocar", map[string]any{"chave": "filme:200"})
	if strings.Contains(tv.corpo("SetAVTransportURI"), "CaptionInfo") {
		t.Fatal("legenda tirada continuou indo para a tela")
	}
}

func TestLegendaDoEpisodioEPorNome(t *testing.T) {
	e := novoAmbiente(t)
	falso, buscas, _ := openSubtitlesFalso(t)
	t.Setenv("OPENSUBTITLES_URL", falso.URL)
	e.pedir(t, "PUT", "/api/opensubtitles", map[string]string{"chave": chaveDeLegendaFalsa})
	e.pedir(t, "GET", "/api/series/300", nil) // guarda os episódios, com o tmdb_id que o provedor mandou

	e.pedir(t, "GET", "/api/legendas/episodio:301?buscar", nil)
	e.pedir(t, "GET", "/api/legendas/episodio:302?buscar", nil) // sem tmdb_id: pelo nome da série
	if len(*buscas) != 2 || (*buscas)[0] != "languages=en%2Cpt-br%2Cpt-pt&tmdb_id=62085" ||
		!strings.Contains((*buscas)[1], "episode_number=2") || !strings.Contains((*buscas)[1], "season_number=1") || !strings.Contains((*buscas)[1], "query=") {
		t.Fatalf("buscas: %v", *buscas)
	}
	if status, _ := e.pedir(t, "GET", "/api/legendas/canal:1?buscar", nil); status != 400 {
		t.Fatalf("canal não tem legenda: %d", status)
	}
}

func TestNomeDoLancamento(t *testing.T) {
	casos := map[string][2]string{
		"Dark.S01E01.720p.WEB":                       {"Dark", ""},
		"Night.of.the.Living.Dead.1968.1080p.BluRay": {"Night of the Living Dead", "1968"},
		"[SubsPlease] Frieren - 05 (1080p)":          {"Frieren", ""},
	}
	for arquivo, quer := range casos {
		nome, ano := nomeDoLancamento(arquivo, "")
		if nome != quer[0] || ano != quer[1] {
			t.Errorf("%s: %q %q", arquivo, nome, ano)
		}
	}
	if nomeDoCatalogo("Duna [L] (2021)") != "Duna" {
		t.Errorf("catálogo: %q", nomeDoCatalogo("Duna [L] (2021)"))
	}
}
