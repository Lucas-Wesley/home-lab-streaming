package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Whisper (S26): o serviço compartilhado do homelab (speaches, API no padrão da OpenAI) transcreve
// trechos do áudio de um vídeo. Serve para acertar a sincronia de uma legenda sem referência (as
// falas do áudio × as da legenda, no mesmo idioma) e para conferir se o áudio é do título.

func urlDoWhisper() string    { return strings.TrimRight(env("WHISPER_URL", ""), "/") }
func modeloDoWhisper() string { return env("WHISPER_MODELO", "Systran/faster-whisper-small") }

type trechoFalado struct {
	Inicio, Fim float64 // segundos desde o início do vídeo
	Texto       string
}

type transcricao struct {
	Idioma string // en, pt…
	Falas  []trechoFalado
}

// audioDoTrecho tira do vídeo (pelo endereço interno) um trecho em WAV mono de 16 kHz.
func audioDoTrecho(ctx context.Context, endereco string, inicio, duracao float64) ([]byte, error) {
	c, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	return exec.CommandContext(c, programaFFmpeg, "-v", "error", "-ss", fmt.Sprintf("%.1f", inicio), "-t", fmt.Sprintf("%.1f", duracao),
		"-i", endereco, "-vn", "-ac", "1", "-ar", "16000", "-f", "wav", "-").Output()
}

// audioDaFaixa: o mesmo, de uma faixa de áudio escolhida (dublado e original no mesmo arquivo).
func audioDaFaixa(ctx context.Context, endereco string, faixa int, inicio, duracao float64, extras ...string) ([]byte, error) {
	c, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	args := append([]string{"-v", "error", "-ss", fmt.Sprintf("%.1f", inicio), "-t", fmt.Sprintf("%.1f", duracao)}, extras...)
	args = append(args, "-i", endereco, "-map", fmt.Sprintf("0:a:%d", faixa), "-ac", "1", "-ar", "16000", "-f", "wav", "-")
	return exec.CommandContext(c, programaFFmpeg, args...).Output()
}

