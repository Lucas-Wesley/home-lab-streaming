package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// Torrents (S17): o Lucas adiciona um link magnet, um arquivo .torrent ou um item do Internet
// Archive; o servidor guarda a ficha (a lista de arquivos) e mostra os vídeos. Assistir baixa só
// o trecho visto; "Baixar" traz o arquivo inteiro para o disco; "Visto" apaga o que foi baixado.
// Não há busca em sites de torrent: a lista é só o que o Lucas trouxer.

// Assistir: o motor fica ligado esta folga depois do último pedido do player, para uma pausa
// não religar tudo.
const folgaDeAssistir = 2 * time.Minute

type torrentGuardado struct {
	Hash         string `json:"hash"`
	Titulo       string `json:"titulo"`
	Origem       string `json:"origem"` // ia, magnet ou arquivo
	Referencia   string `json:"referencia,omitempty"`
	Capa         string `json:"capa,omitempty"`
	Sinopse      string `json:"sinopse,omitempty"`
	Ano          string `json:"ano,omitempty"`
	TemFicha     bool   `json:"tem_ficha"`
	Erro         string `json:"erro,omitempty"`
	AdicionadoEm string `json:"adicionado_em"`
	Videos       int    `json:"videos"`
	Baixados     int    `json:"baixados"`
	Baixando     int    `json:"baixando"`
	Tamanho      int64  `json:"tamanho"`
}

type arquivoDeTorrent struct {
	ID        int64   `json:"id"`
	Hash      string  `json:"hash"`
	Indice    int     `json:"-"`
	Nome      string  `json:"nome"`
	Tamanho   int64   `json:"tamanho"`
	Tipo      string  `json:"tipo"` // video ou legenda
	Temporada int     `json:"temporada,omitempty"`
	Episodio  int     `json:"episodio,omitempty"`
	Download  string  `json:"download"` // "", baixando ou baixado
	Visto     bool    `json:"visto"`
	Baixado   int64   `json:"baixado,omitempty"` // bytes, durante o download
	Posicao   float64 `json:"posicao,omitempty"`
	// Formatos lidos pelo ffprobe (S23): o app diz antes do play se o aparelho toca o som e a imagem.
	Formatos *sondagem `json:"formatos,omitempty"`
}

const esquemaDosTorrents = `
CREATE TABLE IF NOT EXISTS torrents (
	hash          TEXT PRIMARY KEY, -- infohash; para magnet sem ficha ainda, o do próprio link
	titulo        TEXT NOT NULL,
	origem        TEXT NOT NULL,
	referencia    TEXT NOT NULL DEFAULT '', -- o magnet, ou o identificador no Internet Archive
	capa          TEXT NOT NULL DEFAULT '',
	sinopse       TEXT NOT NULL DEFAULT '',
	ano           TEXT NOT NULL DEFAULT '',
	tem_ficha     INTEGER NOT NULL DEFAULT 0,
	erro          TEXT NOT NULL DEFAULT '',
	adicionado_em TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS torrent_arquivos (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	hash      TEXT NOT NULL REFERENCES torrents (hash) ON DELETE CASCADE,
	indice    INTEGER NOT NULL, -- posição do arquivo na ficha
	nome      TEXT NOT NULL,
	tamanho   INTEGER NOT NULL,
	tipo      TEXT NOT NULL, -- video ou legenda
	temporada INTEGER NOT NULL DEFAULT 0,
	episodio  INTEGER NOT NULL DEFAULT 0,
	download  TEXT NOT NULL DEFAULT '', -- '', baixando ou baixado
	visto     INTEGER NOT NULL DEFAULT 0,
	UNIQUE (hash, indice)
);
`

// ---------- De onde vem o torrent ----------

var (
	reMagnet = regexp.MustCompile(`magnet:\?[^\s"'<>]+`)
	// archive.org/details/ID, archive.org/download/ID/..., ou só "ia:ID".
	reArquivoIA = regexp.MustCompile(`(?:archive\.org/(?:details|download|embed)/|^ia:)([A-Za-z0-9._-]+)`)
	// Endereço de rastreador (udp://host:porta/announce): sozinho não identifica torrent nenhum.
	reRastreador = regexp.MustCompile(`^(?:udp|https?|wss?)://\S+/announce\S*$`)
)

// adicionarTorrent entende o texto colado: magnet ou link do Internet Archive.
func (a *app) adicionarTorrent(ctx context.Context, texto string) (string, error) {
	texto = strings.TrimSpace(texto)
	if m := reMagnet.FindString(texto); m != "" {
		return a.adicionarMagnet(ctx, m)
	}
	if m := reArquivoIA.FindStringSubmatch(texto); m != nil {
		return a.adicionarDoArchive(ctx, m[1])
	}
	// Um endereço de rastreador (udp://…/announce) só aponta quem está compartilhando; não diz qual torrent.
	if reRastreador.MatchString(texto) {
		return "", errors.New("Isso é o endereço de um rastreador, não de um torrent. Peça o link magnet (começa com magnet:?xt=) ou o arquivo .torrent.")
	}
	return "", errors.New("Não reconheci o link. Cole um link magnet, um link do Internet Archive ou envie um arquivo .torrent.")
}

