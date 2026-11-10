package main

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Organizar o que os indexadores devolvem para um título do TMDB (S20): descartar o que é de outro
// título, ler temporada e episódio do nome e apontar a melhor opção para assistir sem baixar tudo.

// comparavel deixa o nome só com letras e números separados por um espaço, sem acento.
func comparavel(s string) string {
	s = strings.ReplaceAll(normalizar(s), "&", " and ")
	var b strings.Builder
	espaco := true
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r > 0x7f {
			b.WriteRune(r)
			espaco = false
		} else if !espaco {
			b.WriteByte(' ')
			espaco = true
		}
	}
	return strings.TrimSpace(b.String())
}

var (
	// "[SubsPlease] Nome - 06", "www.site.org - Nome": o que vem antes do nome no lançamento.
	reAntesDoNome = regexp.MustCompile(`^\s*(\[[^\]]*\]\s*|\([^)]*\)\s*|www\.\S+\s*-?\s*)+`)
	// Depois do nome, um lançamento do título tem ano, temporada, episódio ou qualidade.
	reDepoisDoNome = regexp.MustCompile(`^((19|20)\d{2}\b|s\d{1,2}|season|temporada|\d{1,2}(a|ª)? temporada|complete|completa|collection|\d{1,2}x\d{1,3}\b|\d{3,4}p\b|4k\b|uhd|hdtv|web|bluray|brrip|bdrip|dvdrip|remastered|extended|unrated|directors|dual|dublado|legendado|nacional|multi|\d{1,4}\b|e\d{1,3}\b|ep\d|part|parte|vol|the complete|$)`)

	reEpisodioDaTemporada = regexp.MustCompile(`\bs(\d{1,2}) ?e(\d{1,3})\b`)
	reNxM                 = regexp.MustCompile(`\b(\d{1,2})x(\d{1,3})\b`)
	reVariasTemporadas    = regexp.MustCompile(`\bs(\d{1,2}) (?:(?:a|to|ate) )?s(\d{1,2})\b|\b(?:seasons?|temporadas?) (\d{1,2}) (?:(?:a|to|ate|e) )?(\d{1,2})\b`)
	reUmaTemporada        = regexp.MustCompile(`\bs(\d{1,2})\b|\b(?:season|temporada) (\d{1,2})\b|\b(\d{1,2}) ?(?:a|ª)? temporada\b`)
	reSerieCompleta       = regexp.MustCompile(`\b(complete series|serie completa|complete collection|the complete|completa|complete)\b`)
	reEpisodioAbsoluto    = regexp.MustCompile(`^(?:-\s*)?(\d{1,4})\b`)
	reAnoSolto            = regexp.MustCompile(`\b(19|20)\d{2}\b`)
)

// lancamento é o que se lê do nome de um resultado, comparado com um título.
type lancamento struct {
	Temporada    int // 0: não diz
	AteTemporada int // pacote de várias temporadas: da Temporada até esta
	Episodio     int
	Completa     bool // a série inteira
	Absoluto     int  // anime: episódio contado desde o início, sem temporada
	Ano          int
}

// lerLancamento diz se o nome é deste título e o que ele traz. nomes são os nomes do título
// (original, em inglês, em português…).
func lerLancamento(nomeDoTorrent string, nomes []string) (lancamento, bool) {
	limpo := comparavel(reAntesDoNome.ReplaceAllString(nomeDoTorrent, ""))
	var resto string
	achou := false
	for _, n := range nomes {
		c := comparavel(n)
		if c == "" {
			continue
		}
		// "Marvel's Daredevil" aparece como "Marvels Daredevil": comparar sem os espaços do apóstrofo também.
		for _, variacao := range []string{c, strings.ReplaceAll(c, " s ", "s ")} {
			if strings.HasPrefix(limpo, variacao) && (len(limpo) == len(variacao) || limpo[len(variacao)] == ' ') {
				r := strings.TrimSpace(limpo[len(variacao):])
				if reDepoisDoNome.MatchString(r) && (!achou || len(r) < len(resto)) {
					resto, achou = r, true
				}
			}
		}
	}
	if !achou {
		return lancamento{}, false
	}
	var l lancamento
	if m := reAnoSolto.FindString(resto); m != "" {
		l.Ano, _ = strconv.Atoi(m)
	}
	switch {
	case reEpisodioDaTemporada.MatchString(resto):
		m := reEpisodioDaTemporada.FindStringSubmatch(resto)
		l.Temporada, l.Episodio = atoi(m[1]), atoi(m[2])
	case reNxM.MatchString(resto):
		m := reNxM.FindStringSubmatch(resto)
		l.Temporada, l.Episodio = atoi(m[1]), atoi(m[2])
	case reVariasTemporadas.MatchString(resto):
		m := reVariasTemporadas.FindStringSubmatch(resto)
		if m[1] != "" {
			l.Temporada, l.AteTemporada = atoi(m[1]), atoi(m[2])
		} else {
			l.Temporada, l.AteTemporada = atoi(m[3]), atoi(m[4])
		}
		if l.AteTemporada <= l.Temporada {
			l.AteTemporada = 0
		}
	case reUmaTemporada.MatchString(resto):
		m := reUmaTemporada.FindStringSubmatch(resto)
		l.Temporada = atoi(primeiro(m[1], m[2], m[3]))
	case reSerieCompleta.MatchString(resto):
		l.Completa = true
	default:
		// Anime: "Nome - 06 (1080p)". O número não pode ser o ano nem a resolução.
		if m := reEpisodioAbsoluto.FindStringSubmatch(resto); m != nil && !reAnoSolto.MatchString(m[1]) {
			if n := atoi(m[1]); n > 0 && n < 2000 && !strings.HasPrefix(resto[len(m[0]):], "p") {
				l.Absoluto = n
			}
		}
	}
	return l, true
}

