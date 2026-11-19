package main

import (
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------- Quem está assistindo (S51) ----------

var reAparelho = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// donoDoAparelho dá o dono da conexão para um aparelho do app: "app:<id>", ou "app" sem id (as
// versões antigas do app e o primeiro pedido depois de o servidor ligar).
func donoDoAparelho(id string) string {
	if !reAparelho.MatchString(id) {
		return "app"
	}
	return "app:" + id
}

// handleQuemAssiste diz ao player quem tem a conexão: ele confere de tempos em tempos e, se
// outro aparelho pegou, para e avisa em vez de travar sem explicação.
func (a *app) handleQuemAssiste(w http.ResponseWriter, r *http.Request) {
	chave, dono, nome, titulo, desde := a.conexao.quem()
	tipo := ""
	switch {
	case dono == "":
	case dono == donoPorTras:
		tipo = "servidor"
	case dono == "app" || strings.HasPrefix(dono, "app:"):
		tipo = "aparelho"
	default:
		tipo = "tela"
		if t, err := a.tela(r.Context(), dono); err == nil {
			nome = t.Nome
		}
	}
	resposta := map[string]any{"chave": chave, "dono": dono, "tipo": tipo, "nome": nome, "titulo": titulo}
	if !desde.IsZero() {
		resposta["desde"] = desde.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resposta)
}

// ---------- Versões de um canal (S51) ----------

type versaoAlternativa struct {
	ID        int64  `json:"id"`
	Nome      string `json:"nome"`
	Qualidade string `json:"qualidade"` // "4K", "FHD", "HD", "SD" ou ""
	H265      bool   `json:"h265,omitempty"`
	Reserva   bool   `json:"reserva,omitempty"` // servidor reserva (² ³)
	nota      int
}

// qualidadeDoNome lê a qualidade que vem no fim do nome do canal ("ESPN 1 FHD H265²").
func qualidadeDoNome(nome string) (qualidade string, h265, reserva bool) {
	limpo := strings.TrimRight(strings.TrimSpace(nome), "²³*¹ ")
	reserva = limpo != strings.TrimSpace(nome)
	campos := strings.Fields(limpo)
	for i := len(campos) - 1; i > 0; i-- {
		q := strings.ToLower(campos[i])
		if _, ok := qualidades[q]; !ok {
			break
		}
		switch q {
		case "h265", "h.265", "265":
			h265 = true
		case "uhd", "4k":
			qualidade = "4K"
		default:
			if qualidade == "" {
				qualidade = strings.ToUpper(q)
			}
		}
	}
	return
}

// handleAlternativas lista as versões do mesmo canal (SD, HD, FHD, H265, servidores reserva), da
// melhor para a pior, e os outros canais do mesmo grupo (Premiere 2, Premiere 4…): no player do ao
// vivo, para trocar sem sair quando um trava.
func (a *app) handleAlternativas(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var nomeDoCanal string
	if err := a.db.QueryRowContext(r.Context(), `SELECT nome FROM itens WHERE tipo = 'canal' AND id = ?`, id).Scan(&nomeDoCanal); err != nil {
		http.NotFound(w, r)
		return
	}
	base, _ := baseDoCanal(nomeDoCanal)
	alvo := normalizar(base)

	rows, err := a.db.QueryContext(r.Context(), `SELECT i.id, i.nome FROM itens i WHERE i.tipo = 'canal' AND `+filtroAdulto+` ORDER BY i.ordem`, a.mostrarAdulto())
	if err != nil {
		falha(w, err)
		return
	}
	versoes := []versaoAlternativa{}
	for rows.Next() {
		var v versaoAlternativa
		if err := rows.Scan(&v.ID, &v.Nome); err != nil {
			rows.Close()
			falha(w, err)
			return
		}
		b, nota := baseDoCanal(v.Nome)
		if normalizar(b) != alvo {
			continue
		}
		v.nota = nota
		v.Qualidade, v.H265, v.Reserva = qualidadeDoNome(v.Nome)
		versoes = append(versoes, v)
	}
	rows.Close()
	sort.SliceStable(versoes, func(i, j int) bool { return versoes[i].nota > versoes[j].nota })

	irmaos := []canalAgrupado{}
	grupo := ""
	if grupos, err := a.gruposComCanais(r.Context()); err == nil {
	procurar:
		for _, g := range grupos {
			for _, c := range g.Canais {
				for _, v := range c.Versoes {
					if v.ID == id {
						grupo = g.Nome
						for _, outro := range g.Canais {
							if outro.chave != c.chave {
								irmaos = append(irmaos, outro)
							}
						}
						break procurar
					}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nome": base, "versoes": versoes, "grupo": grupo, "irmaos": irmaos})
}
