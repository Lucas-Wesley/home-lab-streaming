package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// legendaFalsa: n falas, de 2 em 2 s a partir de 1 s.
func legendaFalsa(n int) string {
	var s strings.Builder
	for i := 0; i < n; i++ {
		ini := 1 + 2*i
		fmt.Fprintf(&s, "%d\n00:%02d:%02d,000 --> 00:%02d:%02d,500\nLine %d about the pyramids.\n\n", i+1, ini/60, ini%60, ini/60, ini%60+1, i)
	}
	return s.String()
}

// tmdbDeDocumentarios: o filme 77 (inglês, 2 min) e a série 88 (inglês, temporada 1 com dois
// episódios lançados e um por vir); o filme 1396 não é documentário.
func tmdbDeDocumentarios(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/3/configuration":
			fmt.Fprint(w, `{}`)
		case "/3/movie/77":
			fmt.Fprint(w, `{"id":77,"title":"A Era das Pirâmides","original_title":"Age of Pyramids","original_language":"en","release_date":"2015-01-01",
				"runtime":2,"overview":"Como o Egito ergueu as pirâmides.","poster_path":"/p.jpg","genres":[{"name":"Documentário"},{"name":"História"}]}`)
		case "/3/movie/77/credits":
			fmt.Fprint(w, `{"crew":[{"name":"Ana Diretora","job":"Director"},{"name":"Outro","job":"Editor"}]}`)
		case "/3/movie/1396":
			fmt.Fprint(w, `{"id":1396,"title":"Um Drama","original_language":"en","genres":[{"name":"Drama"}]}`)
		case "/3/tv/88":
			fmt.Fprint(w, `{"id":88,"name":"Segredos do Nilo","original_name":"Nile Secrets","original_language":"en","first_air_date":"2020-01-01",
				"genres":[{"name":"Documentário"}],"seasons":[{"season_number":0,"episode_count":1},{"season_number":1,"name":"Temporada 1","episode_count":3}]}`)
		case "/3/tv/88/season/1":
			if r.URL.Query().Get("language") == "en-US" { // o nome que o TMDB não traduziu vem do inglês
				fmt.Fprint(w, `{"episodes":[{"episode_number":1,"name":"The Source"},{"episode_number":2,"name":"The Delta","overview":"Where the Nile meets the sea."}]}`)
				return
			}
			futuro := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
			fmt.Fprintf(w, `{"episodes":[{"episode_number":1,"name":"A Nascente","air_date":"2020-01-01","overview":"Onde o Nilo nasce.","runtime":1},
				{"episode_number":2,"name":"Episódio 2","air_date":"2020-01-08","runtime":1},{"episode_number":3,"name":"Por vir","air_date":"%s"}]}`, futuro)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// openSubtitlesDeDocumentarios: para o filme 77, duas legendas em inglês (a mais baixada é de outra
// versão: termina em 30 s, de um filme de 2 min) e uma em português; para a série, nenhuma.
func openSubtitlesDeDocumentarios(t *testing.T) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	var buscas []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/arquivo/500.srt":
			fmt.Fprint(w, legendaFalsa(15)) // termina em 30 s
		case "/arquivo/501.srt":
			fmt.Fprint(w, legendaFalsa(50)) // termina em 100 s
		case "/api/v1/infos/languages":
			fmt.Fprint(w, `{"data":[]}`)
		case "/api/v1/subtitles":
			buscas = append(buscas, r.URL.RawQuery)
			if r.URL.Query().Get("tmdb_id") != "77" {
				fmt.Fprint(w, `{"data":[]}`)
				return
			}
			fmt.Fprint(w, `{"data":[
				{"attributes":{"language":"en","download_count":9000,"release":"Age.of.Pyramids.TV.Cut","files":[{"file_id":500}]}},
				{"attributes":{"language":"en","download_count":800,"release":"Age.of.Pyramids.2015.WEB","files":[{"file_id":501}]}},
				{"attributes":{"language":"en","download_count":99999,"machine_translated":true,"release":"Maquina","files":[{"file_id":502}]}},
				{"attributes":{"language":"pt-BR","download_count":5000,"release":"Era das Piramides","files":[{"file_id":503}]}}]}`)
		case "/api/v1/download":
			var corpo struct {
				FileID int64 `json:"file_id"`
			}
			json.NewDecoder(r.Body).Decode(&corpo)
			fmt.Fprintf(w, `{"link":"%s/arquivo/%d.srt","remaining":99}`, srv.URL, corpo.FileID)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &buscas
}