// transcrever manda um trecho ao Whisper; os tempos voltam somados ao início do trecho.
func (a *app) transcrever(ctx context.Context, wav []byte, inicio float64) (transcricao, error) {
	base := urlDoWhisper()
	if base == "" {
		return transcricao{}, errors.New("Whisper não configurado")
	}
	pedir := func() (*http.Response, error) {
		var corpo bytes.Buffer
		mw := multipart.NewWriter(&corpo)
		parte, _ := mw.CreateFormFile("file", "trecho.wav")
		parte.Write(wav)
		mw.WriteField("model", modeloDoWhisper())
		mw.WriteField("response_format", "verbose_json")
		mw.WriteField("timestamp_granularities[]", "segment")
		mw.Close()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/audio/transcriptions", &corpo)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		return http.DefaultClient.Do(req)
	}
	resp, err := pedir()
	if err == nil && resp.StatusCode == http.StatusNotFound {
		// Instalação nova do serviço: o modelo ainda não foi baixado. Pede e tenta de novo.
		resp.Body.Close()
		if r, e := http.Post(base+"/v1/models/"+modeloDoWhisper(), "application/json", nil); e == nil {
			r.Body.Close()
		}
		resp, err = pedir()
	}
	if err != nil {
		return transcricao{}, fmt.Errorf("o Whisper não respondeu (%v)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return transcricao{}, fmt.Errorf("o Whisper respondeu %d: %s", resp.StatusCode, b)
	}
	var r struct {
		Language string `json:"language"`
		Segments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		} `json:"segments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return transcricao{}, err
	}
	t := transcricao{Idioma: codigoDoIdioma(r.Language)}
	for _, s := range r.Segments {
		if txt := strings.TrimSpace(s.Text); txt != "" {
			t.Falas = append(t.Falas, trechoFalado{s.Start + inicio, s.End + inicio, txt})
		}
	}
	return t, nil
}

// codigoDoIdioma: o Whisper responde "en" ou "english", conforme a versão.
func codigoDoIdioma(i string) string {
	i = strings.ToLower(i)
	return primeiro(map[string]string{"english": "en", "portuguese": "pt", "spanish": "es", "japanese": "ja"}[i], i)
}

// ouvir transcreve trechos do vídeo (em proporções da duração) e junta as falas.
func (a *app) ouvir(ctx context.Context, endereco string, duracao float64, pontos []float64, tamanho float64) (transcricao, error) {
	var junta transcricao
	idiomas := map[string]int{}
	for _, p := range pontos {
		inicio := math.Max(0, duracao*p-tamanho/2)
		wav, err := audioDoTrecho(ctx, endereco, inicio, tamanho)
		if err != nil || len(wav) < 1000 {
			continue
		}
		t, err := a.transcrever(ctx, wav, inicio)
		if err != nil {
			return junta, err
		}
		idiomas[t.Idioma] += len(t.Falas)
		junta.Falas = append(junta.Falas, t.Falas...)
	}
	for i, n := range idiomas {
		if n > idiomas[junta.Idioma] {
			junta.Idioma = i
		}
	}
	if len(junta.Falas) == 0 {
		return junta, errors.New("não ouvi falas no vídeo")
	}
	return junta, nil
}

// ---------- Comparar o que se ouviu com uma legenda ----------

var rePalavra = regexp.MustCompile(`[\p{L}\p{N}']+`)

func palavras(s string) []string {
	return rePalavra.FindAllString(normalizar(s), -1)
}

// parecidas: fração das palavras da fala que aparecem no texto da legenda (as curtas não contam).
func parecidas(fala string, legenda []string) float64 {
	ps := palavras(fala)
	conjunto := map[string]bool{}
	for _, l := range legenda {
		for _, p := range palavras(l) {
			conjunto[p] = true
		}
	}
	total, iguais := 0, 0
	for _, p := range ps {
		if len([]rune(p)) < 3 {
			continue
		}
		total++
		if conjunto[p] {
			iguais++
		}
	}
	if total < 3 {
		return 0
	}
	return float64(iguais) / float64(total)
}

type falaDaLegenda struct {
	inicio float64
	texto  string
}

var reMarcacaoDeLegenda = regexp.MustCompile(`<[^>]+>|\{[^}]*\}`)

// falasComTexto lê o SRT com o texto de cada fala (o alinhamento só usa os tempos).
func falasComTexto(srt []byte) []falaDaLegenda {
	var out []falaDaLegenda
	for _, bloco := range strings.Split(strings.ReplaceAll(string(srt), "\r\n", "\n"), "\n\n") {
		linhas := strings.Split(strings.TrimSpace(bloco), "\n")
		for i, l := range linhas {
			if m := reTempoDaFala.FindStringSubmatch(l); m != nil {
				texto := reMarcacaoDeLegenda.ReplaceAllString(strings.Join(linhas[i+1:], " "), " ")
				out = append(out, falaDaLegenda{segundosDoSRT(m[1], m[2], m[3], m[4]), texto})
				break
			}
		}
	}
	return out
}

// parDeFalas: uma fala ouvida (pelo índice) e uma fala da legenda que pode ser ela.
type parDeFalas struct {
	ouvida         int
	audio, legenda float64
}

// casarFalas lista, para cada fala ouvida, as falas da legenda (até 3 min antes ou depois) com mais
// palavras em comum: a melhor e as quase tão boas, porque falas parecidas se confundem; quem decide
// é a votação em sincroniaPelasFalas. Devolve também a fração das falas ouvidas que casou.
func casarFalas(ouvidas []trechoFalado, legenda []falaDaLegenda) (pares []parDeFalas, fracao float64) {
	contadas, casadas := 0, 0
	for i, o := range ouvidas {
		if len(palavras(o.Texto)) < 3 {
			continue
		}
		contadas++
		type candidata struct {
			nota, quando float64
		}
		var cands []candidata
		melhor := 0.0
		for j, l := range legenda {
			if math.Abs(l.inicio-o.Inicio) > 180 {
				continue
			}
			nota := parecidas(o.Texto, []string{l.texto})
			// O Whisper pode juntar duas falas da legenda numa só; o par vale um pouco menos, para
			// não empatar com a fala certa quando ela é a segunda do par.
			if j+1 < len(legenda) {
				nota = math.Max(nota, parecidas(o.Texto, []string{l.texto, legenda[j+1].texto})-0.05)
			}
			if nota >= 0.5 {
				cands = append(cands, candidata{nota, l.inicio})
				melhor = math.Max(melhor, nota)
			}
		}
		if len(cands) == 0 {
			continue
		}
		casadas++
		for _, c := range cands {
			if c.nota >= melhor-0.1 {
				pares = append(pares, parDeFalas{i, o.Inicio, c.quando})
			}
		}
	}
	if contadas == 0 {
		return nil, 0
	}
	return pares, float64(casadas) / float64(contadas)
}

// sincroniaPelasFalas: o atraso (e a escala) com o apoio do maior número de falas ouvidas
// diferentes (±1 s). Pede pelo menos 4 falas concordando, e que elas sejam a maioria.
func sincroniaPelasFalas(pares []parDeFalas) (alinhamento, bool) {
	ouvidas := map[int]bool{}
	for _, p := range pares {
		ouvidas[p.ouvida] = true
	}
	melhor, melhorN := alinhamento{Escala: 1}, 0
	for _, escala := range escalasPossiveis {
		for _, base := range pares {
			d := base.audio - base.legenda*escala
			apoio := map[int]float64{} // fala ouvida → a diferença mais perto de d
			for _, p := range pares {
				x := p.audio - p.legenda*escala
				if math.Abs(x-d) <= 1 {
					if v, ok := apoio[p.ouvida]; !ok || math.Abs(x-d) < math.Abs(v-d) {
						apoio[p.ouvida] = x
					}
				}
			}
			if n := len(apoio); n > melhorN || (n == melhorN && escala == 1 && melhor.Escala != 1) {
				var difs []float64
				for _, x := range apoio {
					difs = append(difs, x)
				}
				sort.Float64s(difs)
				melhor, melhorN = alinhamento{Atraso: math.Round(difs[len(difs)/2]*10) / 10, Escala: escala}, n
			}
		}
	}
	if len(ouvidas) > 0 {
		melhor.Acerto = float64(melhorN) / float64(len(ouvidas))
	}
	return melhor, melhorN >= 4 && melhor.Acerto >= 0.5
}

// idiomaDaLegenda: pt-BR e pt-PT são "pt" para comparar com o áudio.
func idiomaDaLegenda(i string) string { return strings.SplitN(strings.ToLower(i), "-", 2)[0] }
