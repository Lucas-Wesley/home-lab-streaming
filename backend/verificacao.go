package main

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// Verificar uma opção da busca antes de assistir (S23): o torrent entra oculto, só para o motor
// ler o que precisa; o ffprobe lê a duração e os formatos do vídeo pelo torrent (alguns MB, não o
// filme); a duração é comparada com a do TMDB; a impressão digital do arquivo é procurada no
// OpenSubtitles; e saem 3 quadros para conferir a olho. O resultado fica guardado por opção.

const esquemaDasVerificacoes = `
CREATE TABLE IF NOT EXISTS verificacoes (
	chave         TEXT PRIMARY KEY, -- infohash, ou r:<sha1 do indexador, nome e tamanho> sem infohash
	dados         TEXT NOT NULL,    -- JSON de verificacao
	atualizado_em TEXT NOT NULL
);
`

// sondagem é o que o ffprobe diz de um vídeo.
type sondagem struct {
	Duracao  float64        `json:"duracao"` // segundos
	Video    string         `json:"video"`   // h264, hevc, av1, mpeg2video…
	Largura  int            `json:"largura,omitempty"`
	Altura   int            `json:"altura,omitempty"`
	Audios   []faixaDeAudio `json:"audios"`
	Legendas []string       `json:"legendas,omitempty"` // idiomas das legendas de dentro do arquivo
}

type faixaDeAudio struct {
	Codec  string `json:"codec"`
	Idioma string `json:"idioma,omitempty"`
	Canais int    `json:"canais,omitempty"`
	Ouvido bool   `json:"ouvido,omitempty"` // o arquivo não dizia: o idioma foi reconhecido pelo Whisper (S28)
}

type verificacao struct {
	Chave        string    `json:"chave"`
	Hash         string    `json:"hash,omitempty"`
	Estado       string    `json:"estado"` // verificando, ok, suspeito, errado, indefinido, falhou
	Resumo       string    `json:"resumo"`
	Detalhes     []string  `json:"detalhes,omitempty"`
	Arquivo      string    `json:"arquivo,omitempty"`
	ArquivoID    int64     `json:"arquivo_id,omitempty"`
	Formatos     *sondagem `json:"formatos,omitempty"`
	Esperada     float64   `json:"esperada,omitempty"` // duração no TMDB, em segundos
	Identificado string    `json:"identificado,omitempty"`
	Quadros      []string  `json:"quadros,omitempty"`
	Parecer      string    `json:"parecer,omitempty"` // do Claude, quando a verificação ficou em dúvida (S27)
	Quando       string    `json:"quando"`
}

var programaFFprobe, programaFFmpeg = env("FFPROBE", "ffprobe"), env("FFMPEG", "ffmpeg")

// ---------- Ler o vídeo pelo torrent ----------

// enderecoInterno serve os arquivos dos torrents só para este processo, para o ffprobe e o ffmpeg
// lerem por HTTP com Range: o motor busca só os pedaços que eles pedem.
func (a *app) enderecoInterno() (string, error) {
	a.interno.once.Do(func() {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			a.interno.err = err
			return
		}
		mux := http.NewServeMux()
		servir := func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			a.servirArquivoDeTorrent(w, r, id, false)
		}
		mux.HandleFunc("GET /arquivo/{id}", servir)
		mux.HandleFunc("HEAD /arquivo/{id}", servir)
		go http.Serve(l, mux)
		a.interno.base = "http://" + l.Addr().String()
	})
	return a.interno.base, a.interno.err
}

func (a *app) sondar(ctx context.Context, f arquivoDeTorrent) (sondagem, error) {
	base, err := a.enderecoInterno()
	if err != nil {
		return sondagem{}, err
	}
	s, err := lerFormatos(ctx, fmt.Sprintf("%s/arquivo/%d", base, f.ID))
	if err != nil {
		return s, err
	}
	b, _ := json.Marshal(s)
	a.db.ExecContext(ctx, `UPDATE torrent_arquivos SET sondagem = ? WHERE id = ?`, string(b), f.ID)
	return s, nil
}

