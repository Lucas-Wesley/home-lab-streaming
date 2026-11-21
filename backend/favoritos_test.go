package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

// O catálogo falso tem o canal 100, os filmes 200 e 201 e a série 300.
func TestPastasDeFavoritos(t *testing.T) {
	e := novoAmbiente(t)

	var p pasta
	status, corpo := e.pedir(t, "POST", "/api/favoritos/pastas", map[string]string{"nome": "  ING - Iniciante "})
	if status != http.StatusOK {
		t.Fatalf("criar pasta: %d %s", status, corpo)
	}
	json.Unmarshal([]byte(corpo), &p)
	if p.Nome != "ING - Iniciante" {
		t.Fatalf("nome da pasta: %q", p.Nome)
	}
	if s, r := e.pedir(t, "POST", "/api/favoritos/pastas", map[string]string{"nome": "ing - iniciante"}); s != http.StatusConflict {
		t.Fatalf("pasta de nome repetido: %d %s", s, r)
	}
	if s, _ := e.pedir(t, "POST", "/api/favoritos/pastas", map[string]string{"nome": " "}); s != http.StatusBadRequest {
		t.Fatalf("pasta sem nome: %d", s)
	}

	if s, r := e.pedir(t, "PUT", "/api/favoritos/serie/300", map[string]int64{"pasta": p.ID}); s != http.StatusNoContent {
		t.Fatalf("favoritar na pasta: %d %s", s, r)
	}
	if s, r := e.pedir(t, "PUT", "/api/favoritos/filme/200", nil); s != http.StatusNoContent {
		t.Fatalf("favoritar sem corpo: %d %s", s, r)
	}
	if s, _ := e.pedir(t, "PUT", "/api/favoritos/serie/300", map[string]int64{"pasta": 99}); s != http.StatusNotFound {
		t.Fatalf("pasta que não existe: %d", s)
	}

	var estado struct {
		Marcado bool  `json:"marcado"`
		Pasta   int64 `json:"pasta"`
	}
	e.json(t, "/api/favoritos/serie/300", &estado)
	if !estado.Marcado || estado.Pasta != p.ID {
		t.Fatalf("estado: %+v", estado)
	}
	e.json(t, "/api/favoritos/canal/100", &estado)
	if estado.Marcado || estado.Pasta != 0 {
		t.Fatalf("estado de não favorito: %+v", estado)
	}

	var pagina struct {
		Pastas []struct {
			pasta
			Itens []item `json:"itens"`
		} `json:"pastas"`
		Itens favoritosPorTipo `json:"itens"`
	}
	e.json(t, "/api/favoritos", &pagina)
	if len(pagina.Pastas) != 1 || len(pagina.Pastas[0].Itens) != 1 || pagina.Pastas[0].Itens[0].ID != 300 {
		t.Fatalf("pastas na página: %+v", pagina.Pastas)
	}
	if len(pagina.Itens.Filme) != 1 || pagina.Itens.Filme[0].ID != 200 || len(pagina.Itens.Serie) != 0 {
		t.Fatalf("sem pasta na página: %+v", pagina.Itens)
	}

	// Um PUT de novo só muda a pasta.
	if s, r := e.pedir(t, "PUT", "/api/favoritos/serie/300", map[string]int64{"pasta": 0}); s != http.StatusNoContent {
		t.Fatalf("mover para sem pasta: %d %s", s, r)
	}
	e.json(t, "/api/favoritos", &pagina)
	if len(pagina.Pastas[0].Itens) != 0 || len(pagina.Itens.Serie) != 1 {
		t.Fatalf("depois de mover: %+v", pagina)
	}

	var renomeada pasta
	status, corpo = e.pedir(t, "PATCH", "/api/favoritos/pastas/1", map[string]string{"nome": "ING - Intermediário"})
	if status != http.StatusOK {
		t.Fatalf("renomear: %d %s", status, corpo)
	}
	json.Unmarshal([]byte(corpo), &renomeada)
	if renomeada.Nome != "ING - Intermediário" {
		t.Fatalf("renomeada: %+v", renomeada)
	}
	if s, _ := e.pedir(t, "PATCH", "/api/favoritos/pastas/99", map[string]string{"nome": "x"}); s != http.StatusNotFound {
		t.Fatalf("renomear a que não existe: %d", s)
	}

	// Apagar a pasta devolve os itens dela para "sem pasta", sem desfavoritar.
	if s, r := e.pedir(t, "PUT", "/api/favoritos/serie/300", map[string]int64{"pasta": p.ID}); s != http.StatusNoContent {
		t.Fatalf("de volta à pasta: %d %s", s, r)
	}
	if s, r := e.pedir(t, "DELETE", "/api/favoritos/pastas/1", nil); s != http.StatusNoContent {
		t.Fatalf("apagar pasta: %d %s", s, r)
	}
	if s, _ := e.pedir(t, "DELETE", "/api/favoritos/pastas/1", nil); s != http.StatusNotFound {
		t.Fatalf("apagar de novo: %d", s)
	}
	var chaves []string
	e.json(t, "/api/favoritos/chaves", &chaves)
	if len(chaves) != 2 {
		t.Fatalf("chaves depois de apagar a pasta: %v", chaves)
	}
	e.json(t, "/api/favoritos", &pagina)
	if len(pagina.Pastas) != 0 || len(pagina.Itens.Serie) != 1 || len(pagina.Itens.Filme) != 1 {
		t.Fatalf("página sem pastas: %+v", pagina)
	}

	if s, r := e.pedir(t, "DELETE", "/api/favoritos/serie/300", nil); s != http.StatusNoContent {
		t.Fatalf("desfavoritar: %d %s", s, r)
	}
	e.json(t, "/api/favoritos/chaves", &chaves)
	if len(chaves) != 1 || chaves[0] != "filme:200" {
		t.Fatalf("chaves no fim: %v", chaves)
	}
}

