package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLerLinkDoTelegram(t *testing.T) {
	casos := []struct {
		link     string
		ok       bool
		chat     string
		mensagem int64
		limpo    string
	}{
		{"https://t.me/c/1234567890/4", true, "1234567890", 4, "https://t.me/c/1234567890/4"},
		{" https://t.me/c/1234567890/4?single ", true, "1234567890", 4, "https://t.me/c/1234567890/4"},
		{"t.me/c/1234567890/7/45", true, "1234567890", 45, "https://t.me/c/1234567890/7/45"},
		{"https://t.me/canal_publico/12", true, "canal_publico", 12, "https://t.me/canal_publico/12"},
		{"https://t.me/c/1234567890", true, "1234567890", 0, "https://t.me/c/1234567890"},
		{"https://exemplo.com/c/1/2", false, "", 0, ""},
		{"https://t.me/+convite", false, "", 0, ""},
	}
	for _, c := range casos {
		l, ok := lerLinkDoTelegram(c.link)
		if ok != c.ok || l.chat != c.chat || l.mensagem != c.mensagem || l.limpo != c.limpo {
			t.Errorf("%q: %+v %v", c.link, l, ok)
		}
	}
}

func comTdlFalso(t *testing.T) string {
	t.Helper()
	bin, _ := filepath.Abs("testdata/tdl-falso")
	t.Setenv("TDL_BIN", bin)
	args := filepath.Join(t.TempDir(), "args")
	t.Setenv("TDL_FALSO_ARGS", args)
	return args
}

type respostaDosMeusVideos struct {
	Videos   []meuVideo         `json:"videos"`
	Pedidos  []pedidoDoTelegram `json:"pedidos"`
	Telegram struct {
		Instalado bool `json:"instalado"`
	} `json:"telegram"`
}

// pedirEBaixar faz o pedido pela API e roda a fila na hora (nos testes, o trabalhador não roda).
func pedirEBaixar(t *testing.T, e *ambiente, corpo map[string]any) pedidoDoTelegram {
	t.Helper()
	status, texto := e.pedir(t, "POST", "/api/meus-videos/telegram", corpo)
	if status != http.StatusAccepted {
		t.Fatalf("pedido: %d %s", status, texto)
	}
	var p pedidoDoTelegram
	json.Unmarshal([]byte(texto), &p)
	e.a.baixarDoTelegram(context.Background(), p.ID)
	p, _ = e.a.pedidoDe(context.Background(), p.ID)
	return p
}

