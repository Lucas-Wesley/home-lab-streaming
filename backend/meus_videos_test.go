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
	// A barra é subpasta (S55). A foto que veio junto ficou de fora.
	if v.Nome != "Praia do Forte" || v.Pasta != "Viagens/Bahia" || v.Origem != "https://t.me/c/1234567890/40" {
		t.Fatalf("vídeo: %+v", v)
	}
	if _, err := os.Stat(filepath.Join(e.a.pastaDosMeusVideos(), "Viagens", "Bahia", "Praia do Forte.mp4")); err != nil {
		t.Fatal(err)
	}
	if fotos, _ := filepath.Glob(filepath.Join(e.a.pastaDosMeusVideos(), "*", "*", "*.jpg")); len(fotos) > 0 {
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
	if p2 == nil || p2.Titulo != "Praia do Forte" || p2.Subtitulo != "Viagens › Bahia" {
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
		Livre        int64
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
	} // O tamanho e a duração vêm da mensagem (raw), antes de baixar.
	if r.Videos[1].Tamanho != 52428800 || r.Videos[1].Duracao != 12.5 || r.Videos[2].Tamanho != 314572800 || r.Videos[0].Tamanho != 0 {
		t.Fatalf("tamanhos: %+v", r.Videos)
	}
	if r.Livre <= 0 {
		t.Fatalf("espaço livre: %d", r.Livre)
	}
}

