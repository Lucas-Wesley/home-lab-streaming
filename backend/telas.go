package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Telas são os aparelhos que tocam vídeo pela rede: a TV e o projetor, por DLNA (S8). O celular
// pede ao servidor, e o servidor manda a tela buscar o vídeo em /tela/..., por HTTP simples (S7).

type tela struct {
	ID           string `json:"id"`
	Nome         string `json:"nome"`
	NomeOriginal string `json:"nome_original"`
	Fabricante   string `json:"fabricante"`
	Endereco     string `json:"endereco"`
	transporte   string
	volume       string
	descricao    string
	VistaEm      string `json:"vista_em"`
	Ligada       bool   `json:"ligada"`
}

// sessao é o que o app mandou a tela tocar, para mostrar no controle.
type sessao struct {
	Chave  string  `json:"chave"`
	Titulo string  `json:"titulo"`
	Capa   string  `json:"capa,omitempty"`
	AoVivo bool    `json:"ao_vivo"`
	Inicio float64 `json:"-"`
	Desde  string  `json:"desde"`
	// Legenda gerada que ainda cresce (S29): até onde ela ia quando foi para a tela; 0 se não é o caso.
	legendaAte float64
}

type controleDasTelas struct {
	a       *app
	mu      sync.Mutex
	sessoes map[string]sessao // por id da tela
	// enderecos guarda onde cada tela foi achada de novo, quando mudou de porta ou de IP.
	enderecos map[string]tela
	http      *http.Client
	// A Samsung só responde ao Seek quando já tem o vídeo do novo ponto: com torrent, passa de 8 s.
	httpDoSeek *http.Client
	// Enquanto uma tela toca, o servidor pergunta a posição a cada intervalo e guarda: o celular
	// pode estar fechado, e "Continuar assistindo" tem de saber onde o vídeo parou na TV.
	intervalo time.Duration
	// O projetor leva um tempo para dizer que só pausou no Stop: confere depois desta espera.
	esperaDoStop time.Duration
	// Endereço do procurador de telas (procurador.go); vazio, a busca SSDP sai daqui mesmo.
	procurador string
}

func novoControleDasTelas(a *app) *controleDasTelas {
	c := &controleDasTelas{a: a, sessoes: map[string]sessao{}, enderecos: map[string]tela{}, http: &http.Client{Timeout: 8 * time.Second}, httpDoSeek: &http.Client{Timeout: 12 * time.Second}, intervalo: 15 * time.Second, esperaDoStop: time.Second, procurador: strings.TrimRight(env("PROCURADOR_DE_TELAS", ""), "/")}
	// Outro aparelho assumiu a assinatura: a tela que tocava para, em vez de ficar com erro na tela.
	a.conexao.aoTrocarDeDono = func(anterior string) {
		t, err := a.tela(context.Background(), anterior)
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = c.estado(ctx, t) // guarda onde parou antes de fechar
		c.mu.Lock()
		delete(c.sessoes, anterior)
		c.mu.Unlock()
		_, _ = c.acao(ctx, t, "Stop", nil)
	}
	return c
}

func (c *controleDasTelas) acompanhar(t tela, desde string) {
	for {
		time.Sleep(c.intervalo)
		c.mu.Lock()
		s, ok := c.sessoes[t.ID]
		c.mu.Unlock()
		if !ok || s.Desde != desde {
			return // a tela parou ou recebeu outro vídeo, que tem o seu próprio acompanhamento
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		e, err := c.estado(ctx, t) // guarda a posição e encerra a sessão se a tela parou
		cancel()
		if err == nil && e.Estado == "PLAYING" && s.legendaAte > 0 {
			c.atualizarLegendaGerada(t, s, e.Posicao)
		}
	}
}

// atualizarLegendaGerada: a tela só lê a legenda quando o vídeo chega. Se a legenda gerada que ela
// recebeu está acabando e já há bem mais pronta (8 min à frente) ou ficou completa, manda o vídeo
// de novo, do mesmo ponto. Cada envio apaga a imagem por uns segundos: com pouco à frente, mandar a
// cada minuto ouvido faria a TV recarregar várias vezes seguidas. Se a da tela já acabou (a geração
// não deu conta, ou o vídeo foi mandado antes de haver legenda ali), 3 min à frente bastam: melhor
// um envio a mais que a tela sem legenda.
const (
	folgaParaReenviar     = 8 * 60.0
	folgaSemLegendaNaTela = 3 * 60.0
)

// tocandoNumaTela: alguma tela toca este vídeo agora (a geração vai mais à frente antes de revisar).
func (c *controleDasTelas) tocandoNumaTela(chave string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.sessoes {
		if s.Chave == chave {
			return true
		}
	}
	return false
}

func (c *controleDasTelas) atualizarLegendaGerada(t tela, s sessao, posicao float64) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	g := c.a.geracaoDe(ctx, s.Chave)
	if g == nil || g.Estado == "parada" || g.Estado == "erro" {
		return
	}
	agora := c.a.coberturaDaGerada(ctx, s.Chave, posicao)
	acabando := posicao > s.legendaAte-45
	acabou := posicao > s.legendaAte-5
	completa := g.Estado == "pronta" && agora > s.legendaAte
	if !acabando || agora <= s.legendaAte || !(agora >= posicao+folgaParaReenviar || completa || (acabou && agora >= posicao+folgaSemLegendaNaTela)) {
		return
	}
	m := reChaveDeVideo.FindStringSubmatch(s.Chave)
	if m == nil {
		return
	}
	id, _ := strconv.ParseInt(m[2], 10, 64)
	log.Printf("tela %s: mais legenda gerada pronta (até %.0f s), mandando o vídeo de novo", t.ID, agora)
	c.tocar(ctx, t, m[1], id, posicao, s.Capa)
}

