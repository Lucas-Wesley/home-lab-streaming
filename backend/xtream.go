package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Cliente da API dos painéis no padrão Xtream Codes (player_api.php), que é o da maioria dos
// provedores de IPTV. Os links de vídeo seguem o mesmo padrão:
//   <servidor>/live/<usuario>/<senha>/<id>.<ts|m3u8>
//   <servidor>/movie/<usuario>/<senha>/<id>.<extensao>
//   <servidor>/series/<usuario>/<senha>/<id do episódio>.<extensao>

// Alguns painéis recusam clientes desconhecidos; o do VLC é aceito por todos os que testamos.
const agenteDoPlayer = "VLC/3.0.20 LibVLC/3.0.20"

type acesso struct {
	Servidor string `json:"servidor"` // esquema, host e porta, sem barra no fim
	Usuario  string `json:"usuario"`
	Senha    string `json:"senha"`
}

func (ac acesso) completo() bool {
	return ac.Servidor != "" && ac.Usuario != "" && ac.Senha != ""
}

func (ac acesso) urlDaApi(acao string, extra url.Values) string {
	q := url.Values{"username": {ac.Usuario}, "password": {ac.Senha}}
	if acao != "" {
		q.Set("action", acao)
	}
	for k, v := range extra {
		q[k] = v
	}
	return ac.Servidor + "/player_api.php?" + q.Encode()
}

// urlDoVideo monta o link do provedor. tipo: live, movie ou series.
func (ac acesso) urlDoVideo(tipo string, id int64, extensao string) string {
	return fmt.Sprintf("%s/%s/%s/%s/%d.%s", ac.Servidor, tipo, url.PathEscape(ac.Usuario), url.PathEscape(ac.Senha), id, extensao)
}

// flex aceita texto ou número no JSON: o painel manda "8", 8 ou 5.9 para o mesmo campo.
type flex string

func (f *flex) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*f = ""
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var t string
		if err := json.Unmarshal(b, &t); err != nil {
			return err
		}
		*f = flex(t)
		return nil
	}
	*f = flex(s)
	return nil
}

func (f flex) int() int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(string(f)), 10, 64)
	return n
}

func (f flex) float() float64 {
	n, _ := strconv.ParseFloat(strings.TrimSpace(string(f)), 64)
	return n
}

type contaDoProvedor struct {
	UserInfo struct {
		Auth           flex   `json:"auth"`
		Status         string `json:"status"`
		ExpDate        flex   `json:"exp_date"`
		MaxConnections flex   `json:"max_connections"`
		ActiveCons     flex   `json:"active_cons"`
		IsTrial        flex   `json:"is_trial"`
	} `json:"user_info"`
}

type categoriaDoProvedor struct {
	ID   flex   `json:"category_id"`
	Nome string `json:"category_name"`
}

type itemDoProvedor struct {
	Num         flex   `json:"num"`
	Nome        string `json:"name"`
	StreamID    flex   `json:"stream_id"`
	SeriesID    flex   `json:"series_id"`
	Icone       string `json:"stream_icon"`
	Capa        string `json:"cover"`
	Nota        flex   `json:"rating"`
	Categoria   flex   `json:"category_id"`
	Extensao    string `json:"container_extension"`
	Adicionado  flex   `json:"added"`
	Modificado  flex   `json:"last_modified"`
	Lancamento  string `json:"releaseDate"`
	Lancamento2 string `json:"release_date"`
	Ano         flex   `json:"year"`
}

var errAcessoRecusado = errors.New("o provedor recusou o usuário e a senha")

func (a *app) buscarJSON(ctx context.Context, endereco string, destino any) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endereco, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", agenteDoPlayer)
	resp, err := a.http.Do(req)
	if err != nil {
		return semSegredos(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errAcessoRecusado
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("o provedor respondeu %d", resp.StatusCode)
	}
	corpo, err := io.ReadAll(resp.Body)
	if err != nil {
		return semSegredos(err)
	}
	if err := json.Unmarshal(corpo, destino); err != nil {
		return fmt.Errorf("resposta inesperada do provedor: %w", err)
	}
	return nil
}

// semSegredos tira da mensagem de erro o endereço pedido, que leva usuário e senha.
func semSegredos(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("sem resposta do provedor (%v)", ue.Err)
	}
	return err
}

func (a *app) consultarConta(ctx context.Context, ac acesso) (contaDoProvedor, error) {
	var c contaDoProvedor
	if err := a.buscarJSON(ctx, ac.urlDaApi("", nil), &c); err != nil {
		return c, err
	}
	if c.UserInfo.Auth.int() != 1 {
		return c, errAcessoRecusado
	}
	return c, nil
}
