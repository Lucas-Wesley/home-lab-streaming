package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // o fuso de Brasília, mesmo sem o tzdata do sistema
)

// Jogos do dia (S33): a agenda do ge diz os jogos, o placar, se está ao vivo e onde passa
// (sportv, Premiere, Disney+…); o guia da assinatura diz em qual canal exato (SporTV 2, ESPN 4),
// achando "Time x Time" no horário do jogo. Sem o guia (Premiere, Disney+), o jogo leva ao grupo
// de canais da emissora; fora da assinatura (Globoplay, CazéTV), ao site.

func urlDaAgenda() string { return env("AGENDA_URL", "https://ge.globo.com/agenda/") }

var fusoDeBrasilia = func() *time.Location {
	if l, err := time.LoadLocation("America/Sao_Paulo"); err == nil {
		return l
	}
	return time.FixedZone("BRT", -3*3600)
}()

// Os campeonatos que entram (nomes do ge, normalizados e sem o ano), e qualquer jogo da Seleção.
var campeonatosDoDia = map[string]bool{
	"campeonato brasileiro": true, "campeonato brasileiro serie a": true, "campeonato brasileiro serie b": true,
	"copa do brasil": true, "taca conmebol libertadores": true, "copa libertadores": true, "libertadores": true,
	"copa sul-americana": true, "champions league": true, "liga dos campeoes": true,
	"copa do mundo de clubes da fifa": true, "mundial de clubes": true, "copa intercontinental": true,
	"premier league": true, "liga das nacoes": true, "amistosos da selecao brasileira": true,
	"eliminatorias da copa - america do sul": true, "copa do mundo da fifa": true, "copa do mundo": true, "copa america": true,
}

var reAnoDoCampeonato = regexp.MustCompile(`\s+\d{4}([/-]\d{2,4})?$`)

func nomeDoCampeonato(s string) string {
	s = strings.NewReplacer("™", "", "®", "").Replace(normalizar(s))
	return strings.Join(strings.Fields(reAnoDoCampeonato.ReplaceAllString(strings.TrimSpace(s), "")), " ")
}

// ---------- Grupos de canais da assinatura ----------

// grupoDeCanais junta os canais de uma emissora; fontes são os nomes que o ge usa para ela.
type grupoDeCanais struct {
	ID, Nome  string
	padrao    *regexp.Regexp // no nome normalizado do canal
	fontes    []string
	principal bool     // aparece em "Esportes"
	guia      bool     // o provedor tem a programação: dá para achar o canal exato
	tambem    []string // outros grupos onde o jogo costuma passar (Disney+ também passa na ESPN)
}

var gruposDeCanais = []grupoDeCanais{
	{ID: "espn", Nome: "ESPN", padrao: regexp.MustCompile(`^espn\b`), fontes: []string{"espn"}, principal: true, guia: true},
	{ID: "sportv", Nome: "SporTV", padrao: regexp.MustCompile(`^sportv\b`), fontes: []string{"sportv"}, principal: true, guia: true},
	{ID: "premiere", Nome: "Premiere", padrao: regexp.MustCompile(`^premiere\b`), fontes: []string{"premiere"}, principal: true, guia: true},
	// A Globo de São Paulo, que é a que o Lucas assiste.
	{ID: "globo", Nome: "Globo", padrao: regexp.MustCompile(`^globo sp: sao paulo\b`), fontes: []string{"globo", "tv globo"}, guia: true},
	{ID: "disney", Nome: "Disney+", padrao: regexp.MustCompile(`^disney\+: canal`), fontes: []string{"disney+", "disney plus", "disney+ premium"}, tambem: []string{"espn"}},
	{ID: "prime", Nome: "Prime Video", padrao: regexp.MustCompile(`^prime video: canal`), fontes: []string{"prime video", "amazon prime video"}},
	{ID: "hbo", Nome: "HBO Max", padrao: regexp.MustCompile(`^hbo max: canal`), fontes: []string{"hbo max", "max"}},
	{ID: "dazn", Nome: "DAZN", padrao: regexp.MustCompile(`^dazn: canal`), fontes: []string{"dazn"}},
	{ID: "getv", Nome: "GE TV", padrao: regexp.MustCompile(`^ge tv\b`), fontes: []string{"ge tv"}},
	{ID: "sportynet", Nome: "SportyNet", padrao: regexp.MustCompile(`^sportynet`), fontes: []string{"sportynet"}},
	{ID: "bandsports", Nome: "BandSports", padrao: regexp.MustCompile(`^bandsports\b`), fontes: []string{"bandsports"}},
	{ID: "band", Nome: "Band", padrao: regexp.MustCompile(`^band (sd|hd|fhd)\b`), fontes: []string{"band"}},
}

