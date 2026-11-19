package main

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Frases de um filme para flashcards (S49), a parte sem IA. A legenda em inglês vira frases
// inteiras (uma fala, ou até três seguidas quando a frase continua), cada uma com o tempo no vídeo
// para cortar o áudio depois. Fica de fora o que não ensina: frase curta ou longa demais, resposta
// trivial, diálogo de duas pessoas na mesma fala (o áudio teria as duas vozes), nome próprio (é da
// história, não do dia a dia) e palavra fora das 20 mil mais comuns (gíria rara, nome, coisa do
// filme). O resto ganha uma nota:
//   - N+1: quantas palavras a frase tem que o Lucas ainda não estuda (fora das mais comuns e dos
//     cartões dos baralhos de inglês dele). O ideal é uma; duas vale menos; nenhuma, menos ainda
//     (treina o ouvido, mas não ensina palavra); três ou mais, fora.
//   - utilidade: a palavra nova ser comum no inglês falado vale mais.
//   - tamanho: de 6 a 14 palavras é o melhor para ouvir e repetir.
// Frase parecida com um cartão que ele já tem, ou com outra frase do filme, fica de fora.

type fraseDoFilme struct {
	Texto         string   `json:"texto"`
	Inicio        float64  `json:"inicio"` // segundos, nos tempos da legenda
	Fim           float64  `json:"fim"`
	Palavras      int      `json:"palavras"`
	Desconhecidas []string `json:"desconhecidas,omitempty"`
	Nota          float64  `json:"nota"`
}

// Quantas das palavras mais comuns contam como conhecidas, antes de os baralhos crescerem.
const palavrasConhecidasDeBase = 1500

const (
	minimoDePalavras = 5
	maximoDePalavras = 18
	// Palavra fora das 20 mil mais comuns: quase sempre nome, gíria rara ou coisa do filme.
	posicaoRara = 20000
)

var (
	reFimDeFrase       = regexp.MustCompile(`[.?!…]["'”’)]*$`)
	reReticencias      = regexp.MustCompile(`(\.\.\.|…)["'”’)]*$`)
	reQuemFala         = regexp.MustCompile(`^(?:[A-Z][A-Za-z.' ]{0,20}|[A-Z0-9 ]{2,}):\s+`)
	reSomEntre         = regexp.MustCompile(`\[[^\]]*\]|\([^)]*\)|\*[^*]*\*`)
	reEtiquetasDaFrase = regexp.MustCompile(`<[^>]+>|\{[^}]*\}`)
	reEspacos          = regexp.MustCompile(`\s+`)
	rePalavraFrase     = regexp.MustCompile(`[A-Za-z]+(?:'[A-Za-z]+)*`)
)

type falaComTempo struct {
	inicio, fim float64
	texto       string
	duasVozes   bool
}

// falasDaLegenda: as falas do SRT, já limpas (sem etiquetas, sons, nome de quem fala, ♪).
func falasDaLegenda(srt []byte) []falaComTempo {
	var out []falaComTempo
	for _, bloco := range strings.Split(strings.ReplaceAll(string(srt), "\r\n", "\n"), "\n\n") {
		linhas := strings.Split(strings.TrimSpace(bloco), "\n")
		for i, l := range linhas {
			m := reTempoDaFala.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			f := falaComTempo{inicio: segundosDoSRT(m[1], m[2], m[3], m[4]), fim: segundosDoSRT(m[5], m[6], m[7], m[8])}
			var partes []string
			tracos := 0
			for _, t := range linhas[i+1:] {
				t = strings.TrimSpace(reSomEntre.ReplaceAllString(reEtiquetasDaFrase.ReplaceAllString(t, ""), ""))
				if strings.Contains(t, "♪") || t == "" {
					continue
				}
				if strings.HasPrefix(t, "-") || strings.HasPrefix(t, "–") {
					tracos++
					t = strings.TrimSpace(strings.TrimLeft(t, "-– "))
				}
				t = reQuemFala.ReplaceAllString(t, "")
				if t != "" {
					partes = append(partes, t)
				}
			}
			f.texto = strings.TrimSpace(reEspacos.ReplaceAllString(strings.Join(partes, " "), " "))
			f.duasVozes = tracos >= 2
			if f.texto != "" {
				out = append(out, f)
			}
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].inicio < out[j].inicio })
	return out
}

