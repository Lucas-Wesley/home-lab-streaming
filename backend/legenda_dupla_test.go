package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestASSDaDupla(t *testing.T) {
	en := legendaDaDupla{texto: []byte("1\n00:00:10,000 --> 00:00:12,000\n<i>I'm going</i>\nhome now.\n\n2\n00:00:13,000 --> 00:00:14,000\n-No!\n-\"No\"?\n"), al: alinhamento{Atraso: 1.5, Escala: 1}}
	pt := legendaDaDupla{texto: []byte("1\r\n00:00:11,000 --> 00:00:13,000\r\nVou para {\\an8}casa.\r\n"), al: alinhamento{Atraso: 0, Escala: 1}}
	ass := assDaDupla(en, pt)
	linhas := strings.Split(ass, "\n")
	var eventos []string
	for _, l := range linhas {
		if strings.HasPrefix(l, "Dialogue:") {
			eventos = append(eventos, l)
		}
	}
	want := []string{
		"Dialogue: 0,0:00:11.00,0:00:13.00,PT,,0,0,0,,Vou para casa.",
		"Dialogue: 1,0:00:11.50,0:00:13.50,EN,,0,0,108,,I'm going home now.", // acima da linha do português
		`Dialogue: 1,0:00:14.50,0:00:15.50,EN,,0,0,108,,-No!\N-"No"?`,        // sem português junto: a mesma altura
	}
	if strings.Join(eventos, "\n") != strings.Join(want, "\n") {
		t.Fatalf("eventos:\n%s", strings.Join(eventos, "\n"))
	}
	if !strings.Contains(ass, "Style: EN,Liberation Sans,60,&H00FFFFFF") || !strings.Contains(ass, "Style: PT,Liberation Sans,46,&H0060E0FF") {
		t.Errorf("estilos: %s", ass)
	}
}

// videoDeTeste: 20 s de imagem e som, gerados pelo ffmpeg.
func videoDeTeste(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath(programaFFmpeg); err != nil {
		t.Skip("sem ffmpeg")
	}
	caminho := filepath.Join(t.TempDir(), "video.mp4")
	saida, err := exec.Command(programaFFmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=320x180:rate=24:duration=20",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=20", "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-shortest", caminho).CombinedOutput()
	if err != nil {
		t.Fatalf("gerar vídeo: %v %s", err, saida)
	}
	return caminho
}