// pararParaOAudio para a tela que toca este vídeo (pela assinatura) para o servidor puxar o
// áudio; a função devolvida manda o vídeo de volta, do mesmo ponto.
func (c *controleDasTelas) pararParaOAudio(ctx context.Context, chave string) (voltar func(), parou bool) {
	c.mu.Lock()
	var idDaTela string
	var s sessao
	for id, ss := range c.sessoes {
		if ss.Chave == chave {
			idDaTela, s = id, ss
		}
	}
	c.mu.Unlock()
	if idDaTela == "" {
		return func() {}, false
	}
	t, err := c.a.tela(ctx, idDaTela)
	if err != nil {
		return func() {}, false
	}
	t = c.maisNova(t)
	e, _ := c.estado(ctx, t)
	posicao := e.Posicao
	if err := c.comandar(ctx, t, "parar", 0); err != nil {
		return func() {}, false
	}
	return func() {
		m := reChaveDeVideo.FindStringSubmatch(chave)
		if m == nil {
			return
		}
		id, _ := strconv.ParseInt(m[2], 10, 64)
		c2, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := c.tocar(c2, t, m[1], id, posicao, s.Capa); err != nil {
			log.Printf("tela %s: não voltou depois de puxar o áudio: %v", t.ID, err)
		}
	}, true
}

// ---------- Banco ----------

const colunasDaTela = `id, nome, nome_original, fabricante, endereco, transporte, volume, descricao, vista_em`

func lerTela(s interface{ Scan(...any) error }) (tela, error) {
	var t tela
	err := s.Scan(&t.ID, &t.Nome, &t.NomeOriginal, &t.Fabricante, &t.Endereco, &t.transporte, &t.volume, &t.descricao, &t.VistaEm)
	return t, err
}

func (a *app) tela(ctx context.Context, id string) (tela, error) {
	return lerTela(a.db.QueryRowContext(ctx, `SELECT `+colunasDaTela+` FROM telas WHERE id = ?`, id))
}

func (a *app) listaDeTelas(ctx context.Context) ([]tela, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT `+colunasDaTela+` FROM telas ORDER BY nome COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lista := []tela{}
	for rows.Next() {
		t, err := lerTela(rows)
		if err != nil {
			return nil, err
		}
		lista = append(lista, t)
	}
	return lista, rows.Err()
}

// ---------- Procura na rede (SSDP) ----------

// portaSSDP é a do padrão; os testes trocam por uma livre.
var portaSSDP = 1900

const buscaSSDP = "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: urn:schemas-upnp-org:device:MediaRenderer:1\r\n\r\n"

// procurar manda a busca SSDP para o grupo e também para cada endereço da rede local: o projetor
// só responde à busca dirigida a ele.
func (c *controleDasTelas) procurar(ctx context.Context) ([]string, error) {
	alvos := []string{"239.255.255.250"}
	if conhecidas, err := c.a.listaDeTelas(ctx); err == nil {
		for _, t := range conhecidas {
			alvos = append(alvos, t.Endereco)
		}
	}
	repetir := len(alvos)
	for _, rede := range redesLocais() {
		alvos = append(alvos, enderecosDa(rede)...)
	}
	locais, err := c.buscarLocais(ctx, alvos, repetir, 3*time.Second, false)
	if err != nil {
		return nil, err
	}

	var achadas []string
	for _, local := range locais {
		t, err := c.lerDescricao(ctx, local)
		if err != nil {
			log.Printf("tela em %s: %v", local, err)
			continue
		}
		if err := c.guardar(ctx, t); err != nil {
			return achadas, err
		}
		c.mu.Lock()
		delete(c.enderecos, t.ID) // o banco já tem o endereço novo
		c.mu.Unlock()
		achadas = append(achadas, t.ID)
	}
	return achadas, nil
}

