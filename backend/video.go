package main

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// O servidor repassa o vídeo (S4): o provedor só fala HTTP, e o PWA em HTTPS não pode tocar
// vídeo HTTP; as telas, por DLNA, não seguem o redirecionamento do provedor. O aparelho nunca
// vê o usuário e a senha.

// ---------- Uma reprodução por vez (S5) ----------

var errOutraReproducao = errors.New("outra reprodução assumiu a assinatura")

// conexao garante que só um vídeo use a assinatura de cada vez. Quem começa a assistir (o
// player do app ou o comando para uma tela) assume a conexão; os pedidos de outro vídeo são
// recusados, e os que estavam abertos são cortados.
type conexao struct {
	mu      sync.Mutex
	atual   string // chave do vídeo dono da conexão: filme:1, episodio:2, canal:3
	dono    string // "app" ou o id da tela
	abertos map[int]aberto
	prox    int
	// Enquanto o servidor usa a conexão por trás (S28), quem era o dono antes dele.
	antes struct{ atual, dono string }
	// aoTrocarDeDono é chamado quando uma tela perde a conexão para outro aparelho.
	aoTrocarDeDono func(donoAnterior string)
}

type aberto struct {
	chave   string
	cancel  context.CancelFunc
	porTras bool // do servidor (ler formatos, baixar): cede a vez a qualquer pedido de verdade
}

// donoPorTras é o "dono" da conexão enquanto o servidor a usa sem ninguém assistindo.
const donoPorTras = "servidor"

func (c *conexao) assumir(chave, dono string) {
	c.mu.Lock()
	c.cortarOQueEPorTras()
	anterior := c.dono
	c.atual, c.dono = chave, dono
	for id, ab := range c.abertos {
		if ab.chave != chave {
			ab.cancel()
			delete(c.abertos, id)
		}
	}
	avisar := c.aoTrocarDeDono
	c.mu.Unlock()
	if anterior != "" && anterior != donoPorTras && anterior != dono && avisar != nil {
		go avisar(anterior)
	}
}

func (c *conexao) donoAtual() (chave, dono string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.atual, c.dono
}

// entrar registra um pedido ao provedor. Sem dono (servidor recém-ligado), o primeiro assume. Um
// pedido de verdade corta o que o servidor fazia por trás e devolve a conexão a quem a tinha.
func (c *conexao) entrar(ctx context.Context, chave string) (context.Context, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cortarOQueEPorTras()
	if c.atual == "" {
		c.atual, c.dono = chave, "app"
	}
	if chave != c.atual {
		return nil, nil, errOutraReproducao
	}
	return c.registrar(ctx, chave, false)
}

// entrarPorTras é para o trabalho do servidor (S28): só entra se ninguém estiver usando a conexão
// com outro vídeo, e sai (o contexto é cancelado) assim que alguém pedir um vídeo.
func (c *conexao) entrarPorTras(ctx context.Context, chave string) (context.Context, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ab := range c.abertos {
		if !ab.porTras || ab.chave != chave {
			return nil, nil, errOutraReproducao
		}
	}
	if c.dono != donoPorTras {
		c.antes.atual, c.antes.dono = c.atual, c.dono
	}
	c.atual, c.dono = chave, donoPorTras
	return c.registrar(ctx, chave, true)
}

func (c *conexao) cortarOQueEPorTras() {
	for id, ab := range c.abertos {
		if ab.porTras {
			ab.cancel()
			delete(c.abertos, id)
		}
	}
	if c.dono == donoPorTras {
		c.atual, c.dono = c.antes.atual, c.antes.dono
	}
}

func (c *conexao) registrar(ctx context.Context, chave string, porTras bool) (context.Context, func(), error) {
	if c.abertos == nil {
		c.abertos = map[int]aberto{}
	}
	ctx, cancel := context.WithCancel(ctx)
	id := c.prox
	c.prox++
	c.abertos[id] = aberto{chave, cancel, porTras}
	return ctx, func() {
		cancel()
		c.mu.Lock()
		delete(c.abertos, id)
		c.mu.Unlock()
	}, nil
}