// S58: título do TMDB fora da assinatura também vai para os favoritos, com pasta, e some ao desmarcar.
func TestFavoritoDeTituloDoTMDB(t *testing.T) {
	e := novoAmbiente(t)
	if s, _ := e.pedir(t, "PUT", "/api/favoritos/tmdb-filme/777", nil); s != http.StatusBadRequest {
		t.Fatalf("sem nome: %d", s)
	}
	dados := map[string]any{"pasta": 0, "nome": "O Silêncio do Lago", "capa": "http://x/c.jpg", "ano": "1993", "nota": 7.4}
	if s, r := e.pedir(t, "PUT", "/api/favoritos/tmdb-filme/777", dados); s != http.StatusNoContent {
		t.Fatalf("favoritar: %d %s", s, r)
	}
	if s, r := e.pedir(t, "PUT", "/api/favoritos/filme/200", nil); s != http.StatusNoContent {
		t.Fatalf("favoritar do catálogo: %d %s", s, r)
	}
	var chaves []string
	e.json(t, "/api/favoritos/chaves", &chaves)
	if len(chaves) != 2 {
		t.Fatalf("chaves: %v", chaves)
	}
	var pagina struct {
		Itens favoritosPorTipo `json:"itens"`
	}
	e.json(t, "/api/favoritos", &pagina)
	if len(pagina.Itens.Filme) != 2 {
		t.Fatalf("filmes: %+v", pagina.Itens.Filme)
	}
	var achou item
	for _, it := range pagina.Itens.Filme {
		if it.Chave != "" {
			achou = it
		}
	}
	if achou.Nome != "O Silêncio do Lago" || achou.Chave != "tmdb-filme:777" || achou.Pagina != "#/titulo/filme/777" || achou.Ano != "1993" {
		t.Fatalf("item do TMDB: %+v", achou)
	}
	// Só mudar a pasta, sem repetir o nome, mantém os dados.
	if s, r := e.pedir(t, "PUT", "/api/favoritos/tmdb-filme/777", map[string]int64{"pasta": 0}); s != http.StatusNoContent {
		t.Fatalf("de novo: %d %s", s, r)
	}
	if s, r := e.pedir(t, "DELETE", "/api/favoritos/tmdb-filme/777", nil); s != http.StatusNoContent {
		t.Fatalf("desfavoritar: %d %s", s, r)
	}
	e.json(t, "/api/favoritos", &pagina)
	if len(pagina.Itens.Filme) != 1 {
		t.Fatalf("depois de desmarcar: %+v", pagina.Itens.Filme)
	}
}
