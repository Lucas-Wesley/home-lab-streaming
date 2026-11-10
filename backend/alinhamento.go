package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Alinhar uma legenda a outra que já está sincronizada com o vídeo (S25). Legendas de versões
// diferentes do mesmo filme costumam ter o mesmo corte das falas, só deslocado (uma abertura mais
// longa) ou esticado (25 × 23,976 quadros por segundo). Comparando só os horários, serve até entre
// idiomas: a tradução segue os tempos do original.

type fala struct {
	inicio, fim float64
}

var reTempoDaFala = regexp.MustCompile(`(\d{1,2}):(\d{2}):(\d{2})[,.](\d{1,3})\s*-->\s*(\d{1,2}):(\d{2}):(\d{2})[,.](\d{1,3})`)

func segundosDoSRT(h, m, s, ms string) float64 {
	hh, _ := strconv.Atoi(h)
	mm, _ := strconv.Atoi(m)
	ss, _ := strconv.Atoi(s)
	for len(ms) < 3 {
		ms += "0"
	}
	mil, _ := strconv.Atoi(ms)
	return float64(hh*3600+mm*60+ss) + float64(mil)/1000
}

func lerFalas(srt []byte) []fala {
	var falas []fala
	for _, m := range reTempoDaFala.FindAllStringSubmatch(string(srt), -1) {
		falas = append(falas, fala{segundosDoSRT(m[1], m[2], m[3], m[4]), segundosDoSRT(m[5], m[6], m[7], m[8])})
	}
	sort.Slice(falas, func(i, j int) bool { return falas[i].inicio < falas[j].inicio })
	return falas
}

func tempoDoSRT(t float64, separador string) string {
	if t < 0 {
		t = 0
	}
	ms := int(math.Round(t * 1000))
	return fmt.Sprintf("%02d:%02d:%02d%s%03d", ms/3600000, ms/60000%60, ms/1000%60, separador, ms%1000)
}

// ajustarTempos aplica t' = t × escala + atraso a todas as falas (SRT ou WebVTT).
func ajustarTempos(texto string, atraso, escala float64) string {
	if atraso == 0 && escala == 1 {
		return texto
	}
	return reTempoDaFala.ReplaceAllStringFunc(texto, func(s string) string {
		m := reTempoDaFala.FindStringSubmatch(s)
		sep := ","
		if strings.Contains(s, ".") {
			sep = "."
		}
		a := segundosDoSRT(m[1], m[2], m[3], m[4])*escala + atraso
		b := segundosDoSRT(m[5], m[6], m[7], m[8])*escala + atraso
		return tempoDoSRT(a, sep) + " --> " + tempoDoSRT(b, sep)
	})
}

type alinhamento struct {
	Atraso float64 // segundos a somar à legenda
	Escala float64 // 1, ou a razão entre as velocidades
	Acerto float64 // fração das falas que caem sobre uma fala da referência
}

// Velocidades que aparecem entre versões: PAL (25) × cinema (23,976 e 24).
var escalasPossiveis = []float64{1, 25.0 / 23.976, 23.976 / 25.0, 24.0 / 23.976, 23.976 / 24.0, 25.0 / 24.0, 24.0 / 25.0} // com ponto: 24 / 25 entre inteiros dá 0

// alinhar acha o atraso (até 3 min para cada lado) e a escala que fazem o maior número de falas
// da legenda começar junto (±0,5 s) de uma fala da referência.
func alinhar(legenda, referencia []fala) alinhamento {
	if len(legenda) < 20 || len(referencia) < 20 {
		return alinhamento{Escala: 1}
	}
	inicios := make([]float64, len(referencia))
	for i, f := range referencia {
		inicios[i] = f.inicio
	}
	// Até 400 falas, espalhadas pelo filme todo: basta para achar o pico e mantém a conta rápida.
	amostra := legenda
	if len(amostra) > 400 {
		amostra = make([]fala, 0, 400)
		for i := 0; i < 400; i++ {
			amostra = append(amostra, legenda[i*len(legenda)/400])
		}
	}
	perto := func(t float64) (float64, bool) {
		i := sort.SearchFloat64s(inicios, t)
		melhor := math.Inf(1)
		for _, j := range []int{i - 1, i} {
			if j >= 0 && j < len(inicios) && math.Abs(inicios[j]-t) < math.Abs(melhor) {
				melhor = inicios[j] - t
			}
		}
		return melhor, math.Abs(melhor) <= 0.5
	}
	acertos := func(atraso, escala float64) int {
		n := 0
		for _, f := range amostra {
			if _, ok := perto(f.inicio*escala + atraso); ok {
				n++
			}
		}
		return n
	}
	melhor := alinhamento{Escala: 1}
	melhorN := acertos(0, 1)
	for _, escala := range escalasPossiveis {
		for passo := -1800; passo <= 1800; passo++ {
			d := float64(passo) / 10
			if n := acertos(d, escala); n > melhorN || (n == melhorN && escala == 1 && math.Abs(d) < math.Abs(melhor.Atraso)) {
				melhor, melhorN = alinhamento{Atraso: d, Escala: escala}, n
			}
		}
	}
	// Afina o atraso pela mediana das diferenças das falas que bateram.
	var difs []float64
	for _, f := range amostra {
		if dif, ok := perto(f.inicio*melhor.Escala + melhor.Atraso); ok {
			difs = append(difs, dif)
		}
	}
	if len(difs) > 0 {
		sort.Float64s(difs)
		melhor.Atraso = math.Round((melhor.Atraso+difs[len(difs)/2])*100) / 100
	}
	melhor.Acerto = float64(melhorN) / float64(len(amostra))
	return melhor
}