// acervoFalso guarda o que chegou em POST /api/documentarios.
type pedidoAoAcervo struct {
	dados   documentarioParaOAcervo
	campo   string
	arquivo []byte
}

func acervoFalso(t *testing.T) (*httptest.Server, func() []pedidoAoAcervo) {
	var mu sync.Mutex
	var pedidos []pedidoAoAcervo
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/documentarios" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			http.Error(w, `{"erro":"multipart"}`, 400)
			return
		}
		var p pedidoAoAcervo
		json.Unmarshal([]byte(r.FormValue("dados")), &p.dados)
		for _, campo := range []string{"legenda", "audio"} {
			if f, _, err := r.FormFile(campo); err == nil {
				p.campo = campo
				p.arquivo, _ = io.ReadAll(f)
				f.Close()
			}
		}
		mu.Lock()
		pedidos = append(pedidos, p)
		n := len(pedidos)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":"ref%d"}`, n)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []pedidoAoAcervo {
		mu.Lock()
		defer mu.Unlock()
		return append([]pedidoAoAcervo(nil), pedidos...)
	}
}

type respostaDosEnvios struct {
	Disponivel bool            `json:"disponivel"`
	Envios     []envioAoAcervo `json:"envios"`
}

func TestDocumentarioParaOAcervo(t *testing.T) {
	if _, err := exec.LookPath(programaFFmpeg); err != nil {
		t.Skip("ffmpeg não instalado")
	}
	e := novoAmbiente(t)
	t.Setenv("TMDB_URL", tmdbDeDocumentarios(t).URL)
	e.pedir(t, "PUT", "/api/tmdb", map[string]string{"chave": "0123456789abcdef0123456789abcdef"})

	// Sem o Acervo ligado, o botão não aparece e o pedido é recusado.
	var r respostaDosEnvios
	r = respostaDosEnvios{}
	e.json(t, "/api/acervo?tipo=filme&tmdb=77", &r)
	if r.Disponivel {
		t.Fatal("sem ACERVO_URL, não devia estar disponível")
	}
	if s, _ := e.pedir(t, "POST", "/api/acervo", map[string]any{"tipo": "filme", "tmdb": 77}); s != http.StatusConflict {
		t.Fatalf("sem Acervo: %d", s)
	}
	acervo, pedidos := acervoFalso(t)
	t.Setenv("ACERVO_URL", acervo.URL)
	legendas, buscas := openSubtitlesDeDocumentarios(t)
	t.Setenv("OPENSUBTITLES_URL", legendas.URL)
	e.a.guardarAjuste("opensubtitles", chaveDeLegendaFalsa)

	// Só documentário.
	if s, corpo := e.pedir(t, "POST", "/api/acervo", map[string]any{"tipo": "filme", "tmdb": 1396}); s != http.StatusBadRequest || !strings.Contains(corpo, "Só documentários") {
		t.Fatalf("drama: %d %s", s, corpo)
	}

	// O filme: a legenda em inglês do OpenSubtitles, achada pelo código do TMDB. A mais baixada
	// termina cedo demais (outra versão) e a traduzida por máquina fica de fora: vai a segunda.
	if s, corpo := e.pedir(t, "POST", "/api/acervo", map[string]any{"tipo": "filme", "tmdb": 77}); s != http.StatusAccepted || !strings.Contains(corpo, `"na_fila"`) {
		t.Fatalf("mandar o filme: %d %s", s, corpo)
	}
	if res := e.a.enviarProximo(context.Background()); res != enviouUm {
		t.Fatalf("enviar: %v", res)
	}
	ps := pedidos()
	if len(ps) != 1 || ps[0].campo != "legenda" || !strings.Contains(string(ps[0].arquivo), "Line 49 about the pyramids.") {
		t.Fatalf("pedido ao Acervo: %+v", ps)
	}
	d := ps[0].dados
	if d.Chave != "filme/77" || d.Nome != "A Era das Pirâmides" || d.Original != "Age of Pyramids" || d.Idioma != "en" || d.IdiomaDoTexto != "en" ||
		d.Duracao != 120 || d.Direcao != "Ana Diretora" || d.TMDB != "https://www.themoviedb.org/movie/77" || d.Texto != "legenda" || !strings.Contains(d.Legenda, "Age.of.Pyramids.2015.WEB") ||
		!strings.HasSuffix(d.Capa, "/w500/p.jpg") || !strings.HasSuffix(d.Link, "/#/titulo/filme/77") || d.Origem != "opensubtitles" {
		t.Fatalf("dados: %+v", d)
	}
	if len(*buscas) != 1 || (*buscas)[0] != "languages=en&tmdb_id=77" {
		t.Fatalf("busca da legenda (só pelo código, no idioma original): %v", *buscas)
	}
	r = respostaDosEnvios{}
	e.json(t, "/api/acervo?tipo=filme&tmdb=77", &r)
	if !r.Disponivel || len(r.Envios) != 1 || r.Envios[0].Estado != "enviado" || r.Envios[0].AcervoID != "ref1" ||
		!strings.HasSuffix(r.Envios[0].Link, "/#/referencia/ref1") || !strings.HasPrefix(r.Envios[0].Via, "Legenda: OpenSubtitles") {
		t.Fatalf("envios do filme: %+v", r)
	}
	// Mandar de novo o que já foi não refaz.
	e.pedir(t, "POST", "/api/acervo", map[string]any{"tipo": "filme", "tmdb": 77})
	if res := e.a.enviarProximo(context.Background()); res != semEnvios {
		t.Fatal("o filme já enviado voltou para a fila")
	}

	// A série, temporada 1: sem legenda, o áudio original (a faixa em inglês) da assinatura.
	e.provedor.Arquivo = videoComAudiosDe(t, 20, "por", "eng")
	e.a.db.Exec(`INSERT INTO assinatura_tmdb (tipo, id, tmdb) VALUES ('serie', 300, 88)`)
	if s, corpo := e.pedir(t, "POST", "/api/acervo", map[string]any{"tipo": "serie", "tmdb": 88, "alcance": "temporada", "temporada": 1}); s != http.StatusAccepted {
		t.Fatalf("mandar a temporada: %d %s", s, corpo)
	}
	r = respostaDosEnvios{}
	e.json(t, "/api/acervo?tipo=serie&tmdb=88", &r)
	if len(r.Envios) != 2 || r.Envios[0].Nome != "Segredos do Nilo · T1 E1 · A Nascente" || r.Envios[1].Nome != "Segredos do Nilo · T1 E2 · The Delta" {
		t.Fatalf("a temporada (sem o episódio por vir): %+v", r.Envios)
	}
	// Com alguém assistindo, espera a conexão (não falha).
	_, sair, _ := e.a.conexao.entrar(context.Background(), "canal:100")
	if res := e.a.enviarProximo(context.Background()); res != esperarAssinatura {
		t.Fatalf("com a assinatura em uso: %v", res)
	}
	r = respostaDosEnvios{}
	e.json(t, "/api/acervo?tipo=serie&tmdb=88", &r)
	if r.Envios[0].Estado != "na_fila" || !strings.Contains(r.Envios[0].Etapa, "assinatura ficar livre") {
		t.Fatalf("esperando: %+v", r.Envios[0])
	}
	sair()
	for i := 0; i < 2; i++ {
		if res := e.a.enviarProximo(context.Background()); res != enviouUm {
			t.Fatalf("episódio %d: %v", i+1, res)
		}
	}
	ps = pedidos()
	if len(ps) != 3 {
		t.Fatalf("pedidos: %d", len(ps))
	}
	ep1 := ps[1]
	if ep1.campo != "audio" || len(ep1.arquivo) < 10_000 || string(ep1.arquivo[:4]) != "OggS" {
		t.Fatalf("áudio do ep1: %s %d bytes", ep1.campo, len(ep1.arquivo))
	}
	if d := ep1.dados; d.Chave != "serie/88" || d.Temporada != 1 || d.Episodio != 1 || d.Episodios != 3 || d.TituloEpisodio != "A Nascente" ||
		d.SinopseEpisodio != "Onde o Nilo nasce." || d.Duracao != 60 || d.TMDB != "https://www.themoviedb.org/tv/88/season/1/episode/1" ||
		d.Texto != "audio" || d.IdiomaDoTexto != "en" || d.Origem != "assinatura" {
		t.Fatalf("dados do ep1: %+v", d)
	}
	// A faixa certa: a segunda (inglês). O áudio tem a duração do vídeo.
	tmp := filepath.Join(t.TempDir(), "ep1.ogg")
	os.WriteFile(tmp, ep1.arquivo, 0o644)
	saida, _ := exec.Command(programaFFprobe, "-v", "error", "-show_entries", "format=duration:stream=channels", "-of", "default=nw=1", tmp).Output()
	if !strings.Contains(string(saida), "channels=1") || !strings.Contains(string(saida), "duration=19.") && !strings.Contains(string(saida), "duration=20.") {
		t.Fatalf("áudio mono com a duração do vídeo: %s", saida)
	}
	if _, err := os.Stat(e.a.audioPuxado("episodio:301")); err == nil {
		t.Fatal("o áudio puxado para o Acervo devia ser apagado")
	}
	r = respostaDosEnvios{}
	e.json(t, "/api/acervo?tipo=serie&tmdb=88", &r)
	if r.Envios[0].Estado != "enviado" || r.Envios[1].Estado != "enviado" || r.Envios[0].Via != "Áudio: assinatura" {
		t.Fatalf("envios da série: %+v", r.Envios)
	}

	// Refazer pelo áudio o filme que foi pela legenda: vai o áudio da assinatura, sem buscar legenda,
	// e o Acervo troca o texto da mesma Referência.
	e.a.db.Exec(`INSERT INTO assinatura_tmdb (tipo, id, tmdb) VALUES ('filme', 200, 77)`)
	antes := len(*buscas)
	if s, corpo := e.pedir(t, "POST", "/api/acervo", map[string]any{"tipo": "filme", "tmdb": 77, "pelo_audio": true}); s != http.StatusAccepted || !strings.Contains(corpo, `"pelo_audio":true`) {
		t.Fatalf("refazer pelo áudio: %d %s", s, corpo)
	}
	if res := e.a.enviarProximo(context.Background()); res != enviouUm {
		t.Fatalf("refazer: %v", res)
	}
	ps = pedidos()
	if p := ps[len(ps)-1]; p.campo != "audio" || !p.dados.Refazer || p.dados.Chave != "filme/77" || len(*buscas) != antes {
		t.Fatalf("refeito pelo áudio: %s refazer=%v buscas=%d→%d", p.campo, p.dados.Refazer, antes, len(*buscas))
	}
	r = respostaDosEnvios{}
	e.json(t, "/api/acervo?tipo=filme&tmdb=77", &r)
	if r.Envios[0].Estado != "enviado" || r.Envios[0].Via != "Áudio: assinatura" {
		t.Fatalf("filme refeito: %+v", r.Envios[0])
	}

	// Um episódio sem legenda e sem vídeo falha com o que fazer; mandar de novo o põe na fila.
	e.a.db.Exec(`DELETE FROM assinatura_tmdb`)
	e.a.db.Exec(`DELETE FROM envios_ao_acervo WHERE episodio = 2`)
	e.pedir(t, "POST", "/api/acervo", map[string]any{"tipo": "serie", "tmdb": 88, "alcance": "episodio", "temporada": 1, "episodio": 2})
	e.a.enviarProximo(context.Background())
	r = respostaDosEnvios{}
	e.json(t, "/api/acervo?tipo=serie&tmdb=88", &r)
	if r.Envios[1].Estado != "falhou" || !strings.Contains(r.Envios[1].Erro, "abra um torrent deste episódio") {
		t.Fatalf("sem fonte: %+v", r.Envios[1])
	}
	e.pedir(t, "POST", "/api/acervo", map[string]any{"tipo": "serie", "tmdb": 88, "alcance": "episodio", "temporada": 1, "episodio": 2})
	r = respostaDosEnvios{}
	e.json(t, "/api/acervo?tipo=serie&tmdb=88", &r)
	if r.Envios[1].Estado != "na_fila" || r.Envios[1].Erro != "" {
		t.Fatalf("de novo na fila: %+v", r.Envios[1])
	}
	// E sai da fila.
	if s, _ := e.pedir(t, "DELETE", fmt.Sprintf("/api/acervo/%d", r.Envios[1].ID), nil); s != http.StatusNoContent {
		t.Fatalf("cancelar: %d", s)
	}
	if s, _ := e.pedir(t, "DELETE", fmt.Sprintf("/api/acervo/%d", r.Envios[0].ID), nil); s != http.StatusConflict {
		t.Fatalf("o enviado não sai: %d", s)
	}
}

