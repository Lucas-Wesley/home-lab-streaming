package main

import (
	"strings"
	"testing"
)

// Os dados aqui são inventados: a repo é pública.
func TestLerAcessoDaMensagemDoProvedor(t *testing.T) {
	mensagem := `*Segue os dados de Acesso!*

*Usuário:* 11112222
*Senha:* 33334444
*Máximo de conexões:* 1
*Data de validade:*
10/11/2026 22:09:07

*DNS:* http://painel.exemplo.invalid
*DNS STB / SmartUp:* 10.0.0.1

*Link (SSIPTV):*
https://outro.exemplo.invalid/s/11112222/33334444/download_m3u/`

	ac, err := lerAcesso(mensagem)
	if err != nil {
		t.Fatal(err)
	}
	want := acesso{Servidor: "http://painel.exemplo.invalid", Usuario: "11112222", Senha: "33334444"}
	if ac != want {
		t.Fatalf("lido %+v, queria %+v", ac, want)
	}
}

func TestLerAcessoPrefereOLinkDaLista(t *testing.T) {
	// Com o link M3U, o servidor vem dele, mesmo que haja outro DNS antes na mensagem.
	mensagem := "DNS: http://antigo.exemplo.invalid\nLink (M3U):\nhttp://novo.exemplo.invalid:8080/get.php?username=ana&password=s3nha&type=m3u_plus&output=ts"
	ac, err := lerAcesso(mensagem)
	if err != nil {
		t.Fatal(err)
	}
	want := acesso{Servidor: "http://novo.exemplo.invalid:8080", Usuario: "ana", Senha: "s3nha"}
	if ac != want {
		t.Fatalf("lido %+v, queria %+v", ac, want)
	}
}

func TestLerAcessoSoComOLink(t *testing.T) {
	ac, err := lerAcesso("  http://x.exemplo.invalid/get.php?username=u&password=p&type=m3u_plus  ")
	if err != nil || ac.Servidor != "http://x.exemplo.invalid" || ac.Usuario != "u" || ac.Senha != "p" {
		t.Fatalf("%+v %v", ac, err)
	}
}

func TestLerAcessoIncompleto(t *testing.T) {
	casos := map[string]string{
		"sem senha":    "Usuário: 1\nDNS: http://a.exemplo.invalid",
		"sem servidor": "Usuário: 1\nSenha: 2",
		"vazio":        "",
	}
	for nome, texto := range casos {
		if _, err := lerAcesso(texto); err == nil {
			t.Errorf("%s: devia recusar", nome)
		}
	}
}

func TestAcessoRecusadoPeloProvedor(t *testing.T) {
	e := novoAmbiente(t)
	status, corpo := e.pedir(t, "PUT", "/api/acesso", map[string]string{"texto": "Usuário: x\nSenha: errada\nDNS: " + e.origem.URL})
	if status != 400 || !strings.Contains(corpo, "recusou") {
		t.Fatalf("%d %s", status, corpo)
	}
}

func TestSituacaoNaoMostraASenha(t *testing.T) {
	e := novoAmbiente(t)
	var s map[string]any
	e.json(t, "/api/acesso", &s)
	if s["usuario"] != "usuario-teste" || s["status"] != "Active" || s["conexoes"] != float64(1) || s["vence_em"] != "2030-01-02T03:04:05Z" {
		t.Fatalf("situação: %v", s)
	}
	for k, v := range s {
		if v == "senha-teste" {
			t.Fatalf("a senha apareceu em %q", k)
		}
	}
}