func TestBaixarDoTelegramParaAPasta(t *testing.T) {
	e := novoAmbiente(t)
	args := comTdlFalso(t)

	p := pedirEBaixar(t, e, map[string]any{"link": "https://t.me/c/1234567890/4?single", "nome": "Praia do Forte", "pasta": "Viagens/Bahia"})
	if p.Estado != "pronto" || p.Videos != 1 {
		t.Fatalf("pedido: %+v", p)
	}
	usados, _ := os.ReadFile(args)
	for _, esperado := range []string{"dl", "-u", "https://t.me/c/1234567890/4", "--group", "--storage"} {
		if !strings.Contains(string(usados), esperado+"\n") && !strings.Contains(string(usados), esperado) {
			t.Errorf("faltou %q nos argumentos:\n%s", esperado, usados)
		}
	}

	var r respostaDosMeusVideos
	e.json(t, "/api/meus-videos", &r)
	if !r.Telegram.Instalado || len(r.Videos) != 1 {
		t.Fatalf("lista: %+v", r)
	}
	v := r.Videos[0]
	// A barra vira espaço: um nível de pasta só. A foto que veio junto ficou de fora.
	if v.Nome != "Praia do Forte" || v.Pasta != "Viagens Bahia" || v.Origem != "https://t.me/c/1234567890/40" {
		t.Fatalf("vídeo: %+v", v)
	}
	if _, err := os.Stat(filepath.Join(e.a.pastaDosMeusVideos(), "Viagens Bahia", "Praia do Forte.mp4")); err != nil {
		t.Fatal(err)
	}
	if fotos, _ := filepath.Glob(filepath.Join(e.a.pastaDosMeusVideos(), "*", "*.jpg")); len(fotos) > 0 {
		t.Fatal("a foto não devia entrar:", fotos)
	}

	// Toca no app (com Range) e na tela, sem usar a assinatura.
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/video/proprio/%d.mp4", e.api.URL, v.ID), nil)
	req.Header.Set("Range", "bytes=0-4")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	corpo, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(corpo) != "video" {
		t.Fatalf("vídeo: %d %q", resp.StatusCode, corpo)
	}
	resp, err = http.Get(fmt.Sprintf("%s/tela/proprio/%d.mp4", e.telas.URL, v.ID))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("contentFeatures.dlna.org") == "" {
		t.Fatalf("tela: %d %v", resp.StatusCode, resp.Header)
	}
	chave := fmt.Sprintf("proprio:%d", v.ID)
	if status, texto := e.pedir(t, "POST", "/api/reproducao", map[string]string{"chave": chave}); status != 204 {
		t.Fatalf("assumir: %d %s", status, texto)
	}
	if c, _ := e.a.conexao.donoAtual(); c != "" {
		t.Fatal("vídeo próprio não deve tomar a assinatura")
	}

	// Entra em "Continuar assistindo", com o nome e a pasta.
	if status, texto := e.pedir(t, "PUT", "/api/progresso/"+chave, map[string]float64{"posicao": 120, "duracao": 600}); status != 204 {
		t.Fatalf("progresso: %d %s", status, texto)
	}
	p2 := e.a.progressoDe(context.Background(), chave)
	if p2 == nil || p2.Titulo != "Praia do Forte" || p2.Subtitulo != "Viagens Bahia" {
		t.Fatalf("progresso: %+v", p2)
	}
	e.json(t, "/api/meus-videos", &r)
	if r.Videos[0].Posicao != 120 {
		t.Fatalf("posição: %+v", r.Videos[0])
	}
}

func TestAlbumDoTelegramNumeraOsVideos(t *testing.T) {
	e := novoAmbiente(t)
	comTdlFalso(t)
	t.Setenv("TDL_FALSO_VIDEOS", "2")
	pedirEBaixar(t, e, map[string]any{"link": "https://t.me/c/1234567890/40", "nome": "Reunião", "pasta": "Trabalho"})
	pedirEBaixar(t, e, map[string]any{"link": "https://t.me/c/1234567890/40", "nome": "Reunião", "pasta": "Trabalho"})
	var r respostaDosMeusVideos
	e.json(t, "/api/meus-videos", &r)
	var nomes []string
	for _, v := range r.Videos {
		nomes = append(nomes, v.Nome)
	}
	// O segundo pedido não apaga o primeiro: os nomes repetidos ganham " 2".
	if got := strings.Join(nomes, ","); got != "Reunião 01,Reunião 01 2,Reunião 02,Reunião 02 2" { // pela data e pelo número da mensagem
		t.Fatal(got)
	}
}

func TestTelegramSemLoginExplica(t *testing.T) {
	e := novoAmbiente(t)
	comTdlFalso(t)
	t.Setenv("TDL_FALSO_ERRO", "callback: not authorized. please login first")
	p := pedirEBaixar(t, e, map[string]any{"link": "https://t.me/c/1234567890/4", "nome": "X", "pasta": "Y"})
	if p.Estado != "erro" || !strings.Contains(p.Mensagem, "não está conectado") {
		t.Fatalf("%+v", p)
	}
	// Tentar de novo volta para a fila.
	status, texto := e.pedir(t, "POST", fmt.Sprintf("/api/meus-videos/telegram/%d/retomar", p.ID), nil)
	if status != 200 || !strings.Contains(texto, `"na fila"`) {
		t.Fatalf("retomar: %d %s", status, texto)
	}
}

func TestPedidoDoTelegramConfereOsCampos(t *testing.T) {
	e := novoAmbiente(t)
	for _, c := range []map[string]any{
		{"link": "https://exemplo.com/x", "nome": "a", "pasta": "b"},
		{"link": "https://t.me/c/1234567890", "nome": "a", "pasta": "b"}, // grupo sem "tudo"
		{"link": "https://t.me/c/1234567890/4", "nome": "a", "pasta": "  "},
	} {
		if status, texto := e.pedir(t, "POST", "/api/meus-videos/telegram", c); status != 400 {
			t.Errorf("%v: %d %s", c, status, texto)
		}
	}
}

