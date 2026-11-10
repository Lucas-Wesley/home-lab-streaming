package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Lucas-Wesley/home-lab-streaming/provedorfalso"
)

// agendaFalsa imita a página da agenda do ge, com os jogos no meio do HTML. Times inventados.
func agendaFalsa(t *testing.T) *httptest.Server {
	t.Helper()
	partida := func(id int, a, b, dia, hora, fontes, placar string) string {
		return fmt.Sprintf(`{"match":{"id":%d,"firstContestant":{"popularName":%q,"badgeSvg":"https://exemplo.invalid/%d-a.svg"},
			"secondContestant":{"popularName":%q,"badgePng":"https://exemplo.invalid/%d-b.png"},"startDate":%q,"startHour":%q,
			"scoreboard":%s,"liveWatchSources":[%s],"phase":{"name":"Final"},
			"transmission":{"broadcastStatus":{"id":"PRE_DIA"},"url":"https://exemplo.invalid/jogo/%d"}}}`, id, a, id, b, id, dia, hora, placar, fontes, id)
	}
	fonte := func(nome, link string) string { return fmt.Sprintf(`{"name":%q,"url":%q}`, nome, link) }
	semPlacar := `{"home":null,"away":null,"penalty":null}`
	pagina := `<html><script>window.enabledFeeds = [];
	window.dataSportsSchedule = {
		sport: {"2030-03-04":{"championshipsAgenda":[
			{"championship":{"name":"Copa do Brasil"},"past":[],"future":[],
			 "now":[` + partida(1, "Time Azul", "Time Verde", "2030-03-04", "19:00:00", fonte("sportv", "")+","+fonte("globoplay", "https://globoplay.globo.com/assine/esportes/?utm_source=x"), `{"home":2,"away":1,"penalty":null}`) + `]},
			{"championship":{"name":"Premier League"},"past":[],"now":[],
			 "future":[` + partida(2, "Time Vermelho", "Time Branco", "2030-03-04", "21:30:00", fonte("Disney+", ""), semPlacar) + `]},
			{"championship":{"name":"Campeonato Pernambucano"},"past":[],"now":[],
			 "future":[` + partida(3, "Time Roxo", "Time Cinza", "2030-03-04", "15:00:00", "", semPlacar) + `]},
			{"championship":{"name":"Copa do Brasil Feminina"},"past":[],"now":[],
			 "future":[` + partida(4, "Time Rosa", "Time Lilás", "2030-03-04", "16:00:00", fonte("sportv", ""), semPlacar) + `]}
		]},"2030-03-05":{"championshipsAgenda":null}},
		extra: 1
	};
	window.brazilianMaleNationalTeamData = {"matches":[{"id":5,"moment":"FUTURE","firstContestant":{"popularName":"Brasil"},
		"secondContestant":{"popularName":"Seleção Inventada"},"startDate":"2030-03-07","startHour":"16:00:00",
		"liveWatchSources":[{"name":"Globo","url":""},{"name":"CazéTV","url":""}],
		"phase":{"name":"Amistoso","championshipEdition":{"championship":{"name":"Amistosos da Seleção Brasileira"}}}}]};
	</script></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, pagina) }))
	t.Cleanup(srv.Close)
	return srv
}

func canalExtra(id int, nome string) map[string]any {
	return map[string]any{"num": id, "name": nome, "stream_id": id, "stream_icon": "", "category_id": "12", "added": "1700000000"}
}

// ambienteDosJogos: o ge falso, canais de esporte no provedor falso e o guia de dois deles.
func ambienteDosJogos(t *testing.T) *ambiente {
	t.Helper()
	agora := time.Date(2030, 3, 4, 19, 40, 0, 0, fusoDeBrasilia)
	inicio := func(h, m int) time.Time { return time.Date(2030, 3, 4, h, m, 0, 0, fusoDeBrasilia) }
	e := novoAmbiente(t)
	e.provedor.CanaisExtras = []map[string]any{
		canalExtra(500, "SporTV 1 HD"), canalExtra(501, "SporTV 2 HD"), canalExtra(502, "SporTV 2 FHD H265²"),
		canalExtra(510, "ESPN 4 FHD"), canalExtra(520, "Globo SP: São Paulo HD"), canalExtra(530, "Disney+: Canal 01"), canalExtra(531, "Disney+: Canal 02"),
	}
	e.provedor.Guia = map[int64][]provedorfalso.Programa{
		501: {{Titulo: "Seleção SporTV", Inicio: inicio(17, 0), Fim: inicio(18, 55)}, {Titulo: "Time Azul x Time Verde", Inicio: inicio(18, 55), Fim: inicio(21, 0)}},
		510: {{Titulo: "Futebol: Time Vermelho x Time Branco", Inicio: inicio(21, 25), Fim: inicio(23, 30)}},
	}
	e.a.agora = func() time.Time { return agora }
	t.Setenv("AGENDA_URL", agendaFalsa(t).URL)
	if status, corpo := e.pedir(t, "POST", "/api/catalogo/sincronizar", nil); status >= 300 {
		t.Fatalf("sincronizar: %d %s", status, corpo)
	}
	return e
}

func TestJogosDoDia(t *testing.T) {
	e := ambienteDosJogos(t)
	var r struct {
		Hoje  string `json:"hoje"`
		Erro  string `json:"erro"`
		Jogos []jogo `json:"jogos"`
	}
	e.json(t, "/api/jogos", &r)
	if r.Hoje != "2030-03-04" || r.Erro != "" {
		t.Fatalf("hoje %q, erro %q", r.Hoje, r.Erro)
	}
	// Só os campeonatos escolhidos e a Seleção: o estadual e o feminino ficam de fora.
	var nomes []string
	for _, j := range r.Jogos {
		nomes = append(nomes, j.Mandante.Nome)
	}
	if strings.Join(nomes, ",") != "Time Azul,Time Vermelho,Brasil" {
		t.Fatalf("jogos: %v", nomes)
	}

	copa := r.Jogos[0]
	if copa.Momento != "ao_vivo" || copa.Placar == nil || copa.Placar.Mandante != 2 || copa.Hora != "19:00" || copa.Mandante.Escudo == "" {
		t.Fatalf("jogo ao vivo: %+v", copa)
	}
	// sportv → SporTV 2 pelo guia (a versão HD, não a reserva²); Globoplay é de fora, sem a campanha.
	if len(copa.Fontes) != 2 || copa.Fontes[0].Canal == nil || copa.Fontes[0].Canal.ID != 501 || copa.Fontes[0].Canal.Nome != "SporTV 2" {
		t.Fatalf("fontes da copa: %+v", copa.Fontes)
	}
	if f := copa.Fontes[1]; f.Nome != "Globoplay" || f.Link != "https://globoplay.globo.com/assine/esportes/" || f.Canal != nil {
		t.Fatalf("globoplay: %+v", f)
	}
	// Disney+ passa na ESPN: o guia da ESPN 4 tem o jogo.
	premier := r.Jogos[1]
	if premier.Momento != "antes" || premier.Placar != nil || len(premier.Fontes) != 1 || premier.Fontes[0].Canal == nil || premier.Fontes[0].Canal.ID != 510 {
		t.Fatalf("premier: %+v", premier)
	}
	// Seleção (lista própria): a Globo é um canal só, já ligado; a CazéTV vai ao site.
	brasil := r.Jogos[2]
	if brasil.Dia != "2030-03-07" || brasil.Campeonato != "Amistosos da Seleção Brasileira" || len(brasil.Fontes) != 2 ||
		brasil.Fontes[0].Canal == nil || brasil.Fontes[0].Canal.ID != 520 || brasil.Fontes[1].Link != "https://www.youtube.com/@CazeTV" {
		t.Fatalf("seleção: %+v", brasil)
	}
}

// Sem o guia achar o jogo, a fonte leva ao grupo da emissora na assinatura.
func TestJogoSemCanalNoGuia(t *testing.T) {
	e := ambienteDosJogos(t)
	e.provedor.Guia = nil
	var r struct{ Jogos []jogo }
	e.json(t, "/api/jogos", &r)
	if f := r.Jogos[0].Fontes[0]; f.Canal != nil || f.Grupo != "sportv" || f.Nome != "SporTV" {
		t.Fatalf("sem guia: %+v", f)
	}
	if f := r.Jogos[1].Fontes[0]; f.Canal != nil || f.Grupo != "disney" {
		t.Fatalf("disney sem guia: %+v", f)
	}
}

func TestAgendaForaDoAr(t *testing.T) {
	e := novoAmbiente(t)
	var r struct {
		Erro  string `json:"erro"`
		Jogos []jogo `json:"jogos"`
	}
	e.json(t, "/api/jogos", &r)
	if r.Erro == "" || r.Jogos == nil || len(r.Jogos) != 0 {
		t.Fatalf("sem a agenda: %+v", r)
	}
}

func TestCanaisDeEsportes(t *testing.T) {
	e := ambienteDosJogos(t)
	var r struct{ Grupos []grupoComCanais }
	e.json(t, "/api/canais/esportes", &r)
	var ids []string
	for _, g := range r.Grupos {
		ids = append(ids, g.ID)
	}
	if strings.Join(ids, ",") != "espn,sportv,globo,disney" {
		t.Fatalf("grupos: %v", ids)
	}
	sportv := r.Grupos[1]
	if !sportv.Principal || len(sportv.Canais) != 2 || sportv.Canais[1].ID != 501 || len(sportv.Canais[1].Versoes) != 2 {
		t.Fatalf("sportv: %+v", sportv)
	}
	if a := sportv.Canais[1].Agora; a == nil || a.Titulo != "Time Azul x Time Verde" || sportv.Canais[1].Depois != nil {
		t.Fatalf("agora no SporTV 2: %+v", sportv.Canais[1])
	}
}

// A página Canais pede os jogos e os canais juntos: normalizar não pode quebrar em paralelo.
func TestJogosECanaisJuntos(t *testing.T) {
	e := ambienteDosJogos(t)
	feito := make(chan int, 8)
	for i := range 8 {
		caminho := "/api/jogos"
		if i%2 == 1 {
			caminho = "/api/canais/esportes"
		}
		go func() {
			status, _ := e.pedir(t, "GET", caminho, nil)
			feito <- status
		}()
	}
	for range 8 {
		if s := <-feito; s != http.StatusOK {
			t.Fatalf("status %d", s)
		}
	}
}

func TestBaseDoCanal(t *testing.T) {
	for nome, quer := range map[string]struct {
		base string
		nota int
	}{
		"ESPN 1 FHD H265²":        {"ESPN 1", 1},
		"ESPN 1 HD":               {"ESPN 1", 3},
		"ESPN 6 FHD 265":          {"ESPN 6", 2},
		"Globo SP: São Paulo FHD": {"Globo SP: São Paulo", 4},
		"Disney+: Canal 01²":      {"Disney+: Canal 01", 2},
		"Prime Video: Canal 01³":  {"Prime Video: Canal 01", 2},
	} {
		if b, n := baseDoCanal(nome); b != quer.base || n != quer.nota {
			t.Errorf("baseDoCanal(%q) = %q %d, queria %q %d", nome, b, n, quer.base, quer.nota)
		}
	}
}
