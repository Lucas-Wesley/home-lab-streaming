package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

type config struct {
	// Endereço pelo qual as telas buscam o vídeo, como elas o veem (ex.: http://192.168.1.20:8447).
	// Vazio: o servidor usa o próprio IP na rede local e a porta de ADDR_TELAS.
	urlDasTelas string
	addrTelas   string
	// Sincronizar o catálogo sozinho a cada tanto (S3).
	intervaloDoCatalogo time.Duration
	// Torrents (S17): fichas e arquivos baixados; semDHT só nos testes, para não sair da máquina.
	pastaDosTorrents string
	semDHT           bool
	// Avisos no celular (S35): o ntfy do homelab, com o tópico (vazio, o app não avisa), e o endereço
	// do app que a notificação abre.
	ntfy string
	link string
}

func configDoAmbiente() config {
	intervalo, err := time.ParseDuration(env("CATALOGO_A_CADA", "24h"))
	if err != nil {
		intervalo = 24 * time.Hour
	}
	return config{
		urlDasTelas:         strings.TrimRight(env("URL_DAS_TELAS", ""), "/"),
		addrTelas:           env("ADDR_TELAS", ":8081"),
		intervaloDoCatalogo: intervalo,
		ntfy:                env("NTFY_URL", ""),
		link:                strings.TrimRight(env("STREAMING_LINK", "https://streaming.internal"), "/"),
	}
}

type app struct {
	db        *sql.DB
	cfg       config
	avisos    *avisador    // nil sem NTFY_URL (S35)
	http      *http.Client // para o provedor: segue os redirecionamentos dele
	conexao   *conexao
	telas     *controleDasTelas
	segredo   []byte // assina os links de segmentos do ao vivo
	motor     *motor
	downloads *downloads
	tmdb      cacheDoTMDB
	// Busca aos poucos (S36): os indexadores do Prowlarr, as vagas de cada um e as buscas guardadas.
	porIndexador buscaPorIndexador
	// Verificações (S23): o servidor interno que o ffprobe lê, e até 3 verificações ao mesmo tempo.
	interno struct {
		once sync.Once
		base string
		err  error
	}
	verificando chan struct{}
	sondando    sync.Mutex // formatos dos vídeos de um torrent, por trás
	// Downloads da assinatura (S28): o trabalhador acorda por aqui; o que baixa agora, para cancelar.
	downloadsDaAssinaturaAcordar chan struct{}
	baixandoMu                   sync.Mutex
	baixandoAgora                string
	cancelarDownload             context.CancelFunc
	// Legendas geradas pelo áudio (S29): o trabalhador acorda por aqui; liberarParaOAudio marca os
	// vídeos cuja tela pode parar enquanto o áudio da assinatura é puxado.
	legendasAcordar   chan struct{}
	liberarMu         sync.Mutex
	liberarParaOAudio map[string]bool
	// Jogos do dia (S33, S34): as agendas (ge, ESPN, FotMob) e o guia dos canais, guardados por um tempo.
	agenda   agendaDosJogos
	guias    guiasDosCanais
	sincMu   sync.Mutex
	sincroni estadoDaSincronizacao
	agora    func() time.Time
	// emFundo roda trabalho fora do pedido (a sincronização); nos testes, roda na hora.
	emFundo func(func())
}

func novoApp(db *sql.DB, cfg config) (*app, error) {
	segredo := make([]byte, 32)
	if _, err := rand.Read(segredo); err != nil {
		return nil, err
	}
	a := &app{
		db:                           db,
		cfg:                          cfg,
		http:                         &http.Client{Timeout: 0},
		conexao:                      &conexao{},
		verificando:                  make(chan struct{}, verificacoesSimultaneas),
		downloadsDaAssinaturaAcordar: make(chan struct{}, 1),
		legendasAcordar:              make(chan struct{}, 1),
		liberarParaOAudio:            map[string]bool{},
		segredo:                      segredo,
		agora:                        time.Now,
		emFundo:                      func(f func()) { go f() },
		motor:                        novoMotor(cfg.pastaDosTorrents, cfg.semDHT),
		downloads:                    &downloads{progressos: map[int64]int64{}, cancelar: map[int64]context.CancelFunc{}},
		avisos:                       novoAvisador(cfg.ntfy),
	}
	a.telas = novoControleDasTelas(a)
	a.motor.aoDesligar = a.limparTrechos
	return a, nil
}

func (a *app) carimbo() string {
	return a.agora().UTC().Truncate(time.Second).Format(time.RFC3339)
}

func (a *app) ajuste(chave string) (string, error) {
	var valor string
	err := a.db.QueryRow(`SELECT valor FROM ajustes WHERE chave = ?`, chave).Scan(&valor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return valor, err
}

func (a *app) guardarAjuste(chave, valor string) error {
	_, err := a.db.Exec(`INSERT INTO ajustes (chave, valor) VALUES (?, ?)
		ON CONFLICT (chave) DO UPDATE SET valor = excluded.valor`, chave, valor)
	return err
}

func (a *app) mostrarAdulto() bool {
	v, _ := a.ajuste("mostrar_adulto")
	return v == "1"
}