// lerFormatos roda o ffprobe num endereço (o servidor interno, ou o vídeo da assinatura, S28).
func lerFormatos(ctx context.Context, endereco string, extras ...string) (sondagem, error) {
	var s sondagem
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	args := append([]string{"-v", "error", "-print_format", "json", "-show_format", "-show_streams"}, extras...)
	saida, err := exec.CommandContext(ctx, programaFFprobe, append(args, endereco)...).Output()
	if err != nil {
		var ee *exec.Error
		if errors.As(err, &ee) {
			return s, errors.New("ffprobe não está instalado no servidor")
		}
		return s, fmt.Errorf("não consegui ler o vídeo (%v)", err)
	}
	var p struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType   string            `json:"codec_type"`
			CodecName   string            `json:"codec_name"`
			Width       int               `json:"width"`
			Height      int               `json:"height"`
			Channels    int               `json:"channels"`
			Tags        map[string]string `json:"tags"`
			Disposition map[string]int    `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(saida, &p); err != nil {
		return s, err
	}
	s.Duracao, _ = strconv.ParseFloat(p.Format.Duration, 64)
	for _, st := range p.Streams {
		switch st.CodecType {
		case "video":
			if s.Video == "" && st.Disposition["attached_pic"] == 0 { // a capa embutida também é "video"
				s.Video, s.Largura, s.Altura = st.CodecName, st.Width, st.Height
			}
		case "audio":
			s.Audios = append(s.Audios, faixaDeAudio{Codec: st.CodecName, Idioma: st.Tags["language"], Canais: st.Channels})
		case "subtitle":
			s.Legendas = append(s.Legendas, primeiro(st.Tags["language"], "?"))
		}
	}
	if s.Duracao == 0 || s.Video == "" {
		return s, errors.New("o arquivo não parece ser um vídeo")
	}
	return s, nil
}

// impressaoDigital: o hash do OpenSubtitles, lido pelo torrent (os primeiros e os últimos 64 KB).
func (a *app) impressaoDigital(ctx context.Context, f arquivoDeTorrent) (string, error) {
	const bloco = 64 << 10
	if f.Tamanho < bloco*2 {
		return "", errors.New("arquivo pequeno demais")
	}
	base, err := a.enderecoInterno()
	if err != nil {
		return "", err
	}
	soma := uint64(f.Tamanho)
	for _, inicio := range []int64{0, f.Tamanho - bloco} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/arquivo/%d", base, f.ID), nil)
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", inicio, inicio+bloco-1))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		buf := make([]byte, bloco)
		_, err = io.ReadFull(resp.Body, buf)
		resp.Body.Close()
		if err != nil {
			return "", err
		}
		for i := 0; i < bloco; i += 8 {
			soma += binary.LittleEndian.Uint64(buf[i:])
		}
	}
	return fmt.Sprintf("%016x", soma), nil
}

// identificarPeloHash pergunta ao OpenSubtitles de que título é o arquivo com este hash.
func (a *app) identificarPeloHash(ctx context.Context, hash string) (filmes, series []int64) {
	chave := a.chaveDoOpenSubtitles()
	if chave == "" || hash == "" {
		return nil, nil
	}
	var r struct {
		Data []struct {
			Attributes struct {
				MoviehashMatch bool `json:"moviehash_match"`
				Feature        struct {
					TmdbID       int64 `json:"tmdb_id"`
					ParentTmdbID int64 `json:"parent_tmdb_id"`
				} `json:"feature_details"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if a.pedirAoOpenSubtitles(ctx, chave, http.MethodGet, "/subtitles?moviehash="+hash, nil, &r) != nil {
		return nil, nil
	}
	for _, d := range r.Data {
		if f := d.Attributes.Feature; d.Attributes.MoviehashMatch {
			if f.ParentTmdbID > 0 {
				series = append(series, f.ParentTmdbID)
			} else if f.TmdbID > 0 {
				filmes = append(filmes, f.TmdbID)
			}
		}
	}
	return filmes, series
}

var reNomeDoQuadro = regexp.MustCompile(`^[0-9a-f]{40}-\d+-\d\.jpg$`)

func (a *app) pastaDosQuadros() string {
	return filepath.Join(filepath.Dir(a.cfg.pastaDosTorrents), "quadros")
}