// ---------- Avaliar cada opção ----------

type opcao struct {
	resultadoDaBusca
	TocaEmTudo  bool    `json:"toca_em_tudo"` // H.264 até 1080p: navegador, celular, TV e projetor
	Pesado      bool    `json:"pesado"`
	Recomendado bool    `json:"recomendado"`
	Episodios   int     `json:"episodios,omitempty"` // quantos episódios o pacote cobre, se se sabe
	Pontos      float64 `json:"pontos"`              // para a página juntar buscas e reordenar
	// Suspeito: filme que ainda não saiu em digital e diz ser WEB ou Blu-ray (S23). Nunca é recomendado.
	Suspeito bool     `json:"suspeito,omitempty"`
	Alertas  []string `json:"alertas,omitempty"`
}

// Áudio Dolby (DD, DD+, Atmos) e DTS: a TV toca, o navegador fica mudo.
var reAudioSemNavegador = regexp.MustCompile(`(?i)(\bddp|\bdd\+|e-?ac-?3|\bac-?3\b|\bdd[ .]?[257][ .]?[01]\b|\bdts|truehd|atmos)`)

var reH265 = regexp.MustCompile(`(?i)\b(x265|h[ .]?265|hevc|av1|10 ?bit)\b`)

func avaliar(r resultadoDaBusca, episodios int, limitePorEpisodio int64) opcao {
	o := opcao{resultadoDaBusca: r, Episodios: episodios}
	q := strings.Join(r.Qualidade, " ")
	quatroK := strings.Contains(q, "4K")
	o.TocaEmTudo = !quatroK && !reH265.MatchString(r.Titulo) && !reAudioSemNavegador.MatchString(r.Titulo)
	if episodios < 1 {
		episodios = 1
	}
	o.Pesado = r.Tamanho/int64(episodios) > limitePorEpisodio
	o.Pontos = math.Log2(1+float64(r.Semeadores)) * 10
	switch {
	case strings.Contains(q, "1080p"):
		o.Pontos += 10
	case strings.Contains(q, "720p"):
		o.Pontos += 6
	case strings.Contains(q, "480p"):
		o.Pontos += 1
	case quatroK:
		o.Pontos -= 4
	}
	if o.TocaEmTudo {
		o.Pontos += 25 // no celular, vídeo sem som ou sem imagem não serve, mesmo baixando rápido
	}
	if strings.Contains(q, "Português") || strings.Contains(q, "Dual áudio") {
		o.Pontos += 4
	}
	if o.Pesado {
		o.Pontos -= 30
	}
	if r.Semeadores == 0 {
		o.Pontos -= 40
	}
	if reCinema.MatchString(r.Titulo) {
		o.Pontos -= 45 // gravado no cinema: só se não houver outra
	}
	return o
}

// ordenar põe as melhores primeiro e marca a recomendada: a melhor que não é pesada nem suspeita
// e tem semeadores.
func ordenar(ops []opcao) []opcao {
	sort.SliceStable(ops, func(i, j int) bool { return ops[i].Pontos > ops[j].Pontos })
	for i := range ops {
		if !ops[i].Pesado && !ops[i].Suspeito && ops[i].Semeadores > 0 {
			ops[i].Recomendado = true
			break
		}
	}
	return ops
}