func (a *app) adicionarMagnet(ctx context.Context, magnet string) (string, error) {
	spec, err := metainfo.ParseMagnetUri(magnet)
	if err != nil {
		return "", errors.New("Link magnet inválido.")
	}
	hash := spec.InfoHash.HexString()
	titulo := primeiro(spec.DisplayName, "Torrent "+hash[:8])
	if _, err := a.db.ExecContext(ctx, `INSERT INTO torrents (hash, titulo, origem, referencia, adicionado_em)
		VALUES (?, ?, 'magnet', ?, ?) ON CONFLICT (hash) DO NOTHING`, hash, titulo, magnet, a.carimbo()); err != nil {
		return "", err
	}
	// A ficha vem da rede: liga o motor só para isso e desliga.
	a.emFundo(func() { a.lerFichas(context.Background(), []string{hash}) })
	return hash, nil
}

// adicionarDaFicha guarda um .torrent enviado (ou baixado do Internet Archive).
func (a *app) adicionarDaFicha(ctx context.Context, mi *metainfo.MetaInfo, origem, referencia string, extra torrentGuardado) (string, error) {
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return "", errors.New("Arquivo .torrent inválido.")
	}
	hash, err := a.motor.guardarFicha(mi)
	if err != nil {
		return "", err
	}
	titulo := primeiro(extra.Titulo, info.BestName())
	oculto := 0
	if ctx.Value(chaveOculto{}) != nil {
		oculto = 1
	}
	if _, err := a.db.ExecContext(ctx, `INSERT INTO torrents (hash, titulo, origem, referencia, capa, sinopse, ano, tem_ficha, oculto, adicionado_em)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT (hash) DO UPDATE SET tem_ficha = 1, erro = ''`,
		hash, titulo, origem, referencia, extra.Capa, extra.Sinopse, extra.Ano, oculto, a.carimbo()); err != nil {
		return "", err
	}
	return hash, a.guardarArquivos(ctx, hash, &info)
}

// ---------- Arquivos da ficha ----------

var (
	extensoesDeVideo   = map[string]int{".mp4": 1, ".m4v": 1, ".mkv": 2, ".webm": 3, ".avi": 4, ".mov": 4, ".mpeg": 5, ".mpg": 5, ".ts": 5, ".ogv": 6}
	extensoesDeLegenda = map[string]bool{".srt": true, ".vtt": true}
	// S01E02, 1x02, "Episódio 3", " - 03 " (anime).
	reTemporadaEpisodio = regexp.MustCompile(`(?i)\bS(\d{1,2})[ ._-]*E(\d{1,3})\b|\b(\d{1,2})x(\d{2,3})\b`)
	reSoEpisodio        = regexp.MustCompile(`(?i)(?:\bep(?:is[oó]dio|isode)?[ ._-]*|[ _]-[ _])(\d{1,3})\b`)
	// O Internet Archive guarda o mesmo vídeo em vários formatos: "filme_512kb.mp4" é a versão menor.
	reVersaoMenor = regexp.MustCompile(`(?i)_512kb$`)
)

func episodioDe(nome string) (temporada, episodio int) {
	if m := reTemporadaEpisodio.FindStringSubmatch(nome); m != nil {
		if m[1] != "" {
			temporada, _ = strconv.Atoi(m[1])
			episodio, _ = strconv.Atoi(m[2])
		} else {
			temporada, _ = strconv.Atoi(m[3])
			episodio, _ = strconv.Atoi(m[4])
		}
		return
	}
	if m := reSoEpisodio.FindStringSubmatch(nome); m != nil {
		episodio, _ = strconv.Atoi(m[1])
		return 1, episodio
	}
	return 0, 0
}

