package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Double check da legenda gerada (S29): o Parakeet erra menos, mas às vezes pula ou troca palavras;
// o Whisper erra mais, mas acerta coisas que o Parakeet perdeu. Com folga à frente de quem assiste,
// o Whisper ouve de novo cada minuto e o Claude junta as duas transcrições, linha por linha, sem
// mexer nos tempos. No teste (Breaking Bad), o texto junto errou menos que cada um sozinho.

// Só revisa com pelo menos 5 min de legenda pronta à frente de quem assiste. Numa tela (TV ou
// projetor), 12 min: ela só lê a legenda quando o vídeo chega, e cada envio novo apaga a imagem;
// com mais à frente, os envios ficam espaçados (folgaParaReenviar).
const (
	folgaParaRevisar       = 300.0
	folgaParaRevisarNaTela = 720.0
)

// Minutos por pedido ao Claude.
const blocosPorRevisao = 5

func revisaoDisponivel(modelo string) bool {
	return modelo != "whisper" && urlDoWhisper() != "" && claudeDisponivel() && env("LEGENDA_REVISAO", "1") != "0"
}

// blocosParaRevisar: até 5 minutos seguidos, já ouvidos e não revisados, a partir de 2 min à frente
// de onde o Lucas está (a revisão leva uns 30 s por minuto: o que está passando agora não dá tempo);
// depois, os de antes.
func (a *app) blocosParaRevisar(ctx context.Context, chave string, feitos map[int]bool, total int, posicao float64) []int {
	revisados := map[int]bool{}
	linhas, err := a.db.QueryContext(ctx, `SELECT bloco FROM blocos_gerados WHERE chave = ? AND revisadas != ''`, chave)
	if err != nil {
		return nil
	}
	for linhas.Next() {
		var b int
		linhas.Scan(&b)
		revisados[b] = true
	}
	linhas.Close()
	inicio := min(max(0, int((posicao+120)/segundosPorBloco)), max(0, total-1))
	var grupo []int
	for i := 0; i < total && len(grupo) < blocosPorRevisao; i++ {
		b := (inicio + i) % total
		pronto := feitos[b] && !revisados[b]
		if pronto && (len(grupo) == 0 || b == grupo[len(grupo)-1]+1) {
			grupo = append(grupo, b)
		} else if len(grupo) > 0 {
			break
		}
	}
	return grupo
}

const esquemaDaRevisao = `{"type":"object","properties":{"linhas":{"type":"array","items":{"type":"string"}}},"required":["linhas"]}`

// revisarBlocos faz o double check de alguns minutos seguidos. Se algo falhar, os minutos ficam
// como o Parakeet ouviu (marcados para não tentar de novo).
func (a *app) revisarBlocos(ctx context.Context, chave string, grupo []int, fonte string, faixa int, idioma string, extras ...string) {
	falhou := func() {
		for _, b := range grupo {
			a.db.ExecContext(ctx, `UPDATE blocos_gerados SET revisadas = 'falhou' WHERE chave = ? AND bloco = ?`, chave, b)
		}
	}
	todos, err := a.blocosGerados(ctx, chave)
	if err != nil {
		return
	}
	noGrupo := map[int]bool{}
	for _, b := range grupo {
		noGrupo[b] = true
	}
	type linha struct {
		bloco int
		fala  falaGerada
	}
	var linhas []linha
	var ouvido strings.Builder
	for _, g := range todos {
		if !noGrupo[g.bloco] {
			continue
		}
		for _, f := range falasDasPalavras(g.palavras) {
			linhas = append(linhas, linha{g.bloco, f})
		}
		inicio := float64(g.bloco) * segundosPorBloco
		wav, err := audioDaFaixa(ctx, fonte, faixa, inicio, segundosPorBloco+sobraDoBloco, extras...)
		if err != nil {
			falhou()
			return
		}
		t, err := a.transcrever(ctx, wav, inicio)
		if err != nil {
			falhou()
			return
		}
		for _, f := range t.Falas {
			if f.Inicio < inicio+segundosPorBloco {
				fmt.Fprintf(&ouvido, "[%.1f] %s\n", f.Inicio, f.Texto)
			}
		}
	}
	if len(linhas) == 0 {
		for _, b := range grupo {
			a.db.ExecContext(ctx, `UPDATE blocos_gerados SET revisadas = '[]' WHERE chave = ? AND bloco = ?`, chave, b)
		}
		return
	}
	var pedido strings.Builder
	titulo := ""
	if p := a.progressoDe(ctx, chave); p != nil {
		titulo = strings.TrimSpace(p.Titulo + " " + p.Subtitulo)
	}
	fmt.Fprintf(&pedido, "Dois reconhecedores de fala transcreveram o mesmo trecho de um vídeo%s, falado em %s.\n"+
		"A é uma legenda, linha a linha, com o segundo de cada linha. Ele costuma acertar mais, mas às vezes pula ou troca palavras.\n"+
		"B ouviu o mesmo trecho: às vezes pega palavras que o A perdeu, mas também pula frases inteiras.\n"+
		"Corrija o texto de cada linha do A combinando os dois. Use só palavras que aparecem em pelo menos um deles (salvo erro "+
		"óbvio de grafia de um nome ou de uma palavra). Uma frase que só o B ouviu vai para a linha do A mais perto do segundo "+
		"em que o B a ouviu. Mantenha o idioma original (não traduza) e a pontuação natural.\n"+
		"Devolva exatamente %d linhas, na mesma ordem.\n\nA:\n", entreParenteses(titulo), idiomaPorExtenso(primeiro(idioma, "en")), len(linhas))
	for i, l := range linhas {
		fmt.Fprintf(&pedido, "%d. [%.1f] %s\n", i+1, l.fala.Inicio, l.fala.Texto)
	}
	pedido.WriteString("\nB:\n" + ouvido.String())
	c, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	var r struct {
		Linhas []string `json:"linhas"`
	}
	if err := a.perguntarAoClaude(c, pedido.String(), esquemaDaRevisao, "", &r); err != nil || len(r.Linhas) != len(linhas) {
		falhou()
		return
	}
	porBloco := map[int][]falaGerada{}
	for i, l := range linhas {
		f := l.fala
		if t := strings.TrimSpace(r.Linhas[i]); t != "" {
			f.Texto = t
		}
		porBloco[l.bloco] = append(porBloco[l.bloco], f)
	}
	for _, b := range grupo {
		dados, _ := json.Marshal(porBloco[b])
		if porBloco[b] == nil {
			dados = []byte("[]")
		}
		a.db.ExecContext(ctx, `UPDATE blocos_gerados SET revisadas = ? WHERE chave = ? AND bloco = ?`, string(dados), chave, b)
	}
}

func entreParenteses(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}