// redesLocais vem de REDE_LOCAL (no container, que não vê a rede de casa) ou das interfaces.
func redesLocais() []*net.IPNet {
	var redes []*net.IPNet
	if v := os.Getenv("REDE_LOCAL"); v != "" {
		for _, s := range strings.Split(v, ",") {
			if _, rede, err := net.ParseCIDR(strings.TrimSpace(s)); err == nil {
				redes = append(redes, rede)
			}
		}
		return redes
	}
	ifaces, _ := net.InterfaceAddrs()
	for _, ad := range ifaces {
		if ipn, ok := ad.(*net.IPNet); ok && ipn.IP.To4() != nil && ipn.IP.IsPrivate() {
			redes = append(redes, ipn)
		}
	}
	return redes
}

// enderecosDa lista os endereços de uma rede, até um /22 (1022 endereços).
func enderecosDa(rede *net.IPNet) []string {
	uns, bits := rede.Mask.Size()
	if bits != 32 || uns < 22 {
		return nil
	}
	base := rede.IP.To4().Mask(rede.Mask)
	total := 1 << (32 - uns)
	var lista []string
	for i := 1; i < total-1; i++ {
		ip := make(net.IP, 4)
		copy(ip, base)
		n := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
		n += uint32(i)
		lista = append(lista, fmt.Sprintf("%d.%d.%d.%d", n>>24, n>>16&255, n>>8&255, n&255))
	}
	return lista
}

type descricaoUPnP struct {
	URLBase string          `xml:"URLBase"`
	Device  dispositivoUPnP `xml:"device"`
}

type dispositivoUPnP struct {
	Tipo       string `xml:"deviceType"`
	Nome       string `xml:"friendlyName"`
	Fabricante string `xml:"manufacturer"`
	UDN        string `xml:"UDN"`
	Servicos   []struct {
		Tipo     string `xml:"serviceType"`
		Controle string `xml:"controlURL"`
	} `xml:"serviceList>service"`
	Filhos []dispositivoUPnP `xml:"deviceList>device"`
}

func (d dispositivoUPnP) renderizador() (dispositivoUPnP, bool) {
	if strings.Contains(d.Tipo, ":MediaRenderer:") {
		return d, true
	}
	for _, f := range d.Filhos {
		if r, ok := f.renderizador(); ok {
			return r, true
		}
	}
	return d, false
}

func (c *controleDasTelas) lerDescricao(ctx context.Context, local string) (tela, error) {
	var t tela
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, local, nil)
	if err != nil {
		return t, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return t, err
	}
	defer resp.Body.Close()
	var d descricaoUPnP
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d); err != nil {
		return t, err
	}
	r, ok := d.Device.renderizador()
	if !ok {
		return t, errors.New("não é um renderizador de mídia")
	}
	base, err := url.Parse(primeiro(d.URLBase, local))
	if err != nil {
		return t, err
	}
	for _, s := range r.Servicos {
		switch {
		case strings.Contains(s.Tipo, ":AVTransport:"):
			t.transporte = resolver(base, strings.TrimSpace(s.Controle))
		case strings.Contains(s.Tipo, ":RenderingControl:"):
			t.volume = resolver(base, strings.TrimSpace(s.Controle))
		}
	}
	if t.transporte == "" {
		return t, errors.New("sem AVTransport")
	}
	t.ID = strings.TrimSpace(r.UDN)
	t.NomeOriginal = html.UnescapeString(strings.TrimSpace(r.Nome))
	t.Fabricante = strings.TrimSpace(r.Fabricante)
	t.Endereco = base.Hostname()
	t.descricao = local
	if t.ID == "" {
		t.ID = t.Endereco
	}
	return t, nil
}

// guardar cria a tela ou atualiza o endereço dela; o nome dado pelo Lucas fica.
func (c *controleDasTelas) guardar(ctx context.Context, t tela) error {
	_, err := c.a.db.ExecContext(ctx, `INSERT INTO telas (id, nome, nome_original, fabricante, endereco, transporte, volume, descricao, vista_em)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET nome_original = excluded.nome_original, fabricante = excluded.fabricante,
			endereco = excluded.endereco, transporte = excluded.transporte, volume = excluded.volume,
			descricao = excluded.descricao, vista_em = excluded.vista_em`,
		t.ID, t.NomeOriginal, t.NomeOriginal, t.Fabricante, t.Endereco, t.transporte, t.volume, t.descricao, c.a.carimbo())
	return err
}

