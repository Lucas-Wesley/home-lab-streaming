package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// parakeetFalso responde como o serviço do homelab: uma palavra a cada 0,5 s do trecho recebido
// ("p5", "p10"… pelo décimo de segundo dentro do trecho), com ponto final a cada 5. Guarda os modelos pedidos.
func parakeetFalso(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var modelos []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			http.NotFound(w, r)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "sem arquivo", 400)
			return
		}
		var cab [44]byte
		f.Read(cab[:])
		resto := 0
		for {
			buf := make([]byte, 32<<10)
			n, err := f.Read(buf)
			resto += n
			if err != nil {
				break
			}
		}
		duracao := float64(resto) / 32000 // 16 kHz, 16 bits, mono
		mu.Lock()
		modelos = append(modelos, r.FormValue("model"))
		mu.Unlock()
		type palavra struct {
			Word  string  `json:"word"`
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		}
		var ps []palavra
		for s, i := 0.5, 0; s < duracao-0.5; s, i = s+0.5, i+1 {
			txt := fmt.Sprintf("p%d", int(s*10+0.5))
			if i%5 == 4 {
				txt += "."
			}
			ps = append(ps, palavra{txt, s, s + 0.4})
		}
		json.NewEncoder(w).Encode(map[string]any{"text": "", "words": ps})
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), modelos...)
	}
}