// tirarQuadros salva 3 imagens do vídeo (a 20%, 50% e 80%) para conferir a olho.
func (a *app) tirarQuadros(ctx context.Context, f arquivoDeTorrent, duracao float64) []string {
	base, err := a.enderecoInterno()
	if err != nil || duracao <= 0 {
		return nil
	}
	os.MkdirAll(a.pastaDosQuadros(), 0o755)
	var nomes []string
	for i, parte := range []float64{0.2, 0.5, 0.8} {
		nome := fmt.Sprintf("%s-%d-%d.jpg", f.Hash, f.Indice, i)
		c, cancel := context.WithTimeout(ctx, 90*time.Second)
		err := exec.CommandContext(c, programaFFmpeg, "-v", "error", "-y", "-ss", strconv.Itoa(int(duracao*parte)),
			"-i", fmt.Sprintf("%s/arquivo/%d", base, f.ID), "-frames:v", "1", "-vf", "scale=480:-2", "-q:v", "5",
			filepath.Join(a.pastaDosQuadros(), nome)).Run()
		cancel()
		if err == nil {
			nomes = append(nomes, nome)
		}
	}
	return nomes
}

func (a *app) handleQuadro(w http.ResponseWriter, r *http.Request) {
	nome := r.PathValue("nome")
	if !reNomeDoQuadro.MatchString(nome) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "max-age=604800")
	http.ServeFile(w, r, filepath.Join(a.pastaDosQuadros(), nome))
}

// ---------- A verificação ----------

type pedidoDeVerificacao struct {
	Chave     string `json:"chave"`
	Magnet    string `json:"magnet,omitempty"`
	Link      string `json:"link,omitempty"` // link de download do Prowlarr, sem a chave
	TMDB      string `json:"tmdb"`           // "filme/123" ou "serie/123"
	Temporada int    `json:"temporada,omitempty"`
	Episodio  int    `json:"episodio,omitempty"`
	Nome      string `json:"nome,omitempty"` // nome do lançamento, para os alertas
}

type chaveOculto struct{}

func (a *app) guardarVerificacao(v verificacao) {
	v.Quando = a.carimbo()
	b, _ := json.Marshal(v)
	a.db.Exec(`INSERT INTO verificacoes (chave, dados, atualizado_em) VALUES (?, ?, ?)
		ON CONFLICT (chave) DO UPDATE SET dados = excluded.dados, atualizado_em = excluded.atualizado_em`, v.Chave, string(b), v.Quando)
}

func (a *app) verificacaoDe(chave string) *verificacao {
	var dados string
	if a.db.QueryRow(`SELECT dados FROM verificacoes WHERE chave = ?`, chave).Scan(&dados) != nil {
		return nil
	}
	var v verificacao
	if json.Unmarshal([]byte(dados), &v) != nil {
		return nil
	}
	return &v
}

// fichaDoPedido traz o torrent para o banco (oculto, se for novo) e devolve o hash.
func (a *app) fichaDoPedido(ctx context.Context, p pedidoDeVerificacao) (string, error) {
	oculto := context.WithValue(ctx, chaveOculto{}, true)
	magnet := p.Magnet
	if magnet == "" && p.Link != "" {
		m, mi, err := a.baixarDoProwlarr(ctx, p.Link)
		if err != nil {
			return "", err
		}
		if mi != nil {
			return a.adicionarDaFicha(oculto, mi, "arquivo", "", torrentGuardado{})
		}
		magnet = m
	}
	spec, err := metainfo.ParseMagnetUri(magnet)
	if err != nil {
		return "", errors.New("Link magnet inválido.")
	}
	hash := spec.InfoHash.HexString()
	var temFicha bool
	if a.db.QueryRowContext(ctx, `SELECT tem_ficha FROM torrents WHERE hash = ?`, hash).Scan(&temFicha) == nil && temFicha {
		return hash, nil // já está na lista (ou já foi verificado)
	}
	u, err := a.motor.usar()
	if err != nil {
		return "", err
	}
	defer u.soltar(0)
	c, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	mi, err := u.fichaDoMagnet(c, magnet)
	if err != nil {
		return "", err
	}
	return a.adicionarDaFicha(oculto, mi, "magnet", magnet, torrentGuardado{})
}

// videoPrincipal escolhe o arquivo a verificar: o episódio pedido ou, se não, o maior vídeo.
func (a *app) videoPrincipal(ctx context.Context, hash string, temporada, episodio int) (arquivoDeTorrent, error) {
	arquivos, err := a.arquivosDoTorrent(ctx, hash)
	if err != nil {
		return arquivoDeTorrent{}, err
	}
	var escolhido *arquivoDeTorrent
	for i := range arquivos {
		f := &arquivos[i]
		if f.Tipo != "video" {
			continue
		}
		if episodio > 0 && f.Episodio == episodio && (temporada == 0 || f.Temporada == temporada || f.Temporada == 0) {
			return *f, nil
		}
		if escolhido == nil || f.Tamanho > escolhido.Tamanho {
			escolhido = f
		}
	}
	if escolhido == nil {
		return arquivoDeTorrent{}, errors.New("o torrent não tem vídeo")
	}
	return *escolhido, nil
}