// escolherArquivos fica com um vídeo por nome (o melhor formato) e com as legendas.
func escolherArquivos(info *metainfo.Info) []arquivoDeTorrent {
	type candidato struct {
		arquivoDeTorrent
		preferencia int
	}
	melhores := map[string]candidato{}
	var legendas []arquivoDeTorrent
	for i, fi := range info.UpvertedFiles() {
		caminho := strings.Join(fi.BestPath(), "/")
		if caminho == "" {
			caminho = info.BestName()
		}
		ext := strings.ToLower(filepath.Ext(caminho))
		base := filepath.Base(caminho)
		if extensoesDeLegenda[ext] {
			legendas = append(legendas, arquivoDeTorrent{Indice: i, Nome: base, Tamanho: fi.Length, Tipo: "legenda"})
			continue
		}
		pref, ok := extensoesDeVideo[ext]
		if !ok || strings.Contains(strings.ToLower(caminho), "sample") {
			continue
		}
		tronco := strings.TrimSuffix(caminho, filepath.Ext(caminho))
		if reVersaoMenor.MatchString(tronco) {
			tronco = reVersaoMenor.ReplaceAllString(tronco, "")
			pref += 10 // só se não houver outra versão
		}
		temporada, episodio := episodioDe(base)
		c := candidato{arquivoDeTorrent{Indice: i, Nome: base, Tamanho: fi.Length, Tipo: "video", Temporada: temporada, Episodio: episodio}, pref}
		if atual, ok := melhores[tronco]; !ok || c.preferencia < atual.preferencia {
			melhores[tronco] = c
		}
	}
	var lista []arquivoDeTorrent
	for _, c := range melhores {
		lista = append(lista, c.arquivoDeTorrent)
	}
	sort.Slice(lista, func(i, j int) bool {
		if lista[i].Temporada != lista[j].Temporada {
			return lista[i].Temporada < lista[j].Temporada
		}
		if lista[i].Episodio != lista[j].Episodio {
			return lista[i].Episodio < lista[j].Episodio
		}
		return lista[i].Nome < lista[j].Nome
	})
	return append(lista, legendas...)
}

func (a *app) guardarArquivos(ctx context.Context, hash string, info *metainfo.Info) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, f := range escolherArquivos(info) {
		if _, err := tx.Exec(`INSERT INTO torrent_arquivos (hash, indice, nome, tamanho, tipo, temporada, episodio)
			VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (hash, indice) DO NOTHING`,
			hash, f.Indice, f.Nome, f.Tamanho, f.Tipo, f.Temporada, f.Episodio); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// lerFichas liga o motor, pega as fichas dos magnets pendentes e desliga.
func (a *app) lerFichas(ctx context.Context, hashes []string) {
	if len(hashes) == 0 {
		return
	}
	u, err := a.motor.usar()
	if err != nil {
		log.Print(err)
		return
	}
	defer u.soltar(0)
	var wg sync.WaitGroup
	for _, hash := range hashes {
		var magnet string
		if err := a.db.QueryRowContext(ctx, `SELECT referencia FROM torrents WHERE hash = ? AND origem = 'magnet'`, hash).Scan(&magnet); err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()
			mi, err := u.fichaDoMagnet(c, magnet)
			if err == nil {
				_, err = a.adicionarDaFicha(ctx, mi, "magnet", magnet, torrentGuardado{})
			}
			erro := ""
			if err != nil {
				erro = err.Error()
			}
			a.db.ExecContext(ctx, `UPDATE torrents SET erro = ? WHERE hash = ?`, erro, hash)
		}()
	}
	wg.Wait()
}

// ---------- Internet Archive ----------

func (a *app) urlDoArchive() string {
	return strings.TrimRight(env("ARCHIVE_URL", "https://archive.org"), "/")
}

type itemDoArchive struct {
	ID        string `json:"id"`
	Titulo    string `json:"titulo"`
	Ano       string `json:"ano,omitempty"`
	Sinopse   string `json:"sinopse,omitempty"`
	Capa      string `json:"capa"`
	Downloads int64  `json:"downloads,omitempty"`
}

var reTags = regexp.MustCompile(`<[^>]+>`)

func textoSimples(s string) string {
	s = reTags.ReplaceAllString(strings.ReplaceAll(s, "<br", "\n<br"), "")
	return strings.TrimSpace(html.UnescapeString(s))
}

// texto do metadado do Archive: às vezes texto, às vezes lista de textos.
func textoDoArchive(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var l []string
	if json.Unmarshal(raw, &l) == nil && len(l) > 0 {
		return l[0]
	}
	return ""
}

func (a *app) adicionarDoArchive(ctx context.Context, id string) (string, error) {
	var meta struct {
		Metadata map[string]json.RawMessage `json:"metadata"`
	}
	if err := a.buscarJSONPublico(ctx, a.urlDoArchive()+"/metadata/"+url.PathEscape(id), &meta); err != nil {
		return "", fmt.Errorf("Internet Archive: %w", err)
	}
	if len(meta.Metadata) == 0 {
		return "", errors.New("Esse item não existe no Internet Archive.")
	}
	extra := torrentGuardado{
		Titulo:  textoDoArchive(meta.Metadata["title"]),
		Sinopse: textoSimples(textoDoArchive(meta.Metadata["description"])),
		Ano:     reAno.FindString(textoDoArchive(meta.Metadata["year"]) + " " + textoDoArchive(meta.Metadata["date"])),
		Capa:    a.urlDoArchive() + "/services/img/" + url.PathEscape(id),
	}
	ficha, err := a.baixarPequeno(ctx, a.urlDoArchive()+"/download/"+url.PathEscape(id)+"/"+url.PathEscape(id)+"_archive.torrent", 20<<20)
	if err != nil {
		return "", fmt.Errorf("torrent do Internet Archive: %w", err)
	}
	mi, err := metainfo.Load(bytes.NewReader(ficha))
	if err != nil {
		return "", errors.New("O Internet Archive mandou um torrent inválido.")
	}
	return a.adicionarDaFicha(ctx, mi, "ia", id, extra)
}