func lerGerada(t *testing.T, e *ambiente, chave string) string {
	t.Helper()
	b, err := os.ReadFile(e.a.arquivoDaLegenda(idDaGerada(chave)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Gerar a legenda de um vídeo de torrent: começa onde o Lucas parou, ouve até o fim e volta para
// o começo; cada minuto entra na legenda assim que fica pronto, com os tempos do vídeo.
func TestGerarLegendaDeTorrent(t *testing.T) {
	e := novoAmbiente(t)
	parakeet, modelos := parakeetFalso(t)
	t.Setenv("PARAKEET_URL", parakeet.URL)
	mi := torrentDeVideoCom(t, "")
	hash := enviarFicha(t, e, mi)
	var d detalheDoTorrent
	e.json(t, "/api/torrents/"+hash, &d)
	chave := fmt.Sprintf("arquivo:%d", d.Arquivos[0].ID)
	// O arquivo diz que o áudio é inglês (sem Whisper para reconhecer).
	e.a.db.Exec(`UPDATE torrent_arquivos SET sondagem = '{"duracao":120,"audios":[{"codec":"aac","idioma":"eng"}]}' WHERE id = ?`, d.Arquivos[0].ID)
	e.pedir(t, "PUT", "/api/progresso/"+chave, map[string]float64{"posicao": 70, "duracao": 120})

	status, corpo := e.pedir(t, "POST", "/api/legendas/"+chave+"/gerar", nil)
	if status != 200 || !strings.Contains(corpo, idDaGerada(chave)) {
		t.Fatalf("gerar: %d %s", status, corpo)
	}
	if e.a.proximoBlocoDe(t, chave) != 1 {
		t.Fatal("devia começar pelo minuto em que o Lucas está")
	}
	e.a.gerarPendentes(context.Background())

	var r struct {
		Escolhida *legendaEscolhida `json:"escolhida"`
	}
	e.json(t, "/api/legendas/"+chave, &r)
	l := r.Escolhida
	if l == nil || l.ID != idDaGerada(chave) || l.Idioma != "en" || l.Geracao == nil || l.Geracao.Estado != "pronta" || l.Geracao.Feitos != 2 || l.Geracao.Ate != 120 {
		t.Fatalf("escolhida: %+v %+v", l, l.Geracao)
	}
	if m := modelos(); len(m) != 2 || m[0] != "parakeet-tdt-0.6b-v2" {
		t.Fatalf("inglês vai para o v2: %v", m)
	}
	srt := lerGerada(t, e, chave)
	// O minuto 2 (60 a 120 s): a primeira palavra em 60,5 s; as da sobra de 3 s do minuto 1 não
	// se repetem.
	if !strings.Contains(srt, "00:01:00,500 --> ") || strings.Count(srt, "\np5 ") != 2 || !strings.HasPrefix(srt, "1\n00:00:00,500 --> ") {
		t.Fatalf("legenda gerada:\n%s", srt)
	}
	// O player recebe em WebVTT, sem cache (a legenda cresce).
	resp, err := http.Get(e.api.URL + "/api/legenda/" + idDaGerada(chave) + ".vtt")
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("vtt: %v %v", err, resp)
	}
	resp.Body.Close()

	// Escolher outra legenda (ou tirar) não apaga o que foi gerado; gerar de novo continua pronta.
	e.pedir(t, "DELETE", "/api/legendas/"+chave, nil)
	e.pedir(t, "POST", "/api/legendas/"+chave+"/gerar", nil)
	e.a.gerarPendentes(context.Background())
	if len(modelos()) != 2 {
		t.Fatal("não devia ouvir de novo o que já foi ouvido")
	}
}

func (a *app) proximoBlocoDe(t *testing.T, chave string) int {
	t.Helper()
	p := a.progressoDe(context.Background(), chave)
	return proximoBloco(a.blocosFeitos(context.Background(), chave), 2, p.Posicao)
}

// Na assinatura, o servidor puxa só o áudio com a conexão livre e gera a partir dele. Com alguém
// assistindo outro vídeo, espera.
func TestGerarLegendaDaAssinatura(t *testing.T) {
	e := novoAmbiente(t)
	parakeet, modelos := parakeetFalso(t)
	t.Setenv("PARAKEET_URL", parakeet.URL)
	e.provedor.Arquivo = videoComAudiosDe(t, 70, "por", "eng")

	_, sair, _ := e.a.conexao.entrar(context.Background(), "canal:100")
	e.pedir(t, "POST", "/api/legendas/filme:200/gerar", map[string]int{"faixa": 0})
	if esperando := e.a.gerarPendentes(context.Background()); !esperando {
		t.Fatal("com a assinatura em uso, devia esperar")
	}
	var r struct {
		Escolhida *legendaEscolhida `json:"escolhida"`
	}
	e.json(t, "/api/legendas/filme:200", &r)
	if g := r.Escolhida.Geracao; g == nil || g.Estado != "esperando" || !strings.Contains(g.Mensagem, "em uso") {
		t.Fatalf("esperando: %+v", g)
	}
	sair()

	e.a.gerarPendentes(context.Background())
	e.json(t, "/api/legendas/filme:200", &r)
	if g := r.Escolhida.Geracao; g == nil || g.Estado != "pronta" || g.Feitos != 2 || r.Escolhida.Idioma != "pt" {
		t.Fatalf("gerada: %+v", g)
	}
	if m := modelos(); len(m) != 2 || m[0] != "parakeet-tdt-0.6b-v3" {
		t.Fatalf("português vai para o v3: %v", m)
	}
	if _, err := os.Stat(e.a.audioPuxado("filme:200")); err == nil {
		t.Fatal("o áudio puxado devia ser apagado no fim")
	}
	if srt := lerGerada(t, e, "filme:200"); !strings.Contains(srt, "00:01:00,500") {
		t.Fatalf("legenda:\n%s", srt)
	}
	if strings.Contains(r.Escolhida.Geracao.Mensagem, "senha") {
		t.Fatal("mensagem com segredo")
	}
}

// Double check: com folga, o Whisper ouve de novo e o Claude corrige o texto, sem mexer nos tempos.
func TestRevisaoDaLegendaGerada(t *testing.T) {
	e := novoAmbiente(t)
	parakeet, _ := parakeetFalso(t)
	t.Setenv("PARAKEET_URL", parakeet.URL)
	whisper, chamadas := whisperFalso(t, legendaDeTeste(0), "english")
	t.Setenv("WHISPER_URL", whisper.URL)
	mi := torrentDeVideoCom(t, "")
	hash := enviarFicha(t, e, mi)
	var d detalheDoTorrent
	e.json(t, "/api/torrents/"+hash, &d)
	chave := fmt.Sprintf("arquivo:%d", d.Arquivos[0].ID)
	e.a.db.Exec(`UPDATE torrent_arquivos SET sondagem = '{"duracao":120,"audios":[{"codec":"aac","idioma":"eng"}]}' WHERE id = ?`, d.Arquivos[0].ID)

	// O Claude devolve o mesmo número de linhas, corrigidas: 23 por minuto (frases de 5 palavras;
	// as 2 primeiras, curtas, numa linha só). A revisão começa 2 min à frente de quem assiste: num
	// vídeo de 2 min, um pedido para o último minuto e outro para o primeiro.
	linhas := make([]string, 23)
	for i := range linhas {
		linhas[i] = fmt.Sprintf("Linha corrigida %d.", i+1)
	}
	resposta, _ := json.Marshal(map[string]any{"linhas": linhas})
	args := claudeFalso(t, string(resposta))
	e.pedir(t, "POST", "/api/legendas/"+chave+"/gerar", nil)
	e.a.gerarPendentes(context.Background())

	srt := lerGerada(t, e, chave)
	if !strings.Contains(srt, "Linha corrigida 1.") || !strings.Contains(srt, "Linha corrigida 23.") || !strings.Contains(srt, "00:01:00,500 --> ") {
		t.Fatalf("revisada:\n%s", srt)
	}
	if *chamadas < 2 || !strings.Contains(args(), "Devolva exatamente 23 linhas") {
		t.Fatalf("revisão: %d chamadas ao Whisper; pedido:\n%s", *chamadas, args())
	}
	var r struct {
		Escolhida *legendaEscolhida `json:"escolhida"`
	}
	e.json(t, "/api/legendas/"+chave, &r)
	if g := r.Escolhida.Geracao; g.Revisados != 2 {
		t.Fatalf("revisados: %+v", g)
	}

	// Resposta com outro número de linhas: fica o texto do Parakeet.
	e.a.apagarGerada(context.Background(), chave)
	claudeFalso(t, `{"linhas":["uma só"]}`)
	e.pedir(t, "POST", "/api/legendas/"+chave+"/gerar", nil)
	e.a.gerarPendentes(context.Background())
	if srt := lerGerada(t, e, chave); strings.Contains(srt, "uma só") || !strings.Contains(srt, "p5") {
		t.Fatalf("revisão inválida devia ser ignorada:\n%s", srt)
	}
}

func TestFalasDasPalavras(t *testing.T) {
	ps := []palavraOuvida{
		{"How", 0.2, 0.4}, {"much", 0.4, 0.6}, {"money", 0.6, 0.8}, {"is", 0.8, 1.0}, {"that?", 1.0, 1.2}, {"It's", 1.5, 1.7}, {"about", 1.7, 1.9},
		{"seven", 1.9, 2.1}, {"hundred", 2.1, 2.3}, {"grand.", 2.3, 2.9},
		{"Later", 5.0, 5.4}, // pausa longa: outra linha
	}
	falas := falasDasPalavras(ps)
	if len(falas) != 3 || falas[0].Texto != "How much money is that?" || falas[1].Texto != "It's about seven hundred grand." || falas[2].Inicio != 5.0 {
		t.Fatalf("falas: %+v", falas)
	}
	srt := srtDasFalas(falas)
	if !strings.HasPrefix(srt, "1\n00:00:00,200 --> 00:00:01,200\nHow much money is that?\n\n2\n") {
		t.Fatalf("srt:\n%s", srt)
	}
	if got := emDuasLinhas("Well, just say the word and I'll take you on a ride along."); got != "Well, just say the word and\nI'll take you on a ride along." {
		t.Fatalf("duas linhas: %q", got)
	}
	// Começa onde o Lucas está; o que falta antes fica para o fim.
	feitos := map[int]bool{3: true, 4: true}
	if b := proximoBloco(feitos, 6, 200); b != 5 {
		t.Fatalf("próximo: %d", b)
	}
	if b := proximoBloco(map[int]bool{3: true, 4: true, 5: true}, 6, 200); b != 0 {
		t.Fatalf("depois do fim, volta ao começo: %d", b)
	}
	if c := coberturaAPartirDe(feitos, 6, 200, 350); c != 300 {
		t.Fatalf("cobertura: %v", c)
	}
}

// Na TV, a legenda só entra junto com o vídeo: quando a que ela tem está acabando e já há mais
// pronta, o servidor manda o vídeo de novo, do mesmo ponto. E a TV pode parar enquanto o áudio da
// assinatura é puxado, voltando depois do mesmo ponto.
func TestLegendaGeradaNaTela(t *testing.T) {
	e := novoAmbiente(t)
	tv := novaTVFalsa(t)
	te := registrarTV(t, e, tv)
	ctx := context.Background()
	chave := "filme:200"
	e.a.db.Exec(`INSERT INTO legendas_geradas (chave, idioma, duracao, estado, criado_em) VALUES (?, 'en', 600, 'gerando', '2026-11-10T00:00:00Z')`, chave)
	for b := 0; b < 3; b++ {
		e.a.db.Exec(`INSERT INTO blocos_gerados (chave, bloco, palavras) VALUES (?, ?, '[]')`, chave, b)
	}
	if err := e.a.escolherGerada(ctx, chave); err != nil {
		t.Fatal(err)
	}
	s, err := e.a.telas.tocar(ctx, te, "filme", 200, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.legendaAte != 180 || !strings.Contains(tv.corpo("SetAVTransportURI"), "gerada-filme-200.srt") {
		t.Fatalf("sessão: %+v\n%s", s, tv.corpo("SetAVTransportURI"))
	}
	envios := func() int {
		tv.mu.Lock()
		defer tv.mu.Unlock()
		n := 0
		for _, a := range tv.acoes {
			if a == "SetAVTransportURI" {
				n++
			}
		}
		return n
	}
	// Longe do fim do que a TV tem: nada.
	e.a.telas.atualizarLegendaGerada(te, s, 60)
	if envios() != 1 {
		t.Fatal("não devia mandar de novo ainda")
	}
	// Perto do fim, sem legenda nova: nada. Com mais 4 minutos prontos: manda de novo.
	e.a.telas.atualizarLegendaGerada(te, s, 150)
	if envios() != 1 {
		t.Fatal("sem legenda nova, não adianta mandar de novo")
	}
	for b := 3; b < 7; b++ {
		e.a.db.Exec(`INSERT INTO blocos_gerados (chave, bloco, palavras) VALUES (?, ?, '[]')`, chave, b)
	}
	e.a.telas.atualizarLegendaGerada(te, s, 150)
	if envios() != 2 {
		t.Fatal("devia mandar de novo com a legenda maior")
	}

	// Parar para puxar o áudio e voltar do mesmo ponto.
	tv.mu.Lock()
	tv.posicao = "0:05:00"
	tv.mu.Unlock()
	voltar, parou := e.a.telas.pararParaOAudio(ctx, chave)
	if !parou || !tv.recebeu("Stop") {
		t.Fatal("a TV devia parar")
	}
	voltar()
	if envios() != 3 {
		t.Fatal("a TV devia voltar")
	}
	e.a.telas.mu.Lock()
	volta := e.a.telas.sessoes[te.ID]
	e.a.telas.mu.Unlock()
	if volta.Inicio != 300 {
		t.Fatalf("devia voltar dos 5 min: %+v", volta)
	}
}
