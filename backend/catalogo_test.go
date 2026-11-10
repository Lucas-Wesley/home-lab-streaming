package main

import (
	"testing"
)

func TestNomeDaCategoria(t *testing.T) {
	casos := map[string]string{
		"FILMES | AÇÃO":               "Ação",
		"FILMES | LANÇAMENTOS CINEMA": "Lançamentos cinema",
		"CANAIS | +PLUTOTV BR":        "Pluto TV Brasil",
		"SÉRIES | HBOMAX":             "HBO Max",
		"CANAIS | UHD 4K":             "UHD 4K",
		"FILMES | FICÇÃO E FANTASIA":  "Ficção e fantasia",
		"Documentários":               "Documentários",
	}
	for bruto, want := range casos {
		if got := nomeDaCategoria(bruto); got != want {
			t.Errorf("nomeDaCategoria(%q) = %q, queria %q", bruto, got, want)
		}
	}
}

func TestTituloDoEpisodio(t *testing.T) {
	casos := map[string]string{
		"Um Trabalho do Inferno - S01E01 - Regras da loja": "Regras da loja",
		"Série S2E10: O fim":                               "O fim",
		"Episódio 3":                                       "Episódio 3",
		"Nome - S01E05":                                    "Nome - S01E05",
	}
	for bruto, want := range casos {
		if got := tituloDoEpisodio(bruto); got != want {
			t.Errorf("tituloDoEpisodio(%q) = %q, queria %q", bruto, got, want)
		}
	}
}

func TestSincronizacaoEBusca(t *testing.T) {
	e := novoAmbiente(t)

	var cats []categoria
	e.json(t, "/api/categorias/canal", &cats)
	// A categoria adulta fica escondida por padrão.
	if len(cats) != 1 || cats[0].Nome != "Noticias" || cats[0].Itens != 1 {
		t.Fatalf("categorias de canais: %+v", cats)
	}

	// Busca sem acento e sem caixa; o canal adulto não aparece.
	var r map[string][]item
	e.json(t, "/api/buscar?q=ACAO", &r)
	if len(r["filme"]) != 1 || r["filme"][0].Nome != "Ação Total (2024)" || r["filme"][0].Ano != "2024" {
		t.Fatalf("busca: %+v", r)
	}
	e.json(t, "/api/buscar?q=canal", &r)
	if len(r["canal"]) != 0 {
		t.Fatalf("o canal adulto apareceu: %+v", r["canal"])
	}

	// Mostrar adulto em Ajustes traz de volta.
	if status, _ := e.pedir(t, "PATCH", "/api/ajustes", map[string]bool{"mostrar_adulto": true}); status != 200 {
		t.Fatal(status)
	}
	e.json(t, "/api/buscar?q=canal", &r)
	if len(r["canal"]) != 1 {
		t.Fatalf("o canal adulto devia aparecer: %+v", r["canal"])
	}

	// Recentes: os filmes mais novos primeiro.
	var itens []item
	e.json(t, "/api/itens/filme", &itens)
	if len(itens) != 2 || itens[0].ID != 201 || itens[0].Nota != 6 || itens[1].Nota != 7.5 {
		t.Fatalf("recentes: %+v", itens)
	}
}

func TestSerieComEpisodios(t *testing.T) {
	e := novoAmbiente(t)
	var s struct {
		Item      item       `json:"item"`
		Detalhes  detalhes   `json:"detalhes"`
		Episodios []episodio `json:"episodios"`
	}
	e.json(t, "/api/series/300", &s)
	if s.Detalhes.Sinopse != "Uma série de teste." || s.Detalhes.Fundo == "" {
		t.Fatalf("detalhes: %+v", s.Detalhes)
	}
	if len(s.Episodios) != 2 || s.Episodios[0].Titulo != "O começo" || s.Episodios[1].Numero != 2 || s.Episodios[1].Temporada != 1 {
		t.Fatalf("episódios: %+v", s.Episodios)
	}
}

func TestFavoritos(t *testing.T) {
	e := novoAmbiente(t)
	for _, c := range []string{"canal/100", "filme/201", "serie/300", "filme/201"} { // marcar de novo não duplica
		if status, corpo := e.pedir(t, "PUT", "/api/favoritos/"+c, nil); status != 204 {
			t.Fatalf("%s: %d %s", c, status, corpo)
		}
	}
	var f map[string][]item
	e.json(t, "/api/favoritos", &f)
	if len(f["canal"]) != 1 || len(f["filme"]) != 1 || f["filme"][0].Nome != "Ação Total (2024)" || len(f["serie"]) != 1 {
		t.Fatalf("favoritos: %+v", f)
	}
	e.pedir(t, "DELETE", "/api/favoritos/filme/201", nil)
	var chaves []string
	e.json(t, "/api/favoritos/chaves", &chaves)
	if len(chaves) != 2 {
		t.Fatalf("chaves: %v", chaves)
	}
	if status, _ := e.pedir(t, "PUT", "/api/favoritos/episodio/1", nil); status != 404 {
		t.Fatalf("tipo inválido aceito: %d", status)
	}
}
