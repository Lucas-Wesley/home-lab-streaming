package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestLerLancamento(t *testing.T) {
	breaking := []string{"Breaking Bad"}
	dark := []string{"Dark"}
	casos := []struct {
		nome  string
		nomes []string
		ok    bool
		quer  lancamento
	}{
		{"Breaking Bad S04E07 Problem Dog 2160p NF WEB-DL DDP5 1 H 265-XEBEC", breaking, true, lancamento{Temporada: 4, Episodio: 7}},
		{"Breaking.Bad.S01E01.Pilot.1080p.HEVC.x265-MeGusta", breaking, true, lancamento{Temporada: 1, Episodio: 1}},
		{"Breaking Bad. S05. 2008 2160P.Ai Upscaled.BluRay", breaking, true, lancamento{Temporada: 5, Ano: 2008}},
		{"Breaking Bad S01-S05 Complete 1080p", breaking, true, lancamento{Temporada: 1, AteTemporada: 5}},
		{"Breaking Bad - The Complete Series 720p", breaking, true, lancamento{Completa: true}},
		{"Breaking Bad 2x03 720p", breaking, true, lancamento{Temporada: 2, Episodio: 3}},
		{"Breaking Bad 1ª Temporada Dublado 720p", breaking, true, lancamento{Temporada: 1}},
		{"Breaking Bad: 1ª Temporada (2008) BDRip 720p Dual Audio", breaking, true, lancamento{Temporada: 1, Ano: 2008}},
		{"Better Call Saul S06E11 Breaking Bad 1080p", breaking, false, lancamento{}},
		{"Untold.Raygun.Breaking.Badly.2026.1080p", breaking, false, lancamento{}},
		{"Dark S01E03 720p WEB", dark, true, lancamento{Temporada: 1, Episodio: 3}},
		{"The Dark S01E01 720p WEB H264-SKYFiRE", dark, false, lancamento{}},
		{"[SubsPlease] Dark Summoner to Dekiteiru - 06 (1080p)", dark, false, lancamento{}},
		{"[SubsPlease] Dark Summoner to Dekiteiru - 06 (1080p) [D9343692].mkv", []string{"Dark Summoner to Dekiteiru"}, true, lancamento{Absoluto: 6}},
		{"Duna.Parte.Dois.2024.1080p.WEB-DL.DUAL", []string{"Dune: Part Two", "Duna: Parte Dois"}, true, lancamento{Ano: 2024}},
		{"Dune.Part.Two.2024.2160p.UHD.BluRay.x265", []string{"Dune: Part Two"}, true, lancamento{Ano: 2024}},
		{"Marvels.Daredevil.S01E01.1080p", []string{"Marvel's Daredevil"}, true, lancamento{Temporada: 1, Episodio: 1}},
	}
	for _, c := range casos {
		l, ok := lerLancamento(c.nome, c.nomes)
		if ok != c.ok || (ok && l != c.quer) {
			t.Errorf("%s: %+v %v, queria %+v %v", c.nome, l, ok, c.quer, c.ok)
		}
	}
}

func TestOrganizarSerie(t *testing.T) {
	titulo := tituloDoTMDB{Tipo: "serie", Ano: "2008", nomes: []string{"Breaking Bad"}, principais: []string{"Breaking Bad"},
		Temporadas: []temporadaDoTMDB{{Numero: 0, Episodios: 9}, {Numero: 1, Episodios: 7}, {Numero: 2, Episodios: 13}}}
	r := func(nome string, semeadores int, gb float64) resultadoDaBusca {
		return resultadoDaBusca{Titulo: nome, Semeadores: semeadores, Tamanho: int64(gb * (1 << 30)), Qualidade: qualidadeDe(nome)}
	}
	o := organizar(titulo, []resultadoDaBusca{
		r("Breaking Bad S01E01 2160p H 265", 90, 5.5),
		r("Breaking Bad S01E01 1080p x264", 20, 1.4),
		r("Breaking Bad S01E01 1080p WEB-DL DDP5 1 H 264", 60, 2.2),
		r("Breaking Bad S01E01 720p HEVC", 50, 0.6),
		r("Breaking Bad S01 1080p BluRay x264", 30, 9),
		r("Breaking Bad. S01. 2008 2160P.Ai Upscaled.60FPS", 80, 53),
		r("Breaking Bad S01-S02 720p", 5, 12),
		r("Better Call Saul S06E11 Breaking Bad", 33, 0.8),
	}, nil, time.Now())
	if o.Total != 7 || len(o.Temporadas) != 1 || len(o.Completas) != 1 {
		t.Fatalf("organização: %+v", o)
	}
	ep := o.Temporadas[0].Episodios["1"]
	if len(ep) != 4 || !strings.Contains(ep[0].Titulo, "1080p x264") || !ep[0].Recomendado || !ep[0].TocaEmTudo || ep[1].TocaEmTudo {
		t.Fatalf("melhor do episódio devia ser o 1080p x264 (toca em tudo; o DDP fica mudo no navegador): %+v", ep)
	}
	pacotes := o.Temporadas[0].Pacotes
	if len(pacotes) != 2 || pacotes[0].Pesado || !pacotes[1].Pesado || pacotes[1].Episodios != 7 {
		t.Fatalf("pacotes da temporada (53 GB em 7 episódios é pesado): %+v", pacotes)
	}
	if o.Completas[0].Episodios != 20 {
		t.Fatalf("pacote S01-S02 cobre 20 episódios: %+v", o.Completas[0])
	}
}

