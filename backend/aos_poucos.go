package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// Busca aos poucos (S36): a página do título pergunta a cada indexador em separado e recebe o que
// já chegou, sem esperar o mais lento. Os indexadores com a tag "cloudflare" passam pelo
// FlareSolverr, que abre um Chrome por pedido (uns 15 s cada, e com vários ao mesmo tempo as abas
// travam por falta de memória): eles recebem uma busca por vez e só as duas primeiras do título.
// O resultado de cada busca fica guardado por um tempo; o próximo episódio da mesma temporada
// reaproveita a busca da temporada.

const (
	tagDoFlareSolverr      = "cloudflare"
	buscasDoIndexadorLento = 2
	buscasSimultaneas      = 3 // por indexador rápido
	guardarBusca           = 20 * time.Minute
	guardarIndexadores     = 5 * time.Minute
	esperaDaBuscaAosPoucos = 90 * time.Second
)

type indexador struct {
	ID    int    `json:"id"`
	Nome  string `json:"nome"`
	Lento bool   `json:"lento"`
}

type buscaPorIndexador struct {
	mu        sync.Mutex
	lista     []indexador
	listaAte  time.Time
	vagas     map[int]chan struct{}
	guardadas map[string]buscaGuardada
}

type buscaGuardada struct {
	resultados []resultadoDaBusca
	expira     time.Time
}

// indexadoresDoProwlarr: os ligados, com os que passam pelo FlareSolverr marcados como lentos.
func (a *app) indexadoresDoProwlarr(ctx context.Context, c configDoProwlarr) ([]indexador, error) {
	b := &a.porIndexador
	b.mu.Lock()
	if time.Now().Before(b.listaAte) {
		defer b.mu.Unlock()
		return b.lista, nil
	}
	b.mu.Unlock()
	var brutos []struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Enable bool   `json:"enable"`
		Tags   []int  `json:"tags"`
	}
	if err := a.pedirAoProwlarr(ctx, c, "/api/v1/indexer", &brutos); err != nil {
		return nil, err
	}
	var tags []struct {
		ID    int    `json:"id"`
		Label string `json:"label"`
	}
	a.pedirAoProwlarr(ctx, c, "/api/v1/tag", &tags) // sem as tags, todos contam como rápidos
	lentas := map[int]bool{}
	for _, t := range tags {
		lentas[t.ID] = t.Label == tagDoFlareSolverr
	}
	lista := []indexador{}
	for _, ix := range brutos {
		if !ix.Enable {
			continue
		}
		novo := indexador{ID: ix.ID, Nome: ix.Name}
		for _, t := range ix.Tags {
			novo.Lento = novo.Lento || lentas[t]
		}
		lista = append(lista, novo)
	}
	b.mu.Lock()
	b.lista, b.listaAte = lista, time.Now().Add(guardarIndexadores)
	b.mu.Unlock()
	return lista, nil
}

