package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// tvFalsa responde como um renderizador DLNA e guarda cada ação recebida.
type tvFalsa struct {
	mu      sync.Mutex
	acoes   []string
	corpos  map[string]string
	estado  string
	posicao string
	srv     *httptest.Server
	// Como a Samsung com torrent: o Seek só responde quando o vídeo do novo ponto chegou.
	demoraDoSeek time.Duration
}

func novaTVFalsa(t *testing.T) *tvFalsa {
	tv := &tvFalsa{corpos: map[string]string{}, estado: "NO_MEDIA_PRESENT", posicao: "0:00:00"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /dmr", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0"?><root xmlns="urn:schemas-upnp-org:device-1-0"><device>
			<deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType>
			<friendlyName>50&quot; TV de Teste</friendlyName><manufacturer>Fabricante</manufacturer><UDN>uuid:tv-teste</UDN>
			<serviceList>
				<service><serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType><controlURL>/upnp/control/AVTransport1</controlURL></service>
				<service><serviceType>urn:schemas-upnp-org:service:RenderingControl:1</serviceType><controlURL>/upnp/control/RenderingControl1</controlURL></service>
			</serviceList></device></root>`)
	})
	mux.HandleFunc("POST /upnp/control/{servico}", func(w http.ResponseWriter, r *http.Request) {
		acao := strings.Trim(r.Header.Get("SOAPACTION"), `"`)
		acao = acao[strings.LastIndex(acao, "#")+1:]
		b, _ := io.ReadAll(r.Body)
		if acao == "Seek" {
			time.Sleep(tv.demoraDoSeek)
		}
		tv.mu.Lock()
		defer tv.mu.Unlock()
		tv.acoes = append(tv.acoes, acao)
		tv.corpos[acao] = string(b)
		resposta := ""
		switch acao {
		case "Play":
			tv.estado = "PLAYING"
		case "Pause":
			tv.estado = "PAUSED_PLAYBACK"
		case "Stop":
			tv.estado = "STOPPED"
		case "Seek":
			i := strings.Index(string(b), "<Target>")
			tv.posicao = string(b)[i+8 : strings.Index(string(b), "</Target>")]
		case "GetTransportInfo":
			resposta = "<CurrentTransportState>" + tv.estado + "</CurrentTransportState>"
		case "GetPositionInfo":
			resposta = "<TrackDuration>1:30:00</TrackDuration><RelTime>" + tv.posicao + "</RelTime>"
		case "GetVolume":
			resposta = "<CurrentVolume>19</CurrentVolume>"
		case "GetMute":
			resposta = "<CurrentMute>0</CurrentMute>"
		}
		fmt.Fprintf(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:%sResponse>%s</u:%sResponse></s:Body></s:Envelope>`, acao, resposta, acao)
	})
	tv.srv = httptest.NewServer(mux)
	t.Cleanup(tv.srv.Close)
	return tv
}

func (tv *tvFalsa) recebeu(acao string) bool {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	for _, a := range tv.acoes {
		if a == acao {
			return true
		}
	}
	return false
}

func (tv *tvFalsa) corpo(acao string) string {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	return tv.corpos[acao]
}

func registrarTV(t *testing.T, e *ambiente, tv *tvFalsa) tela {
	t.Helper()
	ctx := context.Background()
	te, err := e.a.telas.lerDescricao(ctx, tv.srv.URL+"/dmr")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.a.telas.guardar(ctx, te); err != nil {
		t.Fatal(err)
	}
	return te
}

func (tv *tvFalsa) quantas(acao string) int {
	tv.mu.Lock()
	defer tv.mu.Unlock()
	n := 0
	for _, a := range tv.acoes {
		if a == acao {
			n++
		}
	}
	return n
}

// A TV que demora a responder ao Seek recebeu o pedido: o servidor não procura a tela de novo nem
// repete o Seek (a imagem apagaria duas vezes), e o celular não vê erro.
func TestIrParaNaTelaQueDemora(t *testing.T) {
	e := novoAmbiente(t)
	tv := novaTVFalsa(t)
	te := registrarTV(t, e, tv)
	tv.demoraDoSeek = 300 * time.Millisecond
	e.a.telas.httpDoSeek = &http.Client{Timeout: 100 * time.Millisecond}

	if status, corpo := e.pedir(t, "POST", "/api/telas/"+url.PathEscape(te.ID)+"/comando", map[string]any{"comando": "ir", "valor": 600}); status/100 != 2 {
		t.Fatalf("%d %s", status, corpo)
	}
	time.Sleep(400 * time.Millisecond)
	if n := tv.quantas("Seek"); n != 1 {
		t.Fatalf("Seek mandado %d vezes", n)
	}
}

func TestDescricaoDaTela(t *testing.T) {
	e := novoAmbiente(t)
	te := registrarTV(t, e, novaTVFalsa(t))
	if te.ID != "uuid:tv-teste" || te.NomeOriginal != `50" TV de Teste` || !strings.HasSuffix(te.transporte, "/upnp/control/AVTransport1") || te.volume == "" {
		t.Fatalf("tela: %+v", te)
	}
}

func TestTocarNaTelaDoPontoEmQueParou(t *testing.T) {
	e := novoAmbiente(t)
	tv := novaTVFalsa(t)
	te := registrarTV(t, e, tv)

	status, corpo := e.pedir(t, "POST", "/api/telas/"+url.PathEscape(te.ID)+"/tocar", map[string]any{"chave": "filme:200", "posicao": 754, "capa": "https://exemplo.invalid/capa.jpg"})
	if status != 200 {
		t.Fatalf("%d %s", status, corpo)
	}
	envio := tv.corpo("SetAVTransportURI")
	// A TV Samsung recusa &#34;: só entidades nomeadas no XML.
	if !strings.Contains(envio, e.telas.URL+"/tela/filme/200.mp4?envio=") || strings.Contains(envio, "&#") || !strings.Contains(envio, "&lt;DIDL-Lite") ||
		!strings.Contains(envio, "Missão Explosiva") || !strings.Contains(envio, "albumArtURI&gt;https://exemplo.invalid/capa.jpg") || !strings.Contains(envio, "DLNA.ORG_OP=01") {
		t.Fatalf("SetAVTransportURI:\n%s", envio)
	}
	if !tv.recebeu("Play") {
		t.Fatal("não mandou tocar")
	}
	// A posição vai depois que a TV começa a tocar.
	prazo := time.Now().Add(3 * time.Second)
	for !strings.Contains(tv.corpo("Seek"), "<Target>0:12:34</Target>") {
		if time.Now().After(prazo) {
			t.Fatalf("não foi até a posição: %q", tv.corpo("Seek"))
		}
		time.Sleep(20 * time.Millisecond)
	}

	// O que a tela toca vira o dono da assinatura, e o progresso é guardado pelo estado dela.
	if chave, dono := e.a.conexao.donoAtual(); chave != "filme:200" || dono != te.ID {
		t.Fatalf("dono: %s %s", chave, dono)
	}
	var est estadoDaTela
	e.json(t, "/api/telas/"+url.PathEscape(te.ID)+"/estado", &est)
	if est.Estado != "PLAYING" || est.Posicao != 754 || est.Duracao != 5400 || est.Volume != 19 || est.Sessao == nil || est.Sessao.Titulo != "Missão Explosiva" {
		t.Fatalf("estado: %+v", est)
	}
	if p := e.a.progressoDe(context.Background(), "filme:200"); p == nil || p.Posicao != 754 {
		t.Fatalf("progresso: %+v", p)
	}
}

func TestComandosDaTela(t *testing.T) {
	e := novoAmbiente(t)
	tv := novaTVFalsa(t)
	te := registrarTV(t, e, tv)
	tv.posicao = "0:10:00"

	comandos := []struct {
		comando string
		valor   float64
		acao    string
		trecho  string
	}{
		{"pausar", 0, "Pause", ""},
		{"continuar", 0, "Play", "<Speed>1</Speed>"},
		{"pular", 30, "Seek", "<Target>0:10:30</Target>"},
		{"ir", 3725, "Seek", "<Target>1:02:05</Target>"},
		{"volume", 25, "SetVolume", "<DesiredVolume>25</DesiredVolume>"},
		{"mudo", 1, "SetMute", "<DesiredMute>1</DesiredMute>"},
		{"parar", 0, "Stop", ""},
	}
	for _, c := range comandos {
		status, corpo := e.pedir(t, "POST", "/api/telas/"+url.PathEscape(te.ID)+"/comando", map[string]any{"comando": c.comando, "valor": c.valor})
		if status != 204 {
			t.Fatalf("%s: %d %s", c.comando, status, corpo)
		}
		if !strings.Contains(tv.corpo(c.acao), c.trecho) {
			t.Fatalf("%s: %s sem %q:\n%s", c.comando, c.acao, c.trecho, tv.corpo(c.acao))
		}
	}
	if status, _ := e.pedir(t, "POST", "/api/telas/"+url.PathEscape(te.ID)+"/comando", map[string]any{"comando": "explodir"}); status != 400 {
		t.Fatalf("comando desconhecido: %d", status)
	}
}

func TestTelaParaQuandoOAparelhoAssume(t *testing.T) {
	e := novoAmbiente(t)
	tv := novaTVFalsa(t)
	te := registrarTV(t, e, tv)
	if status, corpo := e.pedir(t, "POST", "/api/telas/"+url.PathEscape(te.ID)+"/tocar", map[string]any{"chave": "canal:100"}); status != 200 {
		t.Fatalf("%d %s", status, corpo)
	}
	tv.mu.Lock()
	tv.acoes = nil
	tv.mu.Unlock()

	assumir(t, e, "filme:200") // o Lucas foi assistir no celular
	prazo := time.Now().Add(3 * time.Second)
	for !tv.recebeu("Stop") {
		if time.Now().After(prazo) {
			t.Fatal("a tela devia parar quando outro aparelho assume a assinatura")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRelogioESegundos(t *testing.T) {
	if relogio(3725.9) != "1:02:05" || relogio(0) != "0:00:00" {
		t.Fatal(relogio(3725.9))
	}
	casos := map[string]float64{"1:02:05": 3725, "00:00:07.500": 7.5, "NOT_IMPLEMENTED": 0, "": 0}
	for s, want := range casos {
		if got := segundosDe(s); got != want {
			t.Errorf("segundosDe(%q) = %v, queria %v", s, got, want)
		}
	}
}

// O projetor Android muda a porta do DLNA quando reinicia: o comando acha a tela de novo e funciona.
func TestTelaQueMudouDePorta(t *testing.T) {
	e := novoAmbiente(t)
	tv := novaTVFalsa(t)
	te := registrarTV(t, e, tv)

	// A tela "reinicia" noutra porta: o endereço guardado deixa de responder.
	tv.srv.Close()
	nova := novaTVFalsa(t)

	// Ela responde à busca SSDP dirigida a ela com a descrição nova.
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	antes := portaSSDP
	portaSSDP = conn.LocalAddr().(*net.UDPAddr).Port
	t.Cleanup(func() { portaSSDP = antes })
	go func() {
		buf := make([]byte, 2048)
		for {
			_, de, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			conn.WriteToUDP([]byte("HTTP/1.1 200 OK\r\nST: urn:schemas-upnp-org:device:MediaRenderer:1\r\nLOCATION: "+nova.srv.URL+"/dmr\r\n\r\n"), de)
		}
	}()
	t.Setenv("REDE_LOCAL", "192.0.2.0/30") // se precisar varrer, não sai da máquina

	te.Endereco = "127.0.0.1"
	if _, err := e.a.db.Exec(`UPDATE telas SET endereco = '127.0.0.1' WHERE id = ?`, te.ID); err != nil {
		t.Fatal(err)
	}
	if status, corpo := e.pedir(t, "POST", "/api/telas/"+url.PathEscape(te.ID)+"/comando", map[string]any{"comando": "pausar"}); status != 204 {
		t.Fatalf("%d %s", status, corpo)
	}
	if !nova.recebeu("Pause") {
		t.Fatal("o comando não chegou à tela no endereço novo")
	}
	guardada, err := e.a.tela(context.Background(), te.ID)
	if err != nil || !strings.HasPrefix(guardada.transporte, nova.srv.URL) {
		t.Fatalf("endereço novo não foi guardado: %+v %v", guardada, err)
	}
}

func TestPosicaoDaTelaGuardadaSemOCelular(t *testing.T) {
	e := novoAmbiente(t)
	e.a.telas.intervalo = 50 * time.Millisecond
	tv := novaTVFalsa(t)
	te := registrarTV(t, e, tv)
	if status, corpo := e.pedir(t, "POST", "/api/telas/"+url.PathEscape(te.ID)+"/tocar", map[string]any{"chave": "filme:200"}); status != 200 {
		t.Fatalf("%d %s", status, corpo)
	}
	tv.mu.Lock()
	tv.posicao = "0:42:10"
	tv.mu.Unlock()
	// Ninguém abre o controle: o próprio servidor pergunta à TV e guarda.
	var guardada float64
	for range 100 {
		e.a.db.QueryRow(`SELECT posicao FROM progresso WHERE chave = 'filme:200'`).Scan(&guardada)
		if guardada > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if guardada != 42*60+10 {
		t.Fatalf("posição guardada: %v", guardada)
	}
	// Parar pelo celular guarda o ponto exato antes de fechar.
	tv.mu.Lock()
	tv.posicao = "0:43:00"
	tv.mu.Unlock()
	e.pedir(t, "POST", "/api/telas/"+url.PathEscape(te.ID)+"/comando", map[string]any{"comando": "parar"})
	e.a.db.QueryRow(`SELECT posicao FROM progresso WHERE chave = 'filme:200'`).Scan(&guardada)
	if guardada != 43*60 {
		t.Fatalf("posição ao parar: %v", guardada)
	}
}
