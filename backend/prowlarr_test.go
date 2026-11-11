package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const chaveFalsa = "chave-do-prowlarr-de-teste"

// prowlarrFalso responde como o Prowlarr: lista de indexadores, busca e o link de download, que
// devolve um .torrent (indexador 1) ou redireciona para um magnet (indexador 2).
func prowlarrFalso(t *testing.T, ficha []byte, buscas *[]string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	var mu sync.Mutex // as buscas de um título chegam juntas
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chave := r.Header.Get("X-Api-Key")
		if strings.HasSuffix(r.URL.Path, "/download") {
			chave = r.URL.Query().Get("apikey")
		}
		if chave != chaveFalsa {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/indexer":
			fmt.Fprint(w, `[{"id":1,"name":"Um","enable":true,"tags":[]},{"id":2,"name":"Dois","enable":true,"tags":[7]},{"id":3,"name":"Desligado","enable":false}]`)
		case "/api/v1/tag":
			fmt.Fprint(w, `[{"id":7,"label":"cloudflare"}]`)
		case "/api/v1/search":
			mu.Lock()
			*buscas = append(*buscas, r.URL.RawQuery)
			mu.Unlock()
			link := srv.URL + "/1/download?apikey=" + chaveFalsa + "&link=abc&file=Serie"
			json.NewEncoder(w).Encode([]map[string]any{
				{"title": "Serie Teste S01 1080p x265", "indexer": "Um", "size": 900, "seeders": 3, "protocol": "torrent",
					"downloadUrl": link, "categories": []map[string]int{{"id": 5000}}},
				{"title": "Serie Teste S01 720p &amp; mais", "indexer": "Dois", "size": 500, "seeders": 40, "protocol": "torrent",
					"downloadUrl": srv.URL + "/2/download?apikey=" + chaveFalsa, "categories": []map[string]int{{"id": 5030}}},
				{"title": "Serie Teste nzb", "indexer": "Usenet", "protocol": "usenet", "downloadUrl": link},
			})
		case "/1/download":
			w.Write(ficha)
		case "/2/download":
			http.Redirect(w, r, "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Serie+720p", http.StatusMovedPermanently)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProwlarrBuscarEAdicionar(t *testing.T) {
	e := novoAmbiente(t)
	mi, _ := torrentDeTeste(t)
	var ficha bytes.Buffer
	mi.Write(&ficha)
	var buscas []string
	falso := prowlarrFalso(t, ficha.Bytes(), &buscas)

	if status, _ := e.pedir(t, "GET", "/api/procurar?q=serie", nil); status != http.StatusConflict {
		t.Fatalf("sem Prowlarr a busca devia pedir configuração: %d", status)
	}
	if status, corpo := e.pedir(t, "PUT", "/api/prowlarr", configDoProwlarr{URL: falso.URL, Chave: "errada"}); status != 400 || !strings.Contains(corpo, "recusou a chave") {
		t.Fatalf("chave errada: %d %s", status, corpo)
	}
	status, corpo := e.pedir(t, "PUT", "/api/prowlarr", configDoProwlarr{URL: falso.URL + "/", Chave: chaveFalsa})
	if status != 200 || !strings.Contains(corpo, `"indexadores":2`) || strings.Contains(corpo, chaveFalsa) {
		t.Fatalf("guardar Prowlarr: %d %s", status, corpo)
	}

	var resultados []resultadoDaBusca
	e.json(t, "/api/procurar?q=Serie+Teste&tipo=serie", &resultados)
	if len(buscas) != 1 || !strings.Contains(buscas[0], "categories=5000") || !strings.Contains(buscas[0], "query=Serie+Teste") {
		t.Fatalf("busca enviada ao Prowlarr: %v", buscas)
	}
	if len(resultados) != 2 || resultados[0].Semeadores != 40 || resultados[1].Tipo != "serie" {
		t.Fatalf("resultados (sem usenet, mais semeadores primeiro): %+v", resultados)
	}
	if r := resultados[0]; r.Titulo != "Serie Teste S01 720p & mais" || r.Indexador != "Dois" || r.Tamanho != 500 {
		t.Fatalf("nome (sem entidades HTML), indexador e tamanho: %+v", r)
	}
	if q := strings.Join(resultados[1].Qualidade, ","); q != "1080p,H.265" {
		t.Fatalf("qualidade: %q", q)
	}
	for _, r := range resultados {
		if strings.Contains(r.Link, chaveFalsa) {
			t.Fatal("a chave do Prowlarr foi para o navegador:", r.Link)
		}
	}

	// .torrent pelo link do Prowlarr: a ficha já vem com a lista de arquivos.
	status, corpo = e.pedir(t, "POST", "/api/torrents", map[string]string{"prowlarr": resultados[1].Link})
	if status != 200 || !strings.Contains(corpo, mi.HashInfoBytes().HexString()) {
		t.Fatalf("adicionar .torrent do Prowlarr: %d %s", status, corpo)
	}
	// Redirecionamento para magnet.
	e.a.emFundo = func(func()) {} // ninguém compartilha esse magnet; buscar a ficha só faria esperar
	status, corpo = e.pedir(t, "POST", "/api/torrents", map[string]string{"prowlarr": resultados[0].Link})
	if status != 200 || !strings.Contains(corpo, "0123456789abcdef") {
		t.Fatalf("adicionar magnet do Prowlarr: %d %s", status, corpo)
	}
	// Só links de download do próprio Prowlarr: o servidor não busca endereço qualquer.
	for _, link := range []string{"http://outro.exemplo/1/download", falso.URL + "/api/v1/indexer"} {
		if status, _ := e.pedir(t, "POST", "/api/torrents", map[string]string{"prowlarr": link}); status != 400 {
			t.Fatalf("link de fora devia ser recusado: %s → %d", link, status)
		}
	}
}

func TestQualidadeDoTitulo(t *testing.T) {
	casos := map[string]string{
		"Filme.2024.2160p.WEB-DL.HDR.HEVC": "4K,H.265,HDR",
		"Filme 2024 1080p DUAL Dublado":    "1080p,Dual áudio,Português",
		"Filme (1938)":                     "",
	}
	for titulo, quer := range casos {
		if q := strings.Join(qualidadeDe(titulo), ","); q != quer {
			t.Errorf("%s: %q, queria %q", titulo, q, quer)
		}
	}
}
