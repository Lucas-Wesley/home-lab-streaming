package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
)

// Legenda dupla nas telas (S46): pronta, vai o MP4 (avança e volta como qualquer vídeo); ainda
// gravando, vão os pedaços prontos a partir do ponto, num fluxo contínuo, e "ir para" manda de novo.

var errDuplaSemPedido = errors.New("Peça a legenda dupla deste vídeo primeiro.")
var errDuplaAindaNao = errors.New("A legenda dupla ainda não foi gravada neste ponto.")

func (c *controleDasTelas) tocarDupla(ctx context.Context, t tela, tipo string, id int64, posicao float64, capa string) (sessao, error) {
	if tipo != "arquivo" {
		return sessao{}, errDuplaSemPedido
	}
	chave := fmt.Sprintf("arquivo:%d", id)
	d, err := c.a.legendaDuplaDe(ctx, chave)
	if err != nil {
		return sessao{}, errDuplaSemPedido
	}
	f, err := c.a.fonteDe(ctx, tipo, id, "")
	if err != nil {
		return sessao{}, err
	}
	base, err := c.baseDasTelas(t)
	if err != nil {
		return sessao{}, err
	}
	envio := c.a.agora().UnixNano()
	pronta := c.a.duplaPronta(chave) != ""
	s := sessao{Chave: chave, Titulo: f.titulo, Capa: capa, Inicio: posicao, Desde: c.a.carimbo(), Dupla: true}
	var endereco, mime string
	if pronta {
		endereco, mime = fmt.Sprintf("%s/tela/arquivo/%d.mp4?dupla=1&envio=%d", base, id, envio), "video/mp4"
	} else {
		inicio, ok := c.a.inicioDoFluxo(chave, d.Comeco, posicao)
		if !ok {
			return sessao{}, errDuplaAindaNao
		}
		endereco, mime = fmt.Sprintf("%s/tela/arquivo/%d.ts?dupla=1&desde=%.3f&envio=%d", base, id, inicio, envio), "video/mp2t"
		s.Crescendo, s.deslocamento, s.duracaoDoFilme, s.Inicio = true, inicio, d.Duracao, inicio
	}
	_, _ = c.acao(ctx, t, "Stop", nil)
	// Sem legenda externa: as duas já estão na imagem.
	if _, err := c.acao(ctx, t, "SetAVTransportURI", [][2]string{{"CurrentURI", endereco}, {"CurrentURIMetaData", didl(f.titulo, capa, endereco, mime, s.Crescendo, "")}}); err != nil {
		return sessao{}, err
	}
	if _, err := c.acao(ctx, t, "Play", [][2]string{{"Speed", "1"}}); err != nil {
		return sessao{}, err
	}
	c.mu.Lock()
	c.sessoes[t.ID] = s
	c.mu.Unlock()
	if pronta && posicao > 5 {
		go c.irQuandoTocar(t, posicao)
	}
	go c.acompanhar(t, s.Desde)
	return s, nil
}

// irNaDupla: "ir para" e "pular" com a dupla ainda gravando. A tela não avança num fluxo contínuo:
// o vídeo vai de novo, do ponto pedido, até onde já está gravado (um pouco antes do fim gravado).
func (c *controleDasTelas) irNaDupla(ctx context.Context, t tela, s sessao, alvo float64) error {
	d, err := c.a.legendaDuplaDe(ctx, s.Chave)
	if err != nil {
		return errDuplaSemPedido
	}
	alvo = math.Max(0, alvo)
	if _, ok := c.a.inicioDoFluxo(s.Chave, d.Comeco, alvo); !ok && c.a.duplaPronta(s.Chave) == "" {
		// Fora do que está gravado: o fim do trecho mais perto antes do alvo, com folga.
		melhor := -1.0
		for _, f := range d.Faixas {
			if f[0] <= alvo {
				melhor = math.Max(f[0], f[1]-2*pedacoDaDupla)
			}
		}
		if melhor < 0 {
			return errDuplaAindaNao
		}
		alvo = melhor
	}
	var id int64
	fmt.Sscanf(s.Chave, "arquivo:%d", &id)
	_, err = c.tocarDupla(ctx, t, "arquivo", id, alvo, s.Capa)
	return err
}

// carregadoDe: os trechos (em segundos) que a tela pode tocar sem esperar, para a barra clara do
// controle. A dupla: o que já foi gravado. O torrent: o que o motor já tem (a proporção dos bytes
// vale como proporção do tempo). Baixado ou da assinatura: nada (a barra fica só com o progresso).
func (a *app) carregadoDe(ctx context.Context, s sessao, duracao float64) [][2]float64 {
	if s.Dupla {
		if d, err := a.legendaDuplaDe(ctx, s.Chave); err == nil && a.duplaPronta(s.Chave) == "" {
			return d.Faixas
		}
		return nil
	}
	var id int64
	if _, err := fmt.Sscanf(s.Chave, "arquivo:%d", &id); err != nil || duracao <= 0 {
		return nil
	}
	f, _, err := a.arquivo(ctx, id)
	if err != nil || f.Download == "baixado" {
		return nil
	}
	var out [][2]float64
	for _, p := range a.motor.trechosBaixados(f.Hash, f.Indice) {
		out = append(out, [2]float64{p[0] * duracao, p[1] * duracao})
	}
	return out
}

// servirDupla: o MP4 pronto (app e telas, com avançar); ainda gravando, só as telas, em fluxo.
func (a *app) servirDupla(w http.ResponseWriter, r *http.Request, id int64, paraTela bool) {
	chave := fmt.Sprintf("arquivo:%d", id)
	if c := a.duplaPronta(chave); c != "" {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Cache-Control", "no-store")
		if paraTela {
			cabecalhosDLNA(w, fonte{})
		}
		http.ServeFile(w, r, c)
		return
	}
	if !paraTela {
		http.Error(w, "A legenda dupla ainda está sendo gravada.", http.StatusConflict)
		return
	}
	desde, _ := strconv.ParseFloat(r.URL.Query().Get("desde"), 64)
	a.servirDuplaCrescendo(w, r, chave, desde)
}