// frasesDaLegenda junta as falas em frases inteiras: uma fala que não termina a frase continua na
// seguinte (até três, sem pausa longa). Fala de duas vozes quebra a frase e fica de fora.
func frasesDaLegenda(srt []byte) []fraseDoFilme {
	falas := falasDaLegenda(srt)
	var out []fraseDoFilme
	for i := 0; i < len(falas); {
		f := falas[i]
		if f.duasVozes {
			i++
			continue
		}
		texto, inicio, fim, j := f.texto, f.inicio, f.fim, i+1
		for (!reFimDeFrase.MatchString(texto) || reReticencias.MatchString(texto)) && j < len(falas) && j-i < 3 && !falas[j].duasVozes && falas[j].inicio-fim < 1.0 {
			texto = strings.TrimSuffix(strings.TrimSuffix(texto, "..."), "…") + " " + strings.TrimPrefix(strings.TrimPrefix(falas[j].texto, "..."), "…")
			fim = falas[j].fim
			j++
		}
		i = j
		texto = strings.TrimSpace(reEspacos.ReplaceAllString(texto, " "))
		if !reFimDeFrase.MatchString(texto) || reReticencias.MatchString(texto) {
			continue // a frase não terminou (cortada pela legenda, ou a fala foi interrompida)
		}
		if r := []rune(texto); !unicode.IsUpper(r[0]) {
			continue // começa no meio de uma frase
		}
		out = append(out, fraseDoFilme{Texto: texto, Inicio: inicio, Fim: fim, Palavras: len(rePalavraFrase.FindAllString(texto, -1))})
	}
	return out
}

// palavrasDaFrase: as palavras em minúsculas.
func palavrasDaFrase(s string) []string {
	var out []string
	for _, p := range rePalavraFrase.FindAllString(s, -1) {
		out = append(out, strings.ToLower(p))
	}
	return out
}

// nomesDoFilme: as palavras que, no meio da frase, aparecem quase sempre com maiúscula ("Angela",
// "Joe", "Maggie's"): são nomes da história, mesmo que "Joe" esteja entre as palavras comuns.
func nomesDoFilme(frases []fraseDoFilme) map[string]bool {
	maiuscula, minuscula := map[string]int{}, map[string]int{}
	for _, f := range frases {
		for i, p := range rePalavraFrase.FindAllString(f.Texto, -1) {
			base, _, _ := strings.Cut(p, "'")
			if i == 0 || base == "I" || base == "OK" {
				continue
			}
			if unicode.IsUpper([]rune(base)[0]) {
				maiuscula[strings.ToLower(base)]++
			} else {
				minuscula[strings.ToLower(base)]++
			}
		}
	}
	nomes := map[string]bool{}
	for p, n := range maiuscula {
		if n >= 1 && float64(n) >= 0.8*float64(n+minuscula[p]) {
			nomes[p] = true
		}
	}
	return nomes
}

// temNomeProprio: um nome do filme, ou palavra com maiúscula que não é comum ("Atri"), em qualquer
// posição; "I" e "OK" não contam.
func temNomeProprio(texto string, nomes map[string]bool) bool {
	for _, p := range rePalavraFrase.FindAllString(texto, -1) {
		base, _, _ := strings.Cut(p, "'")
		if base == "I" || base == "OK" {
			continue
		}
		if nomes[strings.ToLower(base)] || (unicode.IsUpper([]rune(base)[0]) && posicaoNaFrequencia(strings.ToLower(base)) > 3000) {
			return true
		}
	}
	return false
}

// palavrasConhecidas: as mais comuns, mais as dos cartões que o Lucas já tem.
func palavrasConhecidas(frentes []string) map[string]bool {
	conhecidas := map[string]bool{}
	posicaoNaFrequencia("the") // carrega a lista
	for p, pos := range posicaoDaPalavra {
		if pos <= palavrasConhecidasDeBase {
			conhecidas[p] = true
		}
	}
	for _, f := range frentes {
		for _, p := range palavrasDaFrase(f) {
			conhecidas[p] = true
		}
	}
	return conhecidas
}