// duracaoEsperada é a do filme no TMDB ou a do episódio, em segundos (0 se o TMDB não sabe).
func (a *app) duracaoEsperada(ctx context.Context, t tituloDoTMDB, temporada, episodio int) float64 {
	if t.Tipo == "filme" {
		return float64(t.Duracao) * 60
	}
	if temporada == 0 {
		temporada = 1
	}
	var d struct {
		Episodes []struct {
			EpisodeNumber int `json:"episode_number"`
			Runtime       int `json:"runtime"`
		} `json:"episodes"`
	}
	if a.perguntarAoTMDB(ctx, a.chaveDoTMDB(), fmt.Sprintf("/tv/%d/season/%d", t.ID, temporada), nil, &d) != nil {
		return 0
	}
	for _, e := range d.Episodes {
		if e.EpisodeNumber == episodio || episodio == 0 {
			return float64(e.Runtime) * 60
		}
	}
	return 0
}

func (a *app) verificar(ctx context.Context, p pedidoDeVerificacao) verificacao {
	v := verificacao{Chave: p.Chave, Estado: "falhou"}
	m := reTMDBGuardado.FindStringSubmatch(p.TMDB)
	if m == nil {
		v.Resumo = "Título inválido."
		return v
	}
	id, _ := strconv.Atoi(m[2])
	t, err := a.tituloCompleto(ctx, a.chaveDoTMDB(), m[1], id)
	if err != nil {
		v.Resumo = err.Error()
		return v
	}
	hash, err := a.fichaDoPedido(ctx, p)
	if err != nil {
		v.Resumo = "Não consegui a lista de arquivos: " + err.Error()
		return v
	}
	v.Hash = hash
	f, err := a.videoPrincipal(ctx, hash, p.Temporada, p.Episodio)
	if err != nil {
		v.Resumo = err.Error()
		return v
	}
	v.Arquivo, v.ArquivoID = filepath.Base(f.Nome), f.ID
	if alerta := arquivoSuspeito(f.Nome); alerta != "" {
		v.Estado, v.Resumo = "errado", alerta
		return v
	}
	s, err := a.sondar(ctx, f)
	if err != nil {
		v.Resumo = "Não consegui ler o vídeo: " + err.Error()
		return v
	}
	v.Formatos = &s
	temporada, episodio := p.Temporada, p.Episodio
	if episodio == 0 {
		temporada, episodio = f.Temporada, f.Episodio
	}
	v.Esperada = a.duracaoEsperada(ctx, t, temporada, episodio)
	if h, err := a.impressaoDigital(ctx, f); err == nil {
		filmes, series := a.identificarPeloHash(ctx, h)
		ids := filmes
		if t.Tipo == "serie" {
			ids = series
		}
		for _, i := range ids {
			if i == int64(t.ID) {
				v.Identificado = "confirmado"
			} else if v.Identificado == "" {
				v.Identificado = "outro"
			}
		}
	}
	v.Quadros = a.tirarQuadros(ctx, f, s.Duracao)
	julgar(&v, t, p.Nome, a.agora())
	// A duração não decidiu: o Whisper ouve um trecho e as falas são comparadas com a legenda do título (S26).
	ouvido := ""
	if (v.Estado == "indefinido" || v.Estado == "suspeito") && urlDoWhisper() != "" {
		ouvido = a.conferirPelaFala(ctx, &v, t, f, s.Duracao, temporada, episodio)
	}
	// Ainda em dúvida: o Claude olha tudo, inclusive os quadros, e dá um parecer (S27). Ele pode
	// confirmar uma dúvida (indefinido → ok) ou desmentir (→ errado); um alerta das regras fica.
	if (v.Estado == "indefinido" || v.Estado == "suspeito") && claudeDisponivel() {
		veredito, explicacao, err := a.parecerDoClaude(ctx, v, t, p.Nome, ouvido)
		switch {
		case err != nil:
			log.Printf("parecer do Claude sobre %s: %v", p.Chave, err)
		case explicacao != "":
			v.Parecer = explicacao
			if veredito == "errado" {
				v.Estado = "errado"
			} else if veredito == "ok" && v.Estado == "indefinido" {
				v.Estado = "ok"
			}
		}
	}
	return v
}

