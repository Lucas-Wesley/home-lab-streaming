package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Frase da legenda para o Flashcards (S31): no controle da tela, a legenda passa no celular; tocar
// numa fala (ou em duas seguidas, quando a frase foi cortada) manda para o app Flashcards, no baralho
// "Inglês", com a frase em inglês na frente e a tradução no verso. O Claude limpa a fala (traço de
// diálogo, ♪, [sons]) e traduz pelo sentido da cena; o Flashcards só recebe o cartão pronto, pela API
// dele (POST /api/notas). Sem FLASHCARDS_URL ou sem o Claude, o botão não aparece. O pedido só entra
// na fila: o servidor responde na hora e faz os cartões um por vez, sem avisar o celular depois.
// "Com áudio" (S39) corta o trecho da fala do próprio vídeo (ffmpeg, na faixa do idioma da legenda) e
// manda junto: o Flashcards toca na frente do cartão. Só de onde o áudio sai sem a conexão da
// assinatura: torrent, vídeo baixado ou o áudio já puxado para gerar a legenda.

func urlDoFlashcards() string { return strings.TrimRight(env("FLASHCARDS_URL", ""), "/") }

const nomeDoBaralhoDeIngles = "Inglês"

func flashcardsDisponivel() bool { return urlDoFlashcards() != "" && claudeDisponivel() }

func (a *app) handleFlashcardsDisponivel(w http.ResponseWriter, r *http.Request) {
	fonte, _ := a.fonteDaFala(r.Context(), r.URL.Query().Get("chave"))
	writeJSON(w, http.StatusOK, map[string]bool{"disponivel": flashcardsDisponivel(), "audio": flashcardsDisponivel() && fonte != ""})
}

const esquemaDoCartao = `{"type":"object","properties":{"ingles":{"type":"string"},"traducao":{"type":"string"},"nota":{"type":"string"}},"required":["ingles","traducao","nota"]}`

// filaDeCartoes deixa um cartão por vez com o Claude.
var filaDeCartoes sync.Mutex

