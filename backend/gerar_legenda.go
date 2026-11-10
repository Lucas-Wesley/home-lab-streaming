package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Legenda gerada pelo áudio (S29): o Parakeet transcreve o vídeo de minuto em minuto, começando
// onde o Lucas está, e a legenda cresce à frente de quem assiste. O Whisper diz o idioma do áudio
// e transcreve os idiomas que o Parakeet não entende. Na assinatura (1 conexão), o servidor puxa
// antes só o áudio do vídeo inteiro (uns 15 s por episódio) e depois a conexão fica livre.

const esquemaDaLegendaGerada = `
CREATE TABLE IF NOT EXISTS legendas_geradas (
	chave     TEXT PRIMARY KEY, -- filme:1, episodio:2, arquivo:3
	faixa     INTEGER NOT NULL DEFAULT 0, -- a faixa de áudio transcrita
	idioma    TEXT NOT NULL DEFAULT '',
	modelo    TEXT NOT NULL DEFAULT '',
	duracao   REAL NOT NULL DEFAULT 0,
	estado    TEXT NOT NULL, -- 'na fila', gerando, esperando, pronta, parada, erro
	mensagem  TEXT NOT NULL DEFAULT '',
	criado_em TEXT NOT NULL
);
-- As palavras ouvidas em cada minuto do vídeo (bloco 0 = 0:00 a 1:00), em JSON. revisadas: as
-- falas do minuto depois do double check (JSON), 'falhou' se a revisão não deu certo, ou vazio.
CREATE TABLE IF NOT EXISTS blocos_gerados (
	chave     TEXT NOT NULL,
	bloco     INTEGER NOT NULL,
	palavras  TEXT NOT NULL,
	revisadas TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (chave, bloco)
);
`

const (
	segundosPorBloco = 60.0
	sobraDoBloco     = 3.0 // cada trecho ouve 3 s a mais, para não cortar a última palavra
)

var (
	errAssinaturaOcupada = errors.New("A assinatura está em uso: o áudio é puxado quando ela ficar livre (uns 15 s).")
	errDeVez             = errors.New("outro vídeo pediu legenda") // deixa a vez para o pedido mais novo
	reIDGerada           = regexp.MustCompile(`^gerada-(filme|episodio|arquivo)-(\d{1,12})$`)
)

func idDaGerada(chave string) string { return "gerada-" + strings.Replace(chave, ":", "-", 1) }

type geracao struct {
	Estado    string  `json:"estado"`
	Mensagem  string  `json:"mensagem,omitempty"`
	Idioma    string  `json:"idioma,omitempty"`
	Feitos    int     `json:"feitos"`    // minutos já ouvidos
	Revisados int     `json:"revisados"` // minutos que passaram pelo double check
	Total     int     `json:"total"`
	Ate       float64 `json:"ate"` // a legenda está pronta, sem buracos, de onde o Lucas está até aqui (s)
	// Puxar: o áudio ainda vai sair da assinatura, que precisa estar livre (o player para uns segundos).
	Puxar   bool `json:"puxar,omitempty"`
	faixa   int
	duracao float64
}

