package main

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Nível de inglês de um título (S48), pela legenda em inglês, para o Lucas escolher o que assistir
// treinando o listening. Sem IA, três medidas viram uma nota de 1 a 10:
//   - velocidade: palavras por minuto de fala (soma do tempo das falas);
//   - densidade: quanto do filme é fala;
//   - vocabulário: a parte das palavras que está entre as 5.000 mais comuns do inglês falado (lista
//     de frequência das legendas do OpenSubtitles, dados/frequencia_en.txt). Nomes próprios não contam.
// Calibrado em 18/11/2026 com as legendas em inglês do servidor: Esqueceram de Mim dá 3,9 (fácil);
// O Convite, ~5,5 (médio: vocabulário comum, mas quase o filme todo é conversa rápida); um
// documentário da 1ª Guerra, com depoimentos de época, 8,1 (difícil).
//
// Com IA, sob pedido ("Analisar com IA"): o Claude (Sonnet) lê uma amostra das falas e dá o nível do
// quadro europeu, os motivos e as expressões que travam. Em teste (4 títulos), o Sonnet separou
// melhor que o Haiku (que pôs Esqueceram de Mim em B2) e escolheu expressões mais úteis, em 9–12 s.

//go:embed dados/frequencia_en.txt
var listaDeFrequencia string

var (
	posicaoDaPalavra     map[string]int
	carregarFrequenciaOK sync.Once
)

func posicaoNaFrequencia(palavra string) int {
	carregarFrequenciaOK.Do(func() {
		posicaoDaPalavra = map[string]int{}
		for i, p := range strings.Split(listaDeFrequencia, "\n") {
			if p = strings.TrimSpace(p); p != "" {
				if _, ja := posicaoDaPalavra[p]; !ja {
					posicaoDaPalavra[p] = i + 1
				}
			}
		}
	})
	if p, ok := posicaoDaPalavra[palavra]; ok {
		return p
	}
	// A lista separa as contrações ("don't" vira "don" e "t"): vale a palavra de antes do apóstrofo.
	if antes, _, ok := strings.Cut(palavra, "'"); ok {
		if p, ok := posicaoDaPalavra[antes]; ok {
			return p
		}
	}
	return 60000
}

const esquemaDoNivel = `
CREATE TABLE IF NOT EXISTS niveis_de_ingles (
	tipo      TEXT NOT NULL,
	id        INTEGER NOT NULL,
	legenda   TEXT NOT NULL, -- a legenda em inglês medida (os-…)
	dados     TEXT NOT NULL, -- JSON: as medidas
	ia        TEXT NOT NULL DEFAULT '', -- JSON: a análise do Claude, se pedida
	criada_em TEXT NOT NULL,
	PRIMARY KEY (tipo, id)
);
`

type palavraRara struct {
	Palavra string `json:"palavra"`
	Vezes   int    `json:"vezes"`
}

type nivelDoIngles struct {
	Palavras      int           `json:"palavras"`
	PorMinuto     float64       `json:"por_minuto"`       // palavras por minuto de fala
	Fala          float64       `json:"fala"`             // % do tempo com fala
	Cobertura     float64       `json:"cobertura"`        // % das palavras entre as 5.000 mais comuns
	NotaVocab     float64       `json:"nota_vocabulario"` // 1 a 10 cada
	NotaVelocid   float64       `json:"nota_velocidade"`
	NotaDensidade float64       `json:"nota_densidade"`
	Nota          float64       `json:"nota"`
	Dificuldade   string        `json:"dificuldade"` // fácil, médio, difícil
	Nivel         string        `json:"nivel"`       // estimativa do quadro europeu (A2 a C2)
	Raras         []palavraRara `json:"raras,omitempty"`
}

type analiseDoIngles struct {
	Nivel       string `json:"nivel_cefr"`
	Dificuldade string `json:"dificuldade"`
	Nota        int    `json:"nota"`
	Motivos     string `json:"motivos"`
	Expressoes  []struct {
		Expressao string `json:"expressao"`
		Sentido   string `json:"sentido"`
	} `json:"expressoes"`
}