func TestNomeDoVideo(t *testing.T) {
	casos := []struct {
		texto, arquivo string
		data           int64
		esperado       string
	}{
		{"#F54 Aula 31 - Ponto final\n\n08. Pontuação", "Aula 31 - Ponto final.mp4", 0, "Aula 31 - Ponto final"},
		{"", "Aula 2 - Adjetivos.mp4", 0, "Aula 2 - Adjetivos"},
		{"", "VID_20260101_090000.mp4", 1767268800, "Vídeo de 01-01-2026 às 09h00"},
		{"", "5012345678.mp4", 1767268800, "Vídeo de 01-01-2026 às 09h00"},
		{"  \n Pôr do sol em Itacaré ", "VID_1.mp4", 0, "Pôr do sol em Itacaré"},
		{"a/b: c?", "", 0, "a b c"},
		{"", "", 0, "Vídeo"},
	}
	for _, c := range casos {
		if got := nomeDoVideo(c.texto, c.arquivo, c.data); got != c.esperado {
			t.Errorf("%q %q: %q", c.texto, c.arquivo, got)
		}
	}
	longo := nomeDoVideo(strings.Repeat("palavra ", 30), "", 0)
	if len([]rune(longo)) > 81 || !strings.HasSuffix(longo, "…") {
		t.Errorf("longo: %q", longo)
	}
}

func TestRastrearOGrupo(t *testing.T) {
	e := novoAmbiente(t)
	comTdlFalso(t)
	e.a.db.Exec(`INSERT INTO meus_videos (pasta, nome, arquivo, origem, criado_em) VALUES ('Aulas', 'a', 'Aulas/a.mp4', 'https://t.me/c/1234567890/4', '2026-01-01T00:00:00Z')`)
	status, texto := e.pedir(t, "POST", "/api/meus-videos/telegram/rastrear", map[string]string{"link": "https://t.me/c/1234567890/40?single"})
	if status != 200 {
		t.Fatalf("%d %s", status, texto)
	}
	var r struct {
		Grupo, Pasta string
		Novos        int
		Videos       []videoDoGrupo
	}
	json.Unmarshal([]byte(texto), &r)
	var nomes []string
	for _, v := range r.Videos {
		nomes = append(nomes, fmt.Sprintf("%d:%s:%v", v.Mensagem, v.Nome, v.Ja))
	}
	// Do mais antigo ao mais novo; o .rar fica de fora; a 4 já foi baixada.
	if r.Grupo != "Grupo Inventado 2026" || r.Pasta != "Grupo Inventado 2026" || r.Novos != 2 ||
		strings.Join(nomes, "|") != "4:Aula 1 - Começo:true|5:Vídeo de 01-01-2026 às 09h00:false|40:Praia do Forte:false" {
		t.Fatalf("%+v %v", r, nomes)
	}
}