func (a *app) baixarPequeno(ctx context.Context, endereco string, limite int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endereco, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "home-lab-streaming")
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("respondeu %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limite))
}

func (a *app) buscarJSONPublico(ctx context.Context, endereco string, destino any) error {
	b, err := a.baixarPequeno(ctx, endereco, 20<<20)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, destino)
}

// Coleções de vídeo do Internet Archive com mais itens (conferido em 09/11/2026).
var colecoesDoArchive = map[string]bool{"feature_films": true, "animationandcartoons": true, "classic_tv": true, "silent_films": true}

func (a *app) handleBuscarNoArchive(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	colecao := q.Get("colecao")
	if !colecoesDoArchive[colecao] {
		colecao = "feature_films"
	}
	consulta := "collection:" + colecao + " AND mediatype:movies"
	if termo := strings.TrimSpace(q.Get("q")); termo != "" {
		consulta += " AND (" + strings.NewReplacer(`"`, "", `(`, "", `)`, "", `:`, " ").Replace(termo) + ")"
	}
	parametros := url.Values{
		"q": {consulta}, "fl[]": {"identifier", "title", "year", "description", "downloads"},
		"sort[]": {"downloads desc"}, "rows": {"36"}, "page": {strconv.Itoa(numero(q.Get("pagina"), 1, 1, 100))}, "output": {"json"},
	}
	var resposta struct {
		Response struct {
			Docs []struct {
				Identifier  string          `json:"identifier"`
				Title       json.RawMessage `json:"title"`
				Year        flex            `json:"year"`
				Description json.RawMessage `json:"description"`
				Downloads   flex            `json:"downloads"`
			} `json:"docs"`
		} `json:"response"`
	}
	if err := a.buscarJSONPublico(r.Context(), a.urlDoArchive()+"/advancedsearch.php?"+parametros.Encode(), &resposta); err != nil {
		erroJSON(w, http.StatusBadGateway, "Não consegui buscar no Internet Archive: "+err.Error())
		return
	}
	lista := []itemDoArchive{}
	for _, d := range resposta.Response.Docs {
		sinopse := []rune(textoSimples(textoDoArchive(d.Description)))
		if len(sinopse) > 280 {
			sinopse = append(sinopse[:280], '…')
		}
		lista = append(lista, itemDoArchive{
			ID: d.Identifier, Titulo: primeiro(textoDoArchive(d.Title), d.Identifier), Ano: string(d.Year),
			Sinopse: string(sinopse), Capa: a.urlDoArchive() + "/services/img/" + url.PathEscape(d.Identifier), Downloads: d.Downloads.int(),
		})
	}
	writeJSON(w, http.StatusOK, lista)
}

// ---------- Consultas ----------