// ---------- Ajuste automático da legenda escolhida ----------

// alinharSozinho acerta a sincronia da legenda escolhida por trás, com uma referência sincronizada
// com o arquivo. Se o Lucas trocou de legenda ou ajustou à mão nesse meio tempo, não mexe.
func (a *app) alinharSozinho(chave, tipo string, id int64, legendaID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	var idioma string
	a.db.QueryRow(`SELECT idioma FROM legendas_escolhidas WHERE chave = ?`, chave).Scan(&idioma)
	ajuste, al := a.acharSincronia(ctx, tipo, id, legendaID, idioma)
	a.db.Exec(`UPDATE legendas_escolhidas SET atraso = ?, escala = ?, ajuste = ?, alinhando = 0 WHERE chave = ? AND id = ? AND alinhando = 1`,
		al.Atraso, al.Escala, ajuste, chave, legendaID)
}

const semReferencia = "Sem referência para ajustar sozinho: se ficar adiantada ou atrasada, use o ajuste manual."

func (a *app) acharSincronia(ctx context.Context, tipo string, id int64, legendaID, idioma string) (string, alinhamento) {
	nada := alinhamento{Escala: 1}
	texto, err := os.ReadFile(a.arquivoDaLegenda(legendaID))
	if err != nil || tipo != "arquivo" {
		return semReferencia, nada // vídeo da assinatura: o arquivo do provedor não dá referência (por ora)
	}
	falas := lerFalas(texto)
	f, _, err := a.arquivo(ctx, id)
	if err != nil {
		return semReferencia, nada
	}
	caminho := a.caminhoBaixado(f) // só devolve o caminho se o arquivo estiver inteiro no disco
	tentar := func(ref []fala, de string) (string, alinhamento, bool) {
		al := alinhar(falas, ref)
		if al.Acerto < 0.3 {
			return "", nada, false
		}
		txt := fmt.Sprintf("Ajustada sozinha em %s pela %s.", segundosComSinal(al.Atraso), de)
		if al.Atraso == 0 && al.Escala == 1 {
			txt = "Conferida pela " + de + ": já estava sincronizada."
		}
		if al.Escala != 1 {
			txt = strings.TrimSuffix(txt, ".") + ", com a velocidade corrigida (versão com outro número de quadros)."
		}
		return txt, al, true
	}
	// 1. A legenda que vem dentro do arquivo baixado (não gasta a cota do OpenSubtitles).
	if caminho != "" {
		if ref := legendaDeDentro(ctx, caminho); len(ref) >= 20 {
			if txt, al, ok := tentar(ref, "legenda que vem dentro do arquivo"); ok {
				return txt, al
			}
		}
	}
	// 2. Uma legenda feita para este arquivo (impressão digital), em qualquer idioma.
	var hash string
	if caminho != "" {
		hash, _ = hashDoOpenSubtitles(caminho)
	} else {
		hash, _ = a.impressaoDigital(ctx, f)
	}
	if exata, idioma := a.legendaExata(ctx, hash, legendaID); exata != "" {
		if a.baixarLegenda(ctx, exata, a.arquivoDaLegenda(exata)) == nil {
			if ref, err := os.ReadFile(a.arquivoDaLegenda(exata)); err == nil {
				if txt, al, ok := tentar(lerFalas(ref), "legenda feita para este arquivo ("+idiomaPorExtenso(idioma)+")"); ok {
					return txt, al
				}
			}
		}
	}
	// 3. O Whisper ouve 3 trechos do vídeo e as falas são casadas com as da legenda (S26).
	if urlDoWhisper() != "" {
		endereco := caminho
		if endereco == "" {
			base, err := a.enderecoInterno()
			if err != nil {
				return semReferencia, nada
			}
			endereco = fmt.Sprintf("%s/arquivo/%d", base, f.ID)
		}
		duracao := a.duracaoDoArquivo(ctx, f)
		ouvido, err := a.ouvir(ctx, endereco, duracao, []float64{0.25, 0.5, 0.75}, 90)
		if err != nil {
			return semReferencia, nada
		}
		if ouvido.Idioma != idiomaDaLegenda(idioma) {
			// Idiomas diferentes: casar pelo sentido é com o Claude (S27).
			return a.sincroniaEntreIdiomas(ctx, ouvido, texto, idioma)
		}
		pares, _ := casarFalas(ouvido.Falas, falasComTexto(texto))
		if al, ok := sincroniaPelasFalas(pares); ok {
			txt := fmt.Sprintf("Ajustada sozinha em %s pelo áudio do vídeo (Whisper).", segundosComSinal(al.Atraso))
			if al.Atraso == 0 {
				txt = "Conferida pelo áudio do vídeo (Whisper): já estava sincronizada."
			}
			return txt, al
		}
		return "O áudio não bateu com esta legenda: talvez seja de outra versão. Escolha outra ou use o ajuste manual.", nada
	}
	return semReferencia, nada
}