func grupoDaFonte(nome string) (grupoDeCanais, bool) {
	n := normalizar(strings.TrimSpace(nome))
	for _, g := range gruposDeCanais {
		for _, f := range g.fontes {
			if n == f {
				return g, true
			}
		}
	}
	// "ESPN 2", "SporTV 3", "Disney+ Premium" (a FotMob diz o canal): o grupo pelo começo do nome.
	for _, g := range gruposDeCanais {
		for _, f := range g.fontes {
			if strings.HasPrefix(n, f+" ") {
				return g, true
			}
		}
	}
	return grupoDeCanais{}, false
}

type programa struct {
	Titulo string `json:"titulo"`
	Inicio string `json:"inicio"`
	Fim    string `json:"fim"`
	inicio time.Time
	fim    time.Time
}

type versaoDoCanal struct {
	ID   int64  `json:"id"`
	Nome string `json:"nome"`
}

// canalAgrupado é um canal só, com a melhor versão (HD, FHD, H265, servidor reserva²) à frente.
type canalAgrupado struct {
	ID        int64           `json:"id"`
	Nome      string          `json:"nome"`
	Capa      string          `json:"capa,omitempty"`
	Versoes   []versaoDoCanal `json:"versoes"`
	Agora     *programa       `json:"agora,omitempty"`
	Depois    *programa       `json:"depois,omitempty"`
	nota      int
	ordem     int
	chave     string
	guiaLista []programa
}

type grupoComCanais struct {
	ID        string          `json:"id"`
	Nome      string          `json:"nome"`
	Principal bool            `json:"principal"`
	Canais    []canalAgrupado `json:"canais"`
	guia      bool
}

var qualidades = map[string]int{"fhd": 4, "hd": 3, "uhd": 4, "4k": 4, "sd": 1, "h265": -2, "h.265": -2, "265": -2}

// baseDoCanal tira do nome a qualidade e a marca do servidor reserva: "ESPN 1 FHD H265²" é
// "ESPN 1", nota 2 (FHD 4, H265 −2, reserva −1). Sem qualidade no nome, nota 3.
func baseDoCanal(nome string) (string, int) {
	limpo := strings.TrimRight(strings.TrimSpace(nome), "²³*¹ ")
	nota := 0
	if limpo != strings.TrimSpace(nome) {
		nota-- // ² e ³ são servidores reserva
	}
	campos := strings.Fields(limpo)
	achou := false
	for len(campos) > 1 {
		q, ok := qualidades[strings.ToLower(campos[len(campos)-1])]
		if !ok {
			break
		}
		nota += q
		achou = true
		campos = campos[:len(campos)-1]
	}
	if !achou {
		nota += 3
	}
	return strings.Join(campos, " "), nota
}

var reNumeroDoCanal = regexp.MustCompile(`\d+`)

