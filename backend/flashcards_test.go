package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// flashcardsFalso imita a API do app Flashcards: baralhos e criar nota.
type flashcardsFalso struct {
	mu       sync.Mutex
	baralhos []map[string]string
	notas    map[string]map[string]string
	audios   map[string][]byte
}

func novoFlashcardsFalso(t *testing.T, baralhos ...string) *flashcardsFalso {
	f := &flashcardsFalso{notas: map[string]map[string]string{}, audios: map[string][]byte{}}
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
	mux.HandleFunc("POST /api/audios", func(w http.ResponseWriter, r *http.Request) {
		arq, _, err := r.FormFile("arquivo")
		if err != nil {
			http.Error(w, `{"erro":"sem arquivo"}`, http.StatusBadRequest)
			return
		}
		b, _ := io.ReadAll(arq)
		f.mu.Lock()
		defer f.mu.Unlock()
		nome := fmt.Sprintf("%032d.mp3", len(f.audios))
		f.audios[nome] = b
		json.NewEncoder(w).Encode(map[string]string{"nome": nome})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("FLASHCARDS_URL", srv.URL)
	return f
}

// nota espera o cartão feito em segundo plano.
func (f *flashcardsFalso) nota(t *testing.T, id string) map[string]string {
	t.Helper()
	for i := 0; i < 100; i++ {
		f.mu.Lock()
		n := f.notas[id]
		f.mu.Unlock()
		if n != nil {
			return n
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("a nota %s não chegou ao Flashcards", id)
	return nil
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
	// Responde na hora; o cartão fica pronto depois.
	if status != http.StatusAccepted {
		t.Fatalf("%d %s", status, corpo)
	}
	n := f.nota(t, "n0")
	pedido := args()
	if !strings.Contains(pedido, "- We're not\ncooking anymore.") || !strings.Contains(pedido, "Jesse, listen.") || !strings.Contains(pedido, "Missão Explosiva") {
		t.Fatalf("pedido ao Claude:\n%s", pedido)
	}
	// Vai para o baralho que já existe (o nome comparado sem acento nem maiúscula).
	if n["baralho_id"] != "b1" || n["frente"] != "We're not cooking anymore." || n["verso"] != "A gente não vai mais cozinhar.\n\n\"Cook\" aqui é gíria para fabricar droga." || n["tags"] != "streaming" {
		t.Fatalf("nota: %v", n)
	}
}

func TestBaralhoDeInglesCriadoQuandoNaoHa(t *testing.T) {
	e := novoAmbiente(t)
	f := novoFlashcardsFalso(t, "Bandeiras")
	claudeFalso(t, `{"ingles":"Say my name.","traducao":"Diga o meu nome.","nota":""}`)
	if status, corpo := e.pedir(t, "POST", "/api/flashcards", map[string]any{"falas": []string{"Say my name."}}); status != http.StatusAccepted {
		t.Fatalf("%d %s", status, corpo)
	}
	n := f.nota(t, "n0")
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.baralhos) != 2 || f.baralhos[1]["nome"] != "Inglês" || n["baralho_id"] != "b1" || n["verso"] != "Diga o meu nome." {
		t.Fatalf("baralhos %v, notas %v", f.baralhos, f.notas)
	}
}

// "Com áudio" (S39): o trecho da fala sai do vídeo do torrent em MP3 e vai junto com a nota.
func TestFalaComAudio(t *testing.T) {
	e := novoAmbiente(t)
	mi := torrentDeVideoCom(t, "")
	hash := enviarFicha(t, e, mi)
	var d detalheDoTorrent
	e.json(t, "/api/torrents/"+hash, &d)
	id := d.Arquivos[0].ID
	e.pedir(t, "POST", fmt.Sprintf("/api/arquivos/%d/baixar", id), nil)
	for range 600 {
		if e.json(t, "/api/torrents/"+hash, &d); d.Arquivos[0].Download == "baixado" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	f := novoFlashcardsFalso(t, "Inglês")
	claudeFalso(t, `{"ingles":"Keep the change.","traducao":"Fica com o troco.","nota":""}`)
	chave := fmt.Sprintf("arquivo:%d", id)

	var disp map[string]bool
	if e.json(t, "/api/flashcards?chave="+chave, &disp); !disp["audio"] {
		t.Fatalf("torrent devia ter áudio: %v", disp)
	}
	if e.json(t, "/api/flashcards?chave=filme:200", &disp); disp["audio"] || !disp["disponivel"] {
		t.Fatalf("assinatura não tem áudio: %v", disp)
	}
	if s, corpo := e.pedir(t, "POST", "/api/flashcards", map[string]any{"chave": "filme:200", "falas": []string{"Hi"}, "audio": true, "inicio": 1, "fim": 2}); s != http.StatusConflict {
		t.Fatalf("assinatura com áudio: %d %s", s, corpo)
	}
	if s, _ := e.pedir(t, "POST", "/api/flashcards", map[string]any{"chave": chave, "falas": []string{"Hi"}, "audio": true, "inicio": 10, "fim": 50}); s != http.StatusBadRequest {
		t.Fatalf("trecho longo demais: %d", s)
	}
	if s, corpo := e.pedir(t, "POST", "/api/flashcards", map[string]any{"chave": chave, "falas": []string{"Keep the change."}, "audio": true, "inicio": 30, "fim": 32.5}); s != http.StatusAccepted {
		t.Fatalf("%d %s", s, corpo)
	}
	n := f.nota(t, "n0")
	f.mu.Lock()
	mp3 := f.audios[n["audio"]]
	f.mu.Unlock()
	if n["audio"] == "" || len(mp3) < 1000 || http.DetectContentType(mp3) != "audio/mpeg" && !(mp3[0] == 0xFF && mp3[1]&0xE0 == 0xE0) {
		t.Fatalf("nota %v, %d bytes de áudio", n, len(mp3))
	}
}