func TestLegendaConfere(t *testing.T) {
	if legendaConfere([]byte(legendaFalsa(30)), 0) {
		t.Error("poucas falas")
	}
	if !legendaConfere([]byte(legendaFalsa(50)), 0) {
		t.Error("sem duração, basta ter falas")
	}
	if !legendaConfere([]byte(legendaFalsa(50)), 120) || legendaConfere([]byte(legendaFalsa(50)), 200) || legendaConfere([]byte(legendaFalsa(50)), 80) {
		t.Error("a última fala tem de cair perto do fim")
	}
}

// A legenda de dentro do arquivo baixado vale só no idioma original.
func TestLegendaDeDentroNoIdioma(t *testing.T) {
	if _, err := exec.LookPath(programaFFmpeg); err != nil {
		t.Skip("ffmpeg não instalado")
	}
	pasta := t.TempDir()
	srt := filepath.Join(pasta, "l.srt")
	os.WriteFile(srt, []byte(legendaFalsa(45)), 0o644)
	video := filepath.Join(pasta, "v.mkv")
	if out, err := exec.Command(programaFFmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=5", "-f", "lavfi", "-i", "sine=frequency=440",
		"-i", srt, "-map", "0", "-map", "1", "-map", "2", "-metadata:s:s:0", "language=eng", "-t", "95", "-c:v", "libx264", "-preset", "ultrafast",
		"-c:a", "aac", "-c:s", "srt", video).CombinedOutput(); err != nil {
		t.Fatalf("gerar vídeo: %v %s", err, out)
	}
	if b := legendaDeDentroNoIdioma(context.Background(), video, "en"); !strings.Contains(string(b), "Line 44 about the pyramids.") {
		t.Fatalf("legenda em inglês: %q", b)
	}
	if b := legendaDeDentroNoIdioma(context.Background(), video, "pt"); b != nil {
		t.Fatal("não há legenda em português")
	}
}

// Já estar no Acervo (409) serve num envio novo, mas não num "refazer": aí o texto não foi trocado.
func TestConflitoDoAcervo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"erro":"Este documentário já está nas Referências.","id":"ref9"}`)
	}))
	defer srv.Close()
	t.Setenv("ACERVO_URL", srv.URL)
	a := &app{http: http.DefaultClient}
	d := documentarioParaOAcervo{Chave: "filme/1", Nome: "X", TMDB: "https://www.themoviedb.org/movie/1", Texto: "legenda"}
	if id, err := a.mandarAoAcervo(context.Background(), d, "legenda", "l.srt", strings.NewReader("x")); err != nil || id != "ref9" {
		t.Fatalf("envio novo: %q %v", id, err)
	}
	d.Refazer = true
	if _, err := a.mandarAoAcervo(context.Background(), d, "audio", "a.ogg", strings.NewReader("x")); err == nil || !strings.Contains(err.Error(), "refaça depois") {
		t.Fatalf("refazer recusado: %v", err)
	}
}
