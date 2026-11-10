package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Busca única do Início (S28): o catálogo da assinatura e os títulos do TMDB numa lista só. Um
// título do TMDB que está na assinatura aparece uma vez (as cópias do catálogo, como "Duna" e
// "Duna [L]", entram nele); os outros podem ser vistos por torrent. O que o TMDB não conhece fica
// como veio do catálogo. A página de todos os títulos do TMDB é a mesma: #/titulo/{tipo}/{id}.

// juntarComOTMDB troca as listas de filmes e séries do resultado pela lista junta. Sem TMDB (ou se
// ele demora), fica só o catálogo.
func (a *app) juntarComOTMDB(ctx context.Context, texto string, resultado map[string][]item) {
	chave := a.chaveDoTMDB()
	if chave == "" || len([]rune(strings.TrimSpace(texto))) < 2 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	titulos, err := a.buscarNoTMDB(ctx, chave, strings.TrimSpace(texto))
	if err != nil {
		return
	}
	for _, tipo := range []string{"filme", "serie"} {
		a.tmdbDosItens(ctx, resultado[tipo])
		resultado[tipo] = juntar(tipo, titulos, resultado[tipo])
	}
}

func juntar(tipo string, titulos []tituloDoTMDB, catalogo []item) []item {
	var doTipo []tituloDoTMDB
	for _, t := range titulos {
		if t.Tipo == tipo {
			doTipo = append(doTipo, t)
		}
	}
	// Cada item do catálogo vai para um título só: primeiro pelo nome exato (ou pelo código já
	// conhecido); o que sobrar, pelo nome com subtítulo e o mesmo ano ("Breaking Bad: A Química do Mal").
	dono := make([]int, len(catalogo))
	for i, it := range catalogo {
		dono[i] = -1
		for j, t := range doTipo {
			if it.tmdb == t.ID || it.tmdb == 0 && casaComOTitulo(t, it) {
				dono[i] = j
				break
			}
		}
	}
	for i, it := range catalogo {
		if dono[i] >= 0 || it.tmdb != 0 {
			continue
		}
		for j, t := range doTipo {
			if casaComSubtitulo(t, it) {
				dono[i] = j
				break
			}
		}
	}

	junta := []item{}
	for j, t := range doTipo {
		novo := item{Tipo: tipo, Nome: t.Titulo, Capa: t.Capa, Nota: t.Nota, Ano: t.Ano, Origem: "torrent",
			Pagina: fmt.Sprintf("#/titulo/%s/%d", tipo, t.ID)}
		for i, it := range catalogo {
			if dono[i] == j {
				novo.Origem = "assinatura"
				if novo.Capa == "" {
					novo.Capa = it.Capa
				}
			}
		}
		// Fora da assinatura, só o que tem capa e alguma relevância (votos ou procura): o resto é
		// quase sempre um título obscuro que só polui a lista. Lançamentos novos têm procura.
		if novo.Origem == "torrent" && (novo.Capa == "" || t.votos < 20 && t.popularidade < 5) {
			continue
		}
		junta = append(junta, novo)
	}
	for i, it := range catalogo {
		if dono[i] < 0 {
			it.Origem = "assinatura"
			junta = append(junta, it)
		}
	}
	return junta
}

// casaComSubtitulo: o nome do catálogo é o título seguido de ":" ou " - " e um subtítulo, e os
// dois têm o mesmo ano. Fica para depois do nome exato: "Duna: Parte Dois" é outro filme que "Duna".
func casaComSubtitulo(t tituloDoTMDB, it item) bool {
	ano := primeiro(it.Ano, reAno.FindString(it.Nome))
	if ano == "" || ano != t.Ano {
		return false
	}
	nome := strings.ToLower(normalizar(it.Nome))
	for _, n := range []string{t.Titulo, t.Original} {
		c := strings.ToLower(normalizar(n))
		if c != "" && (strings.HasPrefix(nome, c+":") || strings.HasPrefix(nome, c+" -") || strings.HasPrefix(nome, c+" –")) {
			return true
		}
	}
	return false
}

// casaComOTitulo: o nome do catálogo é o título (em português ou o original) e, depois, só ano ou
// marcações ("[L]", "(4K)"); com ano dos dois lados, a diferença é de no máximo 1.
func casaComOTitulo(t tituloDoTMDB, it item) bool {
	nome := comparavel(it.Nome)
	for _, n := range []string{t.Titulo, t.Original} {
		c := comparavel(n)
		if c == "" || !strings.HasPrefix(nome, c) {
			continue
		}
		resto := nome[len(c):]
		if resto != "" && !reRestoDoCatalogo.MatchString(resto) {
			continue
		}
		ano := primeiro(it.Ano, reAno.FindString(resto))
		if ano != "" && t.Ano != "" && absInt(atoi(ano)-atoi(t.Ano)) > 1 {
			continue
		}
		return true
	}
	return false
}
