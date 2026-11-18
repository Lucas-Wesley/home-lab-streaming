package main

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
)

// As legendas reais de calibragem não entram na repo (direitos); estas são inventadas: uma conversa
// lenta e simples e uma rápida, densa e de vocabulário raro.
func legendaInventada(falas int, duracaoDaFala, intervalo float64, texto string) []byte {
	var b []byte
	t := 0.0
	for i := 1; i <= falas; i++ {
		b = append(b, []byte(
			strconv.Itoa(i)+"\n"+tempoDoSRT(t, ",")+" --> "+tempoDoSRT(t+duracaoDaFala, ",")+"\n"+texto+"\n\n")...)
		t += duracaoDaFala + intervalo
	}
	return b
}

func TestMedirIngles(t *testing.T) {
	if posicaoNaFrequencia("you") != 1 || posicaoNaFrequencia("don't") > 100 || posicaoNaFrequencia("zzzqx") != 60000 {
		t.Fatalf("frequência: you %d, don't %d", posicaoNaFrequencia("you"), posicaoNaFrequencia("don't"))
	}
	// Fácil: 6 palavras comuns em 3 s (120 por minuto), fala em 30% do tempo.
	facil, err := medirIngles(legendaInventada(200, 3, 7, "I think we should go home now."))
	if err != nil {
		t.Fatal(err)
	}
	if facil.Dificuldade != "fácil" || facil.Cobertura < 99 || facil.PorMinuto > 145 || facil.Fala > 31 {
		t.Errorf("fácil: %+v", facil)
	}
	// Difícil: palavras raras, 10 em 2,5 s (240 por minuto), quase sem pausa; nomes próprios não contam.
	dificil, err := medirIngles(legendaInventada(200, 2.5, 0.3, "The Marquis said the bayonets and garrottes were in the trenches, Lord Ashby."))
	if err != nil {
		t.Fatal(err)
	}
	if dificil.Dificuldade != "difícil" || dificil.Nota < 7 || dificil.Fala < 85 {
		t.Errorf("difícil: %+v", dificil)
	}
	for _, r := range dificil.Raras {
		if r.Palavra == "ashby" {
			t.Errorf("nome próprio entre as raras: %+v", dificil.Raras)
		}
	}
	if _, err := medirIngles([]byte("1\n00:00:01,000 --> 00:00:02,000\nHi.\n")); err == nil {
		t.Error("legenda curta devia dar erro")
	}
}

func TestRotasDoNivelDeIngles(t *testing.T) {
	e := novoAmbiente(t)
	falso, buscas, _ := openSubtitlesFalso(t)
	t.Setenv("OPENSUBTITLES_URL", falso.URL)
	if status, corpo := e.pedir(t, "PUT", "/api/opensubtitles", map[string]string{"chave": chaveDeLegendaFalsa}); status != 200 {
		t.Fatalf("chave: %d %s", status, corpo)
	}
	// A legenda em inglês (os-7) já baixada: não gasta a cota.
	os.MkdirAll(e.a.pastaDasLegendas(), 0o755)
	os.WriteFile(e.a.arquivoDaLegenda("os-7"), legendaInventada(200, 3, 7, "I think we should go home now."), 0o644)

	var r struct {
		Nivel *nivelGuardado `json:"nivel"`
	}
	e.json(t, "/api/titulos/filme/550/ingles", &r)
	if r.Nivel != nil {
		t.Fatalf("antes de medir: %+v", r.Nivel)
	}
	if status, corpo := e.pedir(t, "POST", "/api/titulos/filme/550/ingles", nil); status != 200 {
		t.Fatalf("medir: %d %s", status, corpo)
	}
	if b := strings.Join(*buscas, " "); !strings.Contains(b, "languages=en") || !strings.Contains(b, "tmdb_id=550") {
		t.Errorf("busca da legenda: %s", b)
	}
	e.json(t, "/api/titulos/filme/550/ingles", &r)
	if r.Nivel == nil || r.Nivel.Legenda != "os-7" || r.Nivel.Medidas.Dificuldade != "fácil" || r.Nivel.IA != nil {
		t.Fatalf("medido: %+v", r.Nivel)
	}
	args := claudeFalso(t, `{"nivel_cefr":"A2","dificuldade":"fácil","nota":2,"motivos":"Frases curtas.","expressoes":[{"expressao":"go home","sentido":"ir para casa"}]}`)
	if status, corpo := e.pedir(t, "POST", "/api/titulos/filme/550/ingles/ia", nil); status != 200 {
		t.Fatalf("analisar: %d %s", status, corpo)
	}
	if a := args(); !strings.Contains(a, "--model\nsonnet") || !strings.Contains(a, "I think we should go home now.") || strings.Contains(a, "WebSearch,WebFetch\n") {
		t.Errorf("pedido ao Claude:\n%s", a)
	}
	e.json(t, "/api/titulos/filme/550/ingles", &r)
	if r.Nivel.IA == nil || r.Nivel.IA.Nivel != "A2" || len(r.Nivel.IA.Expressoes) != 1 {
		t.Fatalf("analisado: %+v", r.Nivel.IA)
	}
	if status, _ := e.pedir(t, "POST", "/api/titulos/filme/551/ingles/ia", nil); status != http.StatusConflict {
		t.Errorf("analisar sem medir: %d", status)
	}
}
