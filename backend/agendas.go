package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// As outras agendas dos jogos (S34). O ge continua a principal (é quem sabe os canais do Brasil);
// a ESPN confere horário, placar e ao vivo e traz os jogos que o ge ainda não mostra (a agenda
// dele vai só a uns 5 dias); o guia de TV da FotMob diz o canal quando o ge não diz. Nenhuma das
// três é API oficial: cada uma tem seu estado em "servicos", e a página avisa, discreta, quando
// uma não respondeu ou mudou de formato (aí é o leitor daqui que precisa mudar).

func urlDaESPN() string {
	return env("ESPN_URL", "https://site.api.espn.com/apis/site/v2/sports/soccer/")
}
func urlDaFotMob() string { return env("FOTMOB_URL", "https://www.fotmob.com/pt-BR/tv-guide/br") }

// fonteMudou é a fonte que respondeu num formato que o leitor não entende: precisa mexer no código.
type fonteMudou struct{ error }

func mudou(err error) bool { return errors.As(err, new(fonteMudou)) }

var clienteDasAgendas = &http.Client{Timeout: 20 * time.Second}

func baixarDaFonte(ctx context.Context, nome, endereco string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endereco, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130 Safari/537.36")
	resp, err := clienteDasAgendas.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s não respondeu", nome)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusGone:
		return nil, fonteMudou{fmt.Errorf("%s mudou: o endereço respondeu %d", nome, resp.StatusCode)}
	default:
		return nil, fmt.Errorf("%s respondeu %d", nome, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}

// ---------- Guardar cada agenda por um tempo ----------

type leituraDeJogos struct {
	mu       sync.Mutex
	jogos    []jogo
	lidaEm   time.Time // a última leitura que deu certo
	tentouEm time.Time
	erro     error
}

type agendaDosJogos struct {
	ge, espn, espnEmIngles, fotmob leituraDeJogos
}

// ler devolve o que está guardado, ou lê de novo quando passou a validade (1 minuto depois de um
// erro). Com erro, os jogos da última leitura boa continuam valendo.
func (l *leituraDeJogos) ler(ctx context.Context, agora time.Time, validade func([]jogo, time.Time) time.Duration, ler func(context.Context) ([]jogo, error)) ([]jogo, time.Time, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	espera := validade(l.jogos, agora)
	if l.erro != nil {
		espera = min(espera, time.Minute)
	}
	if l.tentouEm.IsZero() || agora.Sub(l.tentouEm) >= espera {
		l.tentouEm = agora
		jogos, err := ler(ctx)
		if err == nil || len(jogos) > 0 {
			l.jogos, l.lidaEm = jogos, agora
		}
		l.erro = err
	}
	return append([]jogo(nil), l.jogos...), l.lidaEm, l.erro
}

func validadeFixa(d time.Duration) func([]jogo, time.Time) time.Duration {
	return func([]jogo, time.Time) time.Duration { return d }
}

// ---------- ESPN ----------

type ligaDaESPN struct {
	id, campeonato string
	soBrasil       bool // dos amistosos, só os da Seleção
}

var ligasDaESPN = []ligaDaESPN{
	{id: "bra.1", campeonato: "Campeonato Brasileiro Série A"},
	{id: "bra.2", campeonato: "Campeonato Brasileiro Série B"},
	{id: "bra.copa_do_brazil", campeonato: "Copa do Brasil"},
	{id: "conmebol.libertadores", campeonato: "Libertadores"},
	{id: "conmebol.sudamericana", campeonato: "Copa Sul-Americana"},
	{id: "uefa.champions", campeonato: "Champions League"},
	{id: "fifa.cwc", campeonato: "Mundial de Clubes"},
	{id: "eng.1", campeonato: "Premier League"},
	{id: "uefa.nations", campeonato: "Liga das Nações"},
	{id: "fifa.friendly", campeonato: "Amistosos da Seleção Brasileira", soBrasil: true},
	{id: "fifa.worldq.conmebol", campeonato: "Eliminatórias da Copa - América do Sul"},
	{id: "fifa.world", campeonato: "Copa do Mundo"},
	{id: "conmebol.america", campeonato: "Copa América"},
}

type eventoDaESPN struct {
	ID           string `json:"id"`
	Date         string `json:"date"`
	Competitions []struct {
		Competitors []struct {
			HomeAway string `json:"homeAway"`
			Score    string `json:"score"`
			Team     struct {
				DisplayName string `json:"displayName"`
				Logo        string `json:"logo"`
			} `json:"team"`
		} `json:"competitors"`
		Status struct {
			Type struct {
				Name  string `json:"name"`
				State string `json:"state"`
			} `json:"type"`
		} `json:"status"`
	} `json:"competitions"`
}

// mesesDaESPN: a ESPN não aceita intervalo de dias, mas aceita o mês inteiro ("202611"). Os meses
// de ontem até daqui a uma semana.
func mesesDaESPN(agora time.Time) []string {
	var meses []string
	for _, d := range []int{-1, 7} {
		m := agora.In(fusoDeBrasilia).AddDate(0, 0, d).Format("200601")
		if len(meses) == 0 || meses[len(meses)-1] != m {
			meses = append(meses, m)
		}
	}
	return meses
}

// lerESPN lê o placar de todas as ligas, em português (os jogos) ou em inglês (só os nomes, para
// achar o jogo na FotMob). Uma liga que falha não derruba as outras.
func lerESPN(ctx context.Context, agora time.Time, emIngles bool) ([]jogo, error) {
	var (
		mu    sync.Mutex
		jogos []jogo
		erros []error
		wg    sync.WaitGroup
		vagas = make(chan struct{}, 6)
	)
	for _, liga := range ligasDaESPN {
		for _, mes := range mesesDaESPN(agora) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				vagas <- struct{}{}
				defer func() { <-vagas }()
				endereco := urlDaESPN() + liga.id + "/scoreboard?dates=" + mes
				if !emIngles {
					endereco += "&lang=pt&region=br"
				}
				js, err := jogosDaESPN(ctx, endereco, liga, emIngles)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					erros = append(erros, err)
				}
				jogos = append(jogos, js...)
			}()
		}
	}
	wg.Wait()
	if len(erros) == 0 {
		return jogos, nil
	}
	// Uma liga mudada diz mais que uma que não respondeu.
	for _, err := range erros {
		if mudou(err) {
			return jogos, err
		}
	}
	return jogos, erros[0]
}

