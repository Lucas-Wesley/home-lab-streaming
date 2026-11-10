package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// flashcardsFalso imita a API do app Flashcards: baralhos, criar e apagar nota.
type flashcardsFalso struct {
	mu       sync.Mutex
	baralhos []map[string]string
	notas    map[string]map[string]string
}

func novoFlashcardsFalso(t *testing.T, baralhos ...string) *flashcardsFalso {
	f := &flashcardsFalso{notas: map[string]map[string]string{}}
	for i, n := range baralhos {
		f.baralhos = append(f.baralhos, map[string]string{"id": fmt.Sprint("b", i), "nome": n})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/baralhos", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(f.baralhos)
	})
	mux.HandleFunc("POST /api/baralhos", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		json.NewDecoder(r.Body).Decode(&b)
		f.mu.Lock()
		defer f.mu.Unlock()
		b["id"] = fmt.Sprint("b", len(f.baralhos))
		f.baralhos = append(f.baralhos, b)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(b)
	})
	mux.HandleFunc("POST /api/notas", func(w http.ResponseWriter, r *http.Request) {
		var n map[string]string
		json.NewDecoder(r.Body).Decode(&n)
		f.mu.Lock()
		defer f.mu.Unlock()
		n["id"] = fmt.Sprint("n", len(f.notas))
		f.notas[n["id"]] = n
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(n)
	})
	mux.HandleFunc("DELETE /api/notas/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.notas, r.PathValue("id"))
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("FLASHCARDS_URL", srv.URL)
	return f
}

func TestFalaDaLegendaParaOFlashcards(t *testing.T) {
	e := novoAmbiente(t)
	if status, corpo := e.pedir(t, "POST", "/api/flashcards", map[string]any{"falas": []string{"Hi"}}); status != http.StatusConflict {
		t.Fatalf("sem Flashcards: %d %s", status, corpo)
	}
	f := novoFlashcardsFalso(t, "Bandeiras", "inglês")
	args := claudeFalso(t, `{"ingles":"We're not cooking anymore.","traducao":"A gente não vai mais cozinhar.","nota":"\"Cook\" aqui é gíria para fabricar droga."}`)
	var d map[string]bool
	e.json(t, "/api/flashcards", &d)
	if !d["disponivel"] {
		t.Fatal("devia estar disponível")
	}
	e.pedir(t, "PUT", "/api/progresso/filme:200", map[string]float64{"posicao": 70, "duracao": 5000})
	status, corpo := e.pedir(t, "POST", "/api/flashcards", map[string]any{
		"chave": "filme:200", "falas": []string{"- We're not", "cooking anymore."}, "antes": []string{"Jesse, listen."}, "depois": []string{"What?"},
	})
	if status != http.StatusCreated {
		t.Fatalf("%d %s", status, corpo)
	}
	pedido := args()
	if !strings.Contains(pedido, "- We're not\ncooking anymore.") || !strings.Contains(pedido, "Jesse, listen.") || !strings.Contains(pedido, "Missão Explosiva") {
		t.Fatalf("pedido ao Claude:\n%s", pedido)
	}
	f.mu.Lock()
	n := f.notas["n0"]
	f.mu.Unlock()
	// Vai para o baralho que já existe (o nome comparado sem acento nem maiúscula).
	if n["baralho_id"] != "b1" || n["frente"] != "We're not cooking anymore." || n["verso"] != "A gente não vai mais cozinhar.\n\n\"Cook\" aqui é gíria para fabricar droga." || n["tags"] != "streaming" {
		t.Fatalf("nota: %v", n)
	}
	// Desfazer apaga o cartão.
	if status, _ := e.pedir(t, "DELETE", "/api/flashcards/n0", nil); status != http.StatusNoContent || len(f.notas) != 0 {
		t.Fatalf("desfazer: %d %v", status, f.notas)
	}
}

func TestBaralhoDeInglesCriadoQuandoNaoHa(t *testing.T) {
	e := novoAmbiente(t)
	f := novoFlashcardsFalso(t, "Bandeiras")
	claudeFalso(t, `{"ingles":"Say my name.","traducao":"Diga o meu nome.","nota":""}`)
	if status, corpo := e.pedir(t, "POST", "/api/flashcards", map[string]any{"falas": []string{"Say my name."}}); status != http.StatusCreated {
		t.Fatalf("%d %s", status, corpo)
	}
	if len(f.baralhos) != 2 || f.baralhos[1]["nome"] != "Inglês" || f.notas["n0"]["baralho_id"] != "b1" || f.notas["n0"]["verso"] != "Diga o meu nome." {
		t.Fatalf("baralhos %v, notas %v", f.baralhos, f.notas)
	}
}