func (a *app) handleMandarParaOFlashcards(w http.ResponseWriter, r *http.Request) {
	var corpo struct {
		Chave  string   `json:"chave"`  // o vídeo, para o título e o link da fonte
		Falas  []string `json:"falas"`  // a fala tocada (ou as seguidas)
		Antes  []string `json:"antes"`  // falas de antes e de depois, só para o Claude entender a cena
		Depois []string `json:"depois"` //
		Audio  bool     `json:"audio"`  // cortar o trecho da fala no vídeo
		Inicio float64  `json:"inicio"` // segundos no vídeo, já com a sincronia da legenda
		Fim    float64  `json:"fim"`    //
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	frase := strings.TrimSpace(strings.Join(corpo.Falas, "\n"))
	switch {
	case !flashcardsDisponivel():
		erroJSON(w, http.StatusConflict, "O Flashcards não está ligado neste servidor (FLASHCARDS_URL e Claude).")
		return
	case frase == "" || len(frase) > 1000:
		erroJSON(w, http.StatusBadRequest, "Escolha uma fala da legenda.")
		return
	case corpo.Audio && (corpo.Inicio < 0 || corpo.Fim <= corpo.Inicio || corpo.Fim-corpo.Inicio > duracaoMaximaDaFala):
		erroJSON(w, http.StatusBadRequest, "Trecho da fala inválido.")
		return
	}
	fonte, faixa := "", 0
	if corpo.Audio {
		if fonte, faixa = a.fonteDaFala(r.Context(), corpo.Chave); fonte == "" {
			erroJSON(w, http.StatusConflict, "O áudio só sai de vídeo do torrent ou baixado: ouvir a assinatura ocuparia a conexão.")
			return
		}
	}
	titulo, link := "", ""
	if p := a.progressoDe(r.Context(), corpo.Chave); p != nil {
		titulo = p.Titulo
		var tmdb string
		a.db.QueryRowContext(r.Context(), `SELECT tmdb FROM progresso WHERE chave = ?`, corpo.Chave).Scan(&tmdb)
		if m := reTMDBGuardado.FindStringSubmatch(tmdb); m != nil {
			link = "https://www.themoviedb.org/" + tiposDoTMDB[m[1]] + "/" + m[2]
		}
	}
	go func() {
		// O áudio sai antes da fila: o torrent pode parar enquanto outros cartões esperam o Claude.
		audio := ""
		if fonte != "" {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			var err error
			if audio, err = a.mandarAudioDaFala(ctx, fonte, faixa, corpo.Inicio, corpo.Fim); err != nil {
				log.Printf("flashcards: áudio de %q: %v", frase, err) // o cartão sai sem áudio
			}
			cancel()
		}
		filaDeCartoes.Lock()
		defer filaDeCartoes.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := a.fazerCartao(ctx, frase, titulo, link, audio, corpo.Antes, corpo.Depois); err != nil {
			log.Printf("flashcards: %q: %v", frase, err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"estado": "na fila"})
}

// fazerCartao pede ao Claude a frase limpa e a tradução e cria a nota no Flashcards.
func (a *app) fazerCartao(ctx context.Context, frase, titulo, fonte, audio string, antes, depois []string) error {
	var pedido strings.Builder
	fmt.Fprintf(&pedido, "O Lucas está aprendendo inglês vendo um vídeo%s com legenda em inglês. Ele tocou nesta fala para virar "+
		"flashcard (a frase em inglês na frente, a tradução no verso).\n\nFala escolhida:\n%s\n\n", entreParenteses(titulo), frase)
	if len(antes)+len(depois) > 0 {
		fmt.Fprintf(&pedido, "Contexto, só para entender a cena (não entra no cartão):\nAntes:\n%s\nDepois:\n%s\n\n",
			strings.Join(ultimas(antes, 4), "\n"), strings.Join(depois[:min(2, len(depois))], "\n"))
	}
	pedido.WriteString("Devolva:\n" +
		"- ingles: a fala escolhida numa linha só, com as mesmas palavras, sem traço de diálogo, sem nome de quem fala, sem ♪ " +
		"e sem descrições de som entre colchetes ou parênteses. Se a fala for só uma parte de uma frase que continua nas falas " +
		"escolhidas, junte as partes.\n" +
		"- traducao: uma tradução natural em português do Brasil, fiel ao sentido nesta cena.\n" +
		"- nota: se a fala tem expressão idiomática, gíria ou phrasal verb, uma explicação curta (uma frase, em português); senão, vazio.\n")
	var c struct {
		Ingles   string `json:"ingles"`
		Traducao string `json:"traducao"`
		Nota     string `json:"nota"`
	}
	if err := a.perguntarAoClaude(ctx, pedido.String(), esquemaDoCartao, "", &c); err != nil {
		return err
	}
	if strings.TrimSpace(c.Ingles) == "" || strings.TrimSpace(c.Traducao) == "" {
		return errors.New("o Claude não traduziu a fala")
	}
	verso := strings.TrimSpace(c.Traducao)
	if n := strings.TrimSpace(c.Nota); n != "" {
		verso += "\n\n" + n
	}
	baralho, err := a.baralhoDeIngles(ctx)
	if err != nil {
		return err
	}
	nota := map[string]string{"baralho_id": baralho, "tipo": "basico", "frente": strings.TrimSpace(c.Ingles), "verso": verso, "tags": "streaming", "fonte": fonte, "audio": audio}
	return a.pedirAoFlashcards(ctx, http.MethodPost, "/api/notas", nota, nil)
}

// ---------- O áudio da fala ----------

const (
	duracaoMaximaDaFala = 30.0
	folgaAntesDaFala    = 0.25 // a legenda costuma entrar um pouco depois da voz
	folgaDepoisDaFala   = 0.4
)

// fonteDaFala: de onde cortar o áudio sem usar a conexão da assinatura, e a faixa no idioma da
// legenda escolhida (sem saber, a primeira). Vazio: daqui não dá.
func (a *app) fonteDaFala(ctx context.Context, chave string) (string, int) {
	m := reChaveDeVideo.FindStringSubmatch(chave)
	if m == nil {
		return "", 0
	}
	fonte := ""
	if m[1] == "arquivo" {
		var id int64
		fmt.Sscan(m[2], &id)
		f, _, err := a.arquivo(ctx, id)
		if err != nil {
			return "", 0
		}
		if fonte = a.caminhoBaixado(f); fonte == "" {
			base, err := a.enderecoInterno()
			if err != nil {
				return "", 0
			}
			fonte = fmt.Sprintf("%s/arquivo/%d", base, f.ID)
		}
	} else if fonte = a.baixadoDaAssinatura(ctx, chave); fonte == "" {
		// O áudio puxado para gerar a legenda (S29) só tem a faixa que foi ouvida.
		if puxado := a.audioPuxado(chave); semErro(os.Stat(puxado)) {
			return puxado, 0
		}
		return "", 0
	}
	faixa := 0
	if l, s := a.legendaDe(ctx, chave), a.formatosGuardados(ctx, chave); l != nil && s != nil {
		for i, au := range s.Audios {
			if idiomaCurto(strings.ToLower(au.Idioma)) == idiomaDaLegenda(l.Idioma) {
				faixa = i
				break
			}
		}
	}
	return fonte, faixa
}

func semErro(_ os.FileInfo, err error) bool { return err == nil }

// mandarAudioDaFala corta a fala (com uma folga em volta, entrando e saindo devagar) em MP3 e manda
// ao Flashcards; devolve o nome que ele guardou.
func (a *app) mandarAudioDaFala(ctx context.Context, fonte string, faixa int, inicio, fim float64) (string, error) {
	inicio = max(0, inicio-folgaAntesDaFala)
	duracao := fim + folgaDepoisDaFala - inicio
	filtro := fmt.Sprintf("afade=t=in:d=0.08,afade=t=out:st=%.2f:d=0.15", max(0, duracao-0.15))
	mp3, err := exec.CommandContext(ctx, programaFFmpeg, "-v", "error", "-ss", fmt.Sprintf("%.2f", inicio), "-t", fmt.Sprintf("%.2f", duracao),
		"-i", fonte, "-map", fmt.Sprintf("0:a:%d", faixa), "-vn", "-af", filtro, "-ac", "1", "-ar", "44100", "-b:a", "96k", "-f", "mp3", "-").Output()
	if err != nil {
		return "", fmt.Errorf("ffmpeg: %w", err)
	}
	if len(mp3) < 1000 {
		return "", errors.New("o trecho saiu sem áudio")
	}
	var corpo bytes.Buffer
	mw := multipart.NewWriter(&corpo)
	parte, _ := mw.CreateFormFile("arquivo", "fala.mp3")
	parte.Write(mp3)
	mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlDoFlashcards()+"/api/audios", &corpo)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := a.http.Do(req)
	if err != nil {
		return "", errors.New("O Flashcards não respondeu.")
	}
	defer resp.Body.Close()
	var r struct {
		Nome string `json:"nome"`
		Erro string `json:"erro"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&r)
	if resp.StatusCode >= 400 || r.Nome == "" {
		return "", fmt.Errorf("O Flashcards recusou o áudio (%d): %s", resp.StatusCode, primeiro(r.Erro, resp.Status))
	}
	return r.Nome, nil
}

// baralhoDeIngles acha o baralho "Inglês" pelo nome (sem acento nem maiúscula); se não houver, cria.
func (a *app) baralhoDeIngles(ctx context.Context) (string, error) {
	var baralhos []struct {
		ID   string `json:"id"`
		Nome string `json:"nome"`
	}
	if err := a.pedirAoFlashcards(ctx, http.MethodGet, "/api/baralhos", nil, &baralhos); err != nil {
		return "", err
	}
	for _, b := range baralhos {
		if comparavel(b.Nome) == comparavel(nomeDoBaralhoDeIngles) {
			return b.ID, nil
		}
	}
	var novo struct {
		ID string `json:"id"`
	}
	if err := a.pedirAoFlashcards(ctx, http.MethodPost, "/api/baralhos", map[string]string{"nome": nomeDoBaralhoDeIngles}, &novo); err != nil {
		return "", err
	}
	return novo.ID, nil
}

func (a *app) pedirAoFlashcards(ctx context.Context, metodo, caminho string, corpo, destino any) error {
	var leitor io.Reader
	if corpo != nil {
		b, _ := json.Marshal(corpo)
		leitor = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, metodo, urlDoFlashcards()+caminho, leitor)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return errors.New("O Flashcards não respondeu.")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e struct {
			Erro string `json:"erro"`
		}
		json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&e)
		return fmt.Errorf("O Flashcards recusou o cartão (%d): %s", resp.StatusCode, primeiro(e.Erro, resp.Status))
	}
	if destino != nil {
		return json.NewDecoder(resp.Body).Decode(destino)
	}
	return nil
}

func ultimas(falas []string, n int) []string {
	if len(falas) > n {
		return falas[len(falas)-n:]
	}
	return falas
}