func (a *app) listaDeTorrents(ctx context.Context, hash string) ([]torrentGuardado, error) {
	consulta := `SELECT t.hash, t.titulo, t.origem, t.referencia, t.capa, t.sinopse, t.ano, t.tem_ficha, t.erro, t.adicionado_em,
			COUNT(f.id) FILTER (WHERE f.tipo = 'video'),
			COUNT(f.id) FILTER (WHERE f.tipo = 'video' AND f.download = 'baixado'),
			COUNT(f.id) FILTER (WHERE f.tipo = 'video' AND f.download = 'baixando'),
			COALESCE(SUM(f.tamanho) FILTER (WHERE f.tipo = 'video'), 0)
		FROM torrents t LEFT JOIN torrent_arquivos f ON f.hash = t.hash`
	args := []any{}
	if hash != "" {
		consulta += ` WHERE t.hash = ?`
		args = append(args, hash)
	} else {
		consulta += ` WHERE t.oculto = 0` // candidatos de verificação não entram na lista (S23)
	}
	rows, err := a.db.QueryContext(ctx, consulta+` GROUP BY t.hash ORDER BY t.adicionado_em DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lista := []torrentGuardado{}
	for rows.Next() {
		var t torrentGuardado
		if err := rows.Scan(&t.Hash, &t.Titulo, &t.Origem, &t.Referencia, &t.Capa, &t.Sinopse, &t.Ano, &t.TemFicha, &t.Erro, &t.AdicionadoEm,
			&t.Videos, &t.Baixados, &t.Baixando, &t.Tamanho); err != nil {
			return nil, err
		}
		if t.Origem == "magnet" {
			t.Referencia = "" // o link é longo e não serve para a tela
		}
		lista = append(lista, t)
	}
	return lista, rows.Err()
}

func (a *app) arquivosDoTorrent(ctx context.Context, hash string) ([]arquivoDeTorrent, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT f.id, f.hash, f.indice, f.nome, f.tamanho, f.tipo, f.temporada, f.episodio, f.download, f.visto,
			COALESCE(p.posicao, 0), f.sondagem
		FROM torrent_arquivos f LEFT JOIN progresso p ON p.chave = 'arquivo:' || f.id
		WHERE f.hash = ? ORDER BY f.tipo DESC, f.temporada, f.episodio, f.nome`, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lista := []arquivoDeTorrent{}
	for rows.Next() {
		var f arquivoDeTorrent
		var formatos string
		if err := rows.Scan(&f.ID, &f.Hash, &f.Indice, &f.Nome, &f.Tamanho, &f.Tipo, &f.Temporada, &f.Episodio, &f.Download, &f.Visto, &f.Posicao, &formatos); err != nil {
			return nil, err
		}
		if f.Download == "baixando" {
			f.Baixado = a.downloads.progresso(f.ID)
		}
		if formatos != "" {
			f.Formatos = &sondagem{}
			json.Unmarshal([]byte(formatos), f.Formatos)
		}
		lista = append(lista, f)
	}
	return lista, rows.Err()
}

func (a *app) arquivo(ctx context.Context, id int64) (arquivoDeTorrent, torrentGuardado, error) {
	var f arquivoDeTorrent
	err := a.db.QueryRowContext(ctx, `SELECT id, hash, indice, nome, tamanho, tipo, temporada, episodio, download, visto FROM torrent_arquivos WHERE id = ?`, id).
		Scan(&f.ID, &f.Hash, &f.Indice, &f.Nome, &f.Tamanho, &f.Tipo, &f.Temporada, &f.Episodio, &f.Download, &f.Visto)
	if err != nil {
		return f, torrentGuardado{}, err
	}
	ts, err := a.listaDeTorrents(ctx, f.Hash)
	if err != nil || len(ts) == 0 {
		return f, torrentGuardado{}, sql.ErrNoRows
	}
	return f, ts[0], nil
}

// tituloDoArquivo: o título do torrent, mais o episódio quando é série.
func tituloDoArquivo(f arquivoDeTorrent, t torrentGuardado) string {
	if f.Episodio > 0 {
		return fmt.Sprintf("%s T%d E%d", primeiro(nomeDoTorrent(t.Titulo), t.Titulo), f.Temporada, f.Episodio)
	}
	videos := t.Videos
	if videos > 1 {
		return t.Titulo + " · " + strings.TrimSuffix(f.Nome, filepath.Ext(f.Nome))
	}
	return t.Titulo
}

// caminhoBaixado é onde o arquivo fica quando baixado inteiro; vazio se não estiver.
func (a *app) caminhoBaixado(f arquivoDeTorrent) string {
	mi, err := metainfo.LoadFromFile(a.motor.caminhoDaFicha(f.Hash))
	if err != nil {
		return ""
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return ""
	}
	arquivos := info.UpvertedFiles()
	if f.Indice >= len(arquivos) {
		return ""
	}
	c := a.motor.caminhoDoArquivo(f.Hash, &info, &arquivos[f.Indice])
	if st, err := os.Stat(c); err != nil || st.Size() != f.Tamanho {
		return ""
	}
	return c
}

func espacoLivre(pasta string) int64 {
	_ = os.MkdirAll(pasta, 0o755)
	var st syscall.Statfs_t
	if syscall.Statfs(pasta, &st) != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

// ---------- Downloads ----------

type downloads struct {
	mu         sync.Mutex
	progressos map[int64]int64
	cancelar   map[int64]context.CancelFunc
}

func (d *downloads) progresso(id int64) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.progressos[id]
}

// baixar traz o arquivo inteiro e desliga o motor quando termina (S17).
func (a *app) baixar(f arquivoDeTorrent) {
	ctx, cancel := context.WithCancel(context.Background())
	a.downloads.mu.Lock()
	if _, ja := a.downloads.cancelar[f.ID]; ja {
		a.downloads.mu.Unlock()
		cancel()
		return
	}
	a.downloads.cancelar[f.ID] = cancel
	a.downloads.mu.Unlock()
	defer func() {
		cancel()
		a.downloads.mu.Lock()
		delete(a.downloads.cancelar, f.ID)
		delete(a.downloads.progressos, f.ID)
		a.downloads.mu.Unlock()
	}()

	u, err := a.motor.usar()
	if err != nil {
		log.Print(err)
		return
	}
	defer u.soltar(0)
	t, err := u.abrir(f.Hash)
	if err != nil {
		log.Print(err)
		return
	}
	arquivo := t.Files()[f.Indice]
	arquivo.Download()
	relogio := time.NewTicker(time.Second)
	defer relogio.Stop()
	for {
		feito := arquivo.BytesCompleted()
		a.downloads.mu.Lock()
		a.downloads.progressos[f.ID] = feito
		a.downloads.mu.Unlock()
		if feito >= arquivo.Length() {
			// Tira do motor antes de conferir: a biblioteca troca o nome do arquivo parcial no fim.
			t.Drop()
			a.db.Exec(`UPDATE torrent_arquivos SET download = 'baixado' WHERE id = ?`, f.ID)
			if f, tg, err := a.arquivo(context.Background(), f.ID); err == nil {
				a.avisos.avisar(aviso{Titulo: tituloDoArquivo(f, tg), Texto: "Baixado: pronto para assistir.", Link: a.cfg.link + "/#/torrent/" + f.Hash})
			}
			return
		}
		select {
		case <-ctx.Done():
			arquivo.SetPriority(0)
			return
		case <-relogio.C:
		}
	}
}

// retomarDownloads continua, ao ligar o servidor, o que estava baixando.
func (a *app) retomarDownloads() {
	rows, err := a.db.Query(`SELECT id FROM torrent_arquivos WHERE download = 'baixando'`)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if f, _, err := a.arquivo(context.Background(), id); err == nil {
			go a.baixar(f)
		}
	}
}