// conferirPelaFala ouve 2 minutos do meio do vídeo e procura essas falas numa legenda do título,
// no idioma do áudio: se batem, é o título; se quase nada bate, provavelmente é outro conteúdo.
// Devolve o que foi ouvido (para o parecer do Claude).
func (a *app) conferirPelaFala(ctx context.Context, v *verificacao, t tituloDoTMDB, f arquivoDeTorrent, duracao float64, temporada, episodio int) string {
	base, err := a.enderecoInterno()
	if err != nil {
		return ""
	}
	ouvido, err := a.ouvir(ctx, fmt.Sprintf("%s/arquivo/%d", base, f.ID), duracao, []float64{0.5}, 120)
	if err != nil {
		v.Detalhes = append(v.Detalhes, "Whisper: "+err.Error()+".")
		return ""
	}
	var texto strings.Builder
	for _, o := range ouvido.Falas {
		texto.WriteString(o.Texto + "\n")
	}
	v.Detalhes = append(v.Detalhes, "Áudio em "+idiomaPorExtenso(ouvido.Idioma)+" (ouvido pelo Whisper).")
	ref := a.legendaDoTitulo(ctx, t, temporada, episodio, ouvido.Idioma)
	if ref == nil {
		return texto.String()
	}
	contadas := 0
	for _, o := range ouvido.Falas {
		if len(palavras(o.Texto)) >= 3 {
			contadas++
		}
	}
	_, fracao := casarFalas(ouvido.Falas, falasComTexto(ref))
	switch {
	case fracao >= 0.3:
		v.Detalhes = append(v.Detalhes, fmt.Sprintf("As falas do áudio conferem com a legenda do título (%.0f%%).", fracao*100))
		if v.Estado == "indefinido" {
			v.Estado = "ok"
		}
	case contadas >= 5 && fracao < 0.1:
		v.Detalhes = append(v.Detalhes, fmt.Sprintf("As falas do áudio não batem com a legenda do título (%.0f%%): pode ser outro conteúdo.", fracao*100))
		v.Estado = "suspeito"
	}
	return texto.String()
}

// legendaDoTitulo baixa (uma vez, fica guardada) a legenda mais provável do título no idioma do
// áudio, para comparar as falas.
func (a *app) legendaDoTitulo(ctx context.Context, t tituloDoTMDB, temporada, episodio int, idioma string) []byte {
	chave := a.chaveDoOpenSubtitles()
	if chave == "" {
		return nil
	}
	v := videoParaLegenda{tmdbFilme: int64(t.ID)}
	if t.Tipo == "serie" {
		v = videoParaLegenda{tmdbSerie: int64(t.ID), temporada: max(temporada, 1), episodio: max(episodio, 1)}
	}
	opcoes, err := a.buscarLegendas(ctx, chave, v)
	if err != nil {
		return nil
	}
	for _, o := range opcoes {
		if idiomaDaLegenda(o.Idioma) == idioma && !o.Maquina {
			caminho := a.arquivoDaLegenda(o.ID)
			if a.baixarLegenda(ctx, o.ID, caminho) == nil {
				if b, err := os.ReadFile(caminho); err == nil {
					return b
				}
			}
			return nil
		}
	}
	return nil
}

var reArquivoPerigoso = regexp.MustCompile(`(?i)\.(exe|msi|bat|cmd|scr|lnk|js|vbs|apk|zip|rar|7z)$`)

func arquivoSuspeito(nome string) string {
	if reArquivoPerigoso.MatchString(nome) {
		return "O vídeo principal é um arquivo " + strings.ToLower(filepath.Ext(nome)) + ", não um vídeo: é falso e pode ser perigoso."
	}
	return ""
}

