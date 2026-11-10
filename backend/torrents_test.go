package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// torrentDeTeste monta uma "série" com dois episódios e uma legenda, servida por um web seed
// local: o motor baixa dele, sem DHT e sem sair da máquina.
func torrentDeTeste(t *testing.T) (*metainfo.MetaInfo, map[string][]byte) {
	t.Helper()
	raiz := t.TempDir()
	pasta := filepath.Join(raiz, "Serie Teste")
	os.MkdirAll(pasta, 0o755)
	conteudo := map[string][]byte{}
	for _, nome := range []string{"Serie Teste S01E01.mp4", "Serie Teste S01E02.mp4", "Serie Teste S01E01.srt", "sample.mp4"} {
		b := make([]byte, 300<<10)
		rand.Read(b)
		if strings.HasSuffix(nome, ".srt") {
			b = []byte("1\n00:00:01,000 --> 00:00:02,000\nOlá\n")
		}
		os.WriteFile(filepath.Join(pasta, nome), b, 0o644)
		conteudo[nome] = b
	}
	info := metainfo.Info{PieceLength: 64 << 10}
	if err := info.BuildFromFilePath(pasta); err != nil {
		t.Fatal(err)
	}
	semente := httptest.NewServer(http.FileServer(http.Dir(raiz)))
	t.Cleanup(semente.Close)
	mi := &metainfo.MetaInfo{UrlList: []string{semente.URL + "/"}}
	var err error
	if mi.InfoBytes, err = bencode.Marshal(info); err != nil {
		t.Fatal(err)
	}
	return mi, conteudo
}

func enviarFicha(t *testing.T, e *ambiente, mi *metainfo.MetaInfo) string {
	t.Helper()
	var ficha bytes.Buffer
	mi.Write(&ficha)
	var corpo bytes.Buffer
	mw := multipart.NewWriter(&corpo)
	parte, _ := mw.CreateFormFile("arquivo", "serie.torrent")
	parte.Write(ficha.Bytes())
	mw.Close()
	resp, err := http.Post(e.api.URL+"/api/torrents", mw.FormDataContentType(), &corpo)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("enviar .torrent: %d", resp.StatusCode)
	}
	return mi.HashInfoBytes().HexString()
}

type detalheDoTorrent struct {
	Torrent  torrentGuardado    `json:"torrent"`
	Arquivos []arquivoDeTorrent `json:"arquivos"`
}

