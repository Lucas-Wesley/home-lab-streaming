package main

import (
	"net/http"
	"testing"
)

func TestJuntarCatalogoETMDB(t *testing.T) {
	titulos := []tituloDoTMDB{
		{Tipo: "filme", ID: 438631, Titulo: "Duna", Original: "Dune", Ano: "2021", Capa: "duna.jpg", votos: 15917},
		{Tipo: "filme", ID: 841, Titulo: "Duna", Original: "Dune", Ano: "1984", Capa: "duna84.jpg", votos: 3610},
		{Tipo: "filme", ID: 693134, Titulo: "Duna: Parte Dois", Original: "Dune: Part Two", Ano: "2024", Capa: "parte2.jpg", votos: 8933},
		{Tipo: "filme", ID: 999, Titulo: "Duna Obscuro", Ano: "2001", votos: 100},                                   // sem capa e fora da assinatura
		{Tipo: "filme", ID: 1464568, Titulo: "Duna", Ano: "2021", Capa: "outra.jpg", popularidade: 1},               // sem votos nem procura
		{Tipo: "filme", ID: 1170608, Titulo: "Duna: Parte Três", Ano: "2026", Capa: "tres.jpg", popularidade: 25.9}, // vai estrear
		{Tipo: "serie", ID: 90228, Titulo: "Duna: A Profecia", Ano: "2024", Capa: "profecia.jpg", votos: 681},
		{Tipo: "serie", ID: 1396, Titulo: "Breaking Bad", Ano: "2008", Capa: "bb.jpg", votos: 19076},
	}
	catalogo := []item{
		{Tipo: "filme", ID: 1, Nome: "Duna (2021)"},
		{Tipo: "filme", ID: 2, Nome: "Duna [L] 2021", Capa: "c2.jpg"},
		{Tipo: "filme", ID: 3, Nome: "Dune - Part Two 4K", Ano: "2024"},
		{Tipo: "filme", ID: 4, Nome: "Duna: Os Bastidores"}, // não é nenhum dos títulos
	}
	got := juntar("filme", titulos, catalogo)
	type esperado struct{ nome, origem, pagina string }
	want := []esperado{
		{"Duna", "assinatura", "#/titulo/filme/438631"}, // as duas cópias de 2021 viram uma
		{"Duna", "torrent", "#/titulo/filme/841"},       // a de 1984 não está na assinatura
		{"Duna: Parte Dois", "assinatura", "#/titulo/filme/693134"},
		{"Duna: Parte Três", "torrent", "#/titulo/filme/1170608"},
		{"Duna: Os Bastidores", "assinatura", ""}, // fica como veio do catálogo (sem ano: o subtítulo não basta)
	}
	if len(got) != len(want) {
		t.Fatalf("junta: %+v", got)
	}
	for i, w := range want {
		if got[i].Nome != w.nome || got[i].Origem != w.origem || got[i].Pagina != w.pagina {
			t.Errorf("%d: %+v, queria %+v", i, got[i], w)
		}
	}
	if got[4].ID != 4 {
		t.Errorf("o item do catálogo perdeu o id: %+v", got[4])
	}
	// O nome do catálogo com subtítulo e o mesmo ano é a série; com o código já conhecido, também.
	series := juntar("serie", titulos, []item{
		{Tipo: "serie", ID: 83, Nome: "Breaking Bad: A Química do Mal", Ano: "2008"},
		{Tipo: "serie", ID: 84, Nome: "Profecia (série)", tmdb: 90228},
	})
	if len(series) != 2 || series[0].Origem != "assinatura" || series[1].Origem != "assinatura" || series[1].Pagina != "#/titulo/serie/1396" {
		t.Errorf("séries: %+v", series)
	}
}

// "Terror" de 2018 no catálogo é "The Terror", não a série "Terror" ainda sem estreia (sem ano).
func TestCasaComOTituloPeloAno(t *testing.T) {
	terror := item{Tipo: "serie", ID: 1, Nome: "Terror", Ano: "2018"}
	casos := []struct {
		t    tituloDoTMDB
		casa bool
	}{
		{tituloDoTMDB{Tipo: "serie", ID: 320207, Titulo: "Terror"}, false},
		{tituloDoTMDB{Tipo: "serie", ID: 75191, Titulo: "The Terror", Ano: "2018"}, true},
		{tituloDoTMDB{Tipo: "serie", ID: 2, Titulo: "The Terror", Ano: "2024"}, false},
	}
	for _, c := range casos {
		if got := casaComOTitulo(c.t, terror); got != c.casa {
			t.Errorf("%d: %v, queria %v", c.t.ID, got, c.casa)
		}
	}
	// Sem ano no catálogo, o nome exato basta, mas o "The" não sai.
	if !casaComOTitulo(tituloDoTMDB{Titulo: "Terror"}, item{Nome: "Terror"}) || casaComOTitulo(tituloDoTMDB{Titulo: "The Terror", Ano: "2018"}, item{Nome: "Terror"}) {
		t.Error("sem ano no catálogo")
	}
}

func TestBuscaDoInicioComTorrents(t *testing.T) {
	e := novoAmbiente(t)
	e.a.db.Exec(`INSERT INTO itens (tipo, id, nome, busca, categoria_id, ano, ordem) VALUES
		('serie', 50, 'Breaking Bad', 'breaking bad', '1', '2008', 1),
		('serie', 51, 'Breaking Bad (Legendado)', 'breaking bad (legendado)', '1', '', 2),
		('canal', 60, 'Breaking News 24h', 'breaking news 24h', '1', '', 3)`)

	// Sem TMDB: só o catálogo, como antes.
	var r map[string][]item
	e.json(t, "/api/buscar?q=breaking", &r)
	if len(r["serie"]) != 2 || r["serie"][0].Origem != "" || len(r["canal"]) != 1 {
		t.Fatalf("sem TMDB: %+v", r)
	}

	t.Setenv("TMDB_URL", tmdbFalso(t).URL)
	if status, corpo := e.pedir(t, "PUT", "/api/tmdb", map[string]string{"chave": "0123456789abcdef0123456789abcdef"}); status != http.StatusOK {
		t.Fatalf("tmdb: %d %s", status, corpo)
	}
	e.json(t, "/api/buscar?q=breaking", &r)
	s := r["serie"]
	if len(s) != 1 || s[0].Origem != "assinatura" || s[0].Pagina != "#/titulo/serie/1396" || s[0].Capa == "" {
		t.Fatalf("série junta: %+v", s)
	}
	// El Camino não tem capa no TMDB falso e não está na assinatura: fica de fora; o canal continua.
	if len(r["filme"]) != 0 || len(r["canal"]) != 1 {
		t.Fatalf("filmes e canais: %+v", r)
	}
}
