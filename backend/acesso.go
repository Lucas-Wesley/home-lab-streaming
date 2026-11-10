package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// O acesso entra pelo app (S2): o Lucas cola a mensagem do provedor como veio, ou só o link da
// lista, e o servidor tira dela o servidor, o usuário e a senha.

var (
	reLink = regexp.MustCompile(`https?://[^\s"'<>*]+`)
	// "Usuário: 123", "*Senha:* 456", "User = x". O rótulo vem antes de ":" ou "=".
	reCampo = regexp.MustCompile(`(?im)^[\s*_~>-]*(usu[aá]rio|user(?:name)?|login|senha|password|pass|dns|servidor|server|host|url|portal)\s*[*_~]*\s*[:=]\s*[*_~]*\s*(\S+)`)
)

// lerAcesso tira o acesso do texto colado. Prefere o link da lista (get.php ou player_api.php),
// que traz tudo junto; senão, junta os campos rotulados da mensagem.
func lerAcesso(texto string) (acesso, error) {
	var ac acesso
	var outros []*url.URL
	for _, bruto := range reLink.FindAllString(texto, -1) {
		u, err := url.Parse(strings.TrimRight(bruto, ".,;)]"))
		if err != nil || u.Host == "" {
			continue
		}
		q := u.Query()
		if q.Get("username") != "" && q.Get("password") != "" {
			return acesso{Servidor: raiz(u), Usuario: q.Get("username"), Senha: q.Get("password")}, nil
		}
		outros = append(outros, u)
	}

	for _, m := range reCampo.FindAllStringSubmatch(texto, -1) {
		rotulo, valor := strings.ToLower(m[1]), strings.Trim(m[2], "*_~")
		switch {
		case strings.HasPrefix(rotulo, "usu") || strings.HasPrefix(rotulo, "user") || rotulo == "login":
			if ac.Usuario == "" {
				ac.Usuario = valor
			}
		case rotulo == "senha" || strings.HasPrefix(rotulo, "pass"):
			if ac.Senha == "" {
				ac.Senha = valor
			}
		default:
			// Só o primeiro servidor: "DNS STB" e afins, que vêm depois, são para outros aparelhos.
			if ac.Servidor == "" {
				if u, err := url.Parse(valor); err == nil && u.Host != "" && strings.HasPrefix(u.Scheme, "http") {
					ac.Servidor = raiz(u)
				}
			}
		}
	}
	if ac.Servidor == "" && len(outros) > 0 {
		// Um link sem usuário e senha (o "DNS" sozinho numa linha sem rótulo).
		ac.Servidor = raiz(outros[0])
	}

	switch {
	case ac.Usuario == "" || ac.Senha == "":
		return ac, errors.New("Não achei o usuário e a senha. Cole a mensagem do provedor inteira ou o link da lista M3U.")
	case ac.Servidor == "":
		return ac, errors.New("Não achei o endereço do servidor (DNS). Cole a mensagem inteira ou o link da lista M3U.")
	}
	return ac, nil
}

func raiz(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

func (a *app) acesso() (acesso, error) {
	var ac acesso
	v, err := a.ajuste("acesso")
	if err != nil || v == "" {
		return ac, err
	}
	err = json.Unmarshal([]byte(v), &ac)
	return ac, err
}

// situacaoDaConta é o que o app mostra sobre a assinatura, sem a senha.
type situacaoDaConta struct {
	Configurado  bool   `json:"configurado"`
	Servidor     string `json:"servidor,omitempty"`
	Usuario      string `json:"usuario,omitempty"`
	Status       string `json:"status,omitempty"`
	VenceEm      string `json:"vence_em,omitempty"`
	Conexoes     int64  `json:"conexoes,omitempty"`
	Teste        bool   `json:"teste,omitempty"`
	VerificadoEm string `json:"verificado_em,omitempty"`
}

func (a *app) situacao() (situacaoDaConta, error) {
	ac, err := a.acesso()
	if err != nil || !ac.completo() {
		return situacaoDaConta{}, err
	}
	s := situacaoDaConta{Configurado: true}
	if v, _ := a.ajuste("conta"); v != "" {
		_ = json.Unmarshal([]byte(v), &s)
	}
	s.Configurado, s.Servidor, s.Usuario = true, ac.Servidor, ac.Usuario
	return s, nil
}

func (a *app) guardarConta(c contaDoProvedor) error {
	s := situacaoDaConta{
		Status:       c.UserInfo.Status,
		Conexoes:     c.UserInfo.MaxConnections.int(),
		Teste:        c.UserInfo.IsTrial.int() == 1,
		VerificadoEm: a.carimbo(),
	}
	if exp := c.UserInfo.ExpDate.int(); exp > 0 {
		s.VenceEm = time.Unix(exp, 0).UTC().Format(time.RFC3339)
	}
	b, _ := json.Marshal(s)
	return a.guardarAjuste("conta", string(b))
}

func (a *app) handleAcesso(w http.ResponseWriter, r *http.Request) {
	s, err := a.situacao()
	if err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// handleGuardarAcesso recebe o texto colado, confere com o provedor e, se ele aceitar, guarda e
// começa a baixar o catálogo.
func (a *app) handleGuardarAcesso(w http.ResponseWriter, r *http.Request) {
	var corpo struct {
		Texto string `json:"texto"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	ac, err := lerAcesso(corpo.Texto)
	if err != nil {
		erroJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	conta, err := a.consultarConta(r.Context(), ac)
	if errors.Is(err, errAcessoRecusado) {
		erroJSON(w, http.StatusBadRequest, "O provedor recusou esse usuário e senha. Confira se a assinatura está ativa.")
		return
	}
	if err != nil {
		erroJSON(w, http.StatusBadGateway, "Não consegui falar com o provedor: "+err.Error())
		return
	}
	b, _ := json.Marshal(ac)
	if err := a.guardarAjuste("acesso", string(b)); err != nil {
		falha(w, err)
		return
	}
	if err := a.guardarConta(conta); err != nil {
		falha(w, err)
		return
	}
	a.emFundo(func() { a.sincronizar(context.Background()) })
	a.handleAcesso(w, r)
}

func (a *app) handleApagarAcesso(w http.ResponseWriter, r *http.Request) {
	if _, err := a.db.ExecContext(r.Context(), `DELETE FROM ajustes WHERE chave IN ('acesso', 'conta')`); err != nil {
		falha(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
