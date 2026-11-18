package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestBuscasSalvas(t *testing.T) {
	e := novoAmbiente(t)
	salvar := func(texto, onde, endereco string) (int, buscaSalva) {
		var b buscaSalva
		status, r := e.pedir(t, "POST", "/api/buscas-salvas", map[string]string{"texto": texto, "onde": onde, "endereco": endereco})
		if status == http.StatusOK {
			json.Unmarshal([]byte(r), &b)
		}
		return status, b
	}
	if s, _ := salvar("Filme Inventado", "inicio", "#/inicio?q=Filme%20Inventado"); s != http.StatusOK {
		t.Fatalf("salvar: %d", s)
	}
	_, outra := salvar("Coisa Rara 1999", "torrents", "#/torrents?q=Coisa%20Rara%201999")
	// A mesma busca de novo não duplica.
	_, denovo := salvar(" Filme Inventado ", "inicio", "#/inicio?q=Filme%20Inventado")

	var lista []buscaSalva
	e.json(t, "/api/buscas-salvas", &lista)
	if len(lista) != 2 || lista[0].ID != denovo.ID || lista[0].Texto != "Filme Inventado" || lista[1].Onde != "torrents" {
		t.Fatalf("lista: %+v", lista)
	}

	for _, ruim := range [][3]string{{" ", "inicio", "#/inicio"}, {"x", "canal", "#/canais"}, {"x", "inicio", "https://fora"}} {
		if s, _ := salvar(ruim[0], ruim[1], ruim[2]); s != http.StatusBadRequest {
			t.Errorf("%v: %d", ruim, s)
		}
	}

	if s, _ := e.pedir(t, "DELETE", fmt.Sprintf("/api/buscas-salvas/%d", outra.ID), nil); s != http.StatusNoContent {
		t.Errorf("apagar: %d", s)
	}
	e.json(t, "/api/buscas-salvas", &lista)
	if len(lista) != 1 {
		t.Errorf("depois de apagar: %+v", lista)
	}
}
