package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// torrentDeVideo gera com o ffmpeg um filme de 2 minutos (H.264 + AAC) e o põe num torrent servido
// por um web seed local. Sem ffmpeg na máquina, o teste é pulado.
func torrentDeVideo(t *testing.T) []byte {
	var ficha bytes.Buffer
	torrentDeVideoCom(t, "").Write(&ficha)
	return ficha.Bytes()
}

// torrentDeVideoCom: o mesmo, em MKV com a legenda (SRT) dentro, se vier uma.
func torrentDeVideoCom(t *testing.T, legenda string) *metainfo.MetaInfo {
	t.Helper()
	if _, err := exec.LookPath(programaFFmpeg); err != nil {
		t.Skip("ffmpeg não instalado")
	}
	raiz := t.TempDir()
	pasta := filepath.Join(raiz, "Filme Teste 2026 1080p WEB-DL")
	os.MkdirAll(pasta, 0o755)
	video := filepath.Join(pasta, "Filme.Teste.2026.1080p.WEB-DL.mp4")
	args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=10", "-f", "lavfi", "-i", "sine=frequency=440"}
	if legenda != "" {
		srt := filepath.Join(raiz, "dentro.srt")
		os.WriteFile(srt, []byte(legenda), 0o644)
		video = strings.TrimSuffix(video, ".mp4") + ".mkv"
		args = append(args, "-i", srt, "-map", "0", "-map", "1", "-map", "2", "-c:s", "srt")
	}
	args = append(args, "-t", "120", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-shortest", video)
	if out, err := exec.Command(programaFFmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("gerar vídeo: %v %s", err, out)
	}
	info := metainfo.Info{PieceLength: 64 << 10}
	if err := info.BuildFromFilePath(pasta); err != nil {
		t.Fatal(err)
	}
	semente := httptest.NewServer(http.FileServer(http.Dir(raiz)))
	t.Cleanup(semente.Close)
	mi := &metainfo.MetaInfo{UrlList: []string{semente.URL + "/"}}
	mi.InfoBytes, _ = bencode.Marshal(info)
	return mi
}

// tmdbDeFilmes: o filme 1 tem 2 minutos (como o vídeo gerado); o 2 tem 2 h 52 e só sai em digital
// daqui a uma semana.
func tmdbDeFilmes(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		semana := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
		switch r.URL.Path {
		case "/3/configuration":
			fmt.Fprint(w, `{}`)
		case "/3/movie/1":
			fmt.Fprint(w, `{"id":1,"title":"Filme Teste","original_title":"Filme Teste","release_date":"2020-01-01","runtime":2}`)
		case "/3/movie/2":
			fmt.Fprintf(w, `{"id":2,"title":"Filme Teste","original_title":"Filme Teste","release_date":"2026-07-17","runtime":172,
				"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"type":3,"release_date":"2026-07-17T00:00:00.000Z"},{"type":4,"release_date":"%sT00:00:00.000Z"}]}]}}`, semana)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestVerificarOpcao(t *testing.T) {
	e := novoAmbiente(t)
	ficha := torrentDeVideo(t)
	var buscas []string
	prowlarr := prowlarrFalso(t, ficha, &buscas)
	e.pedir(t, "PUT", "/api/prowlarr", configDoProwlarr{URL: prowlarr.URL, Chave: chaveFalsa})
	t.Setenv("TMDB_URL", tmdbDeFilmes(t).URL)
	e.pedir(t, "PUT", "/api/tmdb", map[string]string{"chave": "0123456789abcdef0123456789abcdef"})
	link := prowlarr.URL + "/1/download?link=abc&file=Filme"

	// Duração confere: ok, com formatos e quadros.
	status, corpo := e.pedir(t, "POST", "/api/verificacoes", pedidoDeVerificacao{Chave: "r:um", Link: link, TMDB: "filme/1", Nome: "Filme Teste 2020 1080p WEB-DL"})
	if status != http.StatusAccepted {
		t.Fatalf("verificar: %d %s", status, corpo)
	}
	var vs map[string]verificacao
	e.json(t, "/api/verificacoes?chaves=r:um", &vs)
	v := vs["r:um"]
	if v.Estado != "ok" || v.Formatos == nil || v.Formatos.Video != "h264" || len(v.Formatos.Audios) != 1 || v.Formatos.Audios[0].Codec != "aac" ||
		v.Esperada != 120 || len(v.Quadros) != 3 {
		t.Fatalf("verificação ok: %+v", v)
	}
	if status, _ := e.pedir(t, "GET", "/api/quadros/"+v.Quadros[0], nil); status != 200 {
		t.Fatalf("quadro: %d", status)
	}
	// O candidato fica fora da lista até ser adicionado; a sondagem fica no arquivo.
	var lista struct {
		Torrents []torrentGuardado `json:"torrents"`
	}
	e.json(t, "/api/torrents", &lista)
	if len(lista.Torrents) != 0 {
		t.Fatalf("candidato apareceu na lista: %+v", lista.Torrents)
	}
	var sondagemGuardada string
	e.a.db.QueryRow(`SELECT sondagem FROM torrent_arquivos WHERE id = ?`, v.ArquivoID).Scan(&sondagemGuardada)
	if !strings.Contains(sondagemGuardada, `"video":"h264"`) {
		t.Fatalf("sondagem não guardada: %q", sondagemGuardada)
	}

	// Mesmo arquivo para um filme de 2 h 52 que ainda não saiu em digital: errado, com o alerta.
	e.pedir(t, "POST", "/api/verificacoes", pedidoDeVerificacao{Chave: "r:dois", Link: link, TMDB: "filme/2", Nome: "Filme Teste 2026 1080p WEB-DL"})
	e.json(t, "/api/verificacoes?chaves=r:dois", &vs)
	v = vs["r:dois"]
	if v.Estado != "errado" || !strings.Contains(v.Resumo, "2 min") || !strings.Contains(v.Resumo, "2 h 52 min") || !strings.Contains(strings.Join(v.Detalhes, " "), "ainda não saiu em digital") {
		t.Fatalf("verificação errada: %+v", v)
	}

	// Adicionar de verdade: entra na lista.
	if status, corpo := e.pedir(t, "POST", "/api/torrents", map[string]string{"prowlarr": link}); status != 200 {
		t.Fatalf("adicionar: %d %s", status, corpo)
	}
	e.json(t, "/api/torrents", &lista)
	if len(lista.Torrents) != 1 {
		t.Fatalf("depois de adicionar: %+v", lista.Torrents)
	}
}

func TestAlertasERecomendacao(t *testing.T) {
	agora := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	antes := tituloDoTMDB{Tipo: "filme", Ano: "2026", DigitalEm: "2026-11-17", nomes: []string{"The Odyssey"}, principais: []string{"The Odyssey"}}
	o := organizar(antes, []resultadoDaBusca{
		{Titulo: "The Odyssey (2026) [1080p] [WEBRip] [5.1]", Semeadores: 2900, Tamanho: 2 << 30},
		{Titulo: "The.Odyssey.2026.1080p.TELESYNC.x264", Semeadores: 4300, Tamanho: 3 << 30},
		{Titulo: "The.Odyssey.2026.1080p.BluRray.Multi.x264-KIN", Semeadores: 6, Tamanho: 3 << 30},
	}, nil, agora)
	if len(o.Versoes) != 3 {
		t.Fatalf("versões: %+v", o.Versoes)
	}
	var web, cinema opcao
	for _, v := range o.Versoes {
		switch {
		case strings.Contains(v.Titulo, "WEBRip"):
			web = v
		case strings.Contains(v.Titulo, "TELESYNC"):
			cinema = v
		case !v.Suspeito:
			t.Fatalf("\"BluRray\" (grafia errada) antes do digital também é suspeito: %+v", v)
		}
	}
	if !web.Suspeito || web.Recomendado || !strings.Contains(strings.Join(web.Alertas, " "), "17/11/2026") {
		t.Fatalf("WEBRip antes do digital devia ser suspeito: %+v", web)
	}
	if cinema.Suspeito || !strings.Contains(strings.Join(cinema.Alertas, " "), "Gravado no cinema") {
		t.Fatalf("TELESYNC é gravação de cinema, não suspeito: %+v", cinema)
	}
	// Sem data digital no TMDB: vale a estreia (menos de 150 dias = fora do digital).
	semData := tituloDoTMDB{Tipo: "filme", EstreiaEm: "2026-10-01"}
	if !foraDoDigital(semData, agora) || foraDoDigital(tituloDoTMDB{Tipo: "filme", EstreiaEm: "2025-01-01"}, agora) {
		t.Fatal("regra da estreia")
	}
	if arquivoSuspeito("Filme.2026.1080p.mkv.exe") == "" || arquivoSuspeito("Filme.2026.mkv") != "" {
		t.Fatal("arquivo perigoso")
	}
}

// As 3 melhores opções são verificadas ao mesmo tempo (pedidos de verdade em paralelo, com -race).
func TestVerificacoesAoMesmoTempo(t *testing.T) {
	e := novoAmbiente(t)
	ficha := torrentDeVideo(t)
	var buscas []string
	prowlarr := prowlarrFalso(t, ficha, &buscas)
	e.pedir(t, "PUT", "/api/prowlarr", configDoProwlarr{URL: prowlarr.URL, Chave: chaveFalsa})
	t.Setenv("TMDB_URL", tmdbDeFilmes(t).URL)
	e.pedir(t, "PUT", "/api/tmdb", map[string]string{"chave": "0123456789abcdef0123456789abcdef"})
	e.a.emFundo = func(f func()) { go f() }
	link := prowlarr.URL + "/1/download?link=abc&file=Filme"
	chaves := []string{"r:a", "r:b", "r:c"}
	for _, c := range chaves {
		if status, corpo := e.pedir(t, "POST", "/api/verificacoes", pedidoDeVerificacao{Chave: c, Link: link, TMDB: "filme/1", Nome: "Filme Teste 2020 1080p WEB-DL"}); status != http.StatusAccepted {
			t.Fatalf("verificar %s: %d %s", c, status, corpo)
		}
	}
	var vs map[string]verificacao
	for range 1200 {
		e.json(t, "/api/verificacoes?chaves="+strings.Join(chaves, ","), &vs)
		prontas := 0
		for _, c := range chaves {
			if vs[c].Estado != "verificando" {
				prontas++
			}
		}
		if prontas == len(chaves) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, c := range chaves {
		if vs[c].Estado != "ok" {
			t.Fatalf("%s: %+v", c, vs[c])
		}
	}
}
