package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// O Claude (S27) entra sozinho onde as regras não resolvem: casar pelo sentido uma legenda em outro
// idioma com o que o Whisper ouviu, e dar um parecer quando a verificação de uma opção fica em
// dúvida (olhando os quadros do vídeo). Roda o Claude Code (`claude -p`) como no Acervo, com a
// assinatura do Lucas: o login vem de CLAUDE_CODE_OAUTH_TOKEN (gerado com "claude setup-token"),
// no .env do servidor, fora do Git. Sem o programa ou sem login, essas etapas são puladas.

func programaClaude() string { return env("CLAUDE_BIN", "claude") }
func modeloDoClaude() string { return env("CLAUDE_MODELO", "haiku") } // rápido e barato: as tarefas são curtas

// O Claude só pode ler (as imagens dos quadros); nada de comandos, internet ou escrita.
const ferramentasNegadas = "Bash,Write,Edit,WebFetch,WebSearch,Task,NotebookEdit,Glob,Grep"

func claudeDisponivel() bool {
	_, err := exec.LookPath(programaClaude())
	return err == nil
}

type respostaDoClaude struct {
	Resultado   string          `json:"result"`
	Erro        bool            `json:"is_error"`
	Estruturado json.RawMessage `json:"structured_output"`
}

// perguntarAoClaude manda o pedido e lê a resposta no formato do esquema (JSON Schema). pastaDeLeitura,
// se houver, é a única que ele pode ler (para as imagens).
func (a *app) perguntarAoClaude(ctx context.Context, pedido, esquema, pastaDeLeitura string, destino any) error {
	if !claudeDisponivel() {
		return errors.New("Claude Code não instalado neste servidor")
	}
	args := []string{"-p", pedido, "--output-format", "json", "--model", modeloDoClaude(), "--disallowedTools", ferramentasNegadas}
	if esquema != "" {
		args = append(args, "--json-schema", esquema)
	}
	dir := os.TempDir()
	if pastaDeLeitura != "" {
		os.MkdirAll(pastaDeLeitura, 0o755)
		args = append(args, "--allowedTools", "Read", "--add-dir", pastaDeLeitura)
		dir = pastaDeLeitura
	}
	cmd := exec.CommandContext(ctx, programaClaude(), args...)
	cmd.Env = ambienteDoClaude()
	cmd.Dir = dir
	var saida, erros bytes.Buffer
	cmd.Stdout, cmd.Stderr = &saida, &erros
	errExec := cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var r respostaDoClaude
	if err := json.Unmarshal(saida.Bytes(), &r); err != nil {
		if errExec != nil {
			return fmt.Errorf("o Claude falhou: %s", primeiraLinhaDe(erros.String(), errExec.Error()))
		}
		return errors.New("resposta do Claude ilegível")
	}
	if r.Erro || errExec != nil {
		msg := primeiraLinhaDe(r.Resultado, primeiraLinhaDe(erros.String(), "erro sem mensagem"))
		if strings.Contains(msg, "Not logged in") || strings.Contains(msg, "/login") {
			return errors.New("o Claude recusou o login: troque o token ou a conta no Hub, na tela Claude")
		}
		return fmt.Errorf("o Claude falhou: %s", msg)
	}
	if len(r.Estruturado) > 0 && string(r.Estruturado) != "null" {
		return json.Unmarshal(r.Estruturado, destino)
	}
	// Sem a resposta estruturada: procura o JSON no texto.
	ini, fim := strings.Index(r.Resultado, "{"), strings.LastIndex(r.Resultado, "}")
	if ini < 0 || fim < ini {
		return errors.New("o Claude não devolveu dados")
	}
	return json.Unmarshal([]byte(r.Resultado[ini:fim+1]), destino)
}

func primeiraLinhaDe(s, senao string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return senao
}

// ---------- Legenda em outro idioma ----------

const esquemaDosPares = `{"type":"object","properties":{
	"pares":{"type":"array","items":{"type":"array","items":{"type":"number"},"minItems":2,"maxItems":2}},
	"confianca":{"type":"string","enum":["alta","media","baixa"]}},
	"required":["pares","confianca"]}`