// ---------- A busca organizada ----------

const (
	limiteDoFilme    = 25 << 30 // acima disso é remux de Blu-ray: não precisa para assistir
	limiteDoEpisodio = 6 << 30
)

type temporadaOrganizada struct {
	Numero    int                `json:"numero"`
	Pacotes   []opcao            `json:"pacotes"`
	Episodios map[string][]opcao `json:"episodios"` // número do episódio → opções
}

type torrentsDoTitulo struct {
	Versoes    []opcao                `json:"versoes,omitempty"`    // filme
	Temporadas []*temporadaOrganizada `json:"temporadas,omitempty"` // série
	Completas  []opcao                `json:"completas,omitempty"`  // série inteira ou várias temporadas
	Outros     []opcao                `json:"outros"`               // é do título, mas não deu para dizer o quê
	Total      int                    `json:"total"`
	Buscas     []string               `json:"buscas"` // o que foi perguntado aos indexadores
}

func (a *app) handleTorrentsDoTitulo(w http.ResponseWriter, r *http.Request) {
	t, ok := a.tituloDoPedido(w, r)
	if !ok {
		return
	}
	c, ok := a.prowlarr()
	if !ok {
		erroJSON(w, http.StatusConflict, "Configure o Prowlarr em Ajustes para buscar torrents.")
		return
	}
	temporada, _ := strconv.Atoi(r.URL.Query().Get("temporada"))
	episodio, _ := strconv.Atoi(r.URL.Query().Get("episodio"))
	buscas := buscasDoTitulo(t, temporada, episodio)
	resultados, err := a.procurarVarias(r.Context(), c, buscas, t.Tipo)
	if err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, organizar(t, resultados, buscas, a.agora()))
}

// buscasDoTitulo: os indexadores procuram por palavras soltas, e um nome curto ("Dark") traz
// "Dark Matter", "The Dark"… Por isso vão várias buscas juntas, cada uma com algo que costuma estar
// no nome dos lançamentos do título: o ano, "complete" ou a temporada (que a página pede ao abrir
// a temporada). O filtro de lerLancamento tira o que é de outro título. O episódio ("Nome S02E05")
// é pedido ao abrir um episódio com poucas opções avulsas: a busca da temporada traz mais pacotes.
func buscasDoTitulo(t tituloDoTMDB, temporada, episodio int) []string {
	var buscas []string
	vistos := map[string]bool{}
	juntar := func(q string) {
		if c := comparavel(q); c != "" && !vistos[c] {
			vistos[c] = true
			buscas = append(buscas, q)
		}
	}
	for _, n := range t.principais {
		switch {
		case t.Tipo == "filme" && t.Ano != "":
			juntar(n + " " + t.Ano)
		case t.Tipo == "filme":
			juntar(n)
		case temporada > 0 && episodio > 0:
			juntar(fmt.Sprintf("%s S%02dE%02d", n, temporada, episodio))
		case temporada > 0:
			juntar(fmt.Sprintf("%s S%02d", n, temporada))
			juntar(fmt.Sprintf("%s season %d", n, temporada))
		default:
			juntar(n)
			if t.Ano != "" {
				juntar(n + " " + t.Ano)
			}
			juntar(n + " complete")
		}
	}
	return buscas
}

func (a *app) procurarVarias(ctx context.Context, c configDoProwlarr, buscas []string, tipo string) ([]resultadoDaBusca, error) {
	var (
		mu    sync.Mutex
		todos []resultadoDaBusca
		erros []error
		wg    sync.WaitGroup
	)
	for _, q := range buscas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := a.procurarNoProwlarr(ctx, c, q, tipo)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				erros = append(erros, err)
				return
			}
			todos = append(todos, r...)
		}()
	}
	wg.Wait()
	if len(erros) == len(buscas) && len(erros) > 0 {
		return nil, erros[0]
	}
	// O mesmo lançamento pode vir das duas buscas.
	vistos := map[string]bool{}
	unicos := todos[:0]
	for _, r := range todos {
		chave := r.Indexador + "|" + r.Titulo + "|" + strconv.FormatInt(r.Tamanho, 10)
		if !vistos[chave] {
			vistos[chave] = true
			unicos = append(unicos, r)
		}
	}
	return unicos, nil
}

