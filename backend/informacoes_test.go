package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestBuscarMaisInformacoes(t *testing.T) {
	e := novoAmbiente(t)
	t.Setenv("TMDB_URL", tmdbFalso(t).URL)
	e.pedir(t, "PUT", "/api/tmdb", map[string]string{"chave": "0123456789abcdef0123456789abcdef"})

	var r struct {
		Disponivel  bool                 `json:"disponivel"`
		Informacoes *informacoesDoTitulo `json:"informacoes"`
	}
	e.json(t, "/api/titulos/serie/1396/informacoes", &r)
	if r.Informacoes != nil {
		t.Fatalf("antes de pesquisar: %+v", r)
	}
	args := claudeFalso(t, `{"resumo":"Um professor de química vira fabricante de drogas.","ficha":[{"rotulo":"Criação","valor":"Vince Gilligan"}],"fontes":[{"titulo":"Wikipédia","url":"https://pt.wikipedia.org/wiki/Breaking_Bad"}]}`)
	status, corpo := e.pedir(t, "POST", "/api/titulos/serie/1396/informacoes", nil)
	if status != http.StatusOK {
		t.Fatalf("pesquisar: %d %s", status, corpo)
	}
	a := args()
	for _, quer := range []string{"--model\nsonnet", "--allowedTools\nWebSearch,WebFetch", "Breaking Bad (série", "2008", "não invente"} {
		if !strings.Contains(a, quer) {
			t.Fatalf("pedido sem %q:\n%s", quer, a)
		}
	}
	// Guardada: a página lê sem pesquisar de novo.
	e.json(t, "/api/titulos/serie/1396/informacoes", &r)
	if r.Informacoes == nil || r.Informacoes.Ficha[0].Valor != "Vince Gilligan" || r.Informacoes.Modelo != "sonnet" || len(r.Informacoes.Fontes) != 1 {
		b, _ := json.Marshal(r)
		t.Fatalf("guardada: %s", b)
	}
	if status, _ := e.pedir(t, "POST", "/api/titulos/canal/1/informacoes", nil); status != http.StatusBadRequest {
		t.Errorf("canal: %d", status)
	}
}