func duracaoDe(t *testing.T, caminho string) float64 {
	t.Helper()
	saida, err := exec.Command(programaFFprobe, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", caminho).Output()
	if err != nil {
		t.Fatal(err)
	}
	d, _ := strconv.ParseFloat(strings.TrimSpace(string(saida)), 64)
	return d
}

func TestGravarDuplaEmPedacos(t *testing.T) {
	video := videoDeTeste(t)
	queimar := queimarLegenda
	queimarLegenda = false // o ffmpeg do Mac não tem libass
	t.Cleanup(func() { queimarLegenda = queimar })
	e := novoAmbiente(t)
	chave := "arquivo:77"
	pasta := e.a.pastaDaDupla(chave)
	os.MkdirAll(pasta, 0o755)
	ctx := context.Background()

	// Do ponto pedido (12 s) ao fim, depois o começo: os pedaços se encaixam pelo tempo do original.
	if err := gravarTrecho(ctx, pasta, video, "a", 12, 0, 0); err != nil {
		t.Fatal(err)
	}
	trechos := e.a.trechosDaDupla(chave, 12)
	if len(trechos) == 0 || trechos[0].inicio != 12 || math.Abs(trechos[len(trechos)-1].fim-20) > 0.2 {
		t.Fatalf("trecho a: %+v", trechos)
	}
	if inicio, ok := e.a.inicioDoFluxo(chave, 12, 15); !ok || inicio != 12 {
		t.Errorf("início do fluxo em 15 s: %v %v", inicio, ok)
	}
	if _, ok := e.a.inicioDoFluxo(chave, 12, 5); ok {
		t.Error("5 s ainda não foi gravado")
	}
	if err := gravarTrecho(ctx, pasta, video, "b", 0, 12, 0); err != nil {
		t.Fatal(err)
	}
	faixas := juntarTrechos(e.a.trechosDaDupla(chave, 12))
	if len(faixas) != 1 || faixas[0].inicio != 0 || math.Abs(faixas[0].fim-20) > 0.2 {
		t.Fatalf("faixas: %+v", faixas)
	}

	// Para a tela, enquanto grava: os pedaços de 12 s em diante, um atrás do outro.
	e.a.db.Exec(`INSERT INTO legendas_duplas (chave, estado, comeco, duracao, criada_em) VALUES (?, 'juntando', 12, 20, 'x')`, chave)
	var esperado []byte
	for _, tr := range e.a.trechosDaDupla(chave, 12) {
		if tr.inicio >= 12 {
			b, _ := os.ReadFile(tr.arquivo)
			esperado = append(esperado, b...)
		}
	}
	resp, corpo := baixar(t, "GET", e.telas.URL+"/tela/arquivo/77.ts?dupla=1&desde=12.000", "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "video/mp2t" || !bytes.Equal(corpo, esperado) {
		t.Fatalf("fluxo: %d %s, %d bytes (esperado %d)", resp.StatusCode, resp.Header.Get("Content-Type"), len(corpo), len(esperado))
	}
	if !strings.Contains(resp.Header.Get("contentFeatures.dlna.org"), "OP=00") {
		t.Errorf("fluxo sem as flags de ao vivo: %s", resp.Header.Get("contentFeatures.dlna.org"))
	}
	// O app (celular) só toca a pronta.
	if resp, _ := baixar(t, "GET", e.api.URL+"/api/video/arquivo/77.mp4?dupla=1", ""); resp.StatusCode != http.StatusConflict {
		t.Errorf("app antes de pronta: %d", resp.StatusCode)
	}

	// Junta num MP4 só, com a duração inteira; os pedaços somem.
	if err := e.a.juntarDupla(ctx, chave, 12); err != nil {
		t.Fatal(err)
	}
	e.a.limparPedacos(chave)
	if d := duracaoDe(t, e.a.arquivoDaDupla(chave)); math.Abs(d-20) > 0.5 {
		t.Errorf("duração do MP4: %v", d)
	}
	if _, err := os.Stat(pasta); !os.IsNotExist(err) {
		t.Error("os pedaços ficaram")
	}
	if resp, b := baixar(t, "GET", e.api.URL+"/api/video/arquivo/77.mp4?dupla=1", "bytes=0-99"); resp.StatusCode != 206 || len(b) != 100 {
		t.Errorf("app depois de pronta: %d %d", resp.StatusCode, len(b))
	}
	var d legendaDupla
	e.json(t, "/api/legenda-dupla/"+chave, &d)
	if d.Gravado != 20 || len(d.Faixas) != 1 || d.Tamanho == 0 {
		t.Errorf("estado pronto: %+v", d)
	}
}

func TestPedirLegendaDupla(t *testing.T) {
	e := novoAmbiente(t)
	mi, _ := torrentDeTeste(t)
	hash := enviarFicha(t, e, mi)
	var det detalheDoTorrent
	e.json(t, "/api/torrents/"+hash, &det)
	chave := fmt.Sprintf("arquivo:%d", det.Arquivos[0].ID)

	var d legendaDupla
	e.json(t, "/api/legenda-dupla/"+chave, &d)
	if d.Estado != "" {
		t.Fatalf("sem pedido: %+v", d)
	}
	pedir := func(corpo map[string]any) legendaDupla {
		status, r := e.pedir(t, "POST", "/api/legenda-dupla/"+chave, corpo)
		if status != http.StatusOK {
			t.Fatalf("pedir: %d %s", status, r)
		}
		var d legendaDupla
		json.Unmarshal([]byte(r), &d)
		return d
	}
	if d = pedir(map[string]any{"comeco": 3367.8}); d.Estado != "na fila" || d.Comeco != 3366 {
		t.Fatalf("pedido: %+v", d)
	}
	// Pedir de novo não refaz; enquanto na fila, o ponto muda.
	e.a.db.Exec(`UPDATE legendas_duplas SET estado = 'gravando' WHERE chave = ?`, chave)
	if d = pedir(map[string]any{"comeco": 10}); d.Estado != "gravando" || d.Comeco != 3366 {
		t.Fatalf("de novo, gravando: %+v", d)
	}
	e.a.db.Exec(`UPDATE legendas_duplas SET estado = 'erro', mensagem = 'falhou' WHERE chave = ?`, chave)
	if d = pedir(map[string]any{"comeco": 10, "esperar": true}); d.Estado != "na fila" || d.Comeco != 6 || !d.Esperar || d.Mensagem != "" {
		t.Fatalf("depois do erro: %+v", d)
	}
	// Esperando o download, não é a próxima.
	if p := e.a.proximaDupla(context.Background()); p != "" {
		t.Errorf("próxima sem o download: %q", p)
	}
	if status, _ := e.pedir(t, "POST", "/api/legenda-dupla/filme:1", map[string]any{}); status != http.StatusBadRequest {
		t.Errorf("filme da assinatura: %d", status)
	}
	// Tocar a dupla numa tela antes de gravar: recado claro.
	if status, _ := e.pedir(t, "DELETE", "/api/legenda-dupla/"+chave, nil); status != http.StatusNoContent {
		t.Errorf("apagar: %d", status)
	}
	e.json(t, "/api/legenda-dupla/"+chave, &d)
	if d.Estado != "" {
		t.Errorf("depois de apagar: %+v", d)
	}
}

// Depois de reiniciar no meio, grava só o que falta.
func TestFaltandoNaDupla(t *testing.T) {
	gravados := []trechoGravado{{inicio: 3360, fim: 3900}, {inicio: 4200, fim: 4500}}
	got := fmt.Sprint(faltando([2]float64{3360, 6427}, gravados), faltando([2]float64{0, 3360}, gravados), faltando([2]float64{3360, 3900.3}, gravados))
	if got != "[[3900 4200] [4500 6427]] [[0 3360]] []" {
		t.Errorf("faltando: %s", got)
	}
}