// apagarDados tira do disco o que foi baixado de um arquivo (inteiro ou em parte).
func (a *app) apagarDados(f arquivoDeTorrent) error {
	a.motor.soltarTorrent(f.Hash)
	mi, err := metainfo.LoadFromFile(a.motor.caminhoDaFicha(f.Hash))
	if err != nil {
		return nil // sem ficha, não há o que apagar
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return err
	}
	arquivos := info.UpvertedFiles()
	if f.Indice >= len(arquivos) {
		return nil
	}
	c := a.motor.caminhoDoArquivo(f.Hash, &info, &arquivos[f.Indice])
	for _, p := range []string{c, c + ".part"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// limparTrechos apaga o que foi baixado só para assistir: fica no disco apenas o que o Lucas
// mandou baixar. Roda quando o motor desliga.
func (a *app) limparTrechos() {
	pastas, err := os.ReadDir(a.motor.pastaDosDados())
	if err != nil {
		return
	}
	for _, p := range pastas {
		var guardados int
		if err := a.db.QueryRow(`SELECT COUNT(*) FROM torrent_arquivos WHERE hash = ? AND download != ''`, p.Name()).Scan(&guardados); err != nil || guardados > 0 {
			continue
		}
		if a.motor.ligado() {
			return // religou enquanto limpava: deixa para a próxima
		}
		_ = os.RemoveAll(filepath.Join(a.motor.pastaDosDados(), p.Name()))
	}
}

// ---------- Rotas ----------

func (a *app) handleTorrents(w http.ResponseWriter, r *http.Request) {
	lista, err := a.listaDeTorrents(r.Context(), "")
	if err != nil {
		falha(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"torrents":     lista,
		"motor_ligado": a.motor.ligado(),
		"espaco_livre": espacoLivre(a.motor.pasta),
	})
}

func (a *app) handleAdicionarTorrent(w http.ResponseWriter, r *http.Request) {
	var hash string
	var err error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
		arquivo, _, e := r.FormFile("arquivo")
		if e != nil {
			erroJSON(w, http.StatusBadRequest, "Envie um arquivo .torrent.")
			return
		}
		defer arquivo.Close()
		mi, e := metainfo.Load(arquivo)
		if e != nil {
			erroJSON(w, http.StatusBadRequest, "Arquivo .torrent inválido.")
			return
		}
		hash, err = a.adicionarDaFicha(r.Context(), mi, "arquivo", "", torrentGuardado{})
	} else {
		var corpo struct {
			Texto    string `json:"texto"`
			Prowlarr string `json:"prowlarr"` // link de download de um resultado da busca
			TMDB     string `json:"tmdb"`     // "serie/1396": o título da busca, para a legenda (S21)
		}
		if !lerCorpo(w, r, &corpo) {
			return
		}
		if corpo.Prowlarr != "" {
			hash, err = a.adicionarDoProwlarr(r.Context(), corpo.Prowlarr)
		} else {
			hash, err = a.adicionarTorrent(r.Context(), corpo.Texto)
		}
		if err == nil {
			// Adicionar de verdade um candidato que só tinha sido verificado: ele entra na lista.
			a.db.ExecContext(r.Context(), `UPDATE torrents SET oculto = 0 WHERE hash = ?`, hash)
		}
		if err == nil && reTMDBGuardado.MatchString(corpo.TMDB) {
			a.db.ExecContext(r.Context(), `UPDATE torrents SET tmdb = ? WHERE hash = ?`, corpo.TMDB, hash)
		}
	}
	if err != nil {
		erroJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"hash": hash})
}

