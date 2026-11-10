package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

// Parakeet (S29): o serviço compartilhado do homelab ao lado do Whisper (pasta parakeet/ do
// homelab, API no padrão da OpenAI). Transcreve o vídeo inteiro mais rápido e com menos erros que o
// Whisper, com o tempo de cada palavra; mas não diz o idioma e só entende inglês e 24 idiomas
// europeus. Por isso o Whisper continua: ele reconhece o idioma e cobre os outros.

func urlDoParakeet() string { return strings.TrimRight(env("PARAKEET_URL", ""), "/") }

// Inglês vai para o v2 (feito só para inglês, erra menos); os outros idiomas europeus, para o v3.
func modeloDoParakeet(idioma string) string {
	if idioma == "en" {
		return env("PARAKEET_MODELO_INGLES", "parakeet-tdt-0.6b-v2")
	}
	return env("PARAKEET_MODELO", "parakeet-tdt-0.6b-v3")
}

var idiomasDoParakeet = map[string]bool{}

func init() {
	for _, i := range strings.Fields("bg cs da de el en es et fi fr hr hu it lt lv mt nl pl pt ro ru sk sl sv uk") {
		idiomasDoParakeet[i] = true
	}
}

// palavraOuvida: uma palavra (ou, do Whisper, uma fala inteira) com os tempos no vídeo.
type palavraOuvida struct {
	Texto  string  `json:"t"`
	Inicio float64 `json:"i"`
	Fim    float64 `json:"f"`
}

// transcreverPalavras manda um trecho ao Parakeet; os tempos voltam somados ao início do trecho.
func (a *app) transcreverPalavras(ctx context.Context, wav []byte, inicio float64, modelo string) ([]palavraOuvida, error) {
	base := urlDoParakeet()
	if base == "" {
		return nil, errors.New("Parakeet não configurado")
	}
	var corpo bytes.Buffer
	mw := multipart.NewWriter(&corpo)
	parte, _ := mw.CreateFormFile("file", "trecho.wav")
	parte.Write(wav)
	mw.WriteField("model", modelo)
	mw.WriteField("response_format", "verbose_json")
	mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/audio/transcriptions", &corpo)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("o Parakeet não respondeu (%v)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("o Parakeet respondeu %d: %s", resp.StatusCode, b)
	}
	var r struct {
		Words []struct {
			Word  string  `json:"word"`
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		} `json:"words"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	out := make([]palavraOuvida, 0, len(r.Words))
	for _, w := range r.Words {
		if t := strings.TrimSpace(w.Word); t != "" {
			out = append(out, palavraOuvida{t, w.Start + inicio, w.End + inicio})
		}
	}
	return out, nil
}
