package main

import (
	"strings"
	"testing"
)

// As versões do SporTV 2 (HD e FHD H265 do servidor reserva) vêm da melhor para a pior, e os
// outros canais do grupo (SporTV 1) ficam à mão para trocar sem sair do player (S51).
func TestAlternativasDoCanal(t *testing.T) {
	e := ambienteDosJogos(t)
	var r struct {
		Nome    string
		Grupo   string
		Versoes []versaoAlternativa
		Irmaos  []canalAgrupado
	}
	e.json(t, "/api/canais/502/alternativas", &r)
	if r.Nome != "SporTV 2" || r.Grupo != "SporTV" {
		t.Fatalf("canal: %+v", r)
	}
	if len(r.Versoes) != 2 || r.Versoes[0].ID != 501 || r.Versoes[0].Qualidade != "HD" {
		t.Fatalf("versões: %+v", r.Versoes)
	}
	if v := r.Versoes[1]; v.Qualidade != "FHD" || !v.H265 || !v.Reserva {
		t.Fatalf("reserva: %+v", v)
	}
	if len(r.Irmaos) != 1 || r.Irmaos[0].ID != 500 {
		t.Fatalf("irmãos: %+v", r.Irmaos)
	}
	if status, _ := e.pedir(t, "GET", "/api/canais/999999/alternativas", nil); status != 404 {
		t.Fatalf("canal que não existe: %d", status)
	}
}

func TestQualidadeDoNome(t *testing.T) {
	casos := map[string]string{"ESPN 1 FHD H265²": "FHD h265 reserva", "Premiere 3 SD": "SD", "Globo SP: São Paulo": "", "Canal 4K": "4K"}
	for nome, quer := range casos {
		q, h, res := qualidadeDoNome(nome)
		partes := []string{}
		if q != "" {
			partes = append(partes, q)
		}
		if h {
			partes = append(partes, "h265")
		}
		if res {
			partes = append(partes, "reserva")
		}
		if got := strings.Join(partes, " "); got != quer {
			t.Errorf("%s: %q, quer %q", nome, got, quer)
		}
	}
}

// Dois aparelhos com o mesmo canal: o segundo assume, e o primeiro fica sabendo quem pegou (S51).
func TestQuemAssiste(t *testing.T) {
	e := novoAmbiente(t)
	var r struct{ Chave, Dono, Tipo, Nome, Titulo string }
	e.json(t, "/api/reproducao", &r)
	if r.Dono != "" || r.Tipo != "" {
		t.Fatalf("sem ninguém: %+v", r)
	}
	for _, p := range []map[string]string{
		{"chave": "canal:100", "aparelho": "celular-123456", "nome": "o celular", "titulo": "Premiere 3"},
		{"chave": "canal:100", "aparelho": "computador-9876", "nome": "o computador Windows", "titulo": "Premiere 3"},
	} {
		if status, corpo := e.pedir(t, "POST", "/api/reproducao", p); status != 204 {
			t.Fatalf("assumir: %d %s", status, corpo)
		}
	}
	e.json(t, "/api/reproducao", &r)
	if r.Dono != "app:computador-9876" || r.Tipo != "aparelho" || r.Nome != "o computador Windows" || r.Titulo != "Premiere 3" || r.Chave != "canal:100" {
		t.Fatalf("quem: %+v", r)
	}
	// Sem aparelho (versão antiga do app), continua "app".
	e.pedir(t, "POST", "/api/reproducao", map[string]string{"chave": "canal:100"})
	if _, dono := e.a.conexao.donoAtual(); dono != "app" {
		t.Fatalf("sem aparelho: %q", dono)
	}
}

// O mesmo canal aberto num aparelho é cortado quando outro aparelho o assume: seriam duas conexões.
func TestOutroAparelhoCortaOMesmoVideo(t *testing.T) {
	var c conexao
	c.assumir("canal:1", "app:aaaaaaaa")
	ctx, sair, err := c.entrar(t.Context(), "canal:1")
	if err != nil {
		t.Fatal(err)
	}
	defer sair()
	c.assumir("canal:1", "app:aaaaaaaa") // o mesmo aparelho de novo (atualizar): não corta
	if ctx.Err() != nil {
		t.Fatal("o mesmo aparelho cortou o próprio vídeo")
	}
	c.assumir("canal:1", "app:bbbbbbbb")
	if ctx.Err() == nil {
		t.Fatal("o vídeo do outro aparelho continuou aberto")
	}
}
