package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ntfyFalso guarda o que o app publica.
func ntfyFalso(t *testing.T) (endereco string, recebidos chan map[string]any) {
	t.Helper()
	recebidos = make(chan map[string]any, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		recebidos <- m
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/streaming", recebidos
}

func receberAviso(t *testing.T, recebidos chan map[string]any) map[string]any {
	t.Helper()
	select {
	case m := <-recebidos:
		return m
	case <-time.After(10 * time.Second):
		t.Fatal("nenhum aviso chegou")
		return nil
	}
}

// S35: o download da assinatura avisa quando termina.
func TestAvisoDoDownloadDaAssinatura(t *testing.T) {
	e := novoAmbiente(t)
	endereco, recebidos := ntfyFalso(t)
	e.a.avisos = novoAvisador(endereco)
	if status, corpo := e.pedir(t, "POST", "/api/assinatura/downloads/filme:200", nil); status != http.StatusAccepted {
		t.Fatalf("baixar: %d %s", status, corpo)
	}
	e.a.baixarPendentes(context.Background())
	d := estadoDoDownload(t, e, "filme:200")
	m := receberAviso(t, recebidos)
	if m["topic"] != "streaming" || m["title"] != d.Titulo || m["message"] != "Baixado: dá para assistir sem usar a assinatura." || m["priority"] != nil {
		t.Errorf("aviso: %v", m)
	}
}
