package main

import (
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// falasInventadas: um filme de 90 min com falas de 1 a 6 s, em intervalos irregulares.
func falasInventadas(semente int64) []fala {
	r := rand.New(rand.NewSource(semente))
	var falas []fala
	for t := 30.0; t < 5400; t += 2 + r.Float64()*9 {
		d := 1 + r.Float64()*5
		falas = append(falas, fala{t, t + d})
		t += d
	}
	return falas
}

// tradução: o mesmo corte, mas cada fala com uma variação pequena e algumas juntadas ou faltando.
func traducao(ref []fala, atraso, escala float64, semente int64) []fala {
	r := rand.New(rand.NewSource(semente))
	var out []fala
	for i, f := range ref {
		if i%9 == 4 { // falas que a tradução juntou com a anterior
			continue
		}
		v := (r.Float64() - 0.5) * 0.3
		out = append(out, fala{(f.inicio-atraso)/escala + v, (f.fim-atraso)/escala + v})
	}
	return out
}

func TestAlinharLegendas(t *testing.T) {
	ref := falasInventadas(1)
	casos := []struct {
		nome           string
		atraso, escala float64
	}{
		{"já sincronizada", 0, 1},
		{"abertura 38 s mais longa (A Noite dos Mortos-Vivos)", 38, 1},
		{"legenda atrasada 2,5 s", -2.5, 1},
		{"versão PAL (25 quadros) contra cinema (23,976)", 1.2, 25 / 23.976},
	}
	for _, c := range casos {
		leg := traducao(ref, c.atraso, c.escala, 2)
		a := alinhar(leg, ref)
		if math.Abs(a.Atraso-c.atraso) > 0.3 || math.Abs(a.Escala-c.escala) > 0.001 || a.Acerto < 0.6 {
			t.Errorf("%s: %+v, queria atraso %.2f escala %.4f", c.nome, a, c.atraso, c.escala)
		}
	}
	// Legenda de outro filme: nada bate de verdade.
	if a := alinhar(falasInventadas(7), ref); a.Acerto > 0.45 {
		t.Errorf("legenda de outro filme teve acerto alto: %+v", a)
	}
}

func TestAjustarTempos(t *testing.T) {
	srt := "1\n00:00:02,824 --> 00:00:06,820\nOlá\n\n2\n01:00:00,000 --> 01:00:01,500\nTchau\n"
	got := ajustarTempos(srt, 38.5, 1)
	if !strings.Contains(got, "00:00:41,324 --> 00:00:45,320") || !strings.Contains(got, "01:00:38,500 --> 01:00:40,000") {
		t.Fatalf("ajuste: %q", got)
	}
	if got := ajustarTempos("00:00:10.000 --> 00:00:11.000", -20, 1); got != "00:00:00.000 --> 00:00:00.000" {
		t.Fatalf("não pode ficar negativo, e o WebVTT mantém o ponto: %q", got)
	}
	if len(lerFalas([]byte(srt))) != 2 {
		t.Fatal("ler falas")
	}
}

// legendaDeTeste: 40 falas de 1,2 s a cada 2,5 s, a partir de inicio (segundos).
func legendaDeTeste(inicio float64) string {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		a := inicio + float64(i)*2.5 + float64(i%3)*0.4 // intervalos irregulares, para o pico ser único
		fmt.Fprintf(&b, "%d\n%s --> %s\nFala %d\n\n", i+1, tempoDoSRT(a, ","), tempoDoSRT(a+1.2, ","), i+1)
	}
	return b.String()
}

// Escolher uma legenda 7 s adiantada para um arquivo baixado que traz a própria legenda dentro: o
// servidor acha sozinho os +7 s, e o player e a tela recebem a legenda já ajustada.
func TestAjusteAutomaticoPelaLegendaDeDentro(t *testing.T) {
	e := novoAmbiente(t)
	mi := torrentDeVideoCom(t, legendaDeTeste(10))
	hash := enviarFicha(t, e, mi)
	var d detalheDoTorrent
	e.json(t, "/api/torrents/"+hash, &d)
	id := d.Arquivos[0].ID
	if status, corpo := e.pedir(t, "POST", fmt.Sprintf("/api/arquivos/%d/baixar", id), nil); status >= 300 {
		t.Fatalf("baixar: %d %s", status, corpo)
	}
	for range 600 {
		if e.json(t, "/api/torrents/"+hash, &d); d.Arquivos[0].Download == "baixado" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// O "OpenSubtitles" manda a mesma legenda, 7 s adiantada (de outra versão do filme).
	falso := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/download":
			fmt.Fprintf(w, `{"link":"http://%s/legenda.srt"}`, r.Host)
		case "/legenda.srt":
			fmt.Fprint(w, legendaDeTeste(3))
		default:
			fmt.Fprint(w, `{"data":[]}`)
		}
	}))
	defer falso.Close()
	t.Setenv("OPENSUBTITLES_URL", falso.URL)
	e.a.guardarAjuste("opensubtitles", "chave")

	chave := fmt.Sprintf("arquivo:%d", id)
	if status, corpo := e.pedir(t, "PUT", "/api/legendas/"+chave, legendaEscolhida{ID: "os-9", Idioma: "pt-BR", Nome: "Outra versão"}); status != 200 {
		t.Fatalf("escolher: %d %s", status, corpo)
	}
	var r struct {
		Escolhida legendaEscolhida `json:"escolhida"`
	}
	e.json(t, "/api/legendas/"+chave, &r)
	if l := r.Escolhida; l.Alinhando || math.Abs(l.Atraso-7) > 0.1 || l.Escala != 1 || !strings.Contains(l.Ajuste, "dentro do arquivo") {
		t.Fatalf("ajuste automático: %+v", l)
	}
	if _, vtt := e.pedir(t, "GET", "/api/legenda/os-9.vtt?atraso=7&escala=1", nil); !strings.Contains(vtt, "00:00:10.000 --> 00:00:11.200") {
		t.Fatalf("legenda ajustada no player: %q", vtt[:min(len(vtt), 120)])
	}

	// Ajuste manual por cima.
	if status, corpo := e.pedir(t, "PATCH", "/api/legendas/"+chave, map[string]float64{"atraso": 7.5}); status != 200 || !strings.Contains(corpo, `"atraso":7.5`) || !strings.Contains(corpo, `"ajuste":"manual"`) {
		t.Fatalf("ajuste manual: %d %s", status, corpo)
	}
}