func jogosDaESPN(ctx context.Context, endereco string, liga ligaDaESPN, emIngles bool) ([]jogo, error) {
	corpo, err := baixarDaFonte(ctx, "a ESPN", endereco)
	if err != nil {
		return nil, err
	}
	var r struct {
		Events *[]eventoDaESPN `json:"events"`
	}
	if err := json.Unmarshal(corpo, &r); err != nil || r.Events == nil {
		return nil, fonteMudou{fmt.Errorf("a ESPN mudou: a liga %s veio sem os jogos", liga.id)}
	}
	var jogos []jogo
	for _, ev := range *r.Events {
		if j, ok := jogoDaESPN(ev, liga, emIngles); ok {
			jogos = append(jogos, j)
		}
	}
	return jogos, nil
}

func jogoDaESPN(ev eventoDaESPN, liga ligaDaESPN, emIngles bool) (jogo, bool) {
	id, err := strconv.ParseInt(ev.ID, 10, 64)
	if err != nil || len(ev.Competitions) == 0 {
		return jogo{}, false
	}
	c := ev.Competitions[0]
	j := jogo{ID: -id, Campeonato: liga.campeonato} // negativo: não se confunde com os do ge
	var gols [2]*int
	for _, t := range c.Competitors {
		i := 0
		if t.HomeAway == "away" {
			i = 1
		}
		tj := timeDoJogo{Nome: t.Team.DisplayName, Escudo: t.Team.Logo}
		if i == 0 {
			j.Mandante = tj
		} else {
			j.Visitante = tj
		}
		if g, err := strconv.Atoi(t.Score); err == nil {
			gols[i] = &g
		}
	}
	if j.Mandante.Nome == "" || j.Visitante.Nome == "" {
		return jogo{}, false
	}
	brasil := map[string]bool{"brasil": true, "brazil": true}
	if liga.soBrasil && !emIngles && !brasil[normalizar(j.Mandante.Nome)] && !brasil[normalizar(j.Visitante.Nome)] {
		return jogo{}, false
	}
	inicio, err := time.Parse("2006-01-02T15:04Z07:00", ev.Date)
	if err != nil {
		if inicio, err = time.Parse(time.RFC3339, ev.Date); err != nil {
			return jogo{}, false
		}
	}
	local := inicio.In(fusoDeBrasilia)
	j.inicio, j.Inicio, j.Dia, j.Hora = inicio, inicio.UTC().Format(time.RFC3339), local.Format("2006-01-02"), local.Format("15:04")
	switch c.Status.Type.State {
	case "in":
		j.Momento = "ao_vivo"
	case "post":
		j.Momento = "encerrado"
	default:
		j.Momento = "antes"
	}
	switch c.Status.Type.Name {
	case "STATUS_POSTPONED", "STATUS_CANCELED", "STATUS_SUSPENDED", "STATUS_ABANDONED":
		j.adiado = true
	}
	if j.Momento != "antes" && gols[0] != nil && gols[1] != nil {
		j.Placar = &placarDoJogo{Mandante: *gols[0], Visitante: *gols[1]}
	}
	return j, true
}

