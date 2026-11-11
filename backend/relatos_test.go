package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestTitulosParaVerificar(t *testing.T) {
	e := novoAmbiente(t)
	id := "0b6f7c1e-2d3a-4b5c-8d9e-0f1a2b3c4d5e"
	corpo := map[string]any{
		"tipo": "serie", "tmdb": 75191, "titulo": "The Terror", "ano": "2018", "temporada": 1, "episodio": 3,
		"texto":    "Sem capa e sem torrents.",
		"contexto": map[string]any{"assinatura": []map[string]any{{"id": 8719, "nome": "Terror"}}, "busca": map[string]any{"feitas": 20, "total": 20}},
	}
	if status, r := e.pedir(t, "PUT", "/api/relatos/"+id, corpo); status != http.StatusOK {
		t.Fatalf("guardar: %d %s", status, r)
	}
	// O envio repetido não duplica.
	e.pedir(t, "PUT", "/api/relatos/"+id, corpo)

	var lista []relato
	e.json(t, "/api/relatos?estado=nova", &lista)
	if len(lista) != 1 || lista[0].TMDB != 75191 || lista[0].Episodio != 3 || lista[0].Estado != "nova" || lista[0].Versao == "" || !strings.HasSuffix(lista[0].CriadaEm, "Z") {
		t.Fatalf("lista: %+v", lista)
	}
	var ctx struct {
		Busca struct{ Feitas int } `json:"busca"`
	}
	if json.Unmarshal(lista[0].Contexto, &ctx) != nil || ctx.Busca.Feitas != 20 {
		t.Errorf("contexto: %s", lista[0].Contexto)
	}

	if status, r := e.pedir(t, "PATCH", "/api/relatos/"+id, map[string]string{"estado": "feita", "resposta": "Ligado ao título certo."}); status != http.StatusOK {
		t.Fatalf("marcar: %d %s", status, r)
	}
	e.json(t, "/api/relatos?estado=nova", &lista)
	if len(lista) != 0 {
		t.Errorf("feito ainda na lista dos novos: %+v", lista)
	}
	e.json(t, "/api/relatos", &lista)
	if len(lista) != 1 || lista[0].Resposta != "Ligado ao título certo." {
		t.Errorf("todos: %+v", lista)
	}

	for _, ruim := range []map[string]any{
		{"tipo": "canal", "tmdb": 1, "titulo": "X"},
		{"tipo": "filme", "tmdb": 0, "titulo": "X"},
		{"tipo": "filme", "tmdb": 1, "titulo": " "},
	} {
		if status, _ := e.pedir(t, "PUT", "/api/relatos/1234567890ab", ruim); status != http.StatusBadRequest {
			t.Errorf("%v: %d", ruim, status)
		}
	}
	if status, _ := e.pedir(t, "DELETE", "/api/relatos/"+id, nil); status != http.StatusNoContent {
		t.Errorf("apagar: %d", status)
	}
}