// ---------- SOAP ----------

type erroUPnP struct {
	codigo    string
	descricao string
}

func (e erroUPnP) Error() string {
	return fmt.Sprintf("a tela recusou (%s %s)", e.codigo, e.descricao)
}

// soap chama uma ação e devolve os campos da resposta pelo nome.
func (c *controleDasTelas) soap(ctx context.Context, controle, servico, acao string, args [][2]string) (map[string]string, error) {
	var corpo bytes.Buffer
	corpo.WriteString(`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>`)
	fmt.Fprintf(&corpo, `<u:%s xmlns:u="%s">`, acao, servico)
	for _, kv := range args {
		fmt.Fprintf(&corpo, "<%s>%s</%s>", kv[0], escaparXML(kv[1]), kv[0])
	}
	fmt.Fprintf(&corpo, `</u:%s></s:Body></s:Envelope>`, acao)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, controle, &corpo)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPACTION", fmt.Sprintf(`"%s#%s"`, servico, acao))
	conectou := false
	req = req.WithContext(httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { conectou = true }}))
	cliente := c.http
	if acao == "Seek" {
		cliente = c.httpDoSeek
	}
	resp, err := cliente.Do(req)
	if err != nil {
		var ne net.Error
		if conectou && errors.As(err, &ne) && ne.Timeout() {
			return nil, errTelaDemorou
		}
		return nil, errTelaSemResposta
	}
	defer resp.Body.Close()
	campos := map[string]string{}
	dec := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	var atual string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch v := tok.(type) {
		case xml.StartElement:
			atual = v.Name.Local
		case xml.CharData:
			if atual != "" {
				campos[atual] += string(v)
			}
		case xml.EndElement:
			atual = ""
		}
	}
	if resp.StatusCode >= 400 {
		return campos, erroUPnP{campos["errorCode"], campos["errorDescription"]}
	}
	return campos, nil
}

// escaparXML usa só entidades nomeadas: a TV Samsung recusa o pedido com &#34; (402 Invalid Args).
var escaparXML = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace

const (
	servicoTransporte = "urn:schemas-upnp-org:service:AVTransport:1"
	servicoVolume     = "urn:schemas-upnp-org:service:RenderingControl:1"
)

var errTelaSemResposta = errors.New("a tela não respondeu: está ligada e na mesma rede?")

// errTelaDemorou: a tela recebeu o pedido e não respondeu a tempo. Ela está ali: repetir o comando
// (como depois de errTelaSemResposta) faria a TV buscar o ponto duas vezes, e a imagem apagar de novo.
var errTelaDemorou = errors.New("a tela demorou a responder")

func (c *controleDasTelas) acao(ctx context.Context, t tela, acao string, args [][2]string) (map[string]string, error) {
	return c.chamar(ctx, t, false, acao, append([][2]string{{"InstanceID", "0"}}, args...))
}

func (c *controleDasTelas) acaoDeVolume(ctx context.Context, t tela, acao string, args [][2]string) (map[string]string, error) {
	return c.chamar(ctx, t, true, acao, append([][2]string{{"InstanceID", "0"}, {"Channel", "Master"}}, args...))
}

// chamar fala com a tela pelo endereço mais novo que se conhece. Se ela não responder, pode ter
// mudado de porta (o projetor Android muda a do DLNA quando reinicia) ou de IP: acha de novo e
// repete uma vez.
func (c *controleDasTelas) chamar(ctx context.Context, t tela, volume bool, acao string, args [][2]string) (map[string]string, error) {
	t = c.maisNova(t)
	controle := func(t tela) (string, string) {
		if volume {
			return t.volume, servicoVolume
		}
		return t.transporte, servicoTransporte
	}
	endereco, servico := controle(t)
	if endereco == "" {
		return nil, errors.New("esta tela não deixa mudar o volume pela rede")
	}
	r, err := c.soap(ctx, endereco, servico, acao, args)
	if !errors.Is(err, errTelaSemResposta) {
		return r, err
	}
	nova, e := c.reencontrar(ctx, t)
	if e != nil {
		return r, err
	}
	endereco, servico = controle(nova)
	return c.soap(ctx, endereco, servico, acao, args)
}

func (c *controleDasTelas) maisNova(t tela) tela {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n, ok := c.enderecos[t.ID]; ok {
		return n
	}
	return t
}

