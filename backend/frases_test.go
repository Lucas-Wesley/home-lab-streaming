package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

func TestFrasesDaLegenda(t *testing.T) {
	srt := []byte("1\n00:00:01,000 --> 00:00:02,000\n<i>You hit the jackpot,</i>\n\n" +
		"2\n00:00:02,200 --> 00:00:03,500\nthen, my friend.\n\n" +
		"3\n00:00:04,000 --> 00:00:05,000\n- Hi.\n- Hey, how are you?\n\n" +
		"4\n00:00:06,000 --> 00:00:07,000\nJOE: I was thinking that maybe...\n\n" +
		"5\n00:00:09,000 --> 00:00:10,000\nwe could go out tonight.\n\n" + // pausa longa: a de cima fica sem fim
		"6\n00:00:11,000 --> 00:00:12,500\n[door closes] Can we please just paint this room already?\n\n" +
		"7\n00:00:13,000 --> 00:00:14,000\n♪ la la la ♪\n\n")
	var textos []string
	for _, f := range frasesDaLegenda(srt) {
		textos = append(textos, fmt.Sprintf("%s [%.1f-%.1f]", f.Texto, f.Inicio, f.Fim))
	}
	want := "You hit the jackpot, then, my friend. [1.0-3.5] | Can we please just paint this room already? [11.0-12.5]"
	if got := strings.Join(textos, " | "); got != want {
		t.Errorf("frases:\n%s\nquer:\n%s", got, want)
	}
}

func TestPontuarFrases(t *testing.T) {
	frases := []fraseDoFilme{
		{Texto: "I could've seen that coming a mile away."},
		{Texto: "Can we please just paint this room already?"},
		{Texto: "I really love Angela, you know that."},      // nome do filme
		{Texto: "We told Angela about the dinner party."},    // (de novo, com maiúscula no meio)
		{Texto: "Yes, I am."},                                // curta
		{Texto: "I'm going to shoot up the price tomorrow."}, // parecida com um cartão que já existe
		{Texto: "The quixotic zeitgeist befuddled everyone there."},
		{Texto: "Can we please just paint the room already?"}, // parecida com outra do filme
	}
	for i := range frases {
		frases[i].Palavras = len(palavrasDaFrase(frases[i].Texto))
	}
	boas := pontuarFrases(frases, []string{"I'm going to shoot up the price."})
	var textos []string
	for _, f := range boas {
		textos = append(textos, f.Texto)
	}
	sort.Strings(textos)
	got := strings.Join(textos, " | ")
	if got != "Can we please just paint this room already? | I could've seen that coming a mile away." {
		t.Fatalf("boas: %s", got)
	}
	if boas[0].Nota <= 0 || len(boas[0].Desconhecidas) > 2 {
		t.Errorf("nota: %+v", boas[0])
	}
}

func TestEtiquetaDoTitulo(t *testing.T) {
	if got := etiquetaDoTitulo("O Convite: A Noite!"); got != "o-convite-a-noite" {
		t.Errorf("etiqueta: %q", got)
	}
}

// legendaDasFrasesDeTeste: falas boas nos primeiros 20 s (o vídeo de teste tem 20 s) e música até o fim.
func legendaDasFrasesDeTeste() string {
	falas := []string{
		"I could've seen that coming a mile away.",
		"- Hi, Joe.\n- Hey.",
		"You hit the jackpot,",
		"then, my friend.",
		"Yes, I am.",
		"Can we please just paint this room already?",
		"I'm going to shoot up the price.",
	}
	var s strings.Builder
	for i, f := range falas {
		ini := 1 + 2*i
		fmt.Fprintf(&s, "%d\n00:00:%02d,000 --> 00:00:%02d,800\n%s\n\n", i+1, ini, ini+1, f)
	}
	for i := 0; i < 40; i++ {
		ini := 21 + 2*i
		fmt.Fprintf(&s, "%d\n00:%02d:%02d,000 --> 00:%02d:%02d,500\n♪ music ♪\n\n", len(falas)+i+1, ini/60, ini%60, ini/60, ini%60+1)
	}
	return s.String()
}