func (a *app) gruposComCanais(ctx context.Context) ([]grupoComCanais, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT i.id, i.nome, i.capa FROM itens i WHERE i.tipo = 'canal' AND `+filtroAdulto+` ORDER BY i.ordem`, a.mostrarAdulto())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	porGrupo := map[string]map[string]*canalAgrupado{}
	for rows.Next() {
		var id int64
		var nome, capa string
		if err := rows.Scan(&id, &nome, &capa); err != nil {
			return nil, err
		}
		n := normalizar(nome)
		for _, g := range gruposDeCanais {
			if !g.padrao.MatchString(n) {
				continue
			}
			base, nota := baseDoCanal(nome)
			chave := normalizar(base)
			if porGrupo[g.ID] == nil {
				porGrupo[g.ID] = map[string]*canalAgrupado{}
			}
			c := porGrupo[g.ID][chave]
			if c == nil {
				c = &canalAgrupado{chave: chave, nota: -100, ordem: 1000}
				if m := reNumeroDoCanal.FindString(base); m != "" {
					c.ordem, _ = strconv.Atoi(m)
				}
				porGrupo[g.ID][chave] = c
			}
			c.Versoes = append(c.Versoes, versaoDoCanal{ID: id, Nome: nome})
			if nota > c.nota {
				c.ID, c.Nome, c.nota = id, base, nota
				c.Capa = primeiro(capa, c.Capa)
			} else if c.Capa == "" {
				c.Capa = capa
			}
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var lista []grupoComCanais
	for _, g := range gruposDeCanais {
		if len(porGrupo[g.ID]) == 0 {
			continue
		}
		gc := grupoComCanais{ID: g.ID, Nome: g.Nome, Principal: g.principal, guia: g.guia}
		for _, c := range porGrupo[g.ID] {
			gc.Canais = append(gc.Canais, *c)
		}
		sort.Slice(gc.Canais, func(i, j int) bool {
			if gc.Canais[i].ordem != gc.Canais[j].ordem {
				return gc.Canais[i].ordem < gc.Canais[j].ordem
			}
			return gc.Canais[i].chave < gc.Canais[j].chave
		})
		lista = append(lista, gc)
	}
	return lista, nil
}

// ---------- Guia de programação (get_short_epg) ----------

type guiasDosCanais struct {
	mu     sync.Mutex
	porId  map[int64][]programa
	lidoEm map[int64]time.Time
}

const validadeDoGuia = 20 * time.Minute

func (a *app) guiaDoCanal(ctx context.Context, ac acesso, id int64) []programa {
	g := &a.guias
	g.mu.Lock()
	if t, ok := g.lidoEm[id]; ok && a.agora().Sub(t) < validadeDoGuia {
		p := g.porId[id]
		g.mu.Unlock()
		return p
	}
	g.mu.Unlock()

	var r struct {
		Lista []struct {
			Titulo string `json:"title"`
			Inicio flex   `json:"start_timestamp"`
			Fim    flex   `json:"stop_timestamp"`
		} `json:"epg_listings"`
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var lista []programa
	if err := a.buscarJSON(ctx, ac.urlDaApi("get_short_epg", url.Values{"stream_id": {fmt.Sprint(id)}, "limit": {"40"}}), &r); err == nil {
		for _, x := range r.Lista {
			t, err := base64.StdEncoding.DecodeString(x.Titulo)
			if err != nil || x.Inicio.int() == 0 {
				continue
			}
			ini, fim := time.Unix(x.Inicio.int(), 0), time.Unix(x.Fim.int(), 0)
			lista = append(lista, programa{Titulo: strings.TrimSpace(string(t)), Inicio: ini.UTC().Format(time.RFC3339), Fim: fim.UTC().Format(time.RFC3339), inicio: ini, fim: fim})
		}
	}
	g.mu.Lock()
	if g.porId == nil {
		g.porId, g.lidoEm = map[int64][]programa{}, map[int64]time.Time{}
	}
	g.porId[id], g.lidoEm[id] = lista, a.agora()
	g.mu.Unlock()
	return lista
}

// lerGuias preenche o guia dos canais dos grupos que têm programação, até 6 pedidos por vez.
func (a *app) lerGuias(ctx context.Context, grupos []grupoComCanais, quais map[string]bool) {
	ac, err := a.acesso()
	if err != nil || !ac.completo() {
		return
	}
	var wg sync.WaitGroup
	vagas := make(chan struct{}, 6)
	agora := a.agora()
	for gi := range grupos {
		if !grupos[gi].guia || !quais[grupos[gi].ID] {
			continue
		}
		for ci := range grupos[gi].Canais {
			c := &grupos[gi].Canais[ci]
			wg.Add(1)
			go func() {
				defer wg.Done()
				vagas <- struct{}{}
				defer func() { <-vagas }()
				c.guiaLista = a.guiaDoCanal(ctx, ac, c.ID)
				for i, p := range c.guiaLista {
					if !p.inicio.After(agora) && p.fim.After(agora) {
						c.Agora = &c.guiaLista[i]
						if i+1 < len(c.guiaLista) {
							c.Depois = &c.guiaLista[i+1]
						}
						break
					}
				}
			}()
		}
	}
	wg.Wait()
}

func (a *app) handleCanaisDeEsportes(w http.ResponseWriter, r *http.Request) {
	grupos, err := a.gruposComCanais(r.Context())
	if err != nil {
		falha(w, err)
		return
	}
	principais := map[string]bool{}
	for _, g := range gruposDeCanais {
		principais[g.ID] = g.principal
	}
	a.lerGuias(r.Context(), grupos, principais)
	if grupos == nil {
		grupos = []grupoComCanais{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"grupos": grupos})
}

// ---------- A agenda do ge ----------

type timeDoGe struct {
	PopularName string `json:"popularName"`
	BadgeSvg    string `json:"badgeSvg"`
	BadgePng    string `json:"badgePng"`
}

type partidaDoGe struct {
	ID               int64     `json:"id"`
	FirstContestant  *timeDoGe `json:"firstContestant"`
	SecondContestant *timeDoGe `json:"secondContestant"`
	StartDate        string    `json:"startDate"`
	StartHour        *string   `json:"startHour"`
	Moment           string    `json:"moment"`
	Scoreboard       *struct {
		Home    *int `json:"home"`
		Away    *int `json:"away"`
		Penalty *struct {
			Home *int `json:"home"`
			Away *int `json:"away"`
		} `json:"penalty"`
	} `json:"scoreboard"`
	LiveWatchSources []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"liveWatchSources"`
	Phase *struct {
		Name                string `json:"name"`
		ChampionshipEdition *struct {
			Championship struct {
				Name string `json:"name"`
			} `json:"championship"`
		} `json:"championshipEdition"`
	} `json:"phase"`
	Transmission *struct {
		BroadcastStatus *struct {
			ID string `json:"id"`
		} `json:"broadcastStatus"`
		URL string `json:"url"`
	} `json:"transmission"`
}