// ---------- De onde vem cada vídeo ----------

type fonte struct {
	chave    string
	titulo   string
	url      string // no provedor
	tipoMime string
	aoVivo   bool
}

var mimes = map[string]string{
	"mp4": "video/mp4", "m4v": "video/mp4", "mkv": "video/x-matroska", "avi": "video/x-msvideo",
	"ts": "video/mp2t", "m3u8": "application/vnd.apple.mpegurl", "webm": "video/webm", "mov": "video/quicktime",
}

func mimeDe(extensao string) string {
	if m := mimes[strings.ToLower(extensao)]; m != "" {
		return m
	}
	return "application/octet-stream"
}

// fonteDe acha o vídeo pela chave. Para canais, formato diz se é HLS (app) ou TS (telas).
func (a *app) fonteDe(ctx context.Context, tipo string, id int64, formato string) (fonte, error) {
	if tipo == "proprio" { // meus vídeos (S42): não dependem da assinatura
		return a.fonteDoMeuVideo(ctx, id)
	}
	ac, err := a.acesso()
	if err != nil {
		return fonte{}, err
	}
	if !ac.completo() {
		return fonte{}, errors.New("acesso não configurado")
	}
	chave := fmt.Sprintf("%s:%d", tipo, id)
	switch tipo {
	case "filme":
		it, err := a.item(ctx, "filme", id)
		if err != nil {
			return fonte{}, err
		}
		ext := primeiro(it.Extensao, "mp4")
		return fonte{chave, it.Nome, ac.urlDoVideo("movie", id, ext), mimeDe(ext), false}, nil
	case "episodio":
		e, err := a.episodio(ctx, id)
		if err != nil {
			return fonte{}, err
		}
		titulo := e.Titulo
		if s, err := a.item(ctx, "serie", e.SerieID); err == nil {
			titulo = fmt.Sprintf("%s T%d E%d", s.Nome, e.Temporada, e.Numero)
		}
		return fonte{chave, titulo, ac.urlDoVideo("series", id, e.Extensao), mimeDe(e.Extensao), false}, nil
	case "arquivo":
		f, t, err := a.arquivo(ctx, id)
		if err != nil {
			return fonte{}, err
		}
		// O endereço é só para a extensão: quem entrega é servirArquivoDeTorrent.
		ext := strings.TrimPrefix(filepath.Ext(f.Nome), ".")
		return fonte{chave, tituloDoArquivo(f, t), "torrent://" + f.Hash + "." + ext, mimeDe(ext), false}, nil
	case "canal":
		it, err := a.item(ctx, "canal", id)
		if err != nil {
			return fonte{}, err
		}
		if formato != "ts" {
			formato = "m3u8"
		}
		return fonte{chave, it.Nome, ac.urlDoVideo("live", id, formato), mimeDe(formato), true}, nil
	}
	return fonte{}, sql.ErrNoRows
}

// O provedor redireciona cada pedido para um servidor de entrega com um token. Guardar o destino
// poupa um salto a cada pedaço do vídeo (o player pede vários ao avançar).
type destinoGuardado struct {
	url    string
	expira time.Time
}

var (
	destinosMu sync.Mutex
	destinos   = map[string]destinoGuardado{}
)

func lembrarDestino(origem, final string) {
	destinosMu.Lock()
	defer destinosMu.Unlock()
	destinos[origem] = destinoGuardado{final, time.Now().Add(10 * time.Minute)}
}

func destinoDe(origem string) string {
	destinosMu.Lock()
	defer destinosMu.Unlock()
	d, ok := destinos[origem]
	if !ok || time.Now().After(d.expira) {
		delete(destinos, origem)
		return ""
	}
	return d.url
}

