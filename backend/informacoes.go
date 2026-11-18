package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Mais informações de um título (S47): o menu ⋯ da página do título pede ao Claude que pesquise na
// web (resumo, ficha, curiosidades e as fontes) e guarda, para não pesquisar de novo. Para títulos
// sem descrição no TMDB, ou com pouca.

const esquemaDasInformacoes = `
CREATE TABLE IF NOT EXISTS informacoes_de_titulos (
	tipo      TEXT NOT NULL,
	id        INTEGER NOT NULL,
	dados     TEXT NOT NULL, -- JSON: resumo, ficha, curiosidades, fontes
	modelo    TEXT NOT NULL,
	criada_em TEXT NOT NULL,
	PRIMARY KEY (tipo, id)
);
`

type informacoesDoTitulo struct {
	Resumo string `json:"resumo"`
	Ficha  []struct {
		Rotulo string `json:"rotulo"`
		Valor  string `json:"valor"`
	} `json:"ficha"`
	Curiosidades []string `json:"curiosidades,omitempty"`
	Fontes       []struct {
		Titulo string `json:"titulo,omitempty"`
		URL    string `json:"url"`
	} `json:"fontes"`
	Modelo   string `json:"modelo,omitempty"`
	CriadaEm string `json:"criada_em,omitempty"`
}

const esquemaDaPesquisa = `{"type":"object","properties":{
"resumo":{"type":"string","description":"3 a 6 frases sobre do que trata o título"},
"ficha":{"type":"array","items":{"type":"object","properties":{"rotulo":{"type":"string"},"valor":{"type":"string"}},"required":["rotulo","valor"]}},
"curiosidades":{"type":"array","items":{"type":"string"}},
"fontes":{"type":"array","items":{"type":"object","properties":{"titulo":{"type":"string"},"url":{"type":"string"}},"required":["url"]}}},
"required":["resumo","ficha","fontes"]}`

func (a *app) informacoesGuardadas(ctx context.Context, tipo string, id int) (*informacoesDoTitulo, error) {
	var dados, modelo, quando string
	err := a.db.QueryRowContext(ctx, `SELECT dados, modelo, criada_em FROM informacoes_de_titulos WHERE tipo = ? AND id = ?`, tipo, id).Scan(&dados, &modelo, &quando)
	if err != nil {
		return nil, err
	}
	var info informacoesDoTitulo
	if err := json.Unmarshal([]byte(dados), &info); err != nil {
		return nil, err
	}
	info.Modelo, info.CriadaEm = modelo, quando
	return &info, nil
}

func tipoEIdDoTitulo(r *http.Request) (string, int, bool) {
	tipo := r.PathValue("tipo")
	id, err := strconv.Atoi(r.PathValue("id"))
	return tipo, id, (tipo == "filme" || tipo == "serie") && err == nil && id > 0
}

func (a *app) handleInformacoes(w http.ResponseWriter, r *http.Request) {
	tipo, id, ok := tipoEIdDoTitulo(r)
	if !ok {
		erroJSON(w, http.StatusBadRequest, "Título inválido.")
		return
	}
	info, err := a.informacoesGuardadas(r.Context(), tipo, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusOK, map[string]any{"disponivel": claudeDisponivel()})
		return
	}
	if err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"disponivel": claudeDisponivel(), "informacoes": info})
}

// handleBuscarInformacoes pesquisa agora (uns 30 s) e guarda; pedir de novo pesquisa de novo.
func (a *app) handleBuscarInformacoes(w http.ResponseWriter, r *http.Request) {
	tipo, id, ok := tipoEIdDoTitulo(r)
	if !ok {
		erroJSON(w, http.StatusBadRequest, "Título inválido.")
		return
	}
	chave, ok := a.exigirTMDB(w)
	if !ok {
		return
	}
	t, err := a.tituloCompleto(r.Context(), chave, tipo, id)
	if err != nil {
		erroJSON(w, http.StatusBadGateway, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	var info informacoesDoTitulo
	if err := a.pesquisarComClaude(ctx, pedidoDeInformacoes(t), esquemaDaPesquisa, &info); err != nil {
		erroJSON(w, http.StatusBadGateway, "Não consegui pesquisar: "+err.Error())
		return
	}
	if strings.TrimSpace(info.Resumo) == "" && len(info.Ficha) == 0 {
		erroJSON(w, http.StatusNotFound, "A pesquisa não achou nada confiável sobre este título.")
		return
	}
	b, _ := json.Marshal(info)
	if _, err := a.db.ExecContext(r.Context(), `INSERT INTO informacoes_de_titulos (tipo, id, dados, modelo, criada_em) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (tipo, id) DO UPDATE SET dados = excluded.dados, modelo = excluded.modelo, criada_em = excluded.criada_em`,
		tipo, id, string(b), modeloDaPesquisa(), a.agoraEmTexto()); err != nil {
		falha(w, err)
		return
	}
	info.Modelo, info.CriadaEm = modeloDaPesquisa(), a.agoraEmTexto()
	writeJSON(w, http.StatusOK, map[string]any{"disponivel": true, "informacoes": info})
}

func pedidoDeInformacoes(t tituloDoTMDB) string {
	tipo := map[string]string{"filme": "filme", "serie": "série"}[t.Tipo]
	var b strings.Builder
	b.WriteString("Pesquise na web e escreva, em português do Brasil, informações sobre este título para a página de um app de streaming pessoal.\n")
	fmt.Fprintf(&b, "Título: %s (%s", t.Titulo, tipo)
	if len(t.Generos) > 0 {
		fmt.Fprintf(&b, ", %s", strings.Join(t.Generos, ", "))
	}
	if t.Ano != "" {
		fmt.Fprintf(&b, ", %s", t.Ano)
	}
	b.WriteString(").")
	if t.Original != "" {
		fmt.Fprintf(&b, " Título original: %s.", t.Original)
	}
	if t.ImdbID != "" {
		fmt.Fprintf(&b, " IMDb: %s.", t.ImdbID)
	}
	if t.Sinopse != "" {
		fmt.Fprintf(&b, "\nSinopse que já temos: %s", t.Sinopse)
	}
	b.WriteString("\nRegras: use só o que achar em fontes confiáveis (não invente); confira que é este título (ano e tipo); se algo não for encontrado, deixe de fora. Na ficha, direção, roteiro, elenco principal, país, duração, prêmios, o que houver. Curiosidades: até 5, só as que tiverem fonte. Responda só no formato pedido.")
	return b.String()
}
