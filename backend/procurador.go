package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"
)

// A busca SSDP precisa da rede de casa: o multicast não sai da rede do Docker, e o projetor responde
// de outra porta, que o NAT descarta. Em produção ela roda num segundo processo, o procurador (o
// mesmo binário, "/app procurador", com a rede do servidor), e o app pede a ele por HTTP
// (PROCURADOR_DE_TELAS). Sem a variável, o app busca sozinho, como no Mac.

type pedidoDeBusca struct {
	Alvos    []string `json:"alvos"`
	Espera   float64  `json:"espera"`   // segundos
	Primeira bool     `json:"primeira"` // devolve na primeira descrição achada
}

// locaisSSDP manda a busca aos alvos (até 3 vezes) e junta as descrições que as respostas indicam.
func locaisSSDP(ctx context.Context, alvos []string, espera time.Duration, primeira bool) ([]string, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	fim := time.Now().Add(espera)
	if d, ok := ctx.Deadline(); ok && d.Before(fim) {
		fim = d
	}
	var locais []string
	vistos := map[string]bool{}
	buf := make([]byte, 8192)
	for envio := 0; time.Now().Before(fim) && ctx.Err() == nil; envio++ {
		if envio < 3 {
			for _, ip := range alvos {
				_, _ = conn.WriteToUDP([]byte(buscaSSDP), &net.UDPAddr{IP: net.ParseIP(ip), Port: portaSSDP})
			}
		}
		_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		for _, linha := range strings.Split(string(buf[:n]), "\r\n") {
			if k, v, ok := strings.Cut(linha, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "location") {
				if v = strings.TrimSpace(v); !vistos[v] {
					vistos[v] = true
					locais = append(locais, v)
				}
			}
		}
		if primeira && len(locais) > 0 {
			break
		}
	}
	return locais, nil
}

// buscarLocais faz a busca aqui mesmo ou pede ao procurador.
func (c *controleDasTelas) buscarLocais(ctx context.Context, alvos []string, espera time.Duration, primeira bool) ([]string, error) {
	if c.procurador == "" {
		return locaisSSDP(ctx, alvos, espera, primeira)
	}
	corpo, _ := json.Marshal(pedidoDeBusca{Alvos: alvos, Espera: espera.Seconds(), Primeira: primeira})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.procurador+"/ssdp", bytes.NewReader(corpo))
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("o procurador de telas não respondeu")
	}
	defer resp.Body.Close()
	var r struct {
		Locais []string `json:"locais"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&r) != nil {
		return nil, errors.New("o procurador de telas falhou")
	}
	return r.Locais, nil
}

// procuradorDeTelas só aceita alvos da rede de casa (e o grupo do SSDP): não manda pacotes para fora.
func procuradorDeTelas() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ssdp", func(w http.ResponseWriter, r *http.Request) {
		var p pedidoDeBusca
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&p); err != nil || len(p.Alvos) > 1024 {
			http.Error(w, "pedido inválido", http.StatusBadRequest)
			return
		}
		var alvos []string
		for _, a := range p.Alvos {
			if ip := net.ParseIP(a); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || a == "239.255.255.250") {
				alvos = append(alvos, a)
			}
		}
		espera := time.Duration(min(max(p.Espera, 0.5), 5) * float64(time.Second))
		locais, err := locaisSSDP(r.Context(), alvos, espera, p.Primeira)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string][]string{"locais": locais})
	})
	return mux
}