func (a *app) handleTorrent(w http.ResponseWriter, r *http.Request) {
	lista, err := a.listaDeTorrents(r.Context(), r.PathValue("hash"))
	if err != nil {
		falha(w, err)
		return
	}
	if len(lista) == 0 {
		erroJSON(w, http.StatusNotFound, "Torrent não encontrado.")
		return
	}
	arquivos, err := a.arquivosDoTorrent(r.Context(), lista[0].Hash)
	if err != nil {
		falha(w, err)
		return
	}
	a.sondarQuemFalta(arquivos)
	writeJSON(w, http.StatusOK, map[string]any{"torrent": lista[0], "arquivos": arquivos, "motor_ligado": a.motor.ligado()})
}

// handleAtualizarTorrents busca as fichas dos magnets que ainda não têm (liga e desliga o motor).
func (a *app) handleAtualizarTorrents(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.QueryContext(r.Context(), `SELECT hash FROM torrents WHERE tem_ficha = 0`)
	if err != nil {
		falha(w, err)
		return
	}
	var pendentes []string
	for rows.Next() {
		var h string
		if rows.Scan(&h) == nil {
			pendentes = append(pendentes, h)
		}
	}
	rows.Close()
	a.emFundo(func() { a.lerFichas(context.Background(), pendentes) })
	writeJSON(w, http.StatusAccepted, map[string]int{"pendentes": len(pendentes)})
}