type agendaDoGe map[string]struct {
	ChampionshipsAgenda []struct {
		Championship struct {
			Name string `json:"name"`
		} `json:"championship"`
		Past   []struct{ Match *partidaDoGe } `json:"past"`
		Now    []struct{ Match *partidaDoGe } `json:"now"`
		Future []struct{ Match *partidaDoGe } `json:"future"`
	} `json:"championshipsAgenda"`
}

var (
	reAgendaDoGe  = regexp.MustCompile(`window\.dataSportsSchedule\s*=\s*\{\s*sport:\s*`)
	reSelecaoDoGe = regexp.MustCompile(`window\.brazilianMaleNationalTeamData\s*=\s*`)
)

type timeDoJogo struct {
	Nome   string `json:"nome"`
	Escudo string `json:"escudo,omitempty"`
}

type placarDoJogo struct {
	Mandante          int  `json:"mandante"`
	Visitante         int  `json:"visitante"`
	PenaltisMandante  *int `json:"penaltis_mandante,omitempty"`
	PenaltisVisitante *int `json:"penaltis_visitante,omitempty"`
}

type canalDoJogo struct {
	ID       int64  `json:"id"`
	Nome     string `json:"nome"`
	Capa     string `json:"capa,omitempty"`
	Programa string `json:"programa,omitempty"` // o título no guia, que confirmou o canal
}

// fonteDoJogo é onde o jogo passa: um canal exato da assinatura, um grupo dela (para escolher) ou
// um site de fora.
type fonteDoJogo struct {
	Nome  string       `json:"nome"`
	Canal *canalDoJogo `json:"canal,omitempty"`
	Grupo string       `json:"grupo,omitempty"`
	Link  string       `json:"link,omitempty"`
}

type jogo struct {
	ID         int64         `json:"id"`
	Campeonato string        `json:"campeonato"`
	Fase       string        `json:"fase,omitempty"`
	Dia        string        `json:"dia"`            // no fuso de Brasília
	Hora       string        `json:"hora,omitempty"` // vazia enquanto não tem horário
	Inicio     string        `json:"inicio,omitempty"`
	Mandante   timeDoJogo    `json:"mandante"`
	Visitante  timeDoJogo    `json:"visitante"`
	Placar     *placarDoJogo `json:"placar,omitempty"`
	Momento    string        `json:"momento"` // antes, ao_vivo ou encerrado
	Fontes     []fonteDoJogo `json:"fontes"`
	Pagina     string        `json:"pagina,omitempty"`
	// As outras fontes (S34): quem mais tem o jogo no mesmo horário, e o que diz diferente.
	ConfirmadoPor []string `json:"confirmado_por,omitempty"`
	Divergencia   string   `json:"divergencia,omitempty"`
	inicio        time.Time
	transmissoes  []transmissao
	apelidos      [2][]string // outros nomes dos times (a ESPN em inglês), para achar o jogo na FotMob
	adiado        bool
}