func esquecerDestino(origem string) {
	destinosMu.Lock()
	delete(destinos, origem)
	destinosMu.Unlock()
}

// pedirAoProvedor faz o GET (com o Range do aparelho) e devolve a resposta já no servidor de entrega.
func (a *app) pedirAoProvedor(ctx context.Context, origem, faixa string) (*http.Response, error) {
	tentar := func(endereco string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endereco, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", agenteDoPlayer)
		if faixa != "" {
			req.Header.Set("Range", faixa)
		}
		resp, err := a.http.Do(req)
		if err != nil {
			return nil, semSegredos(err)
		}
		return resp, nil
	}
	if guardado := destinoDe(origem); guardado != "" {
		resp, err := tentar(guardado)
		if err == nil && resp.StatusCode < 400 {
			return resp, nil
		}
		if err == nil {
			resp.Body.Close()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		esquecerDestino(origem) // o token venceu: volta ao provedor
	}
	resp, err := tentar(origem)
	if err != nil {
		return nil, err
	}
	if resp.Request.URL.String() != origem {
		lembrarDestino(origem, resp.Request.URL.String())
	}
	return resp, nil
}

// ---------- Rotas ----------

var reArquivo = regexp.MustCompile(`^(\d+)\.(\w+)$`)

// servirVideo atende /api/video/{tipo}/{arquivo} (app) e /tela/{tipo}/{arquivo} (telas).
func (a *app) servirVideo(paraTela bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := reArquivo.FindStringSubmatch(r.PathValue("arquivo"))
		if m == nil {
			http.NotFound(w, r)
			return
		}
		id, _ := strconv.ParseInt(m[1], 10, 64)
		if paraTela {
			a.legendaNaTela(w, r)
		}
		if r.PathValue("tipo") == "arquivo" {
			a.servirArquivoDeTorrent(w, r, id, paraTela)
			return
		}
		if r.PathValue("tipo") == "proprio" {
			a.servirMeuVideo(w, r, id, paraTela)
			return
		}
		// Baixado da assinatura (S28): sai do disco, sem usar a conexão.
		if tipo := r.PathValue("tipo"); tipo == "filme" || tipo == "episodio" {
			if c := a.baixadoDaAssinatura(r.Context(), fmt.Sprintf("%s:%d", tipo, id)); c != "" {
				w.Header().Set("Content-Type", mimeDe(strings.TrimPrefix(filepath.Ext(c), ".")))
				w.Header().Set("Cache-Control", "no-store")
				if paraTela {
					cabecalhosDLNA(w, fonte{})
				}
				http.ServeFile(w, r, c)
				return
			}
		}
		// Canal: o app recebe HLS (o navegador não toca TS); a tela, o TS contínuo.
		formato := "m3u8"
		if paraTela {
			formato = "ts"
		}
		f, err := a.fonteDe(r.Context(), r.PathValue("tipo"), id, formato)
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		switch {
		case f.aoVivo && !paraTela:
			a.servirListaAoVivo(w, r, f, f.url)
		case f.aoVivo:
			a.servirFluxo(w, r, f)
		default:
			a.servirArquivo(w, r, f, paraTela)
		}
	}
}

func cabecalhosDLNA(w http.ResponseWriter, f fonte) {
	h := w.Header()
	h.Set("transferMode.dlna.org", "Streaming")
	if f.aoVivo {
		// Ao vivo não tem como avançar: flags de fluxo contínuo, sem busca por bytes.
		h.Set("contentFeatures.dlna.org", "DLNA.ORG_OP=00;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000")
	} else {
		h.Set("contentFeatures.dlna.org", "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000")
	}
}

func recusarPorOutraReproducao(w http.ResponseWriter) {
	http.Error(w, "Outro aparelho está usando a assinatura.", http.StatusConflict)
}

