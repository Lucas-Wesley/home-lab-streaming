package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Lucas-Wesley/home-lab-streaming/provedorfalso"
)

// ambiente sobe o app com um banco novo e um provedor falso já configurado e sincronizado.
type ambiente struct {
	a        *app
	api      *httptest.Server
	telas    *httptest.Server
	provedor *provedorfalso.Provedor
	origem   *httptest.Server
}

func novoAmbiente(t *testing.T) *ambiente {
	t.Helper()
	// Nada de serviços de verdade nos testes: o Claude (que gastaria a assinatura) e o Whisper só
	// existem quando o teste põe um falso.
	t.Setenv("CLAUDE_BIN", filepath.Join(t.TempDir(), "sem-claude"))
	t.Setenv("WHISPER_URL", "")
	t.Setenv("PARAKEET_URL", "")
	t.Setenv("FLASHCARDS_URL", "")
	db, err := abrirBanco(filepath.Join(t.TempDir(), "teste.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	p := &provedorfalso.Provedor{}
	origem := httptest.NewServer(p.Handler())
	t.Cleanup(origem.Close)

	telas := httptest.NewUnstartedServer(nil)
	a, err := novoApp(db, config{urlDasTelas: "http://" + telas.Listener.Addr().String(), pastaDosTorrents: filepath.Join(t.TempDir(), "torrents"), semDHT: true})
	if err != nil {
		t.Fatal(err)
	}
	a.emFundo = func(f func()) { f() }
	a.telas.esperaDoStop = time.Millisecond
	telas.Config.Handler = a.servidorDasTelas()
	telas.Start()
	t.Cleanup(telas.Close)

	web := fstest.MapFS{"index.html": {Data: []byte(`<div id="app"></div>`)}}
	api := httptest.NewServer(newServer(a, web, "teste"))
	t.Cleanup(api.Close)

	e := &ambiente{a: a, api: api, telas: telas, provedor: p, origem: origem}
	texto := "Usuário: " + provedorfalso.Usuario + "\nSenha: " + provedorfalso.Senha + "\nDNS: " + origem.URL + "\n"
	if status, corpo := e.pedir(t, "PUT", "/api/acesso", map[string]string{"texto": texto}); status != 200 {
		t.Fatalf("acesso: %d %s", status, corpo)
	}
	// O PUT já sincronizou o catálogo (emFundo roda na hora nos testes).
	if e := a.estadoDoCatalogo().Erro; e != "" {
		t.Fatal("sincronização:", e)
	}
	return e
}

func (e *ambiente) pedir(t *testing.T, metodo, caminho string, corpo any) (int, string) {
	t.Helper()
	var leitor io.Reader
	if corpo != nil {
		b, _ := json.Marshal(corpo)
		leitor = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(metodo, e.api.URL+caminho, leitor)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (e *ambiente) json(t *testing.T, caminho string, destino any) {
	t.Helper()
	status, corpo := e.pedir(t, "GET", caminho, nil)
	if status != 200 {
		t.Fatalf("GET %s: %d %s", caminho, status, corpo)
	}
	if err := json.Unmarshal([]byte(corpo), destino); err != nil {
		t.Fatal(err)
	}
}