// transmissao é onde a fonte diz que o jogo passa, ainda sem ligar aos canais da assinatura.
type transmissao struct{ nome, link string }

// lerAgendaDoGe baixa a página da agenda e tira dela os jogos dos campeonatos escolhidos.
func lerAgendaDoGe(ctx context.Context, endereco string) ([]jogo, error) {
	corpo, err := baixarDaFonte(ctx, "a agenda do ge", endereco)
	if err != nil {
		return nil, err
	}
	return jogosDaPagina(string(corpo))
}

func jogosDaPagina(pagina string) ([]jogo, error) {
	loc := reAgendaDoGe.FindStringIndex(pagina)
	if loc == nil {
		return nil, fonteMudou{errors.New("a agenda do ge mudou: os jogos não estão mais na página")}
	}
	var agenda agendaDoGe
	if err := json.NewDecoder(strings.NewReader(pagina[loc[1]:])).Decode(&agenda); err != nil {
		return nil, fonteMudou{fmt.Errorf("a agenda do ge mudou: %v", err)}
	}
	vistos := map[int64]bool{}
	var jogos []jogo
	juntar := func(p *partidaDoGe, campeonato, momento string) {
		if p == nil || p.FirstContestant == nil || p.SecondContestant == nil || vistos[p.ID] {
			return
		}
		mand, vis := p.FirstContestant.PopularName, p.SecondContestant.PopularName
		if !campeonatosDoDia[nomeDoCampeonato(campeonato)] && normalizar(mand) != "brasil" && normalizar(vis) != "brasil" {
			return
		}
		vistos[p.ID] = true
		jogos = append(jogos, jogoDoGe(p, campeonato, momento))
	}
	for _, dia := range agenda {
		for _, c := range dia.ChampionshipsAgenda {
			for _, e := range c.Past {
				juntar(e.Match, c.Championship.Name, "encerrado")
			}
			for _, e := range c.Now {
				juntar(e.Match, c.Championship.Name, "ao_vivo")
			}
			for _, e := range c.Future {
				juntar(e.Match, c.Championship.Name, "antes")
			}
		}
	}
	// A Seleção tem uma lista própria, que vai além dos 5 dias da agenda.
	if loc := reSelecaoDoGe.FindStringIndex(pagina); loc != nil {
		var s struct {
			Matches []*partidaDoGe `json:"matches"`
		}
		if json.NewDecoder(strings.NewReader(pagina[loc[1]:])).Decode(&s) == nil {
			for _, p := range s.Matches {
				campeonato := "Seleção Brasileira"
				if p != nil && p.Phase != nil && p.Phase.ChampionshipEdition != nil && p.Phase.ChampionshipEdition.Championship.Name != "" {
					campeonato = p.Phase.ChampionshipEdition.Championship.Name
				}
				momento := map[string]string{"PAST": "encerrado", "NOW": "ao_vivo"}[strings.ToUpper(p.momento())]
				juntar(p, campeonato, primeiro(momento, "antes"))
			}
		}
	}
	sort.SliceStable(jogos, func(i, j int) bool {
		if jogos[i].Dia != jogos[j].Dia {
			return jogos[i].Dia < jogos[j].Dia
		}
		return jogos[i].Hora < jogos[j].Hora
	})
	return jogos, nil
}

func (p *partidaDoGe) momento() string {
	if p == nil {
		return ""
	}
	return p.Moment
}

func escudo(t *timeDoGe) string {
	return primeiro(t.BadgeSvg, t.BadgePng)
}