// sincroniaEntreIdiomas: o áudio e a legenda estão em idiomas diferentes, então as palavras não
// batem; o Claude casa as falas pelo sentido (S27) e o atraso sai dos pares que ele achou.
func (a *app) sincroniaEntreIdiomas(ctx context.Context, ouvido transcricao, legenda []byte, idioma string) (string, alinhamento) {
	aviso := fmt.Sprintf("O áudio está em %s e a legenda em %s", idiomaPorExtenso(ouvido.Idioma), idiomaPorExtenso(idioma))
	pares, confianca, err := a.casarPeloSentido(ctx, ouvido, falasComTexto(legenda), idioma)
	if err == nil && confianca != "baixa" {
		if al, ok := sincroniaPelasFalas(pares); ok {
			return fmt.Sprintf("Ajustada sozinha em %s: o Claude casou as falas do áudio em %s com as da legenda.", segundosComSinal(al.Atraso), idiomaPorExtenso(ouvido.Idioma)), al
		}
	}
	return aviso + ": se ficar adiantada ou atrasada, use o ajuste manual.", alinhamento{Escala: 1}
}

// duracaoDoArquivo: a da sondagem guardada ou, se ainda não houver, lida agora.
func (a *app) duracaoDoArquivo(ctx context.Context, f arquivoDeTorrent) float64 {
	var guardada string
	a.db.QueryRowContext(ctx, `SELECT sondagem FROM torrent_arquivos WHERE id = ?`, f.ID).Scan(&guardada)
	var s sondagem
	if json.Unmarshal([]byte(guardada), &s) == nil && s.Duracao > 0 {
		return s.Duracao
	}
	s, _ = a.sondar(ctx, f)
	return s.Duracao
}

func idiomaPorExtenso(i string) string {
	return primeiro(map[string]string{"pt-BR": "português", "pt-PT": "português de Portugal", "pt": "português", "en": "inglês", "es": "espanhol", "ja": "japonês"}[i], i)
}

// legendaDeDentro extrai a primeira legenda em texto do arquivo (as em imagem, PGS, não servem).
func legendaDeDentro(ctx context.Context, caminho string) []fala {
	for i := 0; i < 4; i++ {
		c, cancel := context.WithTimeout(ctx, 2*time.Minute)
		saida, err := exec.CommandContext(c, programaFFmpeg, "-v", "error", "-i", caminho, "-map", fmt.Sprintf("0:s:%d", i), "-f", "srt", "-").Output()
		cancel()
		if err != nil {
			continue
		}
		if falas := lerFalas(saida); len(falas) >= 20 {
			return falas
		}
	}
	return nil
}

// legendaExata acha, pela impressão digital, uma legenda feita para este arquivo (a mais baixada),
// que não seja a própria escolhida.
func (a *app) legendaExata(ctx context.Context, hash, outraQue string) (id, idioma string) {
	chave := a.chaveDoOpenSubtitles()
	if chave == "" || hash == "" {
		return "", ""
	}
	var r struct {
		Data []legendaDoOpenSubtitles `json:"data"`
	}
	if a.pedirAoOpenSubtitles(ctx, chave, http.MethodGet, "/subtitles?"+url.Values{"languages": {"en,pt-br,pt-pt"}, "moviehash": {hash}}.Encode(), nil, &r) != nil {
		return "", ""
	}
	melhor := -1
	for _, l := range r.Data {
		at := l.Attributes
		if !at.MoviehashMatch || len(at.Files) == 0 {
			continue
		}
		cand := "os-" + strconv.FormatInt(at.Files[0].FileID, 10)
		if cand != outraQue && at.DownloadCount > melhor {
			melhor, id, idioma = at.DownloadCount, cand, map[string]string{"pt-br": "pt-BR", "pt-pt": "pt-PT", "en": "en"}[strings.ToLower(at.Language)]
		}
	}
	return id, idioma
}

// segundosComSinal escreve o atraso como no resto do app: "+0,2 s", "−1,5 s".
func segundosComSinal(s float64) string {
	sinal := "+"
	if s < 0 {
		sinal, s = "−", -s
	}
	return sinal + strings.Replace(fmt.Sprintf("%.1f", s), ".", ",", 1) + " s"
}
