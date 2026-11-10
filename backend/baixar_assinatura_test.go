package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Lucas-Wesley/home-lab-streaming/provedorfalso"
)

func estadoDoDownload(t *testing.T, e *ambiente, chave string) downloadDaAssinatura {
	t.Helper()
	var lista []downloadDaAssinatura
	e.json(t, "/api/assinatura/downloads", &lista)
	for _, d := range lista {
		if d.Chave == chave {
			return d
		}
	}
	t.Fatalf("%s não está na lista: %+v", chave, lista)
	return downloadDaAssinatura{}
}

func TestBaixarDaAssinatura(t *testing.T) {
	e := novoAmbiente(t)
	ctx := context.Background()
	if status, corpo := e.pedir(t, "POST", "/api/assinatura/downloads/filme:200", nil); status != http.StatusAccepted {
		t.Fatalf("baixar: %d %s", status, corpo)
	}

	// Alguém assistindo um canal: o download espera, sem atrapalhar.
	_, sair, err := e.a.conexao.entrar(ctx, "canal:100")
	if err != nil {
		t.Fatal(err)
	}
	if !e.a.baixarPendentes(ctx) {
		t.Fatal("devia ficar esperando a conexão")
	}
	if d := estadoDoDownload(t, e, "filme:200"); d.Estado != "esperando" || d.Mensagem == "" {
		t.Fatalf("esperando: %+v", d)
	}
	sair()

	// Livre: baixa inteiro.
	if e.a.baixarPendentes(ctx) {
		t.Fatal("não devia esperar")
	}
	d := estadoDoDownload(t, e, "filme:200")
	if d.Estado != "baixado" || d.Tamanho != int64(len(provedorfalso.Video)) || d.Baixado != d.Tamanho || d.Pagina != "#/filme/200" {
		t.Fatalf("baixado: %+v", d)
	}
	d, _ = e.a.downloadDe(ctx, "filme:200")
	gravado, _ := os.ReadFile(e.a.caminhoDoDownload(d))
	if !bytes.Equal(gravado, provedorfalso.Video) {
		t.Fatal("arquivo diferente do vídeo")
	}

	// Baixado: toca do disco, sem pedir ao provedor, e tocá-lo não tira a conexão de quem vê um canal.
	_, sair, _ = e.a.conexao.entrar(ctx, "canal:100")
	defer sair()
	assumir(t, e, "filme:200")
	if atual, _ := e.a.conexao.donoAtual(); atual != "canal:100" {
		t.Fatalf("o filme baixado tirou a conexão do canal: %s", atual)
	}
	antes := e.provedor.Pedidos.Load()
	resp, corpo := baixar(t, "GET", e.api.URL+"/api/video/filme/200.mp4", "bytes=10-19")
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(corpo, provedorfalso.Video[10:20]) || e.provedor.Pedidos.Load() != antes {
		t.Fatalf("tocar o baixado: %d, %d pedidos ao provedor", resp.StatusCode, e.provedor.Pedidos.Load()-antes)
	}

	// Apagar tira da lista e do disco.
	e.pedir(t, "DELETE", "/api/assinatura/downloads/filme:200", nil)
	if _, err := os.Stat(e.a.caminhoDoDownload(d)); !os.IsNotExist(err) {
		t.Fatal("o arquivo ficou no disco")
	}
}

// Quem começa a assistir pela assinatura corta o download; depois ele continua de onde parou.
func TestDownloadParaEContinua(t *testing.T) {
	e := novoAmbiente(t)
	ctx := context.Background()
	e.pedir(t, "POST", "/api/assinatura/downloads/filme:201", nil)
	e.provedor.Lento.Store(int64(2 * time.Second))

	pronto := make(chan error, 1)
	go func() { pronto <- e.a.baixarUm(ctx, "filme:201") }()
	for range 100 { // espera o download começar
		if atual, dono := e.a.conexao.donoAtual(); atual == "filme:201" && dono == donoPorTras {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assumir(t, e, "canal:100") // alguém liga um canal
	select {
	case err := <-pronto:
		if err != errOutraReproducao {
			t.Fatalf("devia parar para tentar depois: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("o download não parou")
	}
	if atual, dono := e.a.conexao.donoAtual(); atual != "canal:100" || dono != "app" {
		t.Fatalf("dono: %s %s", atual, dono)
	}

	// Uma parte já gravada: continua dali (Range), e o arquivo fica igual ao vídeo.
	e.provedor.Lento.Store(0)
	e.a.conexao.assumir("", "")
	d, _ := e.a.downloadDe(ctx, "filme:201")
	os.MkdirAll(e.a.pastaDaAssinatura(), 0o755)
	os.WriteFile(e.a.caminhoDoDownload(d), provedorfalso.Video[:300000], 0o644)
	e.a.baixarPendentes(ctx)
	d, _ = e.a.downloadDe(ctx, "filme:201")
	gravado, _ := os.ReadFile(e.a.caminhoDoDownload(d))
	if d.Estado != "baixado" || !bytes.Equal(gravado, provedorfalso.Video) {
		t.Fatalf("continuar: %+v, %d bytes", d, len(gravado))
	}
}