func (a *app) geracaoDe(ctx context.Context, chave string) *geracao {
	var g geracao
	if a.db.QueryRowContext(ctx, `SELECT estado, mensagem, idioma, faixa, duracao FROM legendas_geradas WHERE chave = ?`, chave).
		Scan(&g.Estado, &g.Mensagem, &g.Idioma, &g.faixa, &g.duracao) != nil {
		return nil
	}
	feitos := a.blocosFeitos(ctx, chave)
	g.Feitos = len(feitos)
	a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocos_gerados WHERE chave = ? AND revisadas NOT IN ('', 'falhou')`, chave).Scan(&g.Revisados)
	if g.duracao > 0 {
		g.Total = int(math.Ceil(g.duracao / segundosPorBloco))
	}
	posicao := 0.0
	if p := a.progressoDe(ctx, chave); p != nil {
		posicao = p.Posicao
	}
	g.Ate = coberturaAPartirDe(feitos, g.Total, posicao, g.duracao)
	if m := reChaveDeVideo.FindStringSubmatch(chave); m != nil && m[1] != "arquivo" && g.Estado != "pronta" && g.Estado != "parada" && g.Estado != "erro" {
		_, err := os.Stat(a.audioPuxado(chave))
		g.Puxar = err != nil && g.duracao == 0 && a.baixadoDaAssinatura(ctx, chave) == ""
	}
	return &g
}

func (a *app) blocosFeitos(ctx context.Context, chave string) map[int]bool {
	feitos := map[int]bool{}
	linhas, err := a.db.QueryContext(ctx, `SELECT bloco FROM blocos_gerados WHERE chave = ?`, chave)
	if err != nil {
		return feitos
	}
	defer linhas.Close()
	for linhas.Next() {
		var b int
		if linhas.Scan(&b) == nil {
			feitos[b] = true
		}
	}
	return feitos
}

// coberturaAPartirDe: até onde a legenda vai sem buracos a partir da posição (0 se nem ela está pronta).
func coberturaAPartirDe(feitos map[int]bool, total int, posicao, duracao float64) float64 {
	b := int(posicao / segundosPorBloco)
	if !feitos[b] {
		return 0
	}
	for b < total && feitos[b] {
		b++
	}
	if b >= total && duracao > 0 {
		return duracao
	}
	return float64(b) * segundosPorBloco
}

// proximoBloco: o primeiro minuto que falta a partir de onde o Lucas está; depois, os de antes.
func proximoBloco(feitos map[int]bool, total int, posicao float64) int {
	inicio := min(max(0, int(posicao/segundosPorBloco)), max(0, total-1))
	for i := 0; i < total; i++ {
		if b := (inicio + i) % total; !feitos[b] {
			return b
		}
	}
	return -1
}

// ---------- Rotas ----------

func (a *app) handleGerarLegenda(w http.ResponseWriter, r *http.Request) {
	chave, _, _, ok := chaveDaLegenda(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		a.db.ExecContext(r.Context(), `UPDATE legendas_geradas SET estado = 'parada', mensagem = '' WHERE chave = ? AND estado != 'pronta'`, chave)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if urlDoParakeet() == "" && urlDoWhisper() == "" {
		erroJSON(w, http.StatusServiceUnavailable, "Para gerar legendas, configure o Parakeet (PARAKEET_URL) no servidor.")
		return
	}
	var corpo struct {
		Faixa   *int `json:"faixa"`   // sem faixa: a do inglês, se houver, ou a primeira
		Liberar bool `json:"liberar"` // a tela que toca este vídeo pela assinatura para enquanto o áudio é puxado
	}
	if r.ContentLength > 0 && !lerCorpo(w, r, &corpo) {
		return
	}
	faixa := 0
	if corpo.Faixa != nil {
		faixa = max(0, *corpo.Faixa)
	} else if s := a.formatosGuardados(r.Context(), chave); s != nil {
		for i, au := range s.Audios {
			if strings.HasPrefix(strings.ToLower(au.Idioma), "en") {
				faixa = i
				break
			}
		}
	}
	// Outra faixa: começa do zero. A mesma: continua do minuto em que parou.
	var anterior int
	if a.db.QueryRowContext(r.Context(), `SELECT faixa FROM legendas_geradas WHERE chave = ?`, chave).Scan(&anterior) == nil && anterior != faixa {
		a.apagarGerada(r.Context(), chave)
	}
	_, err := a.db.ExecContext(r.Context(), `INSERT INTO legendas_geradas (chave, faixa, estado, criado_em) VALUES (?, ?, 'na fila', ?)
		ON CONFLICT (chave) DO UPDATE SET estado = CASE WHEN estado = 'pronta' THEN 'pronta' ELSE 'na fila' END,
			mensagem = '', criado_em = excluded.criado_em`, chave, faixa, a.agora().UTC().Format(time.RFC3339Nano))
	if err != nil {
		falha(w, err)
		return
	}
	if err := a.escolherGerada(r.Context(), chave); err != nil {
		falha(w, err)
		return
	}
	if corpo.Liberar {
		a.liberarMu.Lock()
		a.liberarParaOAudio[chave] = true
		a.liberarMu.Unlock()
	}
	a.acordarLegendas()
	writeJSON(w, http.StatusOK, a.legendaDe(r.Context(), chave))
}

// escolherGerada põe a legenda gerada como a escolhida do vídeo (o arquivo nasce vazio e cresce).
func (a *app) escolherGerada(ctx context.Context, chave string) error {
	id := idDaGerada(chave)
	caminho := a.arquivoDaLegenda(id)
	if _, err := os.Stat(caminho); err != nil {
		if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(caminho, nil, 0o644); err != nil {
			return err
		}
	}
	_, err := a.db.ExecContext(ctx, `INSERT INTO legendas_escolhidas (chave, id, idioma, nome, atraso, escala, ajuste, alinhando)
		VALUES (?, ?, '', 'Gerada pelo áudio', 0, 1, 'Feita pelo áudio deste vídeo: o tempo já é o dele.', 0)
		ON CONFLICT (chave) DO UPDATE SET id = excluded.id, nome = excluded.nome, atraso = 0, escala = 1,
			ajuste = excluded.ajuste, alinhando = 0`, chave, id)
	if err == nil {
		a.db.ExecContext(ctx, `UPDATE legendas_escolhidas SET idioma = (SELECT idioma FROM legendas_geradas WHERE chave = ?) WHERE chave = ?`, chave, chave)
	}
	return err
}

func (a *app) apagarGerada(ctx context.Context, chave string) {
	a.db.ExecContext(ctx, `DELETE FROM blocos_gerados WHERE chave = ?`, chave)
	a.db.ExecContext(ctx, `DELETE FROM legendas_geradas WHERE chave = ?`, chave)
	os.Remove(a.arquivoDaLegenda(idDaGerada(chave)))
	os.Remove(a.audioPuxado(chave))
}

// formatosGuardados: os formatos já lidos do vídeo, sem abrir a conexão da assinatura.
func (a *app) formatosGuardados(ctx context.Context, chave string) *sondagem {
	var dados string
	if m := reChaveDeVideo.FindStringSubmatch(chave); m != nil && m[1] == "arquivo" {
		a.db.QueryRowContext(ctx, `SELECT sondagem FROM torrent_arquivos WHERE id = ?`, m[2]).Scan(&dados)
	} else {
		a.db.QueryRowContext(ctx, `SELECT dados FROM formatos_da_assinatura WHERE chave = ?`, chave).Scan(&dados)
	}
	var s sondagem
	if dados == "" || json.Unmarshal([]byte(dados), &s) != nil {
		return nil
	}
	return &s
}

// ---------- O trabalhador ----------

func (a *app) acordarLegendas() {
	select {
	case a.legendasAcordar <- struct{}{}:
	default:
	}
}

// trabalharNasLegendas roda enquanto o servidor estiver no ar: uma legenda por vez (o processador
// é um só), a pedida por último antes.
func (a *app) trabalharNasLegendas(ctx context.Context) {
	a.db.ExecContext(ctx, `UPDATE legendas_geradas SET estado = 'na fila' WHERE estado IN ('gerando', 'esperando')`)
	for {
		esperar := a.gerarPendentes(ctx)
		var espera <-chan time.Time
		if esperar {
			espera = time.After(esperaPelaConexao)
		}
		select {
		case <-ctx.Done():
			return
		case <-a.legendasAcordar:
		case <-espera:
		}
	}
}

func (a *app) gerarPendentes(ctx context.Context) (esperando bool) {
	for ctx.Err() == nil {
		var chaves []string
		linhas, err := a.db.QueryContext(ctx, `SELECT chave FROM legendas_geradas WHERE estado IN ('na fila', 'gerando', 'esperando')
			ORDER BY estado = 'esperando', criado_em DESC`)
		if err != nil {
			return false
		}
		for linhas.Next() {
			var c string
			linhas.Scan(&c)
			chaves = append(chaves, c)
		}
		linhas.Close()
		if len(chaves) == 0 {
			return esperando
		}
		esperando = false
		gerou := false
		for _, chave := range chaves {
			err := a.gerarLegenda(ctx, chave)
			switch {
			case errors.Is(err, errAssinaturaOcupada):
				a.db.ExecContext(ctx, `UPDATE legendas_geradas SET estado = 'esperando', mensagem = ? WHERE chave = ? AND estado IN ('na fila', 'gerando')`, err.Error(), chave)
				esperando = true
				continue
			case errors.Is(err, errDeVez):
			case err != nil:
				a.db.ExecContext(ctx, `UPDATE legendas_geradas SET estado = 'erro', mensagem = ? WHERE chave = ?`, semSegredos(err).Error(), chave)
			}
			gerou = true
			break
		}
		if !gerou {
			return esperando
		}
	}
	return false
}

// gerarLegenda ouve o vídeo minuto a minuto até o fim (ou até aparecer um pedido mais novo).
func (a *app) gerarLegenda(ctx context.Context, chave string) error {
	m := reChaveDeVideo.FindStringSubmatch(chave)
	if m == nil {
		return errors.New("vídeo inválido")
	}
	tipo := m[1]
	id, _ := strconv.ParseInt(m[2], 10, 64)
	var faixa int
	var idioma, criado string
	if err := a.db.QueryRowContext(ctx, `SELECT faixa, idioma, criado_em FROM legendas_geradas WHERE chave = ?`, chave).Scan(&faixa, &idioma, &criado); err != nil {
		return nil
	}
	mudar := func(estado, mensagem string) {
		a.db.ExecContext(ctx, `UPDATE legendas_geradas SET estado = ?, mensagem = ? WHERE chave = ? AND estado NOT IN ('parada', 'pronta')`, estado, mensagem, chave)
	}
	mudar("gerando", "")
	fonte, faixaNaFonte, extras, err := a.fonteDoAudio(ctx, chave, tipo, id, faixa, mudar)
	if err != nil {
		return err
	}
	// Da fonte: a duração e o idioma que o arquivo diz (o áudio puxado guarda o do original).
	s, _ := lerFormatos(ctx, fonte, extras...)
	duracao := s.Duracao
	if duracao <= 0 {
		return errors.New("Não consegui ler a duração do vídeo.")
	}
	if idioma == "" && faixaNaFonte < len(s.Audios) && !semIdioma[strings.ToLower(s.Audios[faixaNaFonte].Idioma)] {
		idioma = idiomaCurto(strings.ToLower(s.Audios[faixaNaFonte].Idioma))
	}
	if idioma == "" {
		mudar("gerando", "Reconhecendo o idioma do áudio…")
		idioma = a.idiomaDaFaixa(ctx, chave, fonte, faixaNaFonte, faixa, duracao, extras...)
	}
	modelo, err := modeloParaOIdioma(idioma)
	if err != nil {
		return err
	}
	a.db.ExecContext(ctx, `UPDATE legendas_geradas SET idioma = ?, modelo = ?, duracao = ? WHERE chave = ?`, idioma, modelo, duracao, chave)
	a.db.ExecContext(ctx, `UPDATE legendas_escolhidas SET idioma = ? WHERE chave = ? AND id = ?`, idioma, chave, idDaGerada(chave))
	mudar("gerando", "")
	total := int(math.Ceil(duracao / segundosPorBloco))
	for ctx.Err() == nil {
		// Parou, trocou de legenda ou chegou um pedido mais novo: sai entre um minuto e outro.
		var estado, escolhida string
		a.db.QueryRowContext(ctx, `SELECT estado FROM legendas_geradas WHERE chave = ?`, chave).Scan(&estado)
		a.db.QueryRowContext(ctx, `SELECT id FROM legendas_escolhidas WHERE chave = ?`, chave).Scan(&escolhida)
		if estado == "parada" || estado == "" {
			return nil
		}
		if escolhida != idDaGerada(chave) {
			a.db.ExecContext(ctx, `UPDATE legendas_geradas SET estado = 'parada', mensagem = '' WHERE chave = ?`, chave)
			return nil
		}
		var mais int
		a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM legendas_geradas WHERE estado = 'na fila' AND criado_em > ?`, criado).Scan(&mais)
		if mais > 0 {
			mudar("na fila", "")
			return errDeVez
		}
		posicao := 0.0
		if p := a.progressoDe(ctx, chave); p != nil {
			posicao = p.Posicao
		}
		feitos := a.blocosFeitos(ctx, chave)
		b := proximoBloco(feitos, total, posicao)
		// Com folga à frente de quem assiste (ou tudo ouvido), a vez é da revisão (double check).
		folga := b < 0 || float64(b)*segundosPorBloco-posicao > folgaParaRevisar || b < int(posicao/segundosPorBloco)
		if folga && revisaoDisponivel(modelo) {
			if grupo := a.blocosParaRevisar(ctx, chave, feitos, total, posicao); len(grupo) > 0 {
				mudar("gerando", "")
				a.revisarBlocos(ctx, chave, grupo, fonte, faixaNaFonte, idioma, extras...)
				if err := a.escreverGerada(ctx, chave); err != nil {
					return err
				}
				continue
			}
		}
		if b < 0 {
			break
		}
		inicio := float64(b) * segundosPorBloco
		wav, err := audioDaFaixa(ctx, fonte, faixaNaFonte, inicio, segundosPorBloco+sobraDoBloco, extras...)
		if err != nil {
			return fmt.Errorf("Não consegui tirar o áudio do minuto %d: %v", b, err)
		}
		ouvidas, err := a.ouvirBloco(ctx, wav, inicio, modelo)
		if err != nil {
			return err
		}
		dados, _ := json.Marshal(ouvidas)
		a.db.ExecContext(ctx, `INSERT OR REPLACE INTO blocos_gerados (chave, bloco, palavras, revisadas) VALUES (?, ?, ?, '')`, chave, b, string(dados))
		if err := a.escreverGerada(ctx, chave); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	a.db.ExecContext(ctx, `UPDATE legendas_geradas SET estado = 'pronta', mensagem = '' WHERE chave = ? AND estado != 'parada'`, chave)
	os.Remove(a.audioPuxado(chave))
	return nil
}

