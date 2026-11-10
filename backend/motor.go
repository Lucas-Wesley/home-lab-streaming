package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

// O motor de torrent fica desligado e só liga quando alguém precisa (S17): para ler a ficha de
// um magnet, para baixar ou para assistir. Cada uso segura o motor; quando o último solta, ele
// desliga depois da folga daquele uso (nenhuma para ficha e download, alguns minutos para
// assistir, para uma pausa não religar tudo). Desligado, não compartilha nada com ninguém.
type motor struct {
	pasta  string // DATA_DIR/torrents: fichas/ e dados/<hash>/
	semDHT bool   // nos testes: sem sair da máquina
	// aoDesligar roda depois que o motor desliga (o app apaga os trechos só assistidos).
	aoDesligar func()

	mu sync.Mutex
	// adicionando: conferir se o torrent já está no motor e adicionar é um passo só. Dois pedidos
	// juntos (o ffprobe e a impressão digital, ou dois do navegador) adicionariam o mesmo torrent
	// duas vezes, e a biblioteca (v1.61.0) entra em pânico ao anunciar ("panicif.False" em
	// updateTorrentInput), derrubando o app.
	adicionando sync.Mutex
	adicoes     int // quantas vezes um torrent entrou no motor (os testes conferem que é uma por torrent)
	cl          *torrent.Client
	usos        int
	desligar    *time.Timer
	ligadoEm    time.Time
}

func novoMotor(pasta string, semDHT bool) *motor {
	return &motor{pasta: pasta, semDHT: semDHT}
}

func (m *motor) pastaDasFichas() string { return filepath.Join(m.pasta, "fichas") }
func (m *motor) pastaDosDados() string  { return filepath.Join(m.pasta, "dados") }

func (m *motor) caminhoDaFicha(hash string) string {
	return filepath.Join(m.pastaDasFichas(), hash+".torrent")
}

// caminhoNoTorrent é onde cada arquivo fica dentro da pasta do torrent. O motor grava com esta
// mesma regra, então o servidor sabe onde está um arquivo baixado sem perguntar a ele.
func caminhoNoTorrent(info *metainfo.Info, fi *metainfo.FileInfo) string {
	var partes []string
	if info.BestName() != metainfo.NoName {
		partes = append(partes, info.BestName())
	}
	return filepath.Join(append(partes, fi.BestPath()...)...)
}

func (m *motor) caminhoDoArquivo(hash string, info *metainfo.Info, fi *metainfo.FileInfo) string {
	return filepath.Join(m.pastaDosDados(), hash, caminhoNoTorrent(info, fi))
}

type uso struct {
	m     *motor
	cl    *torrent.Client
	feito sync.Once
}

// usar liga o motor, se preciso, e o segura até soltar.
func (m *motor) usar() (*uso, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.desligar != nil {
		m.desligar.Stop()
		m.desligar = nil
	}
	if m.cl == nil {
		cl, err := m.ligar()
		if err != nil {
			return nil, err
		}
		m.cl, m.ligadoEm = cl, time.Now()
	}
	m.usos++
	return &uso{m: m, cl: m.cl}, nil
}

func (m *motor) ligar() (*torrent.Client, error) {
	for _, p := range []string{m.pastaDasFichas(), m.pastaDosDados()} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return nil, err
		}
	}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = m.pastaDosDados()
	cfg.DefaultStorage = storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir: m.pastaDosDados(),
		TorrentDirMaker: func(base string, _ *metainfo.Info, hash metainfo.Hash) string {
			return filepath.Join(base, hash.HexString())
		},
		FilePathMaker: func(o storage.FilePathMakerOpts) string { return caminhoNoTorrent(o.Info, o.File) },
	})
	cfg.Seed = false
	cfg.ListenPort = 0
	cfg.NoDHT = m.semDHT
	cfg.Slogger = slog.New(slog.NewTextHandler(io.Discard, nil)) // a biblioteca fala muito
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("ligando o torrent: %w", err)
	}
	return cl, nil
}