func TestTorrentAssistirBaixarEApagar(t *testing.T) {
	e := novoAmbiente(t)
	mi, conteudo := torrentDeTeste(t)
	hash := enviarFicha(t, e, mi)

	// A lista vem da ficha, sem baixar nada e sem ligar o motor.
	var d detalheDoTorrent
	e.json(t, "/api/torrents/"+hash, &d)
	if e.a.motor.ligado() {
		t.Fatal("o motor ligou só para listar")
	}
	if d.Torrent.Titulo != "Serie Teste" || d.Torrent.Videos != 2 || len(d.Arquivos) != 3 {
		t.Fatalf("ficha: %+v %+v", d.Torrent, d.Arquivos)
	}
	ep1, ep2 := d.Arquivos[0], d.Arquivos[1]
	if ep1.Temporada != 1 || ep1.Episodio != 1 || ep2.Episodio != 2 || d.Arquivos[2].Tipo != "legenda" {
		t.Fatalf("episódios: %+v", d.Arquivos)
	}

	// Assistir: só o trecho pedido, com o motor ligado enquanto isso.
	resp, b := baixar(t, "GET", fmt.Sprintf("%s/api/video/arquivo/%d.mp4", e.api.URL, ep2.ID), "bytes=100000-100999")
	if resp.StatusCode != 206 || !bytes.Equal(b, conteudo["Serie Teste S01E02.mp4"][100000:101000]) {
		t.Fatalf("trecho: %d, %d bytes", resp.StatusCode, len(b))
	}
	if !e.a.motor.ligado() {
		t.Fatal("o motor devia ficar ligado na folga de assistir")
	}

	// Baixar o episódio 1 inteiro: o motor desliga sozinho ao terminar (o 2 ainda está na folga).
	if status, corpo := e.pedir(t, "POST", fmt.Sprintf("/api/arquivos/%d/baixar", ep1.ID), nil); status != 202 {
		t.Fatalf("baixar: %d %s", status, corpo)
	}
	prazo := time.Now().Add(20 * time.Second)
	for {
		e.json(t, "/api/torrents/"+hash, &d)
		if d.Arquivos[0].Download == "baixado" {
			break
		}
		if time.Now().After(prazo) {
			t.Fatalf("não terminou de baixar: %+v", d.Arquivos[0])
		}
		time.Sleep(50 * time.Millisecond)
	}
	caminho := e.a.caminhoBaixado(ep1)
	if caminho == "" {
		t.Fatal("o arquivo baixado não está onde o servidor espera")
	}
	if b, _ := os.ReadFile(caminho); !bytes.Equal(b, conteudo["Serie Teste S01E01.mp4"]) {
		t.Fatal("o arquivo baixado está diferente do original")
	}

	// Baixado, toca do disco, sem ligar o motor. Desligar apaga os trechos só assistidos (o ep. 2).
	e.a.motor.mu.Lock()
	if e.a.motor.cl != nil {
		e.a.motor.cl.Close()
		e.a.motor.cl = nil
	}
	e.a.motor.mu.Unlock()
	e.a.limparTrechos()
	if e.a.caminhoBaixado(ep1) == "" {
		t.Fatal("a limpeza apagou um arquivo baixado")
	}
	resp, b = baixar(t, "GET", fmt.Sprintf("%s/api/video/arquivo/%d.mp4", e.api.URL, ep1.ID), "bytes=0-9")
	if resp.StatusCode != 206 || !bytes.Equal(b, conteudo["Serie Teste S01E01.mp4"][:10]) || e.a.motor.ligado() {
		t.Fatalf("do disco: %d, motor ligado %v", resp.StatusCode, e.a.motor.ligado())
	}

	// Visto: apaga do disco.
	if status, _ := e.pedir(t, "POST", fmt.Sprintf("/api/arquivos/%d/visto", ep1.ID), nil); status != 204 {
		t.Fatal(status)
	}
	if _, err := os.Stat(caminho); !os.IsNotExist(err) {
		t.Fatal("o arquivo visto continua no disco")
	}
	// Nada mais baixado deste torrent: nem os restos do episódio 2 (assistido em parte) ficam.
	if _, err := os.Stat(filepath.Join(e.a.motor.pastaDosDados(), hash)); !os.IsNotExist(err) {
		t.Fatal("a pasta do torrent ficou depois do último visto")
	}
	e.json(t, "/api/torrents/"+hash, &d)
	if !d.Arquivos[0].Visto || d.Arquivos[0].Download != "" {
		t.Fatalf("visto: %+v", d.Arquivos[0])
	}

	// Apagar o torrent tira tudo.
	if status, _ := e.pedir(t, "DELETE", "/api/torrents/"+hash, nil); status != 204 {
		t.Fatal(status)
	}
	if _, err := os.Stat(filepath.Join(e.a.motor.pastaDosDados(), hash)); !os.IsNotExist(err) {
		t.Fatal("a pasta do torrent ficou")
	}
}

func TestMotorDesligaDepoisDaFolga(t *testing.T) {
	m := novoMotor(t.TempDir(), true)
	u, err := m.usar()
	if err != nil {
		t.Fatal(err)
	}
	u2, _ := m.usar()
	u.soltar(0)
	time.Sleep(50 * time.Millisecond)
	if !m.ligado() {
		t.Fatal("desligou com um uso ainda aberto")
	}
	u2.soltar(30 * time.Millisecond)
	u2.soltar(0) // soltar duas vezes não conta duas
	time.Sleep(10 * time.Millisecond)
	if !m.ligado() {
		t.Fatal("desligou antes da folga")
	}
	time.Sleep(100 * time.Millisecond)
	if m.ligado() {
		t.Fatal("não desligou depois da folga")
	}
}

func TestEpisodioDoNome(t *testing.T) {
	casos := map[string][2]int{
		"Show.S02E05.1080p.mkv":          {2, 5},
		"show 3x07.mp4":                  {3, 7},
		"[Grupo] Anime - 03 [1080p].mkv": {1, 3},
		"Anime Episódio 12.mp4":          {1, 12},
		"Filme (1954).mp4":               {0, 0},
	}
	for nome, want := range casos {
		if tmp, ep := episodioDe(nome); tmp != want[0] || ep != want[1] {
			t.Errorf("episodioDe(%q) = %d, %d; queria %v", nome, tmp, ep, want)
		}
	}
}