func modeloParaOIdioma(idioma string) (string, error) {
	switch {
	case urlDoParakeet() != "" && (idioma == "" || idiomasDoParakeet[idioma]):
		return modeloDoParakeet(idioma), nil
	case urlDoWhisper() != "":
		return "whisper", nil
	case urlDoParakeet() != "":
		return "", fmt.Errorf("O áudio está em %s, que o Parakeet não entende; para este idioma, configure o Whisper.", idiomaPorExtenso(idioma))
	}
	return "", errors.New("Configure o Parakeet ou o Whisper no servidor.")
}

// ouvirBloco transcreve um minuto e guarda as palavras dele (as da sobra ficam para o próximo).
func (a *app) ouvirBloco(ctx context.Context, wav []byte, inicio float64, modelo string) ([]palavraOuvida, error) {
	var ouvidas []palavraOuvida
	if modelo == "whisper" {
		t, err := a.transcrever(ctx, wav, inicio)
		if err != nil {
			return nil, err
		}
		for _, f := range t.Falas {
			ouvidas = append(ouvidas, palavraOuvida{f.Texto, f.Inicio, f.Fim})
		}
	} else {
		var err error
		if ouvidas, err = a.transcreverPalavras(ctx, wav, inicio, modelo); err != nil {
			return nil, err
		}
	}
	out := []palavraOuvida{}
	for _, p := range ouvidas {
		if p.Inicio < inicio+segundosPorBloco {
			out = append(out, p)
		}
	}
	return out, nil
}

