package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

var version = "dev"

//go:embed all:web/dist
var webDist embed.FS

func main() {
	// "/app procurador": só a busca de telas, com a rede do servidor (procurador.go).
	if len(os.Args) > 1 && os.Args[1] == "procurador" {
		srv := &http.Server{Addr: env("ADDR", "127.0.0.1:8452"), Handler: procuradorDeTelas(), ReadHeaderTimeout: 5 * time.Second}
		log.Printf("procurador de telas %s em %s", version, srv.Addr)
		log.Fatal(srv.ListenAndServe())
	}
	dataDir := env("DATA_DIR", "./data")
	addr := env("ADDR", ":8080")
	// As telas (TV, projetor) buscam o vídeo por HTTP simples: o DLNA não confia na nossa autoridade certificadora.
	addrTelas := env("ADDR_TELAS", ":8081")

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatal(err)
	}
	db, err := abrirBanco(filepath.Join(dataDir, "streaming.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := configDoAmbiente()
	cfg.pastaDosTorrents = filepath.Join(dataDir, "torrents")
	a, err := novoApp(db, cfg)
	if err != nil {
		log.Fatal(err)
	}
	go a.sincronizarSempre(ctx)
	a.retomarDownloads()
	go a.limparCandidatos()
	go a.trabalharNosDownloads(context.Background())
	go a.trabalharNasLegendas(context.Background())
	go a.limparTrechos() // sobras de antes de desligar o servidor

	web, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		log.Fatal(err)
	}

	servidores := []*http.Server{
		{Addr: addr, Handler: newServer(a, web, version), ReadHeaderTimeout: 5 * time.Second},
		{Addr: addrTelas, Handler: a.servidorDasTelas(), ReadHeaderTimeout: 5 * time.Second},
	}
	for _, srv := range servidores {
		go func() {
			log.Printf("streaming %s em %s", version, srv.Addr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatal(err)
			}
		}()
	}

	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, srv := range servidores {
		if err := srv.Shutdown(shutdown); err != nil {
			log.Print(err)
		}
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func abrirBanco(caminho string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", caminho+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(esquema + esquemaDosTorrents + esquemaDasLegendas + esquemaDasVerificacoes + esquemaDasOpcoes + esquemaDosDownloadsDaAssinatura + esquemaDaLegendaGerada + esquemaDosRelatos); err != nil {
		db.Close()
		return nil, err
	}
	// Colunas que vieram depois da primeira versão. SQLite não tem "ADD COLUMN IF NOT EXISTS":
	// a coluna que já existe dá erro de "duplicate column", que é o esperado.
	for _, c := range []string{
		`ALTER TABLE episodios ADD COLUMN tmdb INTEGER NOT NULL DEFAULT 0`,           // código do episódio no TMDB, para a legenda
		`ALTER TABLE torrents ADD COLUMN tmdb TEXT NOT NULL DEFAULT ''`,              // "filme/123" ou "serie/123", quando veio da busca
		`ALTER TABLE torrents ADD COLUMN oculto INTEGER NOT NULL DEFAULT 0`,          // candidato de uma verificação, fora da lista (S23)
		`ALTER TABLE torrent_arquivos ADD COLUMN sondagem TEXT NOT NULL DEFAULT ''`,  // formatos lidos pelo ffprobe (JSON)
		`ALTER TABLE legendas_escolhidas ADD COLUMN atraso REAL NOT NULL DEFAULT 0`,  // segundos a somar (S25)
		`ALTER TABLE legendas_escolhidas ADD COLUMN escala REAL NOT NULL DEFAULT 1`,  // velocidade (25 × 23,976 quadros)
		`ALTER TABLE legendas_escolhidas ADD COLUMN ajuste TEXT NOT NULL DEFAULT ''`, // como o atraso foi achado
		`ALTER TABLE legendas_escolhidas ADD COLUMN alinhando INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE progresso ADD COLUMN tmdb TEXT NOT NULL DEFAULT ''`, // "serie/1396": junta a assinatura e os torrents do mesmo título
	} {
		if _, err := db.Exec(c); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}

// Horários em RFC 3339, UTC e sem fração de segundo: assim a ordem do texto é a ordem do tempo.
const esquema = `
-- Ajustes do app, inclusive o acesso ao provedor (S2): nunca no código nem no Git.
CREATE TABLE IF NOT EXISTS ajustes (
	chave TEXT PRIMARY KEY,
	valor TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS categorias (
	tipo   TEXT NOT NULL, -- canal, filme ou serie
	id     TEXT NOT NULL,
	nome   TEXT NOT NULL,
	adulto INTEGER NOT NULL DEFAULT 0,
	ordem  INTEGER NOT NULL,
	PRIMARY KEY (tipo, id)
);
-- O catálogo do provedor, copiado inteiro a cada sincronização (S3).
CREATE TABLE IF NOT EXISTS itens (
	tipo         TEXT NOT NULL,
	id           INTEGER NOT NULL, -- stream_id (canal e filme) ou series_id
	nome         TEXT NOT NULL,
	busca        TEXT NOT NULL, -- nome em minúsculas e sem acento
	categoria_id TEXT NOT NULL,
	capa         TEXT NOT NULL DEFAULT '',
	nota         REAL NOT NULL DEFAULT 0,
	ano          TEXT NOT NULL DEFAULT '',
	extensao     TEXT NOT NULL DEFAULT '',
	adicionado   INTEGER NOT NULL DEFAULT 0, -- unix, para "Recentes"
	ordem        INTEGER NOT NULL,
	PRIMARY KEY (tipo, id)
);
CREATE INDEX IF NOT EXISTS itens_categoria ON itens (tipo, categoria_id, ordem);
CREATE INDEX IF NOT EXISTS itens_recentes ON itens (tipo, adicionado DESC);
-- Episódios das séries abertas no app; vêm do provedor sob demanda.
CREATE TABLE IF NOT EXISTS episodios (
	id        INTEGER PRIMARY KEY,
	serie_id  INTEGER NOT NULL,
	temporada INTEGER NOT NULL,
	numero    INTEGER NOT NULL,
	titulo    TEXT NOT NULL,
	extensao  TEXT NOT NULL,
	duracao   INTEGER NOT NULL DEFAULT 0, -- segundos
	capa      TEXT NOT NULL DEFAULT '',
	sinopse   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS episodios_serie ON episodios (serie_id, temporada, numero);
CREATE TABLE IF NOT EXISTS series_lidas (
	serie_id INTEGER PRIMARY KEY,
	info     TEXT NOT NULL, -- JSON com sinopse, elenco etc.
	lida_em  TEXT NOT NULL
);
-- Onde parou em cada filme e episódio, para "Continuar assistindo" e para passar o vídeo à TV.
CREATE TABLE IF NOT EXISTS progresso (
	chave         TEXT PRIMARY KEY, -- filme:123 ou episodio:456
	titulo        TEXT NOT NULL,
	subtitulo     TEXT NOT NULL DEFAULT '',
	capa          TEXT NOT NULL DEFAULT '',
	serie_id      INTEGER NOT NULL DEFAULT 0,
	posicao       REAL NOT NULL,
	duracao       REAL NOT NULL,
	atualizado_em TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS favoritos (
	tipo       TEXT NOT NULL, -- canal, filme ou serie
	id         INTEGER NOT NULL,
	marcado_em TEXT NOT NULL,
	PRIMARY KEY (tipo, id)
);
-- TV, projetor e outros aparelhos que tocam vídeo pela rede (S8).
CREATE TABLE IF NOT EXISTS telas (
	id            TEXT PRIMARY KEY, -- UDN do aparelho
	nome          TEXT NOT NULL,
	nome_original TEXT NOT NULL,
	fabricante    TEXT NOT NULL DEFAULT '',
	endereco      TEXT NOT NULL, -- IP
	transporte    TEXT NOT NULL, -- URL de controle do AVTransport
	volume        TEXT NOT NULL DEFAULT '', -- URL de controle do RenderingControl
	descricao     TEXT NOT NULL, -- URL da descrição, para achar a tela de novo se mudar de porta
	vista_em      TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sugestoes (
	id            TEXT PRIMARY KEY,
	texto         TEXT NOT NULL,
	tela          TEXT NOT NULL DEFAULT '',
	estado        TEXT NOT NULL DEFAULT 'nova',
	resposta      TEXT NOT NULL DEFAULT '',
	criada_em     TEXT NOT NULL,
	atualizada_em TEXT NOT NULL
);
`
