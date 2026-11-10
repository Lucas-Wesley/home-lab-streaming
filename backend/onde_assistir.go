package main

import (
	"context"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Onde mais assistir (S30): os streamings que o Lucas assina e que têm o título no Brasil, pelo TMDB
// (dados da JustWatch). Tocar o vídeo deles dentro do app não dá (DRM, sem API pública); o app leva
// ao título no app de cada um. A API dá só a lista; o endereço do título em cada serviço vem da
// página "onde assistir" do TMDB, que tem os links da JustWatch (guardados por um dia). Sem ele, o
// link é a própria página do TMDB.

type ondeAssistir struct {
	Servico string `json:"servico"`
	Logo    string `json:"logo,omitempty"`
	Como    string `json:"como"` // assinatura, aluguel ou compra
	Link    string `json:"link"`
	Direto  bool   `json:"direto"` // o link abre o título no serviço (no celular, no app dele)
}

// servicosPrincipais: os códigos do TMDB de cada serviço, na ordem em que aparecem. Os "Amazon
// Channel" (o serviço pago dentro do Prime) ficam de fora: a assinatura do Lucas é a direta.
var servicosPrincipais = []struct {
	nome    string
	codigos []int
}{
	{"Netflix", []int{8, 1796}},
	{"HBO Max", []int{1899, 384}},
	{"Apple TV", []int{350, 2}},
	{"Disney+", []int{337}},
	{"Prime Video", []int{119, 9, 10}},
}

type provedorDoTMDB struct {
	Codigo int    `json:"provider_id"`
	Nome   string `json:"provider_name"`
	Logo   string `json:"logo_path"`
}

var (
	reLinkDaJustWatch = regexp.MustCompile(`<a [^>]*href="(https://click\.justwatch\.com/[^"]+)"[^>]*title="([^"]+)"`)
	linksDiretos      = struct {
		mu    sync.Mutex
		itens map[string]linksGuardados
	}{itens: map[string]linksGuardados{}}
)

type linksGuardados struct {
	links  []linkDireto
	expira time.Time
}

// linkDireto: o texto do link na página ("Assista Breaking Bad na Netflix") e o endereço do título
// no serviço.
type linkDireto struct{ texto, endereco string }

func linkDoServico(links []linkDireto, servico string) string {
	for _, l := range links {
		if strings.HasSuffix(l.texto, " "+servico) {
			return l.endereco
		}
	}
	return ""
}

func (a *app) handleOndeAssistir(w http.ResponseWriter, r *http.Request) {
	chave, ok := a.exigirTMDB(w)
	if !ok {
		return
	}
	caminho, ok := tiposDoTMDB[r.PathValue("tipo")]
	id := atoi(r.PathValue("id"))
	if !ok || (caminho != "movie" && caminho != "tv") || id <= 0 {
		erroJSON(w, http.StatusBadRequest, "Título inválido.")
		return
	}
	var d struct {
		Results map[string]struct {
			Link     string           `json:"link"`
			Flatrate []provedorDoTMDB `json:"flatrate"`
			Rent     []provedorDoTMDB `json:"rent"`
			Buy      []provedorDoTMDB `json:"buy"`
		} `json:"results"`
	}
	if err := a.perguntarAoTMDB(r.Context(), chave, fmt.Sprintf("/%s/%d/watch/providers", caminho, id), nil, &d); err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	br := d.Results["BR"]
	lista := []ondeAssistir{}
	var diretos []linkDireto
	lidos := false
	for _, s := range servicosPrincipais {
		achado := false
		for _, grupo := range []struct {
			como       string
			provedores []provedorDoTMDB
		}{{"assinatura", br.Flatrate}, {"aluguel", br.Rent}, {"compra", br.Buy}} {
			for _, p := range grupo.provedores {
				if achado || !contemInt(s.codigos, p.Codigo) {
					continue
				}
				achado = true
				if !lidos {
					diretos, lidos = a.linksDaPaginaDoTMDB(r.Context(), br.Link), true
				}
				o := ondeAssistir{Servico: s.nome, Como: grupo.como, Link: br.Link}
				if p.Logo != "" {
					o.Logo = imagensDoTMDB + "w92" + p.Logo
				}
				if l := linkDoServico(diretos, p.Nome); l != "" {
					o.Link, o.Direto = l, true
				}
				lista = append(lista, o)
			}
		}
	}
	writeJSON(w, http.StatusOK, lista)
}

// linksDaPaginaDoTMDB lê os links "Assista X na Netflix" da página do TMDB e tira, de cada um, o
// endereço final do serviço (o parâmetro r do link da JustWatch).
func (a *app) linksDaPaginaDoTMDB(ctx context.Context, pagina string) []linkDireto {
	if pagina == "" {
		return nil
	}
	linksDiretos.mu.Lock()
	g, ok := linksDiretos.itens[pagina]
	linksDiretos.mu.Unlock()
	if ok && a.agora().Before(g.expira) {
		return g.links
	}
	var links []linkDireto
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pagina, nil)
	if err != nil {
		return links
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := a.http.Do(req)
	if err != nil {
		log.Printf("onde assistir: a página do TMDB não respondeu: %v", err)
		return links
	}
	defer resp.Body.Close()
	corpo, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return links
	}
	for _, m := range reLinkDaJustWatch.FindAllStringSubmatch(string(corpo), -1) {
		u, err := url.Parse(html.UnescapeString(m[1])) // o texto ("Compre Duna na Apple TV Store") termina no serviço
		if err != nil {
			continue
		}
		final := u.Query().Get("r")
		if !strings.HasPrefix(final, "https://") {
			continue
		}
		// Alguns passam ainda por um link de afiliado (o do Disney+: ...?u=https://www.disneyplus.com/...).
		if f, err := url.Parse(final); err == nil && strings.HasPrefix(f.Query().Get("u"), "https://") {
			final = f.Query().Get("u")
		}
		links = append(links, linkDireto{html.UnescapeString(m[2]), final})
	}
	linksDiretos.mu.Lock()
	if len(linksDiretos.itens) > 300 {
		linksDiretos.itens = map[string]linksGuardados{}
	}
	linksDiretos.itens[pagina] = linksGuardados{links, a.agora().Add(24 * time.Hour)}
	linksDiretos.mu.Unlock()
	return links
}

func contemInt(lista []int, n int) bool {
	for _, x := range lista {
		if x == n {
			return true
		}
	}
	return false
}