// ---------- De onde vem o áudio ----------

func (a *app) audioPuxado(chave string) string {
	return filepath.Join(filepath.Dir(a.cfg.pastaDosTorrents), "audio", strings.Replace(chave, ":", "-", 1)+".mka")
}

// fonteDoAudio: o arquivo no disco (torrent ou download da assinatura inteiro), o torrent pelo
// servidor interno (baixa o que faltar enquanto ouve) ou, da assinatura, o áudio puxado antes.
func (a *app) fonteDoAudio(ctx context.Context, chave, tipo string, id int64, faixa int, mudar func(string, string)) (fonte string, faixaNaFonte int, extras []string, err error) {
	if tipo == "arquivo" {
		f, _, err := a.arquivo(ctx, id)
		if err != nil {
			return "", 0, nil, errors.New("Vídeo não encontrado.")
		}
		if caminho := a.caminhoBaixado(f); caminho != "" {
			return caminho, faixa, nil, nil
		}
		base, err := a.enderecoInterno()
		if err != nil {
			return "", 0, nil, err
		}
		return fmt.Sprintf("%s/arquivo/%d", base, f.ID), faixa, nil, nil
	}
	if caminho := a.baixadoDaAssinatura(ctx, chave); caminho != "" {
		return caminho, faixa, nil, nil
	}
	puxado := a.audioPuxado(chave)
	if _, err := os.Stat(puxado); err == nil {
		return puxado, 0, nil, nil
	}
	endereco, err := a.enderecoDaAssinatura(ctx, tipo, id)
	if err != nil {
		return "", 0, nil, err
	}
	voltar, parouATela := a.liberarAConexao(ctx, chave)
	defer voltar()
	mudar("gerando", "Puxando o áudio da assinatura (uns 15 s)…")
	tentativas := 1
	if parouATela {
		tentativas = 10 // a tela que acabou de parar demora um pouco para soltar a conexão
	}
	if err := a.puxarAudio(ctx, chave, endereco, faixa, puxado, tentativas); err != nil {
		return "", 0, nil, err
	}
	return puxado, 0, nil, nil
}