func (a *app) handleApagarTorrent(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(hash) {
		erroJSON(w, http.StatusNotFound, "Torrent não encontrado.")
		return
	}
	a.motor.soltarTorrent(hash)
	a.downloads.mu.Lock()
	for id, cancel := range a.downloads.cancelar {
		if f, _, err := a.arquivo(r.Context(), id); err == nil && f.Hash == hash {
			cancel()
		}
	}
	a.downloads.mu.Unlock()
	_ = os.RemoveAll(filepath.Join(a.motor.pastaDosDados(), hash))
	_ = os.Remove(a.motor.caminhoDaFicha(hash))
	for _, tabela := range []string{"progresso", "legendas_escolhidas"} {
		if _, err := a.db.ExecContext(r.Context(), `DELETE FROM `+tabela+` WHERE chave IN (SELECT 'arquivo:' || id FROM torrent_arquivos WHERE hash = ?)`, hash); err != nil {
			falha(w, err)
			return
		}
	}
	if _, err := a.db.ExecContext(r.Context(), `DELETE FROM torrents WHERE hash = ?`, hash); err != nil {
		falha(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) arquivoDoCaminho(w http.ResponseWriter, r *http.Request) (arquivoDeTorrent, bool) {
	id, ok := idDoCaminho(w, r)
	if !ok {
		return arquivoDeTorrent{}, false
	}
	f, _, err := a.arquivo(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		erroJSON(w, http.StatusNotFound, "Arquivo não encontrado.")
		return f, false
	}
	if err != nil {
		falha(w, err)
		return f, false
	}
	return f, true
}

func (a *app) handleBaixarArquivo(w http.ResponseWriter, r *http.Request) {
	f, ok := a.arquivoDoCaminho(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		a.downloads.mu.Lock()
		if cancel, ok := a.downloads.cancelar[f.ID]; ok {
			cancel()
		}
		a.downloads.mu.Unlock()
		// Cancelar um download ou apagar o arquivo já baixado: o vídeo continua na lista, para
		// assistir pelo torrent.
		if f.Download != "" {
			if err := a.apagarArquivo(r.Context(), f); err != nil {
				falha(w, err)
				return
			}
		}
		a.db.ExecContext(r.Context(), `UPDATE torrent_arquivos SET download = '' WHERE id = ?`, f.ID)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if f.Download == "baixado" && a.caminhoBaixado(f) != "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Deixa uma folga de 2 GB no disco.
	if livre := espacoLivre(a.motor.pasta); livre >= 0 && f.Tamanho > livre-2<<30 {
		erroJSON(w, http.StatusInsufficientStorage, fmt.Sprintf("Sem espaço: o arquivo tem %.1f GB e sobram %.1f GB no servidor.", float64(f.Tamanho)/(1<<30), float64(livre)/(1<<30)))
		return
	}
	if _, err := a.db.ExecContext(r.Context(), `UPDATE torrent_arquivos SET download = 'baixando', visto = 0 WHERE id = ?`, f.ID); err != nil {
		falha(w, err)
		return
	}
	f.Download = "baixando"
	go a.baixar(f)
	w.WriteHeader(http.StatusAccepted)
}

// handleVisto marca como visto e apaga o que foi baixado; desmarcar só tira a marca.
func (a *app) handleVisto(w http.ResponseWriter, r *http.Request) {
	f, ok := a.arquivoDoCaminho(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		a.db.ExecContext(r.Context(), `UPDATE torrent_arquivos SET visto = 0 WHERE id = ?`, f.ID)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	a.downloads.mu.Lock()
	if cancel, ok := a.downloads.cancelar[f.ID]; ok {
		cancel()
	}
	a.downloads.mu.Unlock()
	if _, err := a.db.ExecContext(r.Context(), `UPDATE torrent_arquivos SET visto = 1 WHERE id = ?`, f.ID); err != nil {
		falha(w, err)
		return
	}
	if err := a.apagarArquivo(r.Context(), f); err != nil {
		falha(w, err)
		return
	}
	a.db.ExecContext(r.Context(), `DELETE FROM progresso WHERE chave = ?`, fmt.Sprintf("arquivo:%d", f.ID))
	w.WriteHeader(http.StatusNoContent)
}

// apagarArquivo tira do disco o que foi baixado de um vídeo.
func (a *app) apagarArquivo(ctx context.Context, f arquivoDeTorrent) error {
	if err := a.apagarDados(f); err != nil {
		return err
	}
	if _, err := a.db.ExecContext(ctx, `UPDATE torrent_arquivos SET download = '' WHERE id = ?`, f.ID); err != nil {
		return err
	}
	// O torrent baixa em blocos, e a ponta de um bloco cai nos arquivos vizinhos (outros formatos,
	// capas). Se nada mais deste torrent está baixado, a pasta inteira sai.
	var guardados int
	a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM torrent_arquivos WHERE hash = ? AND download != ''`, f.Hash).Scan(&guardados)
	if guardados == 0 {
		_ = os.RemoveAll(filepath.Join(a.motor.pastaDosDados(), f.Hash))
	}
	return nil
}

// servirArquivoDeTorrent entrega o vídeo do disco, se baixado, ou do motor, baixando só o trecho pedido.
func (a *app) servirArquivoDeTorrent(w http.ResponseWriter, r *http.Request, id int64, paraTela bool) {
	f, t, err := a.arquivo(r.Context(), id)
	if err != nil || f.Tipo != "video" {
		http.NotFound(w, r)
		return
	}
	nome := f.Nome
	w.Header().Set("Content-Type", mimeDe(strings.TrimPrefix(filepath.Ext(nome), ".")))
	w.Header().Set("Cache-Control", "no-store")
	if paraTela {
		cabecalhosDLNA(w, fonte{})
	}
	if c := a.caminhoBaixado(f); c != "" {
		http.ServeFile(w, r, c)
		return
	}
	u, err := a.motor.usar()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer u.soltar(folgaDeAssistir)
	if !strings.HasPrefix(r.Host, "127.0.0.1:") { // não o próprio ffprobe, pelo endereço interno
		a.sondarAoAssistir(f)
	}
	tt, err := u.abrir(t.Hash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	leitor := tt.Files()[f.Indice].NewReader()
	defer leitor.Close()
	leitor.SetContext(r.Context())
	leitor.SetReadahead(8 << 20)
	if paraTela {
		leitor.SetReadahead(32 << 20) // a TV guarda pouco à frente: um engasgo do torrent vira tela preta
	}
	leitor.SetResponsive()
	http.ServeContent(w, r, nome, time.Time{}, leitor)
}

// sondarQuemFalta lê, por trás, os formatos dos vídeos que ainda não foram lidos (até 3 por vez),
// para a página dizer antes do play se o aparelho toca o som e a imagem. Só os baixados: os outros
// pediriam o motor, que não liga só para listar (S17); eles são lidos ao assistir (sondarAoAssistir).
func (a *app) sondarQuemFalta(arquivos []arquivoDeTorrent) {
	var falta []arquivoDeTorrent
	for _, f := range arquivos {
		if f.Tipo == "video" && f.Formatos == nil && f.Download == "baixado" && len(falta) < 3 {
			falta = append(falta, f)
		}
	}
	if len(falta) == 0 || !a.sondando.TryLock() {
		return
	}
	a.emFundo(func() {
		defer a.sondando.Unlock()
		for _, f := range falta {
			if _, err := a.sondar(context.Background(), f); err != nil {
				log.Printf("formatos de %s: %v", f.Nome, err)
				// Não tentar de novo a cada abertura da página.
				a.db.Exec(`UPDATE torrent_arquivos SET sondagem = '{}' WHERE id = ? AND sondagem = ''`, f.ID)
			}
		}
	})
}

// sondarAoAssistir lê os formatos de um vídeo na primeira vez em que ele é assistido pelo torrent:
// o motor já está ligado para isso.
func (a *app) sondarAoAssistir(f arquivoDeTorrent) {
	var formatos string
	if a.db.QueryRow(`SELECT sondagem FROM torrent_arquivos WHERE id = ?`, f.ID).Scan(&formatos) != nil || formatos != "" {
		return
	}
	a.sondarQuemFalta([]arquivoDeTorrent{{ID: f.ID, Hash: f.Hash, Indice: f.Indice, Nome: f.Nome, Tamanho: f.Tamanho, Tipo: "video", Download: "baixado"}})
}
