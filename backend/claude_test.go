package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claudeFalso põe o Claude falso no lugar, com a resposta dada; devolve onde ler os argumentos.
func claudeFalso(t *testing.T, resposta string) func() string {
	t.Helper()
	bin, _ := filepath.Abs("testdata/claude-falso")
	args := filepath.Join(t.TempDir(), "args")
	t.Setenv("CLAUDE_BIN", bin)
	t.Setenv("CLAUDE_FALSO_RESPOSTA", resposta)
	t.Setenv("CLAUDE_FALSO_ARGS", args)
	return func() string {
		b, _ := os.ReadFile(args)
		return string(b)
	}
}

// Áudio em inglês, legenda em português 38 s adiantada: o Claude casa pelo sentido e o atraso sai.
func TestSincroniaEntreIdiomasPeloClaude(t *testing.T) {
	e := novoAmbiente(t)
	args := claudeFalso(t, `{"pares":[[2703.9,2665.3],[2706.3,2667.7],[2708.4,2669.9],[2711.0,2672.5],[2717.6,2678.9],[2900.1,2862.0]],"confianca":"alta"}`)
	ouvido := transcricao{Idioma: "en", Falas: []trechoFalado{{Inicio: 2703.9, Texto: "Now wait a minute, Mr. Cooper."}}}
	legenda := []byte("1\n00:44:25,300 --> 00:44:27,000\nAgora espere um minuto, Sr. Cooper.\n\n")
	txt, al := e.a.sincroniaEntreIdiomas(context.Background(), ouvido, legenda, "pt-BR")
	if math.Abs(al.Atraso-38.6) > 0.2 || !strings.Contains(txt, "Claude") {
		t.Fatalf("sincronia pelo Claude: %q %+v", txt, al)
	}
	a := args()
	for _, quer := range []string{"--model\nhaiku", "--json-schema", "Bash,Write", "Now wait a minute", "Agora espere um minuto"} {
		if !strings.Contains(a, quer) {
			t.Fatalf("pedido ao Claude sem %q:\n%s", quer, a)
		}
	}
	if strings.Contains(a, "--allowedTools") {
		t.Fatal("sem imagens, o Claude não precisa ler arquivo nenhum")
	}

	// Confiança baixa: não ajusta.
	claudeFalso(t, `{"pares":[[1,2],[3,4],[5,6],[7,8]],"confianca":"baixa"}`)
	if _, al := e.a.sincroniaEntreIdiomas(context.Background(), ouvido, legenda, "pt-BR"); al.Atraso != 0 {
		t.Fatalf("com confiança baixa não devia ajustar: %+v", al)
	}
}

// O parecer sobre uma opção em dúvida: o Claude lê só a pasta dos quadros, e a resposta vira o
// estado e a explicação da verificação.
func TestParecerDoClaude(t *testing.T) {
	e := novoAmbiente(t)
	args := claudeFalso(t, `{"veredito":"errado","explicacao":"Não é o filme: é uma produção independente com o mesmo nome."}`)
	v := verificacao{Estado: "suspeito", Resumo: "1 h 26 min", Arquivo: "The.Odyssey.2026.mp4", Quadros: []string{"abc-0-0.jpg"}}
	t2 := tituloDoTMDB{Tipo: "filme", Titulo: "A Odisseia", Original: "The Odyssey", Ano: "2026", Duracao: 172, DigitalEm: "2026-11-17"}
	veredito, explicacao, err := e.a.parecerDoClaude(context.Background(), v, t2, "The Odyssey (2026) [1080p] [WEBRip]", "I have a home somewhere.")
	if err != nil || veredito != "errado" || !strings.Contains(explicacao, "produção independente") {
		t.Fatalf("parecer: %q %q %v", veredito, explicacao, err)
	}
	a := args()
	for _, quer := range []string{"--allowedTools\nRead", "--add-dir\n" + e.a.pastaDosQuadros(), "abc-0-0.jpg", "A Odisseia", "172 min", "2026-11-17", "I have a home somewhere"} {
		if !strings.Contains(a, quer) {
			t.Fatalf("pedido ao Claude sem %q:\n%s", quer, a)
		}
	}
}

func TestSemClaude(t *testing.T) {
	e := novoAmbiente(t) // CLAUDE_BIN aponta para um programa que não existe
	if claudeDisponivel() {
		t.Fatal("os testes não podem usar o Claude de verdade")
	}
	if _, _, err := e.a.parecerDoClaude(context.Background(), verificacao{}, tituloDoTMDB{}, "", ""); err == nil {
		t.Fatal("sem Claude devia dar erro (e a verificação segue sem parecer)")
	}
}