// julgar compara o que se leu do arquivo com o que o TMDB sabe do título.
func julgar(v *verificacao, t tituloDoTMDB, nome string, agora time.Time) {
	s := v.Formatos
	d := s.Duracao
	v.Detalhes = append(v.Detalhes, "Duração do arquivo: "+duracaoLegivel(d))
	if s.Altura > 0 {
		v.Detalhes = append(v.Detalhes, fmt.Sprintf("Imagem: %dx%d, %s", s.Largura, s.Altura, nomeDoCodec(s.Video)))
	}
	for _, au := range s.Audios {
		v.Detalhes = append(v.Detalhes, "Áudio: "+nomeDoCodec(au.Codec)+idiomaLegivel(au.Idioma))
	}
	if len(s.Legendas) > 0 {
		v.Detalhes = append(v.Detalhes, fmt.Sprintf("Legendas dentro do arquivo: %d (%s)", len(s.Legendas), strings.Join(s.Legendas, ", ")))
	}
	o := "o filme"
	if t.Tipo == "serie" {
		o = "o episódio"
	}
	switch {
	case v.Identificado == "outro":
		v.Estado, v.Resumo = "errado", "A impressão digital do arquivo é de outro título."
	case v.Esperada == 0:
		v.Estado, v.Resumo = "indefinido", fmt.Sprintf("%s. O TMDB não informa a duração para comparar.", duracaoLegivel(d))
	case d < v.Esperada*0.75:
		v.Estado, v.Resumo = "errado", fmt.Sprintf("%s, e %s tem %s: é outro conteúdo ou está incompleto.", duracaoLegivel(d), o, duracaoLegivel(v.Esperada))
	case d < v.Esperada*0.9 || d > v.Esperada*1.3:
		v.Estado, v.Resumo = "suspeito", fmt.Sprintf("%s, e %s tem %s: pode ser outra versão.", duracaoLegivel(d), o, duracaoLegivel(v.Esperada))
	default:
		v.Estado, v.Resumo = "ok", fmt.Sprintf("%s: confere com %s.", duracaoLegivel(d), o)
	}
	if v.Identificado == "confirmado" {
		v.Detalhes = append(v.Detalhes, "Impressão digital confirmada no OpenSubtitles: é este título.")
		if v.Estado != "errado" {
			v.Estado = "ok"
		}
	}
	for _, alerta := range alertasDoNome(nome, t, agora) {
		v.Detalhes = append(v.Detalhes, alerta)
		if v.Estado == "ok" || v.Estado == "indefinido" {
			v.Estado = "suspeito"
		}
	}
}

func duracaoLegivel(s float64) string {
	min := int(math.Round(s / 60))
	if min < 60 {
		return fmt.Sprintf("%d min", min)
	}
	return fmt.Sprintf("%d h %02d min", min/60, min%60)
}

var nomesDeCodecs = map[string]string{
	"h264": "H.264", "hevc": "H.265", "av1": "AV1", "mpeg2video": "MPEG-2", "mpeg4": "MPEG-4 (Xvid)", "vp9": "VP9",
	"aac": "AAC", "eac3": "Dolby Digital Plus", "ac3": "Dolby Digital", "dts": "DTS", "truehd": "Dolby TrueHD",
	"flac": "FLAC", "opus": "Opus", "mp3": "MP3", "vorbis": "Vorbis",
}

func nomeDoCodec(c string) string { return primeiro(nomesDeCodecs[c], c) }

func idiomaLegivel(i string) string {
	nomes := map[string]string{"por": "português", "pt": "português", "eng": "inglês", "en": "inglês", "spa": "espanhol", "jpn": "japonês"}
	if n, ok := nomes[i]; ok {
		return " (" + n + ")"
	}
	return ""
}

// ---------- Alertas pelo nome e pela data ----------

var (
	reCinema       = regexp.MustCompile(`(?i)\b(cam|camrip|hdcam|hd-?ts|telesync|ts|tc|hd-?tc|telecine|scr|screener|dvdscr)\b`)
	reFonteDigital = regexp.MustCompile(`(?i)\b(web-?dl|web-?rip|web|blu-?r+a+y|bdrip|brrip|bdremux|remux|dvdrip|hdrip|amzn|nf|dsnp|hmax|atvp)\b`) // "BluRray" também aparece
)

// alertasDoNome: gravação de cinema, e "digital" de um filme que ainda não saiu em digital.
func alertasDoNome(nome string, t tituloDoTMDB, agora time.Time) []string {
	var alertas []string
	if reCinema.MatchString(nome) {
		alertas = append(alertas, "Gravado no cinema: imagem e som de qualidade baixa.")
	} else if t.Tipo == "filme" && foraDoDigital(t, agora) && reFonteDigital.MatchString(nome) {
		alertas = append(alertas, "O filme ainda não saiu em digital"+quando(t.DigitalEm)+": uma versão WEB ou Blu-ray dele agora é falsa ou é outro filme.")
	}
	return alertas
}