func conhecida(p string, conhecidas map[string]bool) bool {
	if conhecidas[p] {
		return true
	}
	if antes, _, ok := strings.Cut(p, "'"); ok && conhecidas[antes] {
		return true // contração de palavra conhecida
	}
	// Plural, passado, gerúndio de uma palavra conhecida.
	for _, fim := range []string{"s", "es", "ed", "d", "ing"} {
		if base, ok := strings.CutSuffix(p, fim); ok && len(base) >= 3 && conhecidas[base] {
			return true
		}
	}
	return false
}

// frasesParecidas: as palavras das duas frases quase todas em comum.
func frasesParecidas(a, b map[string]bool) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	comum := 0
	for p := range a {
		if b[p] {
			comum++
		}
	}
	return float64(comum)/float64(len(a)+len(b)-comum) >= 0.6
}

func conjunto(palavras []string) map[string]bool {
	c := map[string]bool{}
	for _, p := range palavras {
		c[p] = true
	}
	return c
}

// pontuarFrases filtra e dá a nota (0 a 100) de cada frase; devolve da melhor para a pior, sem as
// parecidas entre si nem com os cartões que já existem.
func pontuarFrases(frases []fraseDoFilme, frentes []string) []fraseDoFilme {
	conhecidas := palavrasConhecidas(frentes)
	var jaTem []map[string]bool
	for _, f := range frentes {
		jaTem = append(jaTem, conjunto(palavrasDaFrase(f)))
	}
	nomes := nomesDoFilme(frases)
	var boas []fraseDoFilme
	for _, f := range frases {
		if f.Palavras < minimoDePalavras || f.Palavras > maximoDePalavras || temNomeProprio(f.Texto, nomes) {
			continue
		}
		// Hesitação no meio ("It's... It's probably") atrapalha o cartão.
		if strings.Contains(strings.TrimRight(f.Texto, ".…"), "...") || strings.Contains(f.Texto, "…") || strings.Contains(f.Texto, "--") {
			continue
		}
		palavras := palavrasDaFrase(f.Texto)
		var novas []string
		utilidade, rara := 1.0, false
		vistas := map[string]bool{}
		for _, p := range palavras {
			if vistas[p] || conhecida(p, conhecidas) {
				continue
			}
			vistas[p] = true
			pos := posicaoNaFrequencia(p)
			if pos > posicaoRara {
				rara = true
				break
			}
			novas = append(novas, p)
			utilidade = math.Min(utilidade, utilidadeDaPalavra(pos))
		}
		if rara || len(novas) > 2 {
			continue
		}
		base := map[int]float64{0: 0.35, 1: 1, 2: 0.6}[len(novas)]
		tamanho := 1.0
		if f.Palavras < 6 || f.Palavras > 14 {
			tamanho = 0.8
		}
		// Frase só de palavras muito comuns e curtas ("I don't know what to do.") ensina pouco.
		informativas := 0
		for p := range conjunto(palavras) {
			if posicaoNaFrequencia(p) > 150 {
				informativas++
			}
		}
		if informativas < 2 {
			continue
		}
		f.Desconhecidas = novas
		f.Nota = math.Round(base*utilidade*tamanho*1000) / 10
		boas = append(boas, f)
	}
	sort.SliceStable(boas, func(i, j int) bool { return boas[i].Nota > boas[j].Nota })

	var escolhidas []fraseDoFilme
	var vistas []map[string]bool
	for _, f := range boas {
		c := conjunto(palavrasDaFrase(f.Texto))
		repetida := false
		for _, j := range append(jaTem, vistas...) {
			if frasesParecidas(c, j) {
				repetida = true
				break
			}
		}
		if !repetida {
			escolhidas = append(escolhidas, f)
			vistas = append(vistas, c)
		}
	}
	return escolhidas
}

// utilidadeDaPalavra: 1 para as 3 mil mais comuns, caindo até 0,3 nas 20 mil.
func utilidadeDaPalavra(pos int) float64 {
	switch {
	case pos <= 3000:
		return 1
	case pos <= 10000:
		return 1 - 0.5*float64(pos-3000)/7000
	default:
		return 0.5 - 0.2*float64(pos-10000)/10000
	}
}
