package main

import (
	"fmt"
	"testing"
)

func TestNomeDoTorrent(t *testing.T) {
	casos := map[string]string{
		"www.UIndex.org    -    Breaking.Bad.S01E01.Pilot.1080p.HEVC.x265-MeGusta": "Breaking Bad",
		"Breaking Bad S01E01 Pilot 2160p NF WEB-DL DDP5 1 H 265-XEBEC":             "Breaking Bad",
		"2001 A Space Odyssey (1968) [BluRay] [1080p] [YTS.AM]":                    "2001 A Space Odyssey",
		"Night.of.the.Living.Dead.1968.REMASTERED.1080p.BluRay.X264-AMIAB":         "Night of the Living Dead",
		"The Odyssey (2026) [1080p] [WEBRip] [5.1]":                                "The Odyssey",
		"Severance Season 1 Mp4 1080p":                                             "Severance",
		"Charlie Chaplin Festival":                                                 "Charlie Chaplin Festival",
	}
	for bruto, quer := range casos {
		if veio := nomeDoTorrent(bruto); veio != quer {
			t.Errorf("%q: veio %q, queria %q", bruto, veio, quer)
		}
	}
}

func TestContinuarJuntaOMesmoTituloELimita(t *testing.T) {
	e := novoAmbiente(t)
	guardar := func(chave, titulo, tmdb string, serie int, quando string) {
		if _, err := e.a.db.Exec(`INSERT INTO progresso (chave, titulo, serie_id, posicao, duracao, atualizado_em, tmdb)
			VALUES (?, ?, ?, 100, 1000, ?, ?)`, chave, titulo, serie, quando, tmdb); err != nil {
			t.Fatal(err)
		}
	}
	// O mesmo episódio pela assinatura e por dois torrents: fica só o mais recente.
	guardar("episodio:1", "Breaking Bad", "serie/1396", 83, "2026-11-10T03:00:00Z")
	guardar("arquivo:2", "www.UIndex.org - Breaking.Bad.S01E01.1080p", "", 0, "2026-11-10T02:00:00Z")
	guardar("arquivo:3", "Breaking Bad", "serie/1396", 0, "2026-11-10T04:00:00Z")
	for i := range 20 {
		guardar(fmt.Sprint("filme:", 100+i), fmt.Sprint("Filme ", i), "", 0, fmt.Sprintf("2026-11-09T%02d:00:00Z", i))
	}
	var lista []progresso
	e.json(t, "/api/progresso", &lista)
	if len(lista) != itensParaContinuar {
		t.Fatalf("itens: %d", len(lista))
	}
	if lista[0].Chave != "arquivo:3" || lista[0].Pagina != "#/titulo/serie/1396" {
		t.Fatalf("primeiro: %+v", lista[0])
	}
	// O torrent guardado sem o TMDB junta pelo nome limpo; o episódio da assinatura, pelo TMDB.
	for _, p := range lista {
		if p.Chave == "episodio:1" || p.Chave == "arquivo:2" {
			t.Fatalf("%s é o mesmo título do torrent mais recente", p.Chave)
		}
	}
}
