package main

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Lucas-Wesley/home-lab-streaming/provedorfalso"
)

func assumir(t *testing.T, e *ambiente, chave string) {
	t.Helper()
	if status, corpo := e.pedir(t, "POST", "/api/reproducao", map[string]string{"chave": chave}); status != 204 {
		t.Fatalf("assumir %s: %d %s", chave, status, corpo)
	}
}

func baixar(t *testing.T, metodo, endereco, faixa string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(metodo, endereco, nil)
	if faixa != "" {
		req.Header.Set("Range", faixa)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestFilmeComRangeSegueORedirecionamento(t *testing.T) {
	e := novoAmbiente(t)
	assumir(t, e, "filme:200")

	resp, corpo := baixar(t, "GET", e.api.URL+"/api/video/filme/200.mp4", "bytes=1000-1999")
	if resp.StatusCode != 206 || !bytes.Equal(corpo, provedorfalso.Video[1000:2000]) {
		t.Fatalf("pedaço: %d, %d bytes", resp.StatusCode, len(corpo))
	}
	if resp.Header.Get("Content-Range") != "bytes 1000-1999/1048576" || resp.Header.Get("Content-Type") != "video/mp4" {
		t.Fatalf("cabeçalhos: %v", resp.Header)
	}

	// O destino do redirecionamento fica guardado: o segundo pedaço não volta ao provedor.
	antes := e.provedor.Pedidos.Load()
	baixar(t, "GET", e.api.URL+"/api/video/filme/200.mp4", "bytes=0-9")
	if e.provedor.Pedidos.Load() != antes {
		t.Fatal("pediu ao provedor de novo, em vez de ir direto à entrega")
	}

	// HEAD diz o tamanho inteiro, sem baixar o vídeo.
	resp, _ = baixar(t, "HEAD", e.api.URL+"/api/video/filme/200.mp4", "")
	if resp.StatusCode != 200 || resp.ContentLength != int64(len(provedorfalso.Video)) {
		t.Fatalf("HEAD: %d, tamanho %d", resp.StatusCode, resp.ContentLength)
	}
}

func TestUmaReproducaoPorVez(t *testing.T) {
	e := novoAmbiente(t)
	assumir(t, e, "filme:200")
	if resp, _ := baixar(t, "GET", e.api.URL+"/api/video/filme/201.mp4", "bytes=0-9"); resp.StatusCode != http.StatusConflict {
		t.Fatalf("outro filme devia ser recusado: %d", resp.StatusCode)
	}
	assumir(t, e, "filme:201")
	if resp, _ := baixar(t, "GET", e.api.URL+"/api/video/filme/201.mp4", "bytes=0-9"); resp.StatusCode != 206 {
		t.Fatalf("depois de assumir, devia tocar: %d", resp.StatusCode)
	}
	if resp, _ := baixar(t, "GET", e.api.URL+"/api/video/filme/200.mp4", "bytes=0-9"); resp.StatusCode != http.StatusConflict {
		t.Fatalf("o filme anterior devia ser recusado: %d", resp.StatusCode)
	}
}

var reParte = regexp.MustCompile(`(?m)^/api/video/canal/100/parte\?u=[^&]+&s=\S+$`)

func TestAoVivoEmHLS(t *testing.T) {
	e := novoAmbiente(t)
	assumir(t, e, "canal:100")

	resp, corpo := baixar(t, "GET", e.api.URL+"/api/video/canal/100.m3u8", "")
	lista := string(corpo)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/vnd.apple.mpegurl" {
		t.Fatalf("lista: %d %s", resp.StatusCode, lista)
	}
	partes := reParte.FindAllString(lista, -1)
	if len(partes) != 2 || strings.Contains(lista, provedorfalso.Senha) || strings.Contains(lista, e.origem.URL) {
		t.Fatalf("a lista devia apontar só para este servidor:\n%s", lista)
	}

	resp, seg := baixar(t, "GET", e.api.URL+partes[0], "")
	if resp.StatusCode != 200 || len(seg) != 188 || seg[0] != 0x47 {
		t.Fatalf("segmento: %d, %d bytes", resp.StatusCode, len(seg))
	}

	// Um endereço com a assinatura trocada não passa: o servidor não é um repasse aberto.
	adulterado := strings.Replace(partes[0], "&s=", "&s=x", 1)
	if resp, _ := baixar(t, "GET", e.api.URL+adulterado, ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("assinatura adulterada: %d", resp.StatusCode)
	}
}

func TestTelaRecebeTSComCabecalhosDLNA(t *testing.T) {
	e := novoAmbiente(t)
	e.a.conexao.assumir("canal:100", "tela-teste")

	resp, corpo := baixar(t, "GET", e.telas.URL+"/tela/canal/100.ts", "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "video/mp2t" || len(corpo) == 0 || corpo[0] != 0x47 {
		t.Fatalf("ts: %d %v (%d bytes)", resp.StatusCode, resp.Header, len(corpo))
	}
	if !strings.Contains(resp.Header.Get("contentFeatures.dlna.org"), "DLNA.ORG_OP=00") {
		t.Fatalf("ao vivo não aceita busca: %v", resp.Header)
	}

	e.a.conexao.assumir("filme:200", "tela-teste")
	resp, _ = baixar(t, "HEAD", e.telas.URL+"/tela/filme/200.mp4", "")
	if resp.StatusCode != 200 || resp.ContentLength != int64(len(provedorfalso.Video)) ||
		!strings.Contains(resp.Header.Get("contentFeatures.dlna.org"), "DLNA.ORG_OP=01") {
		t.Fatalf("HEAD do filme para a tela: %d %v", resp.StatusCode, resp.Header)
	}

	// A porta das telas só tem o vídeo: nada da API.
	if resp, _ := baixar(t, "GET", e.telas.URL+"/api/acesso", ""); resp.StatusCode != 404 {
		t.Fatalf("API exposta na porta das telas: %d", resp.StatusCode)
	}
}