func TestGrupoSoOsVideosEscolhidos(t *testing.T) {
	e := novoAmbiente(t)
	comTdlFalso(t)
	p := pedirEBaixar(t, e, map[string]any{"link": "https://t.me/c/1234567890/40", "pasta": "Escolhidos", "tudo": true, "mensagens": []int{40}})
	if p.Estado != "pronto" || p.Videos != 1 || p.Total != 1 || p.Previsto != 314572800 {
		t.Fatalf("%+v", p)
	}
	var r respostaDosMeusVideos
	e.json(t, "/api/meus-videos", &r)
	if len(r.Videos) != 1 || r.Videos[0].Nome != "Praia do Forte" {
		t.Fatalf("%+v", r.Videos)
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
	if !strings.Contains(string(usados), "export\n-c\n1234567890\n--with-content\n--raw") || !strings.Contains(string(usados), "-f\n") {
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

// Salvos: o grupo fica com a lista do rastreio; o vídeo, com o nome e o peso da mensagem. O "já
// baixado" é de agora, e rastrear de novo atualiza a lista salva.
func TestSalvarGrupoEVideo(t *testing.T) {
	e := novoAmbiente(t)
	comTdlFalso(t)

	grupo := map[string]any{"link": "https://t.me/c/1234567890/5", "tudo": true, "titulo": "Grupo Inventado 2026", "pasta": "Viagens",
		"videos": []map[string]any{{"mensagem": 5, "nome": "Vídeo 5", "tamanho": 52428800}, {"mensagem": 40, "nome": "Praia do Forte", "tamanho": 314572800}}}
	status, texto := e.pedir(t, "POST", "/api/meus-videos/salvos", grupo)
	if status != 200 {
		t.Fatalf("salvar grupo: %d %s", status, texto)
	}
	var s salvoDoTelegram
	json.Unmarshal([]byte(texto), &s)
	if s.Link != "https://t.me/c/1234567890" || !s.Tudo || len(s.Videos) != 2 || s.Pasta != "Viagens" {
		t.Fatalf("salvo: %+v", s)
	}

	status, texto = e.pedir(t, "POST", "/api/meus-videos/salvos", map[string]any{"link": "https://t.me/c/1234567890/40?single", "pasta": "Viagens"})
	if status != 200 {
		t.Fatalf("salvar vídeo: %d %s", status, texto)
	}
	var v salvoDoTelegram
	json.Unmarshal([]byte(texto), &v)
	if v.Link != "https://t.me/c/1234567890/40" || v.Tudo || v.Titulo != "Praia do Forte" || len(v.Videos) != 1 || v.Videos[0].Tamanho != 314572800 {
		t.Fatalf("vídeo salvo: %+v", v)
	}

	// Baixa a 40: nos dois salvos ela passa a "já baixado".
	pedirEBaixar(t, e, map[string]any{"link": "https://t.me/c/1234567890/5", "tudo": true, "pasta": "Viagens", "mensagens": []int64{40}})
	var r struct {
		Salvos []salvoDoTelegram `json:"salvos"`
		Livre  int64             `json:"livre"`
	}
	e.json(t, "/api/meus-videos", &r)
	if len(r.Salvos) != 2 || r.Livre <= 0 {
		t.Fatalf("salvos: %+v", r)
	}
	if !r.Salvos[0].Videos[0].Ja || r.Salvos[1].Videos[0].Ja || !r.Salvos[1].Videos[1].Ja {
		t.Fatalf("já baixados: %+v", r.Salvos)
	}

	// Rastrear o grupo de novo atualiza a lista salva (agora com a aula 4).
	status, texto = e.pedir(t, "POST", "/api/meus-videos/telegram/rastrear", map[string]string{"link": "https://t.me/c/1234567890/40"})
	if status != 200 || !strings.Contains(texto, fmt.Sprintf(`"salvo":%d`, s.ID)) {
		t.Fatalf("rastrear: %d %s", status, texto)
	}
	e.json(t, "/api/meus-videos", &r)
	if g := r.Salvos[1]; len(g.Videos) != 3 || g.Videos[0].Nome != "Aula 1 - Começo" {
		t.Fatalf("lista atualizada: %+v", g)
	}

	if status, _ := e.pedir(t, "DELETE", fmt.Sprintf("/api/meus-videos/salvos/%d", s.ID), nil); status != 204 {
		t.Fatal("apagar:", status)
	}
	e.json(t, "/api/meus-videos", &r)
	if len(r.Salvos) != 1 {
		t.Fatalf("depois de apagar: %+v", r.Salvos)
	}
}

// S55: o texto de cada vídeo de um grupo de curso traz o caminho (curso › módulo › aula).
func TestAulaDaMensagem(t *testing.T) {
	casos := []struct {
		texto, arquivo string
		pastas, nome   string
	}{
		// Nome genérico: a aula é o último nível; o "Indexado por @canal" sai.
		{"#F0002 aula\n\n002 - Bônus\n=001 - System design\n==002 - Plano de capacidade - Indexado por @canalqualquer", "aula.mp4",
			"002 - Bônus/001 - System design", "002 - Plano de capacidade"},
		{"#F002 1. Aula\n\n2. Fundamentos\n=1. O que é", "1. Aula.mp4", "2. Fundamentos", "1. O que é"},
		// Nome próprio: as pastas são o caminho todo.
		{"#F002 1. Conceitos Fundamentais\n\n1. Algoritmos\n=2. Masterclass", "x.mp4", "1. Algoritmos/2. Masterclass", "1. Conceitos Fundamentais"},
		{"#F001 1-Introdução\n\n01 - Introdução", "1-Introdução.mp4", "01 - Introdução", "1-Introdução"},
		// Sem etiqueta ou sem caminho: como antes.
		{"Praia do Forte\nque dia!", "VID.mp4", "", "Praia do Forte"},
		{"#Doc001\n\n#outra", "a.rar", "", "a"},
		{"", "Aula 3.mp4", "", "Aula 3"},
		// Depois do caminho, o resto é descrição.
		{"#F9 Nome\n\nCurso\n=Módulo\nUma descrição qualquer\n=não é pasta", "x.mp4", "Curso/Módulo", "Nome"},
	}
	for _, c := range casos {
		pastas, nome := aulaDaMensagem(c.texto, c.arquivo, 0)
		if got := strings.Join(pastas, "/"); got != c.pastas || nome != c.nome {
			t.Errorf("%q: pastas %q nome %q; queria %q e %q", c.texto, got, nome, c.pastas, c.nome)
		}
	}
	if got := limparPasta(" A / / B:C /"); got != "A/B C" {
		t.Fatal(got)
	}
}

// S55: baixar parte de um grupo de curso põe cada aula na pasta do curso e do módulo; o rastreio
// mostra o caminho e, depois de baixar, o id; a lixeira da pasta apaga o curso inteiro.
func TestGrupoDeCursoEmSubpastasELixeiraDaPasta(t *testing.T) {
	e := novoAmbiente(t)
	args := comTdlFalso(t)
	t.Setenv("TDL_FALSO_CURSO", "1")
	t.Setenv("TDL_LIMITE", "4") // S56: o paralelismo vem do ambiente
	t.Setenv("TDL_PARTES", "8")

	var rastreio struct {
		Videos []videoDoGrupo `json:"videos"`
	}
	if status, texto := e.pedir(t, "POST", "/api/meus-videos/telegram/rastrear", map[string]string{"link": "https://t.me/c/1234567890"}); status != 200 {
		t.Fatalf("%d %s", status, texto)
	} else if err := json.Unmarshal([]byte(texto), &rastreio); err != nil {
		t.Fatal(err)
	}
	caminhos := map[int64]string{}
	for _, v := range rastreio.Videos {
		caminhos[v.Mensagem] = strings.Join(v.Caminho, "/") + " › " + v.Nome
	}
	if caminhos[50] != "001 - Curso Inventado/001 - Módulo A › 001 - Primeira lição" || caminhos[52] != "002 - Outro Curso › Outro" {
		t.Fatalf("%v", caminhos)
	}

	p := pedirEBaixar(t, e, map[string]any{"link": "https://t.me/c/1234567890", "pasta": "Grupo Inventado", "tudo": true, "mensagens": []int64{50, 51}})
	if p.Estado != "pronto" || p.Videos != 2 {
		t.Fatalf("%+v", p)
	}
	if usados, _ := os.ReadFile(args); !strings.Contains(string(usados), "-l\n4\n-t\n8\n") {
		t.Fatalf("paralelismo:\n%s", usados)
	}
	var r respostaDosMeusVideos
	e.json(t, "/api/meus-videos", &r)
	var lista []string
	for _, v := range r.Videos {
		lista = append(lista, v.Pasta+" › "+v.Nome)
	}
	if got := strings.Join(lista, "|"); got != "Grupo Inventado/001 - Curso Inventado/001 - Módulo A › 001 - Primeira lição|Grupo Inventado/001 - Curso Inventado/002 - Módulo B › 2-Segunda" {
		t.Fatal(got)
	}
	if _, err := os.Stat(filepath.Join(e.a.pastaDosMeusVideos(), "Grupo Inventado", "001 - Curso Inventado", "002 - Módulo B", "2-Segunda.mp4")); err != nil {
		t.Fatal(err)
	}
	// O rastreio de novo: os baixados vêm com o id daqui.
	_, texto := e.pedir(t, "POST", "/api/meus-videos/telegram/rastrear", map[string]string{"link": "https://t.me/c/1234567890"})
	json.Unmarshal([]byte(texto), &rastreio)
	for _, v := range rastreio.Videos {
		if (v.Mensagem == 50 || v.Mensagem == 51) != (v.Ja && v.Video > 0) {
			t.Fatalf("%+v", v)
		}
	}

	// Arquivo posto à mão numa subpasta também aparece, com a pasta inteira.
	os.WriteFile(filepath.Join(e.a.pastaDosMeusVideos(), "Grupo Inventado", "001 - Curso Inventado", "extra.mp4"), []byte("v"), 0o644)
	e.json(t, "/api/meus-videos", &r)
	if len(r.Videos) != 3 {
		t.Fatalf("%+v", r.Videos)
	}

	status, texto := e.pedir(t, "POST", "/api/meus-videos/apagar-pasta", map[string]string{"pasta": "Grupo Inventado/001 - Curso Inventado"})
	if status != 200 || !strings.Contains(texto, `"apagados":3`) {
		t.Fatalf("%d %s", status, texto)
	}
	if _, err := os.Stat(filepath.Join(e.a.pastaDosMeusVideos(), "Grupo Inventado")); !os.IsNotExist(err) {
		t.Fatalf("as pastas vazias deviam sumir: %v", err)
	}
	e.json(t, "/api/meus-videos", &r)
	if len(r.Videos) != 0 {
		t.Fatalf("%+v", r.Videos)
	}
}

// S56: o tdl caiu no meio (erro interno): o pedido volta sozinho para a fila, sem aviso, até 3 vezes;
// depois fica com o erro, mostrando a linha do "panic:". Tentar de novo à mão zera a conta.
func TestTelegramTentaDeNovoDepoisDeUmaQueda(t *testing.T) {
	e := novoAmbiente(t)
	comTdlFalso(t)
	t.Setenv("TDL_FALSO_ERRO", "x\npanic: runtime error: index out of range\ngoroutine 1 [running]:\nerrgroup.go:78 +0x95")
	p := pedirEBaixar(t, e, map[string]any{"link": "https://t.me/c/1234567890", "pasta": "Curso", "tudo": true})
	if p.Estado != "na fila" || !strings.Contains(p.Mensagem, "tentando de novo (1 de 3)") {
		t.Fatalf("%+v", p)
	}
	for i := 0; i < 3; i++ {
		e.a.baixarDoTelegram(context.Background(), p.ID)
	}
	p, _ = e.a.pedidoDe(context.Background(), p.ID)
	if p.Estado != "erro" || !strings.Contains(p.Mensagem, "panic: runtime error") {
		t.Fatalf("%+v", p)
	}
	if status, _ := e.pedir(t, "POST", fmt.Sprintf("/api/meus-videos/telegram/%d/retomar", p.ID), nil); status != 200 {
		t.Fatal(status)
	}
	var feitas int
	e.a.db.QueryRow(`SELECT tentativas FROM pedidos_do_telegram WHERE id = ?`, p.ID).Scan(&feitas)
	if feitas != 0 {
		t.Fatal(feitas)
	}
}

// S57: disco que não dá para ler não é pasta vazia: a sincronização para sem apagar nada do banco
// e a lista sai do banco com um aviso.
func TestDiscoIlegivelNaoApagaOsVideos(t *testing.T) {
	e := novoAmbiente(t)
	dir := filepath.Join(e.a.pastaDosMeusVideos(), "Curso")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "aula.mp4"), []byte("v"), 0o644)
	var r struct {
		Videos []meuVideo `json:"videos"`
		Aviso  string     `json:"aviso"`
	}
	e.json(t, "/api/meus-videos", &r)
	if len(r.Videos) != 1 || r.Aviso != "" {
		t.Fatalf("%+v", r)
	}
	// A pasta raiz some do jeito que um disco travado some: existe, mas não dá para ler.
	raiz := e.a.pastaDosMeusVideos()
	os.Chmod(raiz, 0o000)
	defer os.Chmod(raiz, 0o755)
	e.json(t, "/api/meus-videos", &r)
	if len(r.Videos) != 1 || !strings.Contains(r.Aviso, "não consegui ler") {
		t.Fatalf("o vídeo devia continuar no banco, com aviso: %+v", r)
	}
}