func jogoDoGe(p *partidaDoGe, campeonato, momento string) jogo {
	j := jogo{
		ID: p.ID, Campeonato: campeonato, Dia: p.StartDate, Momento: momento,
		Mandante:  timeDoJogo{Nome: p.FirstContestant.PopularName, Escudo: escudo(p.FirstContestant)},
		Visitante: timeDoJogo{Nome: p.SecondContestant.PopularName, Escudo: escudo(p.SecondContestant)},
	}
	if p.Phase != nil {
		j.Fase = p.Phase.Name
	}
	if p.StartHour != nil && len(*p.StartHour) >= 5 {
		j.Hora = (*p.StartHour)[:5]
		if t, err := time.ParseInLocation("2006-01-02 15:04", p.StartDate+" "+j.Hora, fusoDeBrasilia); err == nil {
			j.inicio = t
			j.Inicio = t.UTC().Format(time.RFC3339)
		}
	}
	if s := p.Scoreboard; s != nil && s.Home != nil && s.Away != nil && momento != "antes" {
		j.Placar = &placarDoJogo{Mandante: *s.Home, Visitante: *s.Away}
		if s.Penalty != nil {
			j.Placar.PenaltisMandante, j.Placar.PenaltisVisitante = s.Penalty.Home, s.Penalty.Away
		}
	}
	if p.Transmission != nil {
		j.Pagina = p.Transmission.URL
		if b := p.Transmission.BroadcastStatus; b != nil && strings.Contains(strings.ToUpper(b.ID), "AO_VIVO") {
			j.Momento = "ao_vivo"
		}
	}
	for _, f := range p.LiveWatchSources {
		j.transmissoes = append(j.transmissoes, transmissao{f.Name, f.URL})
	}
	return j
}

// ---------- Ligar o jogo ao canal ----------

var nomesDasFontes = map[string]string{
	"sportv": "SporTV", "globoplay": "Globoplay", "prime video": "Prime Video", "ge tv": "GE TV",
	"cazetv": "CazéTV", "youtube": "YouTube", "premiere": "Premiere", "globo": "Globo",
}

// Sites de quem não está na assinatura, quando o ge não dá o endereço.
var sitesDasFontes = map[string]string{
	"globoplay": "https://globoplay.globo.com/", "disney+": "https://www.disneyplus.com/", "prime video": "https://www.primevideo.com/",
	"cazetv": "https://www.youtube.com/@CazeTV", "youtube": "https://www.youtube.com/", "hbo max": "https://www.max.com/",
	"max": "https://www.max.com/", "dazn": "https://www.dazn.com/", "paramount+": "https://www.paramountplus.com/",
	"ge tv": "https://globoplay.globo.com/categorias/esportes/", "premiere": "https://premiere.globo.com/",
}

var palavrasComuns = map[string]bool{
	"atletico": true, "clube": true, "club": true, "sport": true, "esporte": true, "futebol": true, "real": true,
	"fc": true, "ec": true, "sc": true, "ac": true, "de": true, "do": true, "da": true, "united": true, "city": true,
	"sao": true, "santa": true, "racing": true, "deportivo": true, "independiente": true, "time": true,
}

var reSeparadores = regexp.MustCompile(`[^a-z0-9]+`)

func palavrasDe(s string) string {
	return " " + strings.TrimSpace(reSeparadores.ReplaceAllString(normalizar(s), " ")) + " "
}

// timeNoTitulo: o nome inteiro do time, ou a palavra que o distingue ("paulo" em São Paulo).
func timeNoTitulo(nome, titulo string) bool {
	t := palavrasDe(nome)
	if strings.Contains(titulo, t) {
		return true
	}
	melhor := ""
	for _, p := range strings.Fields(t) {
		if len(p) >= 4 && !palavrasComuns[p] && len(p) > len(melhor) {
			melhor = p
		}
	}
	return melhor != "" && strings.Contains(titulo, " "+melhor+" ")
}

// casaComOJogo: o programa começa perto da hora do jogo e tem os dois times (ou um, com "x" e
// começando quase junto).
func casaComOJogo(p programa, j jogo) bool {
	if j.inicio.IsZero() {
		return false
	}
	dif := p.inicio.Sub(j.inicio)
	if dif < -45*time.Minute || dif > 20*time.Minute {
		return false
	}
	t := palavrasDe(p.Titulo)
	m, v := timeNoTitulo(j.Mandante.Nome, t), timeNoTitulo(j.Visitante.Nome, t)
	if m && v {
		return true
	}
	return (m || v) && strings.Contains(t, " x ") && dif.Abs() <= 20*time.Minute
}

func linkDeFora(nome, link string) string {
	if u, err := url.Parse(link); err == nil && u.Scheme == "https" && u.Host != "" {
		return u.Scheme + "://" + u.Host + u.Path // sem os parâmetros de campanha
	}
	return sitesDasFontes[normalizar(nome)]
}