func openSubtitlesDasFrases(t *testing.T) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/arquivo/700.srt":
			fmt.Fprint(w, legendaDasFrasesDeTeste())
		case "/api/v1/infos/languages":
			fmt.Fprint(w, `{"data":[]}`)
		case "/api/v1/subtitles":
			fmt.Fprint(w, `{"data":[{"attributes":{"language":"en","download_count":10,"release":"Nile.S01E01","files":[{"file_id":700}]}}]}`)
		case "/api/v1/download":
			fmt.Fprintf(w, `{"link":"%s/arquivo/700.srt","remaining":99}`, srv.URL)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFlashcardsDeUmTitulo(t *testing.T) {
	e := novoAmbiente(t)
	if s, _ := e.pedir(t, "POST", "/api/titulos/serie/88/flashcards", map[string]any{}); s != http.StatusConflict {
		t.Fatalf("sem o Flashcards: %d", s)
	}
	t.Setenv("TMDB_URL", tmdbDeDocumentarios(t).URL)
	e.pedir(t, "PUT", "/api/tmdb", map[string]string{"chave": "0123456789abcdef0123456789abcdef"})
	t.Setenv("OPENSUBTITLES_URL", openSubtitlesDasFrases(t).URL)
	e.a.guardarAjuste("opensubtitles", chaveDeLegendaFalsa)
	f := novoFlashcardsFalso(t, "Inglês")
	f.baralhos[0]["tema"] = "Inglês"
	f.notas["n0"] = map[string]string{"id": "n0", "baralho_id": "b0", "frente": "I'm going to shoot up the price."}
	args := claudeFalso(t, `{"frases":[{"numero":2,"traducao":"Você tirou a sorte grande, meu amigo.","expressao":"hit the jackpot","explicacao":"ter muita sorte."},`+
		`{"numero":99,"traducao":"fora da lista","expressao":"","explicacao":""},`+
		`{"numero":1,"traducao":"Eu já via isso de longe.","expressao":"","explicacao":""},`+
		`{"numero":3,"traducao":"Dá pra pintar logo esse quarto?","expressao":"already","explicacao":"impaciência."}],"observacao":"Só as do dia a dia."}`)
	e.provedor.Arquivo = videoComAudiosDe(t, 20, "por", "eng")
	e.a.db.Exec(`INSERT INTO assinatura_tmdb (tipo, id, tmdb) VALUES ('serie', 300, 88)`)

	if s, corpo := e.pedir(t, "POST", "/api/titulos/serie/88/flashcards", map[string]any{"limite": 500}); s != http.StatusBadRequest {
		t.Fatalf("limite grande demais: %d %s", s, corpo)
	}
	s, corpo := e.pedir(t, "POST", "/api/titulos/serie/88/flashcards", map[string]any{"limite": 2, "temporada": 1, "episodio": 1})
	if s != http.StatusOK || !strings.Contains(corpo, `"na_fila"`) {
		t.Fatalf("pedir: %d %s", s, corpo)
	}
	if s, _ := e.pedir(t, "POST", "/api/titulos/serie/88/flashcards", map[string]any{"limite": 2}); s != http.StatusConflict {
		t.Errorf("pedir de novo na fila: %d", s)
	}

	// Fase 1: as frases pela legenda, o Claude escolhe, a proposta vai ao Flashcards.
	if fez, _ := e.a.proximasFrases(context.Background()); !fez {
		t.Fatal("não fez a fase 1")
	}
	var r struct {
		Disponivel bool            `json:"disponivel"`
		Pedido     *pedidoDeFrases `json:"pedido"`
	}
	e.json(t, "/api/titulos/serie/88/flashcards?temporada=1&episodio=1", &r)
	if r.Pedido == nil || r.Pedido.Estado != "enviado" || r.Pedido.Frases != 2 || r.Pedido.Audio != "esperando" || r.Pedido.Geracao != 41 ||
		!strings.HasSuffix(r.Pedido.Link, "/#/geracao/41") || r.Pedido.Nome != "Segredos do Nilo · T1 E1" {
		t.Fatalf("pedido depois da fase 1: %+v (erro %q)", r.Pedido, func() string {
			if r.Pedido != nil {
				return r.Pedido.Erro
			}
			return ""
		}())
	}
	pedido := args()
	for _, quer := range []string{"--model\nsonnet", "Escolha até 2 frases", "1. I could've seen that coming a mile away.", "You hit the jackpot, then, my friend."} {
		if !strings.Contains(pedido, quer) {
			t.Errorf("o pedido ao Claude não tem %q:\n%s", quer, pedido)
		}
	}
	for _, nao := range []string{"shoot up", "Yes, I am", "Hi, Joe"} {
		if strings.Contains(pedido, nao) {
			t.Errorf("o pedido ao Claude tem %q", nao)
		}
	}
	if len(f.propostas) != 1 {
		t.Fatalf("propostas: %d", len(f.propostas))
	}
	p := f.propostas[0]
	if len(f.baralhos) != 2 || f.baralhos[1]["nome"] != "Inglês · Filmes e séries" || f.baralhos[1]["tema"] != "Inglês" || p["baralho_id"] != "b1" ||
		p["origem"] != "streaming" || p["titulo"] != "Frases de Segredos do Nilo · T1 E1" || !strings.HasSuffix(p["link"].(string), "/#/titulo/serie/88") {
		t.Fatalf("proposta: %+v; baralhos %+v", p, f.baralhos)
	}
	b, _ := json.Marshal(p["cartoes"])
	var cartoes []map[string]any
	json.Unmarshal(b, &cartoes)
	if len(cartoes) != 2 || cartoes[0]["frente"] != "You hit the jackpot, then, my friend." ||
		cartoes[0]["verso"] != "Você tirou a sorte grande, meu amigo.\n\nhit the jackpot: ter muita sorte." || cartoes[1]["verso"] != "Eu já via isso de longe." ||
		cartoes[0]["tags"] != "streaming segredos-do-nilo" || cartoes[0]["audio_pendente"] != true || !strings.Contains(cartoes[0]["fonte"].(string), "/#/titulo/serie/88?t=1&e=1&fala=5.0") {
		t.Fatalf("cartões: %s", b)
	}

	// Fase 2: com alguém assistindo pela assinatura, espera.
	_, sair, _ := e.a.conexao.entrar(context.Background(), "canal:100")
	if fez, esperar := e.a.proximasFrases(context.Background()); fez || !esperar {
		t.Fatalf("com a assinatura em uso: fez %v, esperar %v", fez, esperar)
	}
	e.json(t, "/api/titulos/serie/88/flashcards", &r)
	if r.Pedido.Audio != "esperando" || !strings.Contains(r.Pedido.AudioMsg, "conexão") {
		t.Fatalf("esperando a conexão: %+v", r.Pedido)
	}
	sair()
	if fez, _ := e.a.proximasFrases(context.Background()); !fez {
		t.Fatal("não cortou os áudios")
	}
	e.json(t, "/api/titulos/serie/88/flashcards", &r)
	if r.Pedido.Audio != "pronto" || r.Pedido.ComAudio != 2 {
		t.Fatalf("áudios: %+v", r.Pedido)
	}
	if len(f.audiosDaProposta) != 2 || f.audiosDaProposta[0]["geracao"] != "41" || f.audiosDaProposta[0]["audio"] == "" ||
		f.audiosDaProposta[0]["fonte"] != cartoes[0]["fonte"] {
		t.Fatalf("áudios no Flashcards: %+v", f.audiosDaProposta)
	}
	for nome, mp3 := range f.audios {
		if len(mp3) < 1000 {
			t.Errorf("áudio %s vazio", nome)
		}
	}
	// Gerar os áudios de novo: nada falta, fica pronto sem cortar.
	if s, corpo := e.pedir(t, "POST", "/api/titulos/serie/88/flashcards/audios", map[string]any{"temporada": 1, "episodio": 1}); s != http.StatusOK {
		t.Fatalf("refazer áudios: %d %s", s, corpo)
	}
	e.a.proximasFrases(context.Background())
	e.json(t, "/api/titulos/serie/88/flashcards", &r)
	if r.Pedido.Audio != "pronto" || len(f.audiosDaProposta) != 2 {
		t.Errorf("refazer sem faltar: %+v, %d áudios", r.Pedido, len(f.audiosDaProposta))
	}
}