// reencontrar pergunta à tela, no IP dela, onde está o DLNA agora; se não responder ali, procura
// na rede toda (o IP pode ter mudado).
func (c *controleDasTelas) reencontrar(ctx context.Context, t tela) (tela, error) {
	var nova tela
	local, err := c.perguntarSSDP(ctx, t.Endereco)
	if err == nil {
		nova, err = c.lerDescricao(ctx, local)
	}
	if err != nil || nova.ID != t.ID {
		if _, err := c.procurar(ctx); err != nil {
			return t, err
		}
		if nova, err = c.a.tela(ctx, t.ID); err != nil {
			return t, err
		}
	} else if err := c.guardar(ctx, nova); err != nil {
		return t, err
	}
	if nova.transporte == t.transporte && nova.Endereco == t.Endereco {
		return t, errTelaSemResposta // achou no mesmo lugar: está ali, só não respondeu
	}
	log.Printf("tela %s mudou para %s", t.NomeOriginal, nova.descricao)
	c.mu.Lock()
	c.enderecos[t.ID] = nova
	c.mu.Unlock()
	return nova, nil
}

// perguntarSSDP manda a busca só para um IP e devolve a primeira descrição que ele indicar.
func (c *controleDasTelas) perguntarSSDP(ctx context.Context, ip string) (string, error) {
	locais, err := c.buscarLocais(ctx, []string{ip}, 0, 2*time.Second, true)
	if err != nil {
		return "", err
	}
	if len(locais) == 0 {
		return "", errTelaSemResposta
	}
	return locais[0], nil
}

// ---------- Tocar e controlar ----------

// baseDasTelas é o endereço do servidor como a tela o vê.
func (c *controleDasTelas) baseDasTelas(t tela) (string, error) {
	if c.a.cfg.urlDasTelas != "" {
		return c.a.cfg.urlDasTelas, nil
	}
	conn, err := net.Dial("udp4", net.JoinHostPort(t.Endereco, "1900"))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_, porta, _ := net.SplitHostPort(c.a.cfg.addrTelas)
	return "http://" + net.JoinHostPort(conn.LocalAddr().(*net.UDPAddr).IP.String(), porta), nil
}

// didl descreve o vídeo para a tela. A legenda (SRT) vai de três jeitos, porque cada fabricante lê
// um: sec:CaptionInfoEx (Samsung), pv:subtitleFileUri (players Android) e um <res> text/srt.
func didl(titulo, capa, endereco, mime string, aoVivo bool, legenda string) string {
	op := "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"
	if aoVivo {
		op = "DLNA.ORG_OP=00;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"
	}
	e := escaparXML
	var comLegenda, naRes, resDaLegenda string
	if legenda != "" {
		comLegenda = `<sec:CaptionInfoEx sec:type="srt">` + e(legenda) + `</sec:CaptionInfoEx><sec:CaptionInfo sec:type="srt">` + e(legenda) + `</sec:CaptionInfo>`
		naRes = ` pv:subtitleFileUri="` + e(legenda) + `" pv:subtitleFileType="srt"`
		resDaLegenda = `<res protocolInfo="http-get:*:text/srt:*">` + e(legenda) + `</res>`
	}
	return `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/" xmlns:sec="http://www.sec.co.kr/" xmlns:pv="http://www.pv.com/pvns/">` +
		`<item id="1" parentID="0" restricted="1"><dc:title>` + e(titulo) + `</dc:title><upnp:class>object.item.videoItem</upnp:class>` +
		arteDaCapa(capa) + comLegenda +
		`<res protocolInfo="http-get:*:` + e(mime) + `:` + op + `"` + naRes + `>` + e(endereco) + `</res>` + resDaLegenda + `</item></DIDL-Lite>`
}

// arteDaCapa leva a capa para a tela, que pode mostrá-la enquanto carrega ou nas informações.
func arteDaCapa(capa string) string {
	if !strings.HasPrefix(capa, "http") {
		return ""
	}
	return `<upnp:albumArtURI>` + escaparXML(capa) + `</upnp:albumArtURI>`
}

