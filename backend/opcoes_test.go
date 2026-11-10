package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Lucas-Wesley/home-lab-streaming/provedorfalso"
)

// O servidor usa a assinatura por trás só com ela livre, e qualquer pedido de verdade a toma de volta.
func TestConexaoPorTras(t *testing.T) {
	c := &conexao{}
	ctx, sair, err := c.entrarPorTras(context.Background(), "filme:1")
	if err != nil {
		t.Fatal(err)
	}
	_, sairReal, err := c.entrar(context.Background(), "filme:2")
	if err != nil {
		t.Fatalf("o pedido de verdade devia entrar: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("o trabalho por trás devia ser cortado")
	}
	sair()
	if _, _, err := c.entrarPorTras(context.Background(), "filme:3"); !errors.Is(err, errOutraReproducao) {
		t.Fatalf("com alguém assistindo, não entra por trás: %v", err)
	}
	sairReal()

	// A TV assumiu um canal e está parada (sem pedidos abertos): o servidor pode usar a conexão,
	// mas ela continua da TV. Um pedido de outro vídeo é recusado, como antes; o da TV entra.
	c.assumir("canal:9", "tela-tv")
	ctx, sair, err = c.entrarPorTras(context.Background(), "filme:3")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.entrar(context.Background(), "filme:4"); !errors.Is(err, errOutraReproducao) {
		t.Fatalf("a conexão é da TV: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("qualquer pedido corta o trabalho por trás")
	}
	sair()
	if _, sairTV, err := c.entrar(context.Background(), "canal:9"); err != nil {
		t.Fatalf("a TV volta: %v", err)
	} else {
		sairTV()
	}
	if atual, dono := c.donoAtual(); atual != "canal:9" || dono != "tela-tv" {
		t.Fatalf("dono: %s %s", atual, dono)
	}
}

// videoComAudios gera um MP4 curto com o áudio nos idiomas pedidos (códigos ISO 639-2: por, eng).
func videoComAudios(t *testing.T, idiomas ...string) []byte {
	t.Helper()
	return videoComAudiosDe(t, 5, idiomas...)
}

// videoComAudiosDe: o mesmo, com a duração pedida (segundos).
func videoComAudiosDe(t *testing.T, segundos int, idiomas ...string) []byte {
	t.Helper()
	if _, err := exec.LookPath(programaFFmpeg); err != nil {
		t.Skip("ffmpeg não instalado")
	}
	saida := filepath.Join(t.TempDir(), "v.mp4")
	args := []string{"-v", "error", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=10"}
	for range idiomas {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=440")
	}
	args = append(args, "-map", "0")
	for i, idioma := range idiomas {
		args = append(args, "-map", strconv.Itoa(i+1), "-metadata:s:a:"+strconv.Itoa(i), "language="+idioma)
	}
	args = append(args, "-t", strconv.Itoa(segundos), "-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-shortest", "-movflags", "+faststart", saida)
	if out, err := exec.Command(programaFFmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("gerar vídeo: %v %s", err, out)
	}
	b, _ := os.ReadFile(saida)
	return b
}

func TestFormatosDaAssinatura(t *testing.T) {
	e := novoAmbiente(t)
	e.provedor.Arquivo = videoComAudios(t, "por", "eng")
	var r struct {
		Formatos *sondagem `json:"formatos"`
		Erro     string    `json:"erro"`
	}
	e.json(t, "/api/formatos?chave=filme:200", &r)
	if r.Formatos == nil || len(r.Formatos.Audios) != 2 || r.Formatos.Audios[0].Idioma != "por" || r.Formatos.Audios[1].Idioma != "eng" || r.Formatos.Video != "h264" {
		t.Fatalf("formatos: %+v %q", r.Formatos, r.Erro)
	}
	if strings.Contains(r.Erro, provedorfalso.Senha) {
		t.Fatal("senha no erro")
	}
	// Guardados: a segunda vez nem pede ao provedor.
	antes := e.provedor.Pedidos.Load()
	e.json(t, "/api/formatos?chave=filme:200", &r)
	if e.provedor.Pedidos.Load() != antes || r.Formatos == nil {
		t.Fatal("devia vir do banco")
	}

	// Com alguém assistindo outro vídeo da assinatura, não lê: diz que está em uso.
	_, sair, _ := e.a.conexao.entrar(context.Background(), "canal:100")
	defer sair()
	r.Formatos, r.Erro = nil, ""
	e.json(t, "/api/formatos?chave=filme:201", &r)
	if r.Formatos != nil || !strings.Contains(r.Erro, "em uso") {
		t.Fatalf("em uso: %+v %q", r.Formatos, r.Erro)
	}
}

// "Assistir" numa versão de torrent: o servidor abre o torrent, o traz para a lista com o título e
// devolve o vídeo, com os formatos que a folha mostra.
func TestPrepararVersaoDeTorrent(t *testing.T) {
	e := novoAmbiente(t)
	ficha := torrentDeVideo(t)
	var buscas []string
	prowlarr := prowlarrFalso(t, ficha, &buscas)
	e.pedir(t, "PUT", "/api/prowlarr", configDoProwlarr{URL: prowlarr.URL, Chave: chaveFalsa})
	link := prowlarr.URL + "/1/download?link=abc&file=Filme"

	var r struct {
		Hash    string           `json:"hash"`
		Arquivo arquivoDeTorrent `json:"arquivo"`
	}
	status, corpo := e.pedir(t, "POST", "/api/opcoes/preparar", pedidoDeVerificacao{Link: link, TMDB: "filme/1"})
	if status != http.StatusOK {
		t.Fatalf("preparar: %d %s", status, corpo)
	}
	json.Unmarshal([]byte(corpo), &r)
	if r.Hash == "" || r.Arquivo.ID == 0 || !strings.HasSuffix(r.Arquivo.Nome, ".mp4") {
		t.Fatalf("preparar: %+v", r)
	}
	var tmdb string
	var oculto bool
	e.a.db.QueryRow(`SELECT tmdb, oculto FROM torrents WHERE hash = ?`, r.Hash).Scan(&tmdb, &oculto)
	if tmdb != "filme/1" || oculto {
		t.Fatalf("torrent: tmdb %q oculto %v", tmdb, oculto)
	}
	var f struct {
		Formatos *sondagem `json:"formatos"`
	}
	e.json(t, "/api/formatos?chave=arquivo:"+strconv.FormatInt(r.Arquivo.ID, 10), &f)
	if f.Formatos == nil || f.Formatos.Video != "h264" {
		t.Fatalf("formatos do torrent: %+v", f.Formatos)
	}
}

// O arquivo não diz o idioma do áudio (comum na assinatura): o Whisper ouve e reconhece.
func TestIdiomaOuvidoPeloWhisper(t *testing.T) {
	e := novoAmbiente(t)
	e.provedor.Arquivo = videoComAudios(t, "und")
	whisper, chamadas := whisperFalso(t, legendaDeTeste(0), "english")
	t.Setenv("WHISPER_URL", whisper.URL)
	var r struct {
		Formatos *sondagem `json:"formatos"`
		Erro     string    `json:"erro"`
	}
	e.json(t, "/api/formatos?chave=filme:200", &r)
	if r.Formatos == nil || len(r.Formatos.Audios) != 1 || r.Formatos.Audios[0].Idioma != "en" || !r.Formatos.Audios[0].Ouvido || *chamadas != 1 {
		t.Fatalf("idioma ouvido: %+v %q (%d chamadas)", r.Formatos, r.Erro, *chamadas)
	}
}