// Sem vídeo para cortar: as frases deixam de esperar o áudio no Flashcards.
func TestFrasesSemVideo(t *testing.T) {
	e := novoAmbiente(t)
	t.Setenv("TMDB_URL", tmdbDeDocumentarios(t).URL)
	e.pedir(t, "PUT", "/api/tmdb", map[string]string{"chave": "0123456789abcdef0123456789abcdef"})
	t.Setenv("OPENSUBTITLES_URL", openSubtitlesDasFrases(t).URL)
	e.a.guardarAjuste("opensubtitles", chaveDeLegendaFalsa)
	f := novoFlashcardsFalso(t)
	claudeFalso(t, `{"frases":[{"numero":1,"traducao":"Eu já via isso de longe.","expressao":"","explicacao":""}],"observacao":""}`)
	e.pedir(t, "POST", "/api/titulos/serie/88/flashcards", map[string]any{})
	e.a.proximasFrases(context.Background())
	e.a.proximasFrases(context.Background())
	var r struct {
		Pedido *pedidoDeFrases `json:"pedido"`
	}
	e.json(t, "/api/titulos/serie/88/flashcards", &r)
	if r.Pedido == nil || r.Pedido.Estado != "enviado" || r.Pedido.Audio != "sem_video" || !strings.Contains(r.Pedido.AudioMsg, "abra um torrent") {
		t.Fatalf("sem vídeo: %+v", r.Pedido)
	}

	// Um filme não leva temporada e episódio no nome.
	e.pedir(t, "POST", "/api/titulos/filme/77/flashcards", map[string]any{})
	e.a.proximasFrases(context.Background())
	e.json(t, "/api/titulos/filme/77/flashcards", &r)
	if r.Pedido == nil || r.Pedido.Nome != "A Era das Pirâmides" {
		t.Errorf("nome do filme: %+v", r.Pedido)
	}
	if len(f.audiosDaProposta) != 1 || f.audiosDaProposta[0]["audio"] != "" {
		t.Errorf("as frases ainda esperam o áudio: %+v", f.audiosDaProposta)
	}
}