// ---------- FotMob ----------

var reDadosDaFotMob = regexp.MustCompile(`(?s)<script id="__NEXT_DATA__"[^>]*>(.*?)</script>`)

func lerFotMob(ctx context.Context) ([]jogo, error) {
	corpo, err := baixarDaFonte(ctx, "o guia da FotMob", urlDaFotMob())
	if err != nil {
		return nil, err
	}
	return jogosDaFotMob(string(corpo))
}

// jogosDaFotMob acha os jogos do guia de TV nos dados da página, onde estiverem: todo objeto com
// "home", "away", "utcTime" e "channels".
func jogosDaFotMob(pagina string) ([]jogo, error) {
	m := reDadosDaFotMob.FindStringSubmatch(pagina)
	if m == nil {
		return nil, fonteMudou{errors.New("o guia da FotMob mudou: os dados não estão mais na página")}
	}
	var dados any
	if err := json.Unmarshal([]byte(m[1]), &dados); err != nil {
		return nil, fonteMudou{fmt.Errorf("o guia da FotMob mudou: %v", err)}
	}
	vistos := map[float64]bool{}
	var jogos []jogo
	var andar func(any)
	andar = func(o any) {
		switch v := o.(type) {
		case []any:
			for _, x := range v {
				andar(x)
			}
		case map[string]any:
			if j, ok := jogoDaFotMob(v); ok {
				if id, _ := v["id"].(float64); !vistos[id] {
					vistos[id] = true
					jogos = append(jogos, j)
				}
				return
			}
			for _, x := range v {
				andar(x)
			}
		}
	}
	andar(dados)
	if len(jogos) == 0 {
		return nil, fonteMudou{errors.New("o guia da FotMob mudou: nenhum jogo na página")}
	}
	return jogos, nil
}

func jogoDaFotMob(v map[string]any) (jogo, bool) {
	nome := func(k string) string {
		t, _ := v[k].(map[string]any)
		s, _ := t["name"].(string)
		return s
	}
	quando, _ := v["utcTime"].(string)
	canais, temCanais := v["channels"].([]any)
	mand, vis := nome("home"), nome("away")
	inicio, err := time.Parse(time.RFC3339, quando)
	if !temCanais || mand == "" || vis == "" || err != nil {
		return jogo{}, false
	}
	j := jogo{Mandante: timeDoJogo{Nome: mand}, Visitante: timeDoJogo{Nome: vis}, inicio: inicio, Momento: "antes"}
	if aoVivo, _ := v["isLive"].(bool); aoVivo {
		j.Momento = "ao_vivo"
	}
	for _, c := range canais {
		n, _ := c.(map[string]any)["name"].(string)
		if n = strings.TrimSpace(n); n != "" && !strings.Contains(normalizar(n), "a confirmar") {
			j.transmissoes = append(j.transmissoes, transmissao{nome: n})
		}
	}
	return j, true
}

// ---------- Juntar as agendas ----------

// mesmoTime: o nome inteiro de um está no outro, ou a palavra que distingue ("Vasco" e "Vasco da
// Gama", "Athletic Club" e "Athletic").
func mesmoTime(a, b string) bool {
	pa, pb := palavrasDe(a), palavrasDe(b)
	return pa == pb || timeNoTitulo(a, pb) || timeNoTitulo(b, pa)
}