var (
	reMarcasDaFala = regexp.MustCompile(`<[^>]+>|\{[^}]*\}|\[[^\]]*\]|\([^)]*\)|♪[^♪]*♪?|\b[A-Z][A-Z ]+:\s`)
	rePalavraEN    = regexp.MustCompile(`[A-Za-z]+(?:'[A-Za-z]+)?`)
)

func entre(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// medirIngles calcula as medidas a partir de uma legenda SRT em inglês.
func medirIngles(srt []byte) (nivelDoIngles, error) {
	falas := falasDoSRT(srt, alinhamento{Escala: 1})
	if len(falas) < 50 {
		return nivelDoIngles{}, errors.New("legenda curta demais para medir")
	}
	var tempoDeFala float64
	maiuscula, minuscula := map[string]int{}, map[string]int{}
	var palavras []string
	for _, f := range falas {
		texto := strings.TrimSpace(reMarcasDaFala.ReplaceAllString(strings.ReplaceAll(f.texto, `\N`, " "), " "))
		if !rePalavraEN.MatchString(texto) {
			continue // só [sons] ou ♪ música: não é fala
		}
		tempoDeFala += f.fim - f.inicio
		for _, m := range rePalavraEN.FindAllStringIndex(texto, -1) {
			p := texto[m[0]:m[1]]
			minusc := strings.ToLower(p)
			palavras = append(palavras, minusc)
			antes := strings.TrimRight(texto[:m[0]], " ")
			if antes == "" || strings.ContainsAny(antes[len(antes)-1:], `.!?-"…`) {
				continue // começo de frase: a maiúscula não diz nada
			}
			if unicode.IsUpper(rune(p[0])) {
				maiuscula[minusc]++
			} else {
				minuscula[minusc]++
			}
		}
	}
	duracao := falas[len(falas)-1].fim - falas[0].inicio
	if len(palavras) < 300 || tempoDeFala <= 0 || duracao <= 0 {
		return nivelDoIngles{}, errors.New("legenda curta demais para medir")
	}
	// Nome próprio: sempre com maiúscula no meio da frase.
	nome := func(p string) bool { return p != "i" && maiuscula[p] >= 2 && minuscula[p] == 0 }
	comuns, contadas := 0, 0
	raras := map[string]int{}
	for _, p := range palavras {
		if nome(p) {
			continue
		}
		contadas++
		pos := posicaoNaFrequencia(p)
		if pos <= 5000 {
			comuns++
		}
		if pos > 8000 {
			raras[p]++
		}
	}
	n := nivelDoIngles{
		Palavras:  len(palavras),
		PorMinuto: math.Round(float64(len(palavras))/(tempoDeFala/60)*10) / 10,
		Fala:      math.Round(tempoDeFala/duracao*1000) / 10,
		Cobertura: math.Round(float64(comuns)/float64(max(1, contadas))*1000) / 10,
	}
	n.NotaVocab = math.Round(entre((97-n.Cobertura)/1.1+1, 1, 10)*10) / 10
	n.NotaVelocid = math.Round(entre((n.PorMinuto-130)/6.7+1, 1, 10)*10) / 10
	n.NotaDensidade = math.Round(entre((n.Fala-30)/6.7+1, 1, 10)*10) / 10
	n.Nota = math.Round((0.35*n.NotaVocab+0.4*n.NotaVelocid+0.25*n.NotaDensidade)*10) / 10
	switch {
	case n.Nota <= 4:
		n.Dificuldade = "fácil"
	case n.Nota <= 6.5:
		n.Dificuldade = "médio"
	default:
		n.Dificuldade = "difícil"
	}
	switch {
	case n.Nota < 3:
		n.Nivel = "A2"
	case n.Nota < 4.5:
		n.Nivel = "B1"
	case n.Nota < 6.5:
		n.Nivel = "B2"
	case n.Nota < 8:
		n.Nivel = "C1"
	default:
		n.Nivel = "C2"
	}
	for p, v := range raras {
		if v >= 2 {
			n.Raras = append(n.Raras, palavraRara{p, v})
		}
	}
	sort.Slice(n.Raras, func(i, j int) bool {
		if n.Raras[i].Vezes != n.Raras[j].Vezes {
			return n.Raras[i].Vezes > n.Raras[j].Vezes
		}
		return n.Raras[i].Palavra < n.Raras[j].Palavra
	})
	if len(n.Raras) > 20 {
		n.Raras = n.Raras[:20]
	}
	return n, nil
}

// ---------- A legenda em inglês do título ----------

// legendaEmInglesDoTitulo acha (pelo código do TMDB) e baixa a melhor legenda em inglês; de uma
// série, a do primeiro episódio. Cada legenda é baixada uma vez (a cota do OpenSubtitles é diária).
func (a *app) legendaEmInglesDoTitulo(ctx context.Context, tipo string, id int) (string, []byte, error) {
	chave := a.chaveDoOpenSubtitles()
	if chave == "" {
		return "", nil, errors.New("Configure o OpenSubtitles em Ajustes para medir o inglês.")
	}
	v := videoParaLegenda{idiomas: "en"}
	if tipo == "filme" {
		v.tmdbFilme = int64(id)
	} else {
		v.tmdbSerie, v.temporada, v.episodio = int64(id), 1, 1
	}
	opcoes, err := a.buscarLegendas(ctx, chave, v)
	if err != nil {
		return "", nil, err
	}
	for _, maquina := range []bool{false, true} {
		for _, o := range opcoes {
			if o.Idioma != "en" || o.Maquina != maquina {
				continue
			}
			caminho := a.arquivoDaLegenda(o.ID)
			if err := a.baixarLegenda(ctx, o.ID, caminho); err != nil {
				return "", nil, err
			}
			b, err := os.ReadFile(caminho)
			return o.ID, b, err
		}
	}
	return "", nil, errors.New("Não achei legenda em inglês para este título.")
}

// ---------- Rotas ----------

type nivelGuardado struct {
	Legenda  string           `json:"legenda"`
	Medidas  nivelDoIngles    `json:"medidas"`
	IA       *analiseDoIngles `json:"ia,omitempty"`
	CriadaEm string           `json:"criada_em"`
}

func (a *app) nivelGuardado(ctx context.Context, tipo string, id int) (*nivelGuardado, error) {
	var g nivelGuardado
	var dados, ia string
	err := a.db.QueryRowContext(ctx, `SELECT legenda, dados, ia, criada_em FROM niveis_de_ingles WHERE tipo = ? AND id = ?`, tipo, id).
		Scan(&g.Legenda, &dados, &ia, &g.CriadaEm)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(dados), &g.Medidas)
	if ia != "" {
		g.IA = &analiseDoIngles{}
		json.Unmarshal([]byte(ia), g.IA)
	}
	return &g, nil
}