// casarPeloSentido pede ao Claude os pares (segundo no áudio, segundo na legenda) das falas que
// dizem a mesma coisa, em idiomas diferentes. Cada trecho ouvido vai com as falas da legenda dos
// 3 min em volta (a legenda pode estar deslocada até aí).
func (a *app) casarPeloSentido(ctx context.Context, ouvido transcricao, legenda []falaDaLegenda, idioma string) ([]parDeFalas, string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Você recebe o que foi falado em trechos de um vídeo (transcrição do áudio, em %s, com o segundo de cada fala) "+
		"e as falas de uma legenda em %s do mesmo vídeo, com o segundo em que cada uma aparece. A legenda é tradução do áudio, mas pode "+
		"estar deslocada (até 3 minutos) por ser de outra versão. Case as falas que dizem a mesma coisa e devolva os pares "+
		"[segundo_no_audio, segundo_na_legenda] mais seguros (até 15, de trechos diferentes, se houver). Se quase nada corresponder, "+
		"devolva poucos pares e confiança baixa.\n\nÁUDIO:\n", idiomaPorExtenso(ouvido.Idioma), idiomaPorExtenso(idioma))
	for _, f := range ouvido.Falas {
		fmt.Fprintf(&b, "%.1f %s\n", f.Inicio, f.Texto)
	}
	b.WriteString("\nLEGENDA:\n")
	for _, l := range legenda {
		for _, f := range ouvido.Falas {
			if l.inicio >= f.Inicio-180 && l.inicio <= f.Inicio+180 {
				fmt.Fprintf(&b, "%.1f %s\n", l.inicio, strings.Join(strings.Fields(l.texto), " "))
				break
			}
		}
	}
	var r struct {
		Pares     [][]float64 `json:"pares"`
		Confianca string      `json:"confianca"`
	}
	if err := a.perguntarAoClaude(ctx, b.String(), esquemaDosPares, "", &r); err != nil {
		return nil, "", err
	}
	var pares []parDeFalas
	for i, p := range r.Pares {
		if len(p) == 2 {
			pares = append(pares, parDeFalas{ouvida: i, audio: p[0], legenda: p[1]})
		}
	}
	return pares, r.Confianca, nil
}

// ---------- Parecer sobre uma opção da busca ----------

const esquemaDoParecer = `{"type":"object","properties":{
	"veredito":{"type":"string","enum":["ok","suspeito","errado"]},
	"explicacao":{"type":"string"}},
	"required":["veredito","explicacao"]}`

// parecerDoClaude: com tudo o que a verificação juntou (e os quadros, que ele olha), o Claude diz se
// a opção é o título, em uma ou duas frases para o Lucas.
func (a *app) parecerDoClaude(ctx context.Context, v verificacao, t tituloDoTMDB, nome string, ouvido string) (string, string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Ajude a decidir se um arquivo de vídeo baixado por torrent é mesmo o título abaixo, e se presta para assistir. "+
		"Responda em português do Brasil, em uma ou duas frases curtas, para alguém que não é técnico.\n\n"+
		"TÍTULO (TMDB): %s (%s), %s, %s.\n", t.Titulo, primeiro(t.Original, t.Titulo), primeiro(t.Ano, "ano ?"), map[string]string{"filme": "filme", "serie": "série"}[t.Tipo])
	if t.Duracao > 0 {
		fmt.Fprintf(&b, "Duração no TMDB: %d min.\n", t.Duracao)
	}
	if t.DigitalEm != "" {
		fmt.Fprintf(&b, "Lançamento em digital/disco: %s (hoje é %s).\n", t.DigitalEm, a.agora().Format("2006-01-02"))
	}
	if t.Sinopse != "" {
		fmt.Fprintf(&b, "Sinopse: %s\n", t.Sinopse)
	}
	fmt.Fprintf(&b, "\nNOME DO LANÇAMENTO: %s\nARQUIVO: %s\nVERIFICAÇÃO AUTOMÁTICA: %s — %s\n", nome, v.Arquivo, v.Estado, v.Resumo)
	for _, d := range v.Detalhes {
		b.WriteString("- " + d + "\n")
	}
	if ouvido != "" {
		b.WriteString("\nTRECHO DO ÁUDIO (transcrito):\n" + ouvido + "\n")
	}
	if len(v.Quadros) > 0 {
		b.WriteString("\nOlhe os quadros do vídeo (a 20%, 50% e 80%) nestes arquivos: " + strings.Join(v.Quadros, ", ") +
			". Uma gravação feita no cinema costuma ter imagem torta, escura, com bordas, cabeças ou a tela inclinada; uma cópia profissional, não.\n")
	}
	b.WriteString("\nVeredito: ok (é o título e presta), suspeito (dúvida, gravação de cinema ou qualidade ruim) ou errado (não é o título).")
	var r struct {
		Veredito   string `json:"veredito"`
		Explicacao string `json:"explicacao"`
	}
	pasta := ""
	if len(v.Quadros) > 0 {
		pasta = a.pastaDosQuadros()
	}
	if err := a.perguntarAoClaude(ctx, b.String(), esquemaDoParecer, pasta, &r); err != nil {
		return "", "", err
	}
	return r.Veredito, strings.TrimSpace(r.Explicacao), nil
}