// liberarAConexao: se pediram (o vídeo toca numa tela pela assinatura), a tela para enquanto o
// áudio é puxado e depois volta do mesmo ponto. Sem pedido, não faz nada.
func (a *app) liberarAConexao(ctx context.Context, chave string) (voltar func(), parou bool) {
	a.liberarMu.Lock()
	pedido := a.liberarParaOAudio[chave]
	delete(a.liberarParaOAudio, chave)
	a.liberarMu.Unlock()
	if !pedido {
		return func() {}, false
	}
	return a.telas.pararParaOAudio(ctx, chave)
}

// puxarAudio copia só a faixa de áudio do vídeo da assinatura, pela conexão, quando ela está livre.
func (a *app) puxarAudio(ctx context.Context, chave, endereco string, faixa int, destino string, tentativas int) error {
	var c context.Context
	var sair func()
	var err error
	for tentativa := 0; tentativa < tentativas; tentativa++ {
		if c, sair, err = a.conexao.entrarPorTras(ctx, chave); err == nil || tentativa == tentativas-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if err != nil {
		return errAssinaturaOcupada
	}
	defer sair()
	if err := os.MkdirAll(filepath.Dir(destino), 0o755); err != nil {
		return err
	}
	temporario := destino + ".puxando"
	c2, cancel := context.WithTimeout(c, 15*time.Minute)
	defer cancel()
	saida, err := exec.CommandContext(c2, programaFFmpeg, "-v", "error", "-y", "-user_agent", agenteDoPlayer, "-i", endereco,
		"-map", fmt.Sprintf("0:a:%d", faixa), "-vn", "-c:a", "copy", "-f", "matroska", temporario).CombinedOutput()
	if err != nil {
		os.Remove(temporario)
		if c.Err() != nil && ctx.Err() == nil {
			return errAssinaturaOcupada
		}
		return fmt.Errorf("Não consegui puxar o áudio: %s", semSegredos(errors.New(strings.TrimSpace(string(saida)))))
	}
	return os.Rename(temporario, destino)
}

// idiomaDaFaixa: o que os formatos guardados dizem; sem isso, o Whisper ouve 30 s.
func (a *app) idiomaDaFaixa(ctx context.Context, chave, fonte string, faixaNaFonte, faixa int, duracao float64, extras ...string) string {
	if s := a.formatosGuardados(ctx, chave); s != nil && faixa < len(s.Audios) {
		if i := strings.ToLower(s.Audios[faixa].Idioma); !semIdioma[i] {
			return idiomaCurto(i)
		}
	}
	if urlDoWhisper() == "" {
		return ""
	}
	inicio := duracao * 0.3
	wav, err := audioDaFaixa(ctx, fonte, faixaNaFonte, inicio, 30, extras...)
	if err != nil {
		return ""
	}
	t, err := a.transcrever(ctx, wav, inicio)
	if err != nil {
		return ""
	}
	return t.Idioma
}

// idiomaCurto: "eng" e "por" (como vêm nos arquivos) viram "en" e "pt".
func idiomaCurto(i string) string {
	if c, ok := map[string]string{"eng": "en", "por": "pt", "spa": "es", "fre": "fr", "fra": "fr", "ger": "de", "deu": "de", "ita": "it", "jpn": "ja", "kor": "ko", "chi": "zh", "zho": "zh", "rus": "ru"}[i]; ok {
		return c
	}
	return strings.SplitN(i, "-", 2)[0]
}

// ---------- O arquivo da legenda ----------

// falaGerada: uma linha da legenda, com os tempos no vídeo.
type falaGerada struct {
	Inicio float64 `json:"i"`
	Fim    float64 `json:"f"`
	Texto  string  `json:"t"`
}

type blocoGerado struct {
	bloco     int
	palavras  []palavraOuvida // já sem a palavra repetida da emenda com o minuto anterior
	revisadas []falaGerada    // depois do double check; vazio se ainda não houve
	revisado  bool            // passou pela revisão (mesmo que ela tenha falhado)
}

// blocosGerados lê os minutos ouvidos, em ordem.
func (a *app) blocosGerados(ctx context.Context, chave string) ([]blocoGerado, error) {
	linhas, err := a.db.QueryContext(ctx, `SELECT bloco, palavras, revisadas FROM blocos_gerados WHERE chave = ? ORDER BY bloco`, chave)
	if err != nil {
		return nil, err
	}
	defer linhas.Close()
	var out []blocoGerado
	ultimo, fimAnterior := -2, 0.0
	for linhas.Next() {
		var g blocoGerado
		var dados, revisadas string
		var ps []palavraOuvida
		if linhas.Scan(&g.bloco, &dados, &revisadas) != nil || json.Unmarshal([]byte(dados), &ps) != nil {
			continue
		}
		for _, p := range ps {
			// A palavra cortada no começo do minuto já entrou inteira no minuto anterior.
			if g.bloco == ultimo+1 && p.Inicio < fimAnterior-0.05 {
				continue
			}
			g.palavras = append(g.palavras, p)
		}
		fimAnterior = 0
		if len(ps) > 0 {
			fimAnterior = ps[len(ps)-1].Fim
		}
		ultimo = g.bloco
		g.revisado = revisadas != ""
		if g.revisado && revisadas != "falhou" {
			json.Unmarshal([]byte(revisadas), &g.revisadas)
		}
		out = append(out, g)
	}
	return out, nil
}

// falas: as linhas do minuto, revisadas se houver.
func (g blocoGerado) falas() []falaGerada {
	if len(g.revisadas) > 0 {
		return g.revisadas
	}
	return falasDasPalavras(g.palavras)
}

// escreverGerada junta os minutos ouvidos numa legenda SRT, trocando o arquivo de uma vez.
func (a *app) escreverGerada(ctx context.Context, chave string) error {
	blocos, err := a.blocosGerados(ctx, chave)
	if err != nil {
		return err
	}
	var falas []falaGerada
	for _, g := range blocos {
		falas = append(falas, g.falas()...)
	}
	caminho := a.arquivoDaLegenda(idDaGerada(chave))
	temporario := caminho + ".escrevendo"
	if err := os.WriteFile(temporario, []byte(srtDasFalas(falas)), 0o644); err != nil {
		return err
	}
	return os.Rename(temporario, caminho)
}

// Uma legenda: até 2 linhas de 42 letras e 6 s; corta nas pausas e no fim das frases.
const (
	letrasPorLegenda   = 84
	segundosPorLegenda = 6.0
	pausaEntreLegendas = 0.6
)

func falasDasPalavras(ps []palavraOuvida) []falaGerada {
	var falas []falaGerada
	var atual []string
	var inicio, fim float64
	tamanho := 0
	for _, p := range ps {
		if len(atual) > 0 {
			ultima := atual[len(atual)-1]
			fimDeFrase := strings.ContainsAny(ultima[len(ultima)-1:], ".?!")
			if p.Inicio-fim > pausaEntreLegendas || tamanho+1+len(p.Texto) > letrasPorLegenda ||
				p.Fim-inicio > segundosPorLegenda || (fimDeFrase && tamanho >= 20) {
				falas = append(falas, falaGerada{inicio, fim, strings.Join(atual, " ")})
				atual, tamanho = nil, 0
			}
		}
		if len(atual) == 0 {
			inicio = p.Inicio
		} else {
			tamanho++
		}
		atual = append(atual, p.Texto)
		tamanho += len(p.Texto)
		fim = p.Fim
	}
	if len(atual) > 0 {
		falas = append(falas, falaGerada{inicio, fim, strings.Join(atual, " ")})
	}
	return falas
}

func srtDasFalas(falas []falaGerada) string {
	sort.SliceStable(falas, func(i, j int) bool { return falas[i].Inicio < falas[j].Inicio })
	var b strings.Builder
	n := 0
	for i, f := range falas {
		if strings.TrimSpace(f.Texto) == "" {
			continue
		}
		fim := f.Fim
		if i+1 < len(falas) {
			fim = math.Min(fim, falas[i+1].Inicio)
		}
		n++
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", n, tempoDoSRT(f.Inicio, ","), tempoDoSRT(math.Max(fim, f.Inicio+0.3), ","), emDuasLinhas(strings.TrimSpace(f.Texto)))
	}
	return b.String()
}

// emDuasLinhas quebra o texto perto do meio, se passar de 42 letras.
func emDuasLinhas(texto string) string {
	if len([]rune(texto)) <= 42 {
		return texto
	}
	meio, melhor := len(texto)/2, -1
	for i, c := range texto {
		if c == ' ' && (melhor < 0 || absInt(i-meio) < absInt(melhor-meio)) {
			melhor = i
		}
	}
	if melhor < 0 {
		return texto
	}
	return texto[:melhor] + "\n" + texto[melhor+1:]
}

// opcoesDeGerar: se dá para gerar a legenda deste vídeo e de quais faixas de áudio (as que já se
// conhecem; sem os formatos, uma opção só, a faixa que o servidor escolher).
func (a *app) opcoesDeGerar(ctx context.Context, chave string) map[string]any {
	disponivel := urlDoParakeet() != "" || urlDoWhisper() != ""
	type faixaDeAudio struct {
		Indice int    `json:"indice"`
		Idioma string `json:"idioma"`
	}
	faixas := []faixaDeAudio{}
	if s := a.formatosGuardados(ctx, chave); s != nil && len(s.Audios) > 1 {
		for i, au := range s.Audios {
			idioma := strings.ToLower(au.Idioma)
			if !semIdioma[idioma] {
				idioma = idiomaCurto(idioma)
			}
			faixas = append(faixas, faixaDeAudio{i, idioma})
		}
	}
	return map[string]any{"disponivel": disponivel, "faixas": faixas, "revisao": disponivel && revisaoDisponivel(""), "geracao": a.geracaoDe(ctx, chave)}
}

// coberturaDaGerada: até onde a legenda gerada vai, sem buracos, a partir de uma posição.
func (a *app) coberturaDaGerada(ctx context.Context, chave string, posicao float64) float64 {
	var duracao float64
	a.db.QueryRowContext(ctx, `SELECT duracao FROM legendas_geradas WHERE chave = ?`, chave).Scan(&duracao)
	if duracao <= 0 {
		return 0
	}
	return coberturaAPartirDe(a.blocosFeitos(ctx, chave), int(math.Ceil(duracao/segundosPorBloco)), posicao, duracao)
}