func TestAdicionarDoArchive(t *testing.T) {
	e := novoAmbiente(t)
	mi, _ := torrentDeTeste(t)
	var ficha bytes.Buffer
	mi.Write(&ficha)
	falso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metadata/filme_teste":
			fmt.Fprint(w, `{"metadata":{"title":"Filme de Teste","description":"<b>Um</b> filme.","date":"1938-01-01"}}`)
		case "/download/filme_teste/filme_teste_archive.torrent":
			w.Write(ficha.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer falso.Close()
	t.Setenv("ARCHIVE_URL", falso.URL)

	hash, err := e.a.adicionarTorrent(context.Background(), "https://archive.org/details/filme_teste")
	if err != nil {
		t.Fatal(err)
	}
	var d detalheDoTorrent
	e.json(t, "/api/torrents/"+hash, &d)
	if d.Torrent.Titulo != "Filme de Teste" || d.Torrent.Sinopse != "Um filme." || d.Torrent.Ano != "1938" || d.Torrent.Origem != "ia" ||
		!strings.HasSuffix(d.Torrent.Capa, "/services/img/filme_teste") {
		t.Fatalf("item do Archive: %+v", d.Torrent)
	}
	if _, err := e.a.adicionarTorrent(context.Background(), "qualquer coisa"); err == nil {
		t.Fatal("texto sem link devia ser recusado")
	}
	if _, err := e.a.adicionarTorrent(context.Background(), "udp://tracker.exemplo.org:1984/announce"); err == nil || !strings.Contains(err.Error(), "rastreador") {
		t.Fatalf("endereço de rastreador devia ter recado próprio: %v", err)
	}
}

// Vários pedidos juntos abrindo o mesmo torrent (o ffprobe e a impressão digital, na verificação)
// recebem o mesmo, adicionado uma vez só: adicionar duas vezes, com rastreadores na ficha, fazia a
// biblioteca entrar em pânico e derrubava o app.
func TestAbrirOMesmoTorrentAoMesmoTempo(t *testing.T) {
	e := novoAmbiente(t)
	mi, _ := torrentDeTeste(t)
	mi.AnnounceList = [][]string{{"udp://127.0.0.1:9/announce"}, {"http://127.0.0.1:9/announce"}}
	hash := enviarFicha(t, e, mi)
	u, err := e.a.motor.usar()
	if err != nil {
		t.Fatal(err)
	}
	defer u.soltar(0)
	abertos := make(chan any, 16)
	for range 16 {
		go func() {
			tt, err := u.abrir(hash)
			if err != nil {
				abertos <- err
				return
			}
			abertos <- tt
		}()
	}
	primeiro := <-abertos
	for range 15 {
		if outro := <-abertos; outro != primeiro {
			t.Fatalf("pedidos juntos abriram torrents diferentes: %v e %v", primeiro, outro)
		}
	}
	e.a.motor.adicionando.Lock()
	adicoes := e.a.motor.adicoes
	e.a.motor.adicionando.Unlock()
	if adicoes != 1 {
		t.Fatalf("o torrent entrou %d vezes no motor; tem de ser uma", adicoes)
	}
}

// Um torrent "híbrido" (v1 + v2) aberto pela ficha: a biblioteca (v1.61.0) não registrava o
// identificador v2, e o anúncio aos rastreadores para ele derrubava o app ("panicif.False" em
// updateTorrentInput). Aconteceu na verificação de opções reais da busca.
func TestAbrirTorrentHibrido(t *testing.T) {
	e := novoAmbiente(t)
	dados := make([]byte, 16<<10)
	rand.Read(dados)
	v1 := sha1.Sum(dados)
	v2 := sha256.Sum256(dados) // arquivo de um bloco só: a raiz da árvore é o hash do bloco
	// A biblioteca lê, mas não grava, a árvore de arquivos do v2: a ficha vai montada à mão.
	info := map[string]any{
		"name": "hibrido.mp4", "length": len(dados), "piece length": 16 << 10, "pieces": v1[:], "meta version": 2,
		"file tree": map[string]any{"hibrido.mp4": map[string]any{"": map[string]any{"length": len(dados), "pieces root": v2[:]}}},
	}
	mi := &metainfo.MetaInfo{AnnounceList: [][]string{{"http://127.0.0.1:9/announce"}}}
	var err error
	if mi.InfoBytes, err = bencode.Marshal(info); err != nil {
		t.Fatal(err)
	}
	if i, err := mi.UnmarshalInfo(); err != nil || !i.HasV1() || !i.HasV2() {
		t.Fatalf("o torrent de teste devia ser híbrido: %v", err)
	}
	hash := enviarFicha(t, e, mi)
	u, err := e.a.motor.usar()
	if err != nil {
		t.Fatal(err)
	}
	defer u.soltar(0)
	if _, err := u.abrir(hash); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // os anúncios rodam ao soltar a trava do motor
}

// O mesmo defeito por um magnet híbrido (com os dois identificadores no link): a ficha não chega
// (ninguém compartilha), mas o motor não pode cair.
func TestMagnetHibrido(t *testing.T) {
	e := novoAmbiente(t)
	u, err := e.a.motor.usar()
	if err != nil {
		t.Fatal(err)
	}
	defer u.soltar(0)
	v1 := strings.Repeat("ab", 20)
	v2 := "1220" + strings.Repeat("cd", 32)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := u.fichaDoMagnet(ctx, "magnet:?xt=urn:btih:"+v1+"&xt=urn:btmh:"+v2+"&tr=http%3A%2F%2F127.0.0.1%3A9%2Fannounce"); err == nil {
		t.Fatal("ninguém compartilha: devia dar tempo esgotado")
	}
	time.Sleep(300 * time.Millisecond)
}