func nomesDoTime(j jogo, i int) []string {
	t := j.Mandante
	if i == 1 {
		t = j.Visitante
	}
	return append([]string{t.Nome}, j.apelidos[i]...)
}

func timeBate(a jogo, i int, b jogo, k int) bool {
	for _, x := range nomesDoTime(a, i) {
		for _, y := range nomesDoTime(b, k) {
			if mesmoTime(x, y) {
				return true
			}
		}
	}
	return false
}

// casam: os dois times (em qualquer ordem) com até um dia e meio de diferença, ou um time só no
// mesmo horário (um nome muito diferente numa das fontes).
func casam(a, b jogo) bool {
	var dif time.Duration
	if !a.inicio.IsZero() && !b.inicio.IsZero() {
		if dif = a.inicio.Sub(b.inicio).Abs(); dif > 36*time.Hour {
			return false
		}
	} else if a.Dia != b.Dia {
		return false
	}
	if (timeBate(a, 0, b, 0) && timeBate(a, 1, b, 1)) || (timeBate(a, 0, b, 1) && timeBate(a, 1, b, 0)) {
		return true
	}
	um := timeBate(a, 0, b, 0) || timeBate(a, 1, b, 1) || timeBate(a, 0, b, 1) || timeBate(a, 1, b, 0)
	return um && !a.inicio.IsZero() && !b.inicio.IsZero() && dif <= 15*time.Minute
}

var pesoDoMomento = map[string]int{"antes": 0, "ao_vivo": 1, "encerrado": 2}

// conferir compara o jogo com o de outra fonte: confirma, ou guarda o que ela diz diferente.
func conferir(j *jogo, o jogo, quem string) {
	switch {
	case o.adiado:
		j.Divergencia = "Na " + quem + ": adiado"
	case o.inicio.IsZero():
	case j.inicio.IsZero():
		j.Dia, j.Hora, j.Inicio, j.inicio = o.inicio.In(fusoDeBrasilia).Format("2006-01-02"), o.inicio.In(fusoDeBrasilia).Format("15:04"), o.Inicio, o.inicio
		j.ConfirmadoPor = append(j.ConfirmadoPor, quem)
	case j.inicio.Sub(o.inicio).Abs() > 10*time.Minute:
		if j.Divergencia == "" {
			l := o.inicio.In(fusoDeBrasilia)
			quando := l.Format("15:04")
			if l.Format("2006-01-02") != j.Dia {
				quando = l.Format("02/01, 15:04")
			}
			j.Divergencia = "Na " + quem + ": " + quando
		}
	default:
		j.ConfirmadoPor = append(j.ConfirmadoPor, quem)
	}
}

func golsDe(p *placarDoJogo) int { return p.Mandante + p.Visitante }

// atualizarPlacar: vale a fonte mais adiantada (ao vivo antes de encerrado, mais gols antes de menos).
func atualizarPlacar(j *jogo, o jogo) {
	if pesoDoMomento[o.Momento] > pesoDoMomento[j.Momento] {
		j.Momento, j.Placar = o.Momento, o.Placar
	} else if o.Momento == j.Momento && o.Placar != nil && (j.Placar == nil || golsDe(o.Placar) > golsDe(j.Placar)) {
		j.Placar = o.Placar
	}
}