// procurarNoIndexador faz uma busca num indexador só, respeitando as vagas dele.
func (a *app) procurarNoIndexador(ctx context.Context, c configDoProwlarr, ix indexador, texto, tipo string) ([]resultadoDaBusca, error) {
	b := &a.porIndexador
	chave := strconv.Itoa(ix.ID) + "|" + tipo + "|" + comparavel(texto)
	b.mu.Lock()
	if g, ok := b.guardadas[chave]; ok && time.Now().Before(g.expira) {
		b.mu.Unlock()
		return g.resultados, nil
	}
	if b.vagas == nil {
		b.vagas = map[int]chan struct{}{}
	}
	vagas := b.vagas[ix.ID]
	if vagas == nil {
		vagas = make(chan struct{}, map[bool]int{true: 1, false: buscasSimultaneas}[ix.Lento])
		b.vagas[ix.ID] = vagas
	}
	b.mu.Unlock()

	select {
	case vagas <- struct{}{}:
		defer func() { <-vagas }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	q := url.Values{"query": {texto}, "type": {"search"}, "limit": {"100"}, "categories": categoriasDaBusca[tipo],
		"indexerIds": {strconv.Itoa(ix.ID)}}
	var lancamentos []lancamentoDoProwlarr
	if err := a.pedirAoProwlarr(ctx, c, "/api/v1/search?"+q.Encode(), &lancamentos); err != nil {
		return nil, err
	}
	resultados := resultadosDoProwlarr(lancamentos)

	b.mu.Lock()
	if b.guardadas == nil {
		b.guardadas = map[string]buscaGuardada{}
	}
	agora := time.Now()
	for k, g := range b.guardadas {
		if agora.After(g.expira) {
			delete(b.guardadas, k)
		}
	}
	b.guardadas[chave] = buscaGuardada{resultados, agora.Add(guardarBusca)}
	b.mu.Unlock()
	return resultados, nil
}

// buscasAosPoucos: no episódio, o episódio e a temporada ("Nome S02E05", "Nome S02"), porque muitos
// episódios avulsos só aparecem na busca pela temporada. As lentas são as do primeiro nome, na
// ordem do que mais costuma achar.
func buscasAosPoucos(t tituloDoTMDB, temporada, episodio int) (todas, lentas []string) {
	todas = buscasDoTitulo(t, temporada, episodio)
	if episodio > 0 {
		todas = append(todas, buscasDoTitulo(t, temporada, 0)...)
	}
	if len(t.principais) > 0 {
		so := t
		so.principais = t.principais[:1]
		lentas = buscasDoTitulo(so, temporada, episodio)
		if episodio > 0 {
			lentas = append(lentas, buscasDoTitulo(so, temporada, 0)...)
		}
	}
	return todas, lentas[:min(len(lentas), buscasDoIndexadorLento)]
}

// parteDaBusca é uma linha da resposta (NDJSON): o que já chegou, organizado, e o andamento (cada
// busca em cada indexador conta uma). Falharam: os indexadores em que nenhuma busca deu certo.
type parteDaBusca struct {
	Feitas   int               `json:"feitas"`
	Total    int               `json:"total"`
	Falharam []string          `json:"falharam"`
	Torrents *torrentsDoTitulo `json:"torrents,omitempty"`
}

func (a *app) handleTorrentsAosPoucos(w http.ResponseWriter, r *http.Request) {
	t, ok := a.tituloDoPedido(w, r)
	if !ok {
		return
	}
	c, ok := a.prowlarr()
	if !ok {
		erroJSON(w, http.StatusConflict, "Configure o Prowlarr em Ajustes para buscar torrents.")
		return
	}
	ixs, err := a.indexadoresDoProwlarr(r.Context(), c)
	if err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	temporada, _ := strconv.Atoi(r.URL.Query().Get("temporada"))
	episodio, _ := strconv.Atoi(r.URL.Query().Get("episodio"))
	todas, lentas := buscasAosPoucos(t, temporada, episodio)

	ctx, cancel := context.WithTimeout(r.Context(), esperaDaBuscaAosPoucos)
	defer cancel()
	type feita struct {
		ix         indexador
		resultados []resultadoDaBusca
		err        error
	}
	feitas := make(chan feita)
	quantas := map[int]int{}   // indexador → buscas pedidas
	pendentes := map[int]int{} // e as que faltam
	erros := map[int]int{}
	total := 0
	for _, ix := range ixs {
		buscas := todas
		if ix.Lento {
			buscas = lentas
		}
		quantas[ix.ID], pendentes[ix.ID] = len(buscas), len(buscas)
		total += len(buscas)
		for _, q := range buscas {
			go func() {
				rs, err := a.procurarNoIndexador(ctx, c, ix, q, t.Tipo)
				select {
				case feitas <- feita{ix, rs, err}:
				case <-r.Context().Done():
				}
			}()
		}
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enviar := func(p parteDaBusca) bool {
		if enc.Encode(p) != nil {
			return false // a página fechou
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return true
	}
	parte := parteDaBusca{Total: total, Falharam: []string{}}
	if !enviar(parte) {
		return
	}
	var todos []resultadoDaBusca
	for parte.Feitas < total {
		f := <-feitas
		parte.Feitas++
		pendentes[f.ix.ID]--
		if f.err != nil {
			erros[f.ix.ID]++
			if pendentes[f.ix.ID] == 0 && erros[f.ix.ID] == quantas[f.ix.ID] {
				parte.Falharam = append(parte.Falharam, f.ix.Nome)
			}
			if parte.Feitas < total {
				continue // nada novo para mostrar
			}
		}
		todos = append(todos, f.resultados...)
		org := organizar(t, semRepetir(todos), todas, a.agora())
		parte.Torrents = &org
		if !enviar(parte) {
			return
		}
	}
}

// semRepetir: o mesmo lançamento pode vir de duas buscas.
func semRepetir(resultados []resultadoDaBusca) []resultadoDaBusca {
	vistos := map[string]bool{}
	unicos := []resultadoDaBusca{}
	for _, r := range resultados {
		chave := r.Indexador + "|" + r.Titulo + "|" + strconv.FormatInt(r.Tamanho, 10)
		if !vistos[chave] {
			vistos[chave] = true
			unicos = append(unicos, r)
		}
	}
	return unicos
}