func TestOrganizarFilmeEAnime(t *testing.T) {
	filme := tituloDoTMDB{Tipo: "filme", Ano: "1968", nomes: []string{"Night of the Living Dead"}}
	o := organizar(filme, []resultadoDaBusca{
		{Titulo: "Night of the Living Dead 1968 1080p BluRay x264", Semeadores: 40, Tamanho: 8 << 30},
		{Titulo: "Night of the Living Dead 1990 1080p", Semeadores: 90},
		{Titulo: "Night of the Living Dead 1968 REMUX", Semeadores: 100, Tamanho: 30 << 30},
	}, nil, time.Now())
	if len(o.Versoes) != 2 || !o.Versoes[0].Recomendado || !o.Versoes[1].Pesado {
		t.Fatalf("filme: remake de 1990 fora, remux pesado por último: %+v", o.Versoes)
	}

	anime := tituloDoTMDB{Tipo: "serie", nomes: []string{"Frieren"}, Temporadas: []temporadaDoTMDB{{Numero: 1, Episodios: 28}, {Numero: 2, Episodios: 10}}}
	o = organizar(anime, []resultadoDaBusca{{Titulo: "[SubsPlease] Frieren - 30 (1080p) [ABC].mkv", Semeadores: 10}}, nil, time.Now())
	if len(o.Temporadas) != 1 || o.Temporadas[0].Numero != 2 || len(o.Temporadas[0].Episodios["2"]) != 1 {
		t.Fatalf("anime: episódio 30 é o 2 da temporada 2: %+v", o.Temporadas)
	}
}