// juntarAgendas: os jogos do ge, conferidos pela ESPN e pela FotMob; os jogos que só a ESPN tem
// (de ontem a uma semana) entram também, com o canal pela FotMob.
func juntarAgendas(ge, espn, espnEmIngles, fotmob []jogo, hoje time.Time) []jogo {
	emIngles := map[int64][2]string{}
	for _, j := range espnEmIngles {
		emIngles[j.ID] = [2]string{j.Mandante.Nome, j.Visitante.Nome}
	}
	desde, ate := hoje.AddDate(0, 0, -1).Format("2006-01-02"), hoje.AddDate(0, 0, 7).Format("2006-01-02")
	lista := append([]jogo(nil), ge...)
	doGe := len(lista)
	for _, e := range espn {
		if n, ok := emIngles[e.ID]; ok {
			e.apelidos = [2][]string{{n[0]}, {n[1]}}
		}
		achou := false
		for i := range lista[:doGe] {
			if !casam(lista[i], e) {
				continue
			}
			achou = true
			j := &lista[i]
			conferir(j, e, "ESPN")
			if !e.adiado {
				atualizarPlacar(j, e)
			}
			for k, t := range []timeDoJogo{e.Mandante, e.Visitante} {
				j.apelidos[k] = append(j.apelidos[k], append([]string{t.Nome}, e.apelidos[k]...)...)
			}
			break
		}
		if !achou && !e.adiado && e.Dia >= desde && e.Dia <= ate {
			lista = append(lista, e)
		}
	}
	for _, f := range fotmob {
		for i := range lista {
			if !casam(lista[i], f) {
				continue
			}
			conferir(&lista[i], f, "FotMob")
			if len(lista[i].transmissoes) == 0 {
				lista[i].transmissoes = f.transmissoes
			}
			break
		}
	}
	sort.SliceStable(lista, func(i, j int) bool {
		if lista[i].Dia != lista[j].Dia {
			return lista[i].Dia < lista[j].Dia
		}
		return lista[i].Hora < lista[j].Hora
	})
	return lista
}

// ---------- O estado das fontes ----------

type servicoDosJogos struct {
	ID     string `json:"id"`
	Nome   string `json:"nome"`
	OK     bool   `json:"ok"`
	Mudou  bool   `json:"mudou,omitempty"` // respondeu num formato que o leitor não entende
	Erro   string `json:"erro,omitempty"`
	LidaEm string `json:"lida_em,omitempty"` // a última leitura que deu certo
}

func servicoDe(id, nome string, lidaEm time.Time, err error) servicoDosJogos {
	s := servicoDosJogos{ID: id, Nome: nome, OK: err == nil}
	if err != nil {
		s.Erro, s.Mudou = err.Error(), mudou(err)
	}
	if !lidaEm.IsZero() {
		s.LidaEm = lidaEm.UTC().Format(time.RFC3339)
	}
	return s
}

// jogosDasFontes lê as quatro leituras juntas e devolve a lista junta, o estado de cada serviço e,
// do ge, quando leu e o erro.
func (a *app) jogosDasFontes(ctx context.Context) ([]jogo, []servicoDosJogos, time.Time, string) {
	agora := a.agora()
	type resultado struct {
		jogos  []jogo
		lidaEm time.Time
		erro   error
	}
	var ge, espn, emIngles, fotmob resultado
	var wg sync.WaitGroup
	ler := func(r *resultado, l *leituraDeJogos, validade func([]jogo, time.Time) time.Duration, f func(context.Context) ([]jogo, error)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.jogos, r.lidaEm, r.erro = l.ler(ctx, agora, validade, f)
		}()
	}
	ag := &a.agenda
	ler(&ge, &ag.ge, validadeDaAgenda, func(ctx context.Context) ([]jogo, error) { return lerAgendaDoGe(ctx, urlDaAgenda()) })
	ler(&espn, &ag.espn, validadeDaAgenda, func(ctx context.Context) ([]jogo, error) { return lerESPN(ctx, agora, false) })
	ler(&emIngles, &ag.espnEmIngles, validadeFixa(6*time.Hour), func(ctx context.Context) ([]jogo, error) { return lerESPN(ctx, agora, true) })
	ler(&fotmob, &ag.fotmob, validadeFixa(30*time.Minute), lerFotMob)
	wg.Wait()

	erroDaESPN := espn.erro
	if erroDaESPN == nil {
		erroDaESPN = emIngles.erro
	}
	servicos := []servicoDosJogos{
		servicoDe("ge", "ge", ge.lidaEm, ge.erro),
		servicoDe("espn", "ESPN", espn.lidaEm, erroDaESPN),
		servicoDe("fotmob", "FotMob", fotmob.lidaEm, fotmob.erro),
	}
	erro := ""
	if ge.erro != nil {
		erro = ge.erro.Error()
	}
	return juntarAgendas(ge.jogos, espn.jogos, emIngles.jogos, fotmob.jogos, agora.In(fusoDeBrasilia)), servicos, ge.lidaEm, erro
}