// tocar manda a tela abrir o vídeo e, se for o caso, ir até onde o Lucas parou.
func (c *controleDasTelas) tocar(ctx context.Context, t tela, tipo string, id int64, posicao float64, capa string) (sessao, error) {
	formato := "ts"
	f, err := c.a.fonteDe(ctx, tipo, id, formato)
	if err != nil {
		return sessao{}, err
	}
	extensao := "ts"
	if !f.aoVivo {
		extensao = f.url[strings.LastIndex(f.url, ".")+1:]
	}
	base, err := c.baseDasTelas(t)
	if err != nil {
		return sessao{}, err
	}
	// Um endereço novo a cada envio: o projetor lembra a posição de um endereço já visto e abre
	// pausado, num aviso próprio de "continuar"; quem leva ao ponto certo é irQuandoTocar.
	endereco := fmt.Sprintf("%s/tela/%s/%d.%s?envio=%d", base, tipo, id, extensao, c.a.agora().UnixNano())
	legenda, legendaAte := "", 0.0
	if l := c.a.legendaDe(ctx, f.chave); l != nil && !f.aoVivo {
		if l.Geracao != nil && l.Geracao.Estado != "pronta" {
			legendaAte = max(1, c.a.coberturaDaGerada(ctx, f.chave, posicao))
		}
		sincronia := url.Values{"atraso": {strconv.FormatFloat(l.Atraso, 'f', 2, 64)}, "escala": {strconv.FormatFloat(l.Escala, 'f', 6, 64)}}.Encode()
		legenda = base + "/legenda/" + l.ID + ".srt?" + sincronia
		endereco += "&legenda=" + l.ID + "&" + sincronia
	}
	mime := f.tipoMime
	if f.aoVivo {
		mime = "video/mp2t"
	}

	if c.a.usaAConexao(ctx, f.chave) {
		c.a.conexao.assumir(f.chave, t.ID)
	}
	_, _ = c.acao(ctx, t, "Stop", nil) // algumas telas recusam um vídeo novo com outro tocando
	if _, err := c.acao(ctx, t, "SetAVTransportURI", [][2]string{{"CurrentURI", endereco}, {"CurrentURIMetaData", didl(f.titulo, capa, endereco, mime, f.aoVivo, legenda)}}); err != nil {
		return sessao{}, err
	}
	if _, err := c.acao(ctx, t, "Play", [][2]string{{"Speed", "1"}}); err != nil {
		return sessao{}, err
	}
	s := sessao{Chave: f.chave, Titulo: f.titulo, Capa: capa, AoVivo: f.aoVivo, Inicio: posicao, Desde: c.a.carimbo(), legendaAte: legendaAte}
	c.mu.Lock()
	c.sessoes[t.ID] = s
	c.mu.Unlock()
	if posicao > 5 && !f.aoVivo {
		go c.irQuandoTocar(t, posicao)
	}
	if !f.aoVivo {
		go c.acompanhar(t, s.Desde)
	}
	return s, nil
}

// irQuandoTocar espera o vídeo andar de verdade e vai até a posição. O projetor diz PLAYING
// enquanto ainda carrega, e ignora a busca feita cedo demais: por isso confere e tenta de novo.
func (c *controleDasTelas) irQuandoTocar(t tela, posicao float64) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	esperar := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}
	tentativas := 0
	for esperar(time.Second) {
		info, err := c.acao(ctx, t, "GetTransportInfo", nil)
		if err != nil || info["CurrentTransportState"] != "PLAYING" {
			continue
		}
		pos, err := c.acao(ctx, t, "GetPositionInfo", nil)
		if err != nil || segundosDe(pos["TrackDuration"]) == 0 {
			continue
		}
		atual := segundosDe(pos["RelTime"])
		if atual >= posicao-15 {
			return // chegou (ou o Lucas já avançou pelo controle)
		}
		if tentativas == 3 {
			log.Printf("tela %s: não foi até %s", t.Nome, relogio(posicao))
			return
		}
		tentativas++
		if _, err := c.acao(ctx, t, "Seek", [][2]string{{"Unit", "REL_TIME"}, {"Target", relogio(posicao)}}); err != nil {
			log.Printf("tela %s: ir para %s: %v", t.Nome, relogio(posicao), err)
		}
		esperar(2 * time.Second)
	}
}

func relogio(segundos float64) string {
	s := int(segundos)
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
}

// segundosDe lê "1:02:03", "01:02:03.500" ou "NOT_IMPLEMENTED" (zero).
func segundosDe(s string) float64 {
	partes := strings.Split(strings.TrimSpace(s), ":")
	if len(partes) != 3 {
		return 0
	}
	h, e1 := strconv.ParseFloat(partes[0], 64)
	m, e2 := strconv.ParseFloat(partes[1], 64)
	seg, e3 := strconv.ParseFloat(partes[2], 64)
	if e1 != nil || e2 != nil || e3 != nil {
		return 0
	}
	return h*3600 + m*60 + seg
}