func (a *app) handleNivelDeIngles(w http.ResponseWriter, r *http.Request) {
	tipo, id, ok := tipoEIdDoTitulo(r)
	if !ok {
		erroJSON(w, http.StatusBadRequest, "Título inválido.")
		return
	}
	g, err := a.nivelGuardado(r.Context(), tipo, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"pode_medir": a.chaveDoOpenSubtitles() != "", "pode_analisar": claudeDisponivel()})
		return
	}
	if err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pode_medir": true, "pode_analisar": claudeDisponivel(), "nivel": g})
}

// handleMedirIngles: sem IA, em poucos segundos (o tempo de achar e baixar a legenda).
func (a *app) handleMedirIngles(w http.ResponseWriter, r *http.Request) {
	tipo, id, ok := tipoEIdDoTitulo(r)
	if !ok {
		erroJSON(w, http.StatusBadRequest, "Título inválido.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	legenda, srt, err := a.legendaEmInglesDoTitulo(ctx, tipo, id)
	if err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	n, err := medirIngles(srt)
	if err != nil {
		erroJSON(w, http.StatusUnprocessableEntity, "Não deu para medir: "+err.Error())
		return
	}
	b, _ := json.Marshal(n)
	if _, err := a.db.ExecContext(r.Context(), `INSERT INTO niveis_de_ingles (tipo, id, legenda, dados, criada_em) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (tipo, id) DO UPDATE SET legenda = excluded.legenda, dados = excluded.dados, criada_em = excluded.criada_em`,
		tipo, id, legenda, string(b), a.agoraEmTexto()); err != nil {
		falha(w, err)
		return
	}
	a.handleNivelDeIngles(w, r)
}

const esquemaDaAnaliseDoIngles = `{"type":"object","properties":{
"nivel_cefr":{"type":"string","enum":["A2","B1","B2","C1","C2"]},
"dificuldade":{"type":"string","enum":["fácil","médio","difícil"]},
"nota":{"type":"integer"},
"motivos":{"type":"string"},
"expressoes":{"type":"array","items":{"type":"object","properties":{"expressao":{"type":"string"},"sentido":{"type":"string"}},"required":["expressao","sentido"]}}},
"required":["nivel_cefr","dificuldade","nota","motivos","expressoes"]}`

// amostraDasFalas: até 300 falas espalhadas pelo título inteiro (uns 2.000 a 2.600 palavras).
func amostraDasFalas(srt []byte) string {
	falas := falasDoSRT(srt, alinhamento{Escala: 1})
	passo := max(1, len(falas)/300)
	var linhas []string
	for i := 0; i < len(falas) && len(linhas) < 300; i += passo {
		if t := strings.TrimSpace(reMarcasDaFala.ReplaceAllString(strings.ReplaceAll(falas[i].texto, `\N`, " "), " ")); t != "" {
			linhas = append(linhas, t)
		}
	}
	return strings.Join(linhas, "\n")
}

// handleAnalisarIngles: o Claude lê a amostra (precisa ter medido antes: usa a mesma legenda).
func (a *app) handleAnalisarIngles(w http.ResponseWriter, r *http.Request) {
	tipo, id, ok := tipoEIdDoTitulo(r)
	if !ok {
		erroJSON(w, http.StatusBadRequest, "Título inválido.")
		return
	}
	g, err := a.nivelGuardado(r.Context(), tipo, id)
	if err != nil {
		erroJSON(w, http.StatusConflict, "Meça o inglês do título primeiro.")
		return
	}
	srt, err := os.ReadFile(a.arquivoDaLegenda(g.Legenda))
	if err != nil {
		erroJSON(w, http.StatusConflict, "A legenda medida não está mais no servidor: meça de novo.")
		return
	}
	pedido := "Abaixo, uma amostra das falas (legenda em inglês) de um filme ou série, em ordem. Avalie a dificuldade do inglês para um brasileiro que estuda inglês e quer assistir com legenda em inglês, treinando o listening.\n" +
		"Responda em português do Brasil, no formato pedido:\n- nivel_cefr: o nível do quadro europeu necessário para acompanhar (A2, B1, B2, C1 ou C2);\n- dificuldade: fácil, médio ou difícil;\n- nota: 1 (muito fácil) a 10 (muito difícil);\n" +
		"- motivos: 2 a 4 frases curtas (vocabulário, gírias, ritmo das falas, tipo de linguagem);\n- expressoes: até 10 gírias, expressões idiomáticas ou construções que podem travar quem estuda, com o sentido em português.\n" +
		fmt.Sprintf("Medidas sem IA, para referência: %.0f palavras por minuto de fala, %.0f%% do tempo com fala, %.1f%% das palavras entre as 5.000 mais comuns.\n", g.Medidas.PorMinuto, g.Medidas.Fala, g.Medidas.Cobertura) +
		"Amostra:\n" + amostraDasFalas(srt)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	var analise analiseDoIngles
	if err := a.consultarClaude(ctx, pedido, esquemaDaAnaliseDoIngles, "", modeloDaPesquisa(), false, &analise); err != nil {
		erroJSON(w, http.StatusBadGateway, "Não consegui analisar: "+err.Error())
		return
	}
	b, _ := json.Marshal(analise)
	if _, err := a.db.ExecContext(r.Context(), `UPDATE niveis_de_ingles SET ia = ? WHERE tipo = ? AND id = ?`, string(b), tipo, id); err != nil {
		falha(w, err)
		return
	}
	a.handleNivelDeIngles(w, r)
}
