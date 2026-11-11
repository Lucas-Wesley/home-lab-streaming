package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Avisos no celular pelo ntfy do homelab. NTFY_URL traz o servidor e o tópico (http://ntfy/acervo, http://ntfy/flashcards…);
// sem ela, o app não avisa. Só se avisa o que pede ação ou o que o Lucas está esperando, nunca
// "começou": aviso demais vira ruído.

type aviso struct {
	Titulo  string
	Texto   string
	Link    string // abre ao tocar na notificação
	Urgente bool   // falhas: prioridade alta e o ícone de alerta
}

type avisador struct {
	servidor string
	topico   string
	cliente  *http.Client
}

// novoAvisador devolve nil (não avisa) sem endereço ou com um endereço sem tópico.
func novoAvisador(endereco string) *avisador {
	u, err := url.Parse(strings.TrimRight(endereco, "/"))
	if err != nil || u.Host == "" {
		return nil
	}
	topico := strings.TrimPrefix(u.Path, "/")
	if topico == "" || strings.Contains(topico, "/") {
		log.Printf("NTFY_URL sem tópico (%s): os avisos ficam desligados", endereco)
		return nil
	}
	u.Path = "/"
	return &avisador{servidor: u.String(), topico: topico, cliente: &http.Client{Timeout: 10 * time.Second}}
}

// avisar manda em segundo plano: um ntfy fora do ar não atrasa nem derruba nada.
func (v *avisador) avisar(av aviso) {
	if v == nil {
		return
	}
	go func() {
		if err := v.enviar(context.Background(), av); err != nil {
			log.Printf("aviso %q não foi: %v", av.Titulo, err)
		}
	}()
}

func (v *avisador) enviar(ctx context.Context, av aviso) error {
	// JSON, e não cabeçalhos: o título e o texto têm acento.
	corpo := map[string]any{"topic": v.topico, "title": cortarAviso(av.Titulo, 120), "message": cortarAviso(av.Texto, 500)}
	if av.Link != "" {
		corpo["click"] = av.Link
	}
	if av.Urgente {
		corpo["priority"] = 4
		corpo["tags"] = []string{"warning"}
	}
	b, _ := json.Marshal(corpo)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.servidor, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.cliente.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("o ntfy respondeu %s", resp.Status)
	}
	return nil
}

func cortarAviso(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