type estadoDaTela struct {
	Estado  string  `json:"estado"` // PLAYING, PAUSED_PLAYBACK, STOPPED, TRANSITIONING, NO_MEDIA_PRESENT
	Posicao float64 `json:"posicao"`
	Duracao float64 `json:"duracao"`
	Volume  int     `json:"volume"`
	Mudo    bool    `json:"mudo"`
	Sessao  *sessao `json:"sessao,omitempty"`
}

func (c *controleDasTelas) estado(ctx context.Context, t tela) (estadoDaTela, error) {
	var e estadoDaTela
	info, err := c.acao(ctx, t, "GetTransportInfo", nil)
	if err != nil {
		return e, err
	}
	e.Estado = info["CurrentTransportState"]
	if pos, err := c.acao(ctx, t, "GetPositionInfo", nil); err == nil {
		e.Posicao, e.Duracao = segundosDe(pos["RelTime"]), segundosDe(pos["TrackDuration"])
	}
	if t.volume != "" {
		if v, err := c.acaoDeVolume(ctx, t, "GetVolume", nil); err == nil {
			e.Volume, _ = strconv.Atoi(strings.TrimSpace(v["CurrentVolume"]))
		}
		if v, err := c.acaoDeVolume(ctx, t, "GetMute", nil); err == nil {
			e.Mudo = strings.TrimSpace(v["CurrentMute"]) == "1"
		}
	}
	c.mu.Lock()
	if s, ok := c.sessoes[t.ID]; ok {
		if e.Estado == "STOPPED" || e.Estado == "NO_MEDIA_PRESENT" {
			// Passou um tempo parada: o vídeo acabou ou foi fechado no controle da TV.
			if desde, err := time.Parse(time.RFC3339, s.Desde); err == nil && c.a.agora().Sub(desde) > 20*time.Second {
				delete(c.sessoes, t.ID)
			} else {
				e.Sessao = &s
			}
		} else {
			e.Sessao = &s
		}
	}
	c.mu.Unlock()
	if e.Sessao != nil && !e.Sessao.AoVivo && e.Posicao > 0 {
		c.a.guardarProgressoDaTela(*e.Sessao, e.Posicao, e.Duracao)
	}
	return e, nil
}

func (c *controleDasTelas) comandar(ctx context.Context, t tela, comando string, valor float64) error {
	var err error
	switch comando {
	case "pausar":
		_, err = c.acao(ctx, t, "Pause", nil)
	case "continuar":
		_, err = c.acao(ctx, t, "Play", [][2]string{{"Speed", "1"}})
	case "parar":
		_, _ = c.estado(ctx, t) // guarda onde parou antes de fechar
		_, err = c.acao(ctx, t, "Stop", nil)
		// O projetor, depois de um Seek, às vezes só pausa no primeiro Stop (e segura o vídeo,
		// que pela assinatura é a única conexão): confere e manda de novo.
		for i := 0; err == nil && i < 3; i++ {
			select {
			case <-ctx.Done():
			case <-time.After(c.esperaDoStop):
			}
			info, e := c.acao(ctx, t, "GetTransportInfo", nil)
			if e != nil || (info["CurrentTransportState"] != "PAUSED_PLAYBACK" && info["CurrentTransportState"] != "PLAYING") {
				break
			}
			_, err = c.acao(ctx, t, "Stop", nil)
		}
		c.mu.Lock()
		delete(c.sessoes, t.ID)
		c.mu.Unlock()
	case "ir":
		_, err = c.acao(ctx, t, "Seek", [][2]string{{"Unit", "REL_TIME"}, {"Target", relogio(max(0, valor))}})
		if errors.Is(err, errTelaDemorou) {
			err = nil // a TV está indo; o próximo estado mostra onde chegou
		}
	case "pular":
		pos, e := c.acao(ctx, t, "GetPositionInfo", nil)
		if e != nil {
			return e
		}
		alvo := max(0, segundosDe(pos["RelTime"])+valor)
		if d := segundosDe(pos["TrackDuration"]); d > 0 {
			alvo = min(alvo, d-2)
		}
		_, err = c.acao(ctx, t, "Seek", [][2]string{{"Unit", "REL_TIME"}, {"Target", relogio(alvo)}})
		if errors.Is(err, errTelaDemorou) {
			err = nil
		}
	case "volume", "mudo":
		if comando == "volume" {
			_, err = c.acaoDeVolume(ctx, t, "SetVolume", [][2]string{{"DesiredVolume", strconv.Itoa(int(max(0, min(100, valor))))}})
		} else {
			mudo := "0"
			if valor != 0 {
				mudo = "1"
			}
			_, err = c.acaoDeVolume(ctx, t, "SetMute", [][2]string{{"DesiredMute", mudo}})
		}
	default:
		return errComandoDesconhecido
	}
	return err
}