// soltar devolve o motor; se ninguém mais usa, ele desliga depois da folga.
func (u *uso) soltar(folga time.Duration) {
	u.feito.Do(func() {
		m := u.m
		m.mu.Lock()
		defer m.mu.Unlock()
		m.usos--
		if m.usos > 0 {
			return
		}
		if m.desligar != nil {
			m.desligar.Stop()
		}
		m.desligar = time.AfterFunc(folga, func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.usos == 0 && m.cl != nil {
				m.cl.Close()
				m.cl = nil
				if m.aoDesligar != nil {
					go m.aoDesligar()
				}
			}
		})
	})
}

func (m *motor) ligado() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cl != nil
}

// abrir põe o torrent no motor a partir da ficha guardada (sem esperar ninguém na rede). Se ele já
// está no motor (a ficha acabou de vir de um magnet, ou outro pedido o abriu), usa o mesmo: ver
// adicionando.
func (u *uso) abrir(hash string) (*torrent.Torrent, error) {
	u.m.adicionando.Lock()
	var h metainfo.Hash
	if err := h.FromHexString(hash); err == nil {
		if t, ok := u.cl.Torrent(h); ok {
			u.m.adicionando.Unlock()
			<-t.GotInfo()
			return t, nil
		}
	}
	mi, err := metainfo.LoadFromFile(u.m.caminhoDaFicha(hash))
	if err != nil {
		u.m.adicionando.Unlock()
		return nil, fmt.Errorf("ficha do torrent: %w", err)
	}
	// Torrent híbrido (v1 + v2): abrir sem dizer o identificador v2. Com os dois de antemão, a
	// biblioteca (v1.61.0) não registra o v2, e o anúncio aos rastreadores para ele a faz entrar em
	// pânico, derrubando o app; sem ele, ela o calcula e registra ao ler a ficha (como num magnet).
	spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		u.m.adicionando.Unlock()
		return nil, fmt.Errorf("ficha do torrent: %w", err)
	}
	if spec.InfoHash != (metainfo.Hash{}) {
		spec.InfoHashV2.SetNone()
	}
	t, _, err := u.cl.AddTorrentSpec(spec)
	u.m.adicoes++
	u.m.adicionando.Unlock()
	if err != nil {
		return nil, err
	}
	<-t.GotInfo()
	return t, nil
}

// fichaDoMagnet procura a ficha de um magnet na rede e guarda.
func (u *uso) fichaDoMagnet(ctx context.Context, magnet string) (*metainfo.MetaInfo, error) {
	// Já aberto no motor (com a ficha ou ainda buscando): usa o mesmo; ver adicionando.
	u.m.adicionando.Lock()
	var t *torrent.Torrent
	if spec, err := metainfo.ParseMagnetUri(magnet); err == nil {
		t, _ = u.cl.Torrent(spec.InfoHash)
	}
	var err error
	if t == nil {
		// Magnet híbrido (btih + btmh): sem o identificador v2, pelo mesmo motivo de abrir.
		var spec *torrent.TorrentSpec
		if spec, err = torrent.TorrentSpecFromMagnetUri(magnet); err == nil {
			if spec.InfoHash != (metainfo.Hash{}) {
				spec.InfoHashV2.SetNone()
			}
			t, _, err = u.cl.AddTorrentSpec(spec)
			u.m.adicoes++
		}
	}
	u.m.adicionando.Unlock()
	if err != nil {
		return nil, fmt.Errorf("link magnet inválido: %w", err)
	}
	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		t.Drop()
		return nil, errors.New("ninguém mandou a ficha deste torrent a tempo; tente atualizar depois")
	}
	mi := t.Metainfo()
	return &mi, nil
}

// soltarTorrent tira o torrent do motor (antes de apagar os arquivos dele).
func (m *motor) soltarTorrent(hash string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cl == nil {
		return
	}
	var h metainfo.Hash
	if err := h.FromHexString(hash); err != nil {
		return
	}
	if t, ok := m.cl.Torrent(h); ok {
		t.Drop()
	}
}

func (m *motor) guardarFicha(mi *metainfo.MetaInfo) (string, error) {
	if err := os.MkdirAll(m.pastaDasFichas(), 0o755); err != nil {
		return "", err
	}
	hash := mi.HashInfoBytes().HexString()
	f, err := os.Create(m.caminhoDaFicha(hash))
	if err != nil {
		return "", err
	}
	if err := mi.Write(f); err != nil {
		f.Close()
		return "", err
	}
	return hash, f.Close()
}
