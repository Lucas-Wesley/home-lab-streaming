package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// A busca SSDP precisa da rede de casa: o multicast não sai da rede do Docker, e o projetor responde
// de outra porta, que o NAT descarta. Em produção ela roda num segundo processo, o procurador (o
// mesmo binário, "/app procurador", com a rede do servidor), e o app pede a ele por HTTP
// (PROCURADOR_DE_TELAS). Sem a variável, o app busca sozinho, como no Mac.

type pedidoDeBusca struct {
	Alvos    []string `json:"alvos"`
	Repetir  int      `json:"repetir"`  // só os primeiros alvos vão de novo nas outras rodadas (0: todos)
	Espera   float64  `json:"espera"`   // segundos
	Primeira bool     `json:"primeira"` // devolve na primeira descrição achada
}

// locaisSSDP manda a busca aos alvos e junta as descrições que as respostas indicam. Os primeiros
// alvos (o grupo e as telas conhecidas) vão 3 vezes; a varredura da rede vai uma vez e por outro
// socket: no Linux, os pacotes à espera do ARP ocupam o buffer do socket, e a busca ao grupo
// atrasava até perder a TV ou o projetor.
func locaisSSDP(ctx context.Context, alvos []string, repetir int, espera time.Duration, primeira bool) ([]string, error) {
	if repetir <= 0 || repetir > len(alvos) {
		repetir = len(alvos)
	}
	fim := time.Now().Add(espera)
	if d, ok := ctx.Deadline(); ok && d.Before(fim) {
		fim = d
	}
	ctx, cancel := context.WithDeadline(ctx, fim)
	defer cancel()

	var (
		mu     sync.Mutex
		locais []string
		vistos = map[string]bool{}
		wg     sync.WaitGroup
		erro   error
	)
	ouvir := func(lote []string, rodadas int) {
		defer wg.Done()
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
		if err != nil {
			mu.Lock()
			erro = err
			mu.Unlock()
			return
		}
		defer conn.Close()
		buf := make([]byte, 8192)
		for envio := 0; ctx.Err() == nil; envio++ {
			if envio < rodadas {
				for _, ip := range lote {
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
					mu.Lock()
					if v = strings.TrimSpace(v); !vistos[v] {
						vistos[v] = true
						locais = append(locais, v)
					}
					mu.Unlock()
				}
			}
			if primeira {
				mu.Lock()
				achou := len(locais) > 0
				mu.Unlock()
				if achou {
					cancel()
				}
			}
		}
	}
	wg.Add(1)
	go ouvir(alvos[:repetir], 3)
	if repetir < len(alvos) {
		wg.Add(1)
		go ouvir(alvos[repetir:], 1)
	}
	wg.Wait()
	if len(locais) == 0 && erro != nil {
		return nil, erro
	}
	return locais, nil
}

// buscarLocais faz a busca aqui mesmo ou pede ao procurador.
func (c *controleDasTelas) buscarLocais(ctx context.Context, alvos []string, repetir int, espera time.Duration, primeira bool) ([]string, error) {
	if c.procurador == "" {
		return locaisSSDP(ctx, alvos, repetir, espera, primeira)
	}
	corpo, _ := json.Marshal(pedidoDeBusca{Alvos: alvos, Repetir: repetir, Espera: espera.Seconds(), Primeira: primeira})
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
		repetir := 0
		for i, a := range p.Alvos {
			if ip := net.ParseIP(a); ip != nil && (ip.IsPrivate() || ip.IsLoopback() || a == "239.255.255.250") {
				alvos = append(alvos, a)
				if i < p.Repetir {
					repetir++ // conta só os aceitos; 0 continua sendo "todos"
				}
			}
		}
		espera := time.Duration(min(max(p.Espera, 0.5), 5) * float64(time.Second))
		locais, err := locaisSSDP(r.Context(), alvos, repetir, espera, p.Primeira)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string][]string{"locais": locais})
	})
	return mux
}