var errComandoDesconhecido = errors.New("comando desconhecido")

// ---------- Rotas ----------

func (a *app) servidorDasTelas() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tela/{tipo}/{arquivo}", a.servirVideo(true))
	mux.HandleFunc("HEAD /tela/{tipo}/{arquivo}", a.servirVideo(true))
	mux.HandleFunc("GET /legenda/{arquivo}", a.servirLegenda(false))
	return mux
}

func (a *app) handleTelas(w http.ResponseWriter, r *http.Request) {
	lista, err := a.listaDeTelas(r.Context())
	if err != nil {
		falha(w, err)
		return
	}
	// Ligada = respondeu agora. Todas ao mesmo tempo, para a lista não demorar.
	var wg sync.WaitGroup
	for i := range lista {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			_, err := a.telas.acao(ctx, lista[i], "GetTransportInfo", nil)
			lista[i].Ligada = err == nil
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, lista)
}

func (a *app) handleProcurarTelas(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if _, err := a.telas.procurar(ctx); err != nil {
		falha(w, err)
		return
	}
	a.handleTelas(w, r)
}

func (a *app) telaDoCaminho(w http.ResponseWriter, r *http.Request) (tela, bool) {
	t, err := a.tela(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		erroJSON(w, http.StatusNotFound, "Tela não encontrada.")
		return t, false
	}
	if err != nil {
		falha(w, err)
		return t, false
	}
	return t, true
}

func (a *app) handleMudarTela(w http.ResponseWriter, r *http.Request) {
	t, ok := a.telaDoCaminho(w, r)
	if !ok {
		return
	}
	var corpo struct {
		Nome string `json:"nome"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	nome := strings.TrimSpace(corpo.Nome)
	if nome == "" {
		nome = t.NomeOriginal
	}
	if _, err := a.db.ExecContext(r.Context(), `UPDATE telas SET nome = ? WHERE id = ?`, nome, t.ID); err != nil {
		falha(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) handleApagarTela(w http.ResponseWriter, r *http.Request) {
	if _, err := a.db.ExecContext(r.Context(), `DELETE FROM telas WHERE id = ?`, r.PathValue("id")); err != nil {
		falha(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) handleTocarNaTela(w http.ResponseWriter, r *http.Request) {
	t, ok := a.telaDoCaminho(w, r)
	if !ok {
		return
	}
	var corpo struct {
		Chave   string  `json:"chave"`
		Posicao float64 `json:"posicao"`
		Capa    string  `json:"capa"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	tipo, idTexto, _ := strings.Cut(corpo.Chave, ":")
	id, err := strconv.ParseInt(idTexto, 10, 64)
	if err != nil {
		erroJSON(w, http.StatusBadRequest, "Vídeo inválido.")
		return
	}
	s, err := a.telas.tocar(r.Context(), t, tipo, id, corpo.Posicao, corpo.Capa)
	if errors.Is(err, sql.ErrNoRows) {
		erroJSON(w, http.StatusNotFound, "Vídeo não encontrado.")
		return
	}
	if err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (a *app) handleEstadoDaTela(w http.ResponseWriter, r *http.Request) {
	t, ok := a.telaDoCaminho(w, r)
	if !ok {
		return
	}
	e, err := a.telas.estado(r.Context(), t)
	if err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (a *app) handleComandoNaTela(w http.ResponseWriter, r *http.Request) {
	t, ok := a.telaDoCaminho(w, r)
	if !ok {
		return
	}
	var corpo struct {
		Comando string  `json:"comando"`
		Valor   float64 `json:"valor"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	err := a.telas.comandar(r.Context(), t, corpo.Comando, corpo.Valor)
	if errors.Is(err, errComandoDesconhecido) {
		erroJSON(w, http.StatusBadRequest, "Comando desconhecido.")
		return
	}
	if err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSessoes diz o que cada tela está tocando por ordem do app, para a barra de controle.
func (a *app) handleSessoes(w http.ResponseWriter, r *http.Request) {
	a.telas.mu.Lock()
	ids := make([]string, 0, len(a.telas.sessoes))
	for id := range a.telas.sessoes {
		ids = append(ids, id)
	}
	a.telas.mu.Unlock()
	lista := []map[string]any{}
	for _, id := range ids {
		t, err := a.tela(r.Context(), id)
		if err != nil {
			continue
		}
		a.telas.mu.Lock()
		s, ok := a.telas.sessoes[id]
		a.telas.mu.Unlock()
		if ok {
			lista = append(lista, map[string]any{"tela": t, "sessao": s})
		}
	}
	writeJSON(w, http.StatusOK, lista)
}