// servirArquivo repassa filme ou episódio, com Range (avançar e voltar).
func (a *app) servirArquivo(w http.ResponseWriter, r *http.Request, f fonte, paraTela bool) {
	ctx, sair, err := a.conexao.entrar(r.Context(), f.chave)
	if err != nil {
		recusarPorOutraReproducao(w)
		return
	}
	defer sair()

	faixa := r.Header.Get("Range")
	if r.Method == http.MethodHead {
		faixa = "bytes=0-0" // só para saber o tamanho
	}
	resp, err := a.pedirAoProvedor(ctx, f.url, faixa)
	if err != nil {
		if ctx.Err() == nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		http.Error(w, fmt.Sprintf("O provedor respondeu %d.", resp.StatusCode), http.StatusBadGateway)
		return
	}

	h := w.Header()
	tipo := resp.Header.Get("Content-Type")
	if tipo == "" || strings.HasPrefix(tipo, "application/octet-stream") || strings.HasPrefix(tipo, "text/") {
		tipo = f.tipoMime
	}
	h.Set("Content-Type", tipo)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Cache-Control", "no-store")
	for _, k := range []string{"Last-Modified", "ETag"} {
		if v := resp.Header.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	if paraTela {
		cabecalhosDLNA(w, f)
	}

	if r.Method == http.MethodHead {
		if total := totalDoIntervalo(resp.Header.Get("Content-Range")); total > 0 {
			h.Set("Content-Length", strconv.FormatInt(total, 10))
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	for _, k := range []string{"Content-Length", "Content-Range"} {
		if v := resp.Header.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	copiarAoVivo(w, resp.Body)
}

// totalDoIntervalo lê o tamanho total de "bytes 0-0/463264440".
func totalDoIntervalo(cr string) int64 {
	i := strings.LastIndex(cr, "/")
	if i < 0 {
		return 0
	}
	n, _ := strconv.ParseInt(cr[i+1:], 10, 64)
	return n
}

// copiarAoVivo manda cada pedaço assim que chega, sem esperar encher o buffer.
func copiarAoVivo(w http.ResponseWriter, corpo io.Reader) {
	rc := http.NewResponseController(w)
	buf := make([]byte, 256<<10)
	for {
		n, err := corpo.Read(buf)
		if n > 0 {
			if _, e := w.Write(buf[:n]); e != nil {
				return
			}
			_ = rc.Flush()
		}
		if err != nil {
			return
		}
	}
}

// servirFluxo repassa o canal em TS contínuo, para as telas.
func (a *app) servirFluxo(w http.ResponseWriter, r *http.Request, f fonte) {
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Cache-Control", "no-store")
	cabecalhosDLNA(w, f)
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	ctx, sair, err := a.conexao.entrar(r.Context(), f.chave)
	if err != nil {
		recusarPorOutraReproducao(w)
		return
	}
	defer sair()
	esquecerDestino(f.url) // o token do ao vivo é de uso curto
	resp, err := a.pedirAoProvedor(ctx, f.url, "")
	if err != nil {
		if ctx.Err() == nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		http.Error(w, fmt.Sprintf("O provedor respondeu %d.", resp.StatusCode), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusOK)
	copiarAoVivo(w, resp.Body)
}

// ---------- Ao vivo em HLS, para o app ----------

// assinar impede que /parte vire um repasse aberto para qualquer endereço.
func (a *app) assinar(chave, endereco string) string {
	m := hmac.New(sha256.New, a.segredo)
	m.Write([]byte(chave + "\n" + endereco))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

func (a *app) linkDaParte(f fonte, endereco string) string {
	tipo, id, _ := strings.Cut(f.chave, ":")
	return fmt.Sprintf("/api/video/%s/%s/parte?u=%s&s=%s", tipo, id,
		base64.RawURLEncoding.EncodeToString([]byte(endereco)), a.assinar(f.chave, endereco))
}

var reURIAtributo = regexp.MustCompile(`URI="([^"]+)"`)

// servirListaAoVivo busca a lista HLS no provedor e troca cada endereço por um daqui.
func (a *app) servirListaAoVivo(w http.ResponseWriter, r *http.Request, f fonte, endereco string) {
	ctx, sair, err := a.conexao.entrar(r.Context(), f.chave)
	if err != nil {
		recusarPorOutraReproducao(w)
		return
	}
	defer sair()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if endereco == f.url {
		esquecerDestino(f.url) // a lista principal sempre passa pelo provedor: o token renova
	}
	resp, err := a.pedirAoProvedor(ctx, endereco, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		http.Error(w, fmt.Sprintf("O provedor respondeu %d.", resp.StatusCode), http.StatusBadGateway)
		return
	}
	base := resp.Request.URL
	var saida strings.Builder
	leitor := bufio.NewScanner(io.LimitReader(resp.Body, 4<<20))
	leitor.Buffer(make([]byte, 64<<10), 1<<20)
	for leitor.Scan() {
		linha := strings.TrimSpace(leitor.Text())
		switch {
		case linha == "":
		case strings.HasPrefix(linha, "#"):
			linha = reURIAtributo.ReplaceAllStringFunc(linha, func(m string) string {
				u := reURIAtributo.FindStringSubmatch(m)[1]
				return `URI="` + a.linkDaParte(f, resolver(base, u)) + `"`
			})
		default:
			linha = a.linkDaParte(f, resolver(base, linha))
		}
		saida.WriteString(linha + "\n")
	}
	if err := leitor.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, saida.String())
}

func resolver(base *url.URL, ref string) string {
	u, err := base.Parse(ref)
	if err != nil {
		return ref
	}
	return u.String()
}

// handleParte entrega um segmento (ou uma sublista) do ao vivo, só com a assinatura certa.
func (a *app) handleParte(w http.ResponseWriter, r *http.Request) {
	tipo := r.PathValue("tipo")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || tipo != "canal" {
		http.NotFound(w, r)
		return
	}
	bruto, err := base64.RawURLEncoding.DecodeString(r.URL.Query().Get("u"))
	endereco := string(bruto)
	chave := fmt.Sprintf("%s:%d", tipo, id)
	if err != nil || !hmac.Equal([]byte(a.assinar(chave, endereco)), []byte(r.URL.Query().Get("s"))) {
		http.Error(w, "Link inválido.", http.StatusForbidden)
		return
	}
	f, err := a.fonteDe(r.Context(), tipo, id, "m3u8")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	caminho := strings.ToLower(strings.SplitN(endereco, "?", 2)[0])
	if strings.HasSuffix(caminho, ".m3u8") || strings.HasSuffix(caminho, ".m3u") {
		a.servirListaAoVivo(w, r, f, endereco)
		return
	}

	ctx, sair, err := a.conexao.entrar(r.Context(), f.chave)
	if err != nil {
		recusarPorOutraReproducao(w)
		return
	}
	defer sair()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	resp, err := a.pedirAoProvedor(ctx, endereco, r.Header.Get("Range"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil && ctx.Err() == nil {
		log.Printf("segmento: %v", semSegredos(err))
	}
}

// handleAssumir é chamado pelo player do app antes de tocar: este vídeo passa a ser o dono da
// conexão, e o que estava tocando em outro lugar para (S5).
func (a *app) handleAssumir(w http.ResponseWriter, r *http.Request) {
	var corpo struct {
		Chave string `json:"chave"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	tipo, id, ok := strings.Cut(corpo.Chave, ":")
	if _, err := strconv.ParseInt(id, 10, 64); !ok || err != nil || !map[string]bool{"filme": true, "episodio": true, "canal": true, "arquivo": true, "proprio": true}[tipo] {
		erroJSON(w, http.StatusBadRequest, "Vídeo inválido.")
		return
	}
	if a.usaAConexao(r.Context(), corpo.Chave) {
		a.conexao.assumir(corpo.Chave, "app")
	}
	w.WriteHeader(http.StatusNoContent)
}