func quando(dia string) string {
	if d, err := time.Parse("2006-01-02", dia); err == nil {
		return " (sai em " + d.Format("02/01/2006") + ")"
	}
	return ""
}

// foraDoDigital: a data digital é futura ou, sem data digital no TMDB, o filme estreou há menos
// de 150 dias (o tempo comum entre o cinema e o digital).
func foraDoDigital(t tituloDoTMDB, agora time.Time) bool {
	if t.DigitalEm != "" {
		return t.DigitalEm > agora.Format("2006-01-02")
	}
	estreia, err := time.Parse("2006-01-02", t.EstreiaEm)
	return err == nil && agora.Sub(estreia) < 150*24*time.Hour
}

// chaveDoResultado identifica uma opção da busca entre buscas: o infohash, quando se sabe.
func chaveDoResultado(r resultadoDaBusca, infohash string) string {
	if infohash == "" && r.Magnet != "" {
		if spec, err := metainfo.ParseMagnetUri(r.Magnet); err == nil {
			infohash = spec.InfoHash.HexString()
		}
	}
	if infohash != "" {
		return strings.ToLower(infohash)
	}
	h := sha1.Sum([]byte(r.Indexador + "|" + r.Titulo + "|" + strconv.FormatInt(r.Tamanho, 10)))
	return "r:" + hex.EncodeToString(h[:])
}

const verificacoesSimultaneas = 3

// ---------- Rotas ----------

func (a *app) handleVerificar(w http.ResponseWriter, r *http.Request) {
	var p pedidoDeVerificacao
	if !lerCorpo(w, r, &p) {
		return
	}
	if p.Chave == "" || (p.Magnet == "" && p.Link == "") || !reTMDBGuardado.MatchString(p.TMDB) {
		erroJSON(w, http.StatusBadRequest, "Pedido de verificação incompleto.")
		return
	}
	if v := a.verificacaoDe(p.Chave); v != nil && (v.Estado == "verificando" || !r.URL.Query().Has("de-novo")) && v.Estado != "falhou" {
		writeJSON(w, http.StatusOK, v)
		return
	}
	v := verificacao{Chave: p.Chave, Estado: "verificando", Resumo: "Verificando…"}
	a.guardarVerificacao(v)
	a.emFundo(func() {
		// Até 3 ao mesmo tempo (cada uma de um torrent, com os próprios semeadores); a página verifica
		// as 3 melhores opções de uma vez.
		a.verificando <- struct{}{}
		defer func() { <-a.verificando }()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		resultado := a.verificar(ctx, p)
		if resultado.Estado == "falhou" {
			log.Printf("verificação de %s: %s", p.Chave, resultado.Resumo)
		}
		a.guardarVerificacao(resultado)
	})
	writeJSON(w, http.StatusAccepted, v)
}

func (a *app) handleVerificacoes(w http.ResponseWriter, r *http.Request) {
	resposta := map[string]*verificacao{}
	for _, c := range strings.Split(r.URL.Query().Get("chaves"), ",") {
		if c = strings.TrimSpace(c); c != "" && len(resposta) < 200 {
			if v := a.verificacaoDe(c); v != nil {
				resposta[c] = v
			}
		}
	}
	writeJSON(w, http.StatusOK, resposta)
}

// limparCandidatos apaga os torrents que só foram verificados e ficaram mais de 7 dias sem ser
// adicionados, e as verificações que o servidor interrompeu ao parar (ficariam "verificando").
func (a *app) limparCandidatos() {
	a.db.Exec(`DELETE FROM verificacoes WHERE json_extract(dados, '$.estado') = 'verificando'`)
	limite := a.agora().AddDate(0, 0, -7).UTC().Format(time.RFC3339)
	rows, err := a.db.Query(`SELECT hash FROM torrents WHERE oculto = 1 AND adicionado_em < ?`, limite)
	if err != nil {
		return
	}
	var hashes []string
	for rows.Next() {
		var h string
		if rows.Scan(&h) == nil {
			hashes = append(hashes, h)
		}
	}
	rows.Close()
	for _, h := range hashes {
		os.Remove(a.motor.caminhoDaFicha(h))
		os.RemoveAll(filepath.Join(a.motor.pastaDosDados(), h))
		a.db.Exec(`DELETE FROM torrents WHERE hash = ?`, h)
	}
}