func TestTodosOsVideosDoGrupoComOsNomesDasMensagens(t *testing.T) {
	e := novoAmbiente(t)
	args := comTdlFalso(t)
	// A mensagem 4 já veio antes (o arquivo existe: sem ele, a sincronização tiraria o registro).
	os.MkdirAll(filepath.Join(e.a.pastaDosMeusVideos(), "Aulas"), 0o755)
	os.WriteFile(filepath.Join(e.a.pastaDosMeusVideos(), "Aulas", "a.mp4"), []byte("video"), 0o644)
	e.a.db.Exec(`INSERT INTO meus_videos (pasta, nome, arquivo, origem, criado_em) VALUES ('Aulas', 'a', 'Aulas/a.mp4', 'https://t.me/c/1234567890/4', '2026-01-01T00:00:00Z')`)

	p := pedirEBaixar(t, e, map[string]any{"link": "https://t.me/c/1234567890/40", "nome": "ignorado", "pasta": "Grupo Inventado", "tudo": true})
	if p.Estado != "pronto" || p.Videos != 2 || p.Total != 2 {
		t.Fatalf("%+v", p)
	}
	usados, _ := os.ReadFile(args)
	if !strings.Contains(string(usados), "export\n-c\n1234567890\n--with-content") || !strings.Contains(string(usados), "-f\n") {
		t.Fatalf("argumentos:\n%s", usados)
	}
	var r respostaDosMeusVideos
	e.json(t, "/api/meus-videos", &r)
	var nomes []string
	for _, v := range r.Videos {
		if v.Pasta == "Grupo Inventado" {
			nomes = append(nomes, v.Nome+" "+v.CriadoEm)
		}
	}
	// Na ordem das mensagens, com a data delas; a 4 e o .rar não vieram de novo.
	if got := strings.Join(nomes, "|"); got != "Vídeo de 01-01-2026 às 09h00 2026-01-01T12:00:00Z|Praia do Forte 2026-01-02T12:00:00Z" {
		t.Fatal(got)
	}

	// De novo: nada novo.
	p = pedirEBaixar(t, e, map[string]any{"link": "https://t.me/c/1234567890", "pasta": "Grupo Inventado", "tudo": true})
	if p.Estado != "pronto" || p.Mensagem != "Nenhum vídeo novo no grupo." {
		t.Fatalf("%+v", p)
	}
}

func TestVideoSemNomeUsaOTextoDaMensagem(t *testing.T) {
	e := novoAmbiente(t)
	comTdlFalso(t)
	p := pedirEBaixar(t, e, map[string]any{"link": "https://t.me/c/1234567890/40", "pasta": "Viagens"})
	if p.Estado != "pronto" {
		t.Fatalf("%+v", p)
	}
	var r respostaDosMeusVideos
	e.json(t, "/api/meus-videos", &r)
	if len(r.Videos) != 1 || r.Videos[0].Nome != "Praia do Forte" {
		t.Fatalf("%+v", r.Videos)
	}
}

func TestMeusVideosRenomearMoverApagarEPastaNoDisco(t *testing.T) {
	e := novoAmbiente(t)
	// Posto à mão na pasta: aparece na lista.
	dir := filepath.Join(e.a.pastaDosMeusVideos(), "Viagens")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "Chile.mp4"), []byte("video"), 0o644)
	os.WriteFile(filepath.Join(dir, "notas.txt"), []byte("x"), 0o644)
	var r respostaDosMeusVideos
	e.json(t, "/api/meus-videos", &r)
	if len(r.Videos) != 1 || r.Videos[0].Nome != "Chile" || r.Videos[0].Pasta != "Viagens" {
		t.Fatalf("%+v", r.Videos)
	}
	id := r.Videos[0].ID

	status, texto := e.pedir(t, "PATCH", fmt.Sprintf("/api/meus-videos/%d", id), map[string]string{"nome": "Chile 2025", "pasta": "Viagens antigas"})
	if status != 200 {
		t.Fatalf("mudar: %d %s", status, texto)
	}
	if _, err := os.Stat(filepath.Join(e.a.pastaDosMeusVideos(), "Viagens antigas", "Chile 2025.mp4")); err != nil {
		t.Fatal(err)
	}
	e.json(t, "/api/meus-videos", &r)
	if len(r.Videos) != 1 || r.Videos[0].ID != id {
		t.Fatalf("o vídeo movido não devia virar outro: %+v", r.Videos)
	}

	if status, _ := e.pedir(t, "DELETE", fmt.Sprintf("/api/meus-videos/%d", id), nil); status != 204 {
		t.Fatal(status)
	}
	e.json(t, "/api/meus-videos", &r)
	if len(r.Videos) != 0 {
		t.Fatalf("%+v", r.Videos)
	}
	// Arquivo apagado à mão: sai da lista.
	os.WriteFile(filepath.Join(dir, "Peru.mp4"), []byte("video"), 0o644)
	e.json(t, "/api/meus-videos", &r)
	os.Remove(filepath.Join(dir, "Peru.mp4"))
	e.json(t, "/api/meus-videos", &r)
	if len(r.Videos) != 0 {
		t.Fatalf("%+v", r.Videos)
	}
}