func organizar(t tituloDoTMDB, resultados []resultadoDaBusca, buscas []string, agora time.Time) torrentsDoTitulo {
	o := torrentsDoTitulo{Outros: []opcao{}, Buscas: buscas}
	ano := atoi(t.Ano)
	episodiosDa := map[int]int{}
	totalDeEpisodios := 0
	for _, s := range t.Temporadas {
		episodiosDa[s.Numero] = s.Episodios
		if s.Numero > 0 {
			totalDeEpisodios += s.Episodios
		}
	}
	temporadas := map[int]*temporadaOrganizada{}
	daTemporada := func(n int) *temporadaOrganizada {
		if temporadas[n] == nil {
			temporadas[n] = &temporadaOrganizada{Numero: n, Pacotes: []opcao{}, Episodios: map[string][]opcao{}}
		}
		return temporadas[n]
	}
	for _, r := range resultados {
		l, ok := lerLancamento(r.Titulo, t.nomes)
		if !ok {
			continue
		}
		if t.Tipo == "filme" {
			// Filme: o ano do lançamento tem de bater (remakes têm o mesmo nome); série não, porque
			// o ano no nome pode ser o da temporada.
			if l.Ano != 0 && ano != 0 && absInt(l.Ano-ano) > 1 {
				continue
			}
			if l.Temporada != 0 || l.Episodio != 0 {
				continue
			}
			v := avaliar(r, 1, limiteDoFilme)
			v.Alertas = alertasDoNome(r.Titulo, t, agora)
			if t.Tipo == "filme" && foraDoDigital(t, agora) && !reCinema.MatchString(r.Titulo) && reFonteDigital.MatchString(r.Titulo) {
				v.Suspeito = true
				v.Pontos -= 80
			}
			o.Versoes = append(o.Versoes, v)
			o.Total++
			continue
		}
		o.Total++
		if l.Absoluto > 0 {
			// Anime: o episódio 15 é o 2 da segunda temporada, se a primeira tem 13.
			l.Temporada, l.Episodio = temporadaDoAbsoluto(l.Absoluto, t.Temporadas)
		}
		switch {
		case l.Episodio > 0 && l.Temporada > 0:
			s := daTemporada(l.Temporada)
			n := strconv.Itoa(l.Episodio)
			s.Episodios[n] = append(s.Episodios[n], avaliar(r, 1, limiteDoEpisodio))
		case l.Temporada > 0 && l.AteTemporada == 0:
			eps := episodiosDa[l.Temporada]
			daTemporada(l.Temporada).Pacotes = append(daTemporada(l.Temporada).Pacotes, avaliar(r, eps, limiteDoEpisodio))
		case l.Completa || l.AteTemporada > 0:
			eps := totalDeEpisodios
			if l.AteTemporada > 0 {
				eps = 0
				for n := l.Temporada; n <= l.AteTemporada; n++ {
					eps += episodiosDa[n]
				}
			}
			o.Completas = append(o.Completas, avaliar(r, eps, limiteDoEpisodio))
		default:
			o.Outros = append(o.Outros, avaliar(r, 1, limiteDoEpisodio))
		}
	}
	o.Versoes = ordenar(o.Versoes)
	o.Completas = ordenar(o.Completas)
	o.Outros = ordenar(o.Outros)
	for _, s := range temporadas {
		s.Pacotes = ordenar(s.Pacotes)
		for n := range s.Episodios {
			s.Episodios[n] = ordenar(s.Episodios[n])
		}
		o.Temporadas = append(o.Temporadas, s)
	}
	sort.Slice(o.Temporadas, func(i, j int) bool { return o.Temporadas[i].Numero < o.Temporadas[j].Numero })
	return o
}

func temporadaDoAbsoluto(n int, temporadas []temporadaDoTMDB) (int, int) {
	resto := n
	for _, s := range temporadas {
		if s.Numero == 0 || s.Episodios == 0 {
			continue
		}
		if resto <= s.Episodios {
			return s.Numero, resto
		}
		resto -= s.Episodios
	}
	return 1, n // o TMDB ainda não sabe desses episódios: fica na primeira temporada
}

// procurarNoProwlarr é a busca da aba Torrents, sem o HTTP em volta.
func (a *app) procurarNoProwlarr(ctx context.Context, c configDoProwlarr, texto, tipo string) ([]resultadoDaBusca, error) {
	q := url.Values{"query": {texto}, "type": {"search"}, "limit": {"100"}, "categories": categoriasDaBusca[tipo]}
	var lancamentos []lancamentoDoProwlarr
	if err := a.pedirAoProwlarr(ctx, c, "/api/v1/search?"+q.Encode(), &lancamentos); err != nil {
		return nil, err
	}
	return resultadosDoProwlarr(lancamentos), nil
}