// ligarAosCanais decide, fonte a fonte, o canal exato, o grupo da assinatura ou o site.
func ligarAosCanais(jogos []jogo, grupos []grupoComCanais) {
	porID := map[string]*grupoComCanais{}
	for i := range grupos {
		porID[grupos[i].ID] = &grupos[i]
	}
	for ji := range jogos {
		j := &jogos[ji]
		j.Fontes = []fonteDoJogo{}
		var deFora []fonteDoJogo
		for _, f := range j.transmissoes {
			nome := primeiro(nomesDasFontes[normalizar(f.nome)], f.nome)
			g, ok := grupoDaFonte(f.nome)
			gc := porID[g.ID]
			if !ok || gc == nil {
				deFora = append(deFora, fonteDoJogo{Nome: nome, Link: linkDeFora(f.nome, f.link)})
				continue
			}
			fonte := fonteDoJogo{Nome: primeiro(g.Nome, nome), Grupo: g.ID}
			// O canal exato pelo guia: no próprio grupo e nos que costumam passar o mesmo jogo.
			for _, id := range append([]string{g.ID}, g.tambem...) {
				if fonte.Canal != nil || porID[id] == nil {
					continue
				}
				for _, c := range porID[id].Canais {
					for _, p := range c.guiaLista {
						if casaComOJogo(p, *j) {
							fonte.Canal = &canalDoJogo{ID: c.ID, Nome: c.Nome, Capa: c.Capa, Programa: p.Titulo}
							break
						}
					}
					if fonte.Canal != nil {
						break
					}
				}
			}
			// A fonte já diz o canal ("ESPN 2"): o desse nome no grupo.
			if fonte.Canal == nil {
				for _, c := range gc.Canais {
					if c.chave == normalizar(strings.TrimSpace(f.nome)) {
						fonte.Canal = &canalDoJogo{ID: c.ID, Nome: c.Nome, Capa: c.Capa}
						break
					}
				}
			}
			// Grupo de um canal só (Globo SP, GE TV): já é o canal.
			if fonte.Canal == nil && len(gc.Canais) == 1 {
				c := gc.Canais[0]
				fonte.Canal = &canalDoJogo{ID: c.ID, Nome: c.Nome, Capa: c.Capa}
			}
			if fonte.Canal != nil {
				fonte.Grupo = ""
			}
			dup := false
			for _, x := range j.Fontes {
				if fonte.Canal != nil && x.Canal != nil && x.Canal.ID == fonte.Canal.ID {
					dup = true
				}
			}
			if !dup {
				j.Fontes = append(j.Fontes, fonte)
			}
		}
		// Primeiro o canal exato, depois o grupo da assinatura, e por último os sites de fora.
		sort.SliceStable(j.Fontes, func(a, b int) bool { return j.Fontes[a].Canal != nil && j.Fontes[b].Canal == nil })
		j.Fontes = append(j.Fontes, deFora...)
	}
}

// ---------- A rota ----------

// validadeDaAgenda: 2 minutos com jogo acontecendo (para o placar), 30 no resto do tempo.
func validadeDaAgenda(jogos []jogo, agora time.Time) time.Duration {
	for _, j := range jogos {
		if j.Momento == "ao_vivo" || (!j.inicio.IsZero() && j.Momento == "antes" && agora.After(j.inicio.Add(-10*time.Minute)) && agora.Before(j.inicio.Add(3*time.Hour))) {
			return 2 * time.Minute
		}
	}
	return 30 * time.Minute
}

func (a *app) handleJogos(w http.ResponseWriter, r *http.Request) {
	jogos, servicos, lidaEm, erro := a.jogosDasFontes(r.Context())
	grupos, err := a.gruposComCanais(r.Context())
	if err != nil {
		falha(w, err)
		return
	}
	// O guia só dos grupos que aparecem nos jogos.
	quais := map[string]bool{}
	for _, j := range jogos {
		for _, f := range j.transmissoes {
			if g, ok := grupoDaFonte(f.nome); ok {
				quais[g.ID] = true
				for _, t := range g.tambem {
					quais[t] = true
				}
			}
		}
	}
	a.lerGuias(r.Context(), grupos, quais)
	ligarAosCanais(jogos, grupos)
	if jogos == nil {
		jogos = []jogo{}
	}
	resp := map[string]any{"hoje": a.agora().In(fusoDeBrasilia).Format("2006-01-02"), "jogos": jogos, "erro": erro, "servicos": servicos}
	if !lidaEm.IsZero() {
		resp["lida_em"] = lidaEm.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resp)
}
