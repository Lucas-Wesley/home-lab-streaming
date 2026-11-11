package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
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

func urlDoFlashcards() string { return strings.TrimRight(env("FLASHCARDS_URL", ""), "/") }

const nomeDoBaralhoDeIngles = "Inglês"

func flashcardsDisponivel() bool { return urlDoFlashcards() != "" && claudeDisponivel() }

func (a *app) handleFlashcardsDisponivel(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"disponivel": flashcardsDisponivel()})
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
	}
	titulo, fonte := "", ""
	if p := a.progressoDe(r.Context(), corpo.Chave); p != nil {
		titulo = p.Titulo
		var tmdb string
		a.db.QueryRowContext(r.Context(), `SELECT tmdb FROM progresso WHERE chave = ?`, corpo.Chave).Scan(&tmdb)
		if m := reTMDBGuardado.FindStringSubmatch(tmdb); m != nil {
			fonte = "https://www.themoviedb.org/" + tiposDoTMDB[m[1]] + "/" + m[2]
		}
	}
	go func() {
		filaDeCartoes.Lock()
		defer filaDeCartoes.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := a.fazerCartao(ctx, frase, titulo, fonte, corpo.Antes, corpo.Depois); err != nil {
			log.Printf("flashcards: %q: %v", frase, err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"estado": "na fila"})
}

// fazerCartao pede ao Claude a frase limpa e a tradução e cria a nota no Flashcards.
func (a *app) fazerCartao(ctx context.Context, frase, titulo, fonte string, antes, depois []string) error {
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
	nota := map[string]string{"baralho_id": baralho, "tipo": "basico", "frente": strings.TrimSpace(c.Ingles), "verso": verso, "tags": "streaming", "fonte": fonte}
	return a.pedirAoFlashcards(ctx, http.MethodPost, "/api/notas", nota, nil)
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
