package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// legendaFalada: 40 falas com texto de verdade ("esta é a fala número N…"), a partir de inicio.
func legendaFalada(inicio float64) string {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		a := inicio + float64(i)*2.5 + float64(i%3)*0.4
		fmt.Fprintf(&b, "%d\n%s --> %s\nesta é a fala número %s do filme\n\n", i+1, tempoDoSRT(a, ","), tempoDoSRT(a+1.2, ","), numeroPorExtenso(i+1))
	}
	return b.String()
}

func numeroPorExtenso(n int) string {
	nomes := []string{"zero", "um", "dois", "três", "quatro", "cinco", "seis", "sete", "oito", "nove", "dez"}
	return nomes[n%10] + " " + nomes[n/10] + "vezes"
}

func TestCasarFalasESincronia(t *testing.T) {
	leg := falasComTexto([]byte(legendaFalada(10)))
	if len(leg) != 40 || !strings.Contains(leg[0].texto, "fala número") {
		t.Fatalf("ler legenda com texto: %+v", leg[:1])
	}
	// O áudio diz as mesmas falas 5 s depois (com o Whisper às vezes juntando ou errando uma palavra).
	var ouvidas []trechoFalado
	for i, l := range leg {
		texto := l.texto
		if i%4 == 0 {
			texto = strings.Replace(texto, "filme", "fime", 1)
		}
		ouvidas = append(ouvidas, trechoFalado{Inicio: l.inicio + 5, Fim: l.inicio + 6, Texto: texto})
	}
	pares, fracao := casarFalas(ouvidas, leg)
	al, ok := sincroniaPelasFalas(pares)
	if !ok || math.Abs(al.Atraso-5) > 0.1 || al.Escala != 1 || fracao < 0.9 {
		t.Fatalf("sincronia: %+v ok=%v fração %.2f", al, ok, fracao)
	}
	// Falas de outro filme: nada casa.
	if _, f := casarFalas([]trechoFalado{{Inicio: 20, Texto: "where is the money you promised me yesterday"}}, leg); f != 0 {
		t.Fatalf("outro filme casou: %.2f", f)
	}
}

// whisperFalso responde como o speaches: no primeiro trecho, as falas da legenda 5 s depois.
func whisperFalso(t *testing.T, legenda string, idioma string) (*httptest.Server, *int) {
	var mu sync.Mutex
	chamadas := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" || r.FormValue("model") == "" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		chamadas++
		primeira := chamadas == 1
		mu.Unlock()
		type seg struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		}
		var segs []seg
		if primeira {
			for _, f := range falasComTexto([]byte(legenda)) {
				if f.inicio+5 < 85 {
					segs = append(segs, seg{f.inicio + 5, f.inicio + 6, " " + f.texto})
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"language": idioma, "segments": segs})
	}))
	t.Cleanup(srv.Close)
	return srv, &chamadas
}

func TestAjustePeloWhisper(t *testing.T) {
	e := novoAmbiente(t)
	mi := torrentDeVideoCom(t, "") // sem legenda dentro
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
	legenda := legendaFalada(3)
	os := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/download":
			fmt.Fprintf(w, `{"link":"http://%s/legenda.srt"}`, r.Host)
		case "/legenda.srt":
			fmt.Fprint(w, legenda)
		default:
			fmt.Fprint(w, `{"data":[]}`) // a impressão digital não acha nada
		}
	}))
	defer os.Close()
	t.Setenv("OPENSUBTITLES_URL", os.URL)
	e.a.guardarAjuste("opensubtitles", "chave")
	whisper, chamadas := whisperFalso(t, legenda, "portuguese")
	t.Setenv("WHISPER_URL", whisper.URL)

	chave := fmt.Sprintf("arquivo:%d", id)
	e.pedir(t, "PUT", "/api/legendas/"+chave, legendaEscolhida{ID: "os-11", Idioma: "pt-BR", Nome: "Legenda"})
	var r struct {
		Escolhida legendaEscolhida `json:"escolhida"`
	}
	e.json(t, "/api/legendas/"+chave, &r)
	if l := r.Escolhida; math.Abs(l.Atraso-5) > 0.1 || !strings.Contains(l.Ajuste, "Whisper") || *chamadas != 3 {
		t.Fatalf("ajuste pelo Whisper: %+v (%d trechos ouvidos)", l, *chamadas)
	}
}