// tmdbFalso responde como o TMDB para Breaking Bad (série 1396).
func tmdbFalso(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A página "onde assistir" do site (sem chave), com os links da JustWatch.
		if r.URL.Path == "/tv/1396-breaking-bad/watch" {
			fmt.Fprint(w, `<a class="relative" href="https://click.justwatch.com/a?cx=x&amp;r=https%3A%2F%2Fwww.netflix.com%2Ftitle%2F70143836&amp;uct_country=br" title="Assista Breaking Bad na Netflix" target="_blank">
				<a class="relative" href="https://click.justwatch.com/a?r=https%3A%2F%2Fexemplo.invalid%2Fc%3Fu%3Dhttps%253A%252F%252Fwww.disneyplus.com%252Fbrowse%252Fbb" title="Assista Breaking Bad no Disney Plus">`)
			return
		}
		if r.URL.Query().Get("api_key") != "0123456789abcdef0123456789abcdef" {
			http.Error(w, `{"status_message":"Invalid API key"}`, http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/3/configuration":
			fmt.Fprint(w, `{}`)
		case "/3/search/multi":
			fmt.Fprint(w, `{"results":[{"media_type":"tv","id":1396,"name":"Breaking Bad","original_name":"Breaking Bad","first_air_date":"2008-01-20","poster_path":"/bb.jpg","vote_count":19076},
				{"media_type":"person","id":17419,"name":"Bryan Cranston"},
				{"media_type":"movie","id":559969,"title":"El Camino: A Breaking Bad Movie","release_date":"2019-10-11"}]}`)
		case "/3/tv/1396":
			fmt.Fprint(w, `{"id":1396,"name":"Breaking Bad","original_name":"Breaking Bad","first_air_date":"2008-01-20",
				"seasons":[{"season_number":1,"name":"Temporada 1","episode_count":7}],
				"external_ids":{"imdb_id":"tt0903747"},
				"alternative_titles":{"results":[{"title":"ブレイキング・バッド"},{"title":"Breaking Bad: Reazioni collaterali"}]}}`)
		case "/3/tv/1396/watch/providers":
			fmt.Fprintf(w, `{"results":{"US":{"flatrate":[{"provider_id":15,"provider_name":"Hulu"}]},"BR":{"link":"%s/tv/1396-breaking-bad/watch?locale=BR",
				"flatrate":[{"provider_id":8,"provider_name":"Netflix","logo_path":"/n.jpg"},{"provider_id":1796,"provider_name":"Netflix Standard with Ads"},
					{"provider_id":337,"provider_name":"Disney Plus"},{"provider_id":1825,"provider_name":"HBO Max Amazon Channel"},{"provider_id":484,"provider_name":"Claro tv+"}],
				"buy":[{"provider_id":2,"provider_name":"Apple TV Store"},{"provider_id":10,"provider_name":"Amazon Video"}]}}}`, srv.URL)
		case "/3/tv/1396/season/1":
			fmt.Fprint(w, `{"episodes":[{"episode_number":1,"name":"Piloto"},{"episode_number":2,"name":"O Gato Está no Saco..."}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTitulosDoTMDB(t *testing.T) {
	e := novoAmbiente(t)
	t.Setenv("TMDB_URL", tmdbFalso(t).URL)
	var buscas []string
	falso := prowlarrFalso(t, nil, &buscas)
	e.pedir(t, "PUT", "/api/prowlarr", configDoProwlarr{URL: falso.URL, Chave: chaveFalsa})

	if status, _ := e.pedir(t, "GET", "/api/titulos?q=breaking", nil); status != http.StatusConflict {
		t.Fatalf("sem chave do TMDB devia pedir configuração: %d", status)
	}
	if status, corpo := e.pedir(t, "PUT", "/api/tmdb", map[string]string{"chave": "ffffffffffffffffffffffffffffffff"}); status != 400 || !strings.Contains(corpo, "recusou") {
		t.Fatalf("chave errada: %d %s", status, corpo)
	}
	if status, corpo := e.pedir(t, "PUT", "/api/tmdb", map[string]string{"chave": "0123456789abcdef0123456789abcdef"}); status != 200 {
		t.Fatalf("guardar TMDB: %d %s", status, corpo)
	}

	var titulos []tituloDoTMDB
	e.json(t, "/api/titulos?q=breaking", &titulos)
	if len(titulos) != 2 || titulos[0].Tipo != "serie" || titulos[0].Ano != "2008" || !strings.HasSuffix(titulos[0].Capa, "/w342/bb.jpg") || titulos[1].Tipo != "filme" {
		t.Fatalf("busca de títulos (sem pessoas): %+v", titulos)
	}

	var pagina struct {
		Titulo     tituloDoTMDB       `json:"titulo"`
		Assinatura []itemDaAssinatura `json:"assinatura"`
	}
	e.json(t, "/api/titulos/serie/1396", &pagina)
	if pagina.Titulo.ImdbID != "tt0903747" || len(pagina.Titulo.Temporadas) != 1 {
		t.Fatalf("título: %+v", pagina.Titulo)
	}

	var eps []episodioDoTMDB
	e.json(t, "/api/titulos/serie/1396/temporada/1", &eps)
	if len(eps) != 2 || eps[0].Nome != "Piloto" {
		t.Fatalf("episódios: %+v", eps)
	}

	// O Prowlarr falso devolve "Serie Teste …": nada é de Breaking Bad, então tudo fica de fora.
	var org torrentsDoTitulo
	e.json(t, "/api/titulos/serie/1396/torrents", &org)
	if org.Total != 0 || len(buscas) != 3 || !strings.Contains(strings.Join(buscas, " "), "query=Breaking+Bad+2008") {
		t.Fatalf("torrents do título (nome, nome e ano, nome e complete): %+v, buscas %v", org, buscas)
	}
	e.json(t, "/api/titulos/serie/1396/torrents?temporada=1", &org)
	if strings.Join(org.Buscas, "|") != "Breaking Bad S01|Breaking Bad season 1" {
		t.Fatalf("buscas da temporada: %v", org.Buscas)
	}
	// O nome em japonês e o italiano (título alternativo) não viram busca.
	for _, b := range org.Buscas {
		if strings.Contains(b, "ブ") || strings.Contains(b, "Reazioni") {
			t.Fatalf("busca com nome não latino: %v", org.Buscas)
		}
	}
	b, _ := json.Marshal(org)
	if strings.Contains(string(b), chaveFalsa) {
		t.Fatal("chave do Prowlarr na resposta")
	}
}

// Onde mais assistir (S30): só os serviços principais, no Brasil, um por serviço (a assinatura antes
// da compra), com o endereço do título no serviço quando a página do TMDB o tem.
func TestOndeAssistir(t *testing.T) {
	e := novoAmbiente(t)
	t.Setenv("TMDB_URL", tmdbFalso(t).URL)
	e.pedir(t, "PUT", "/api/tmdb", map[string]string{"chave": "0123456789abcdef0123456789abcdef"})
	var lista []ondeAssistir
	e.json(t, "/api/titulos/serie/1396/onde", &lista)
	var veio []string
	for _, o := range lista {
		veio = append(veio, fmt.Sprintf("%s|%s|%v|%s", o.Servico, o.Como, o.Direto, regexp.MustCompile(`^http://127\.0\.0\.1:\d+`).ReplaceAllString(o.Link, "")))
	}
	pagina := "/tv/1396-breaking-bad/watch?locale=BR"
	quer := []string{
		"Netflix|assinatura|true|https://www.netflix.com/title/70143836",
		"Apple TV|compra|false|" + pagina,
		"Disney+|assinatura|true|https://www.disneyplus.com/browse/bb",
		"Prime Video|compra|false|" + pagina,
	}
	if len(veio) != len(quer) {
		t.Fatalf("veio:\n%s", strings.Join(veio, "\n"))
	}
	for i := range quer {
		if veio[i] != quer[i] {
			t.Fatalf("veio:\n%s", strings.Join(veio, "\n"))
		}
	}
	if lista[0].Logo != imagensDoTMDB+"w92/n.jpg" {
		t.Fatalf("logo: %q", lista[0].Logo)
	}
}
