package main

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"path"
	"strings"
)

func newServer(a *app, web fs.FS, version string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.handleHealth(version))

	mux.HandleFunc("GET /api/acesso", a.handleAcesso)
	mux.HandleFunc("PUT /api/acesso", a.handleGuardarAcesso)
	mux.HandleFunc("DELETE /api/acesso", a.handleApagarAcesso)
	mux.HandleFunc("GET /api/ajustes", a.handleAjustes)
	mux.HandleFunc("PATCH /api/ajustes", a.handleMudarAjustes)

	mux.HandleFunc("GET /api/catalogo", a.handleCatalogo)
	mux.HandleFunc("POST /api/catalogo/sincronizar", a.handleSincronizar)
	mux.HandleFunc("GET /api/categorias/{tipo}", a.handleCategorias)
	mux.HandleFunc("GET /api/itens/{tipo}", a.handleItens)
	mux.HandleFunc("GET /api/buscar", a.handleBuscar)
	mux.HandleFunc("GET /api/filmes/{id}", a.handleFilme)
	mux.HandleFunc("GET /api/series/{id}", a.handleSerie)

	mux.HandleFunc("GET /api/video/{tipo}/{arquivo}", a.servirVideo(false))
	mux.HandleFunc("HEAD /api/video/{tipo}/{arquivo}", a.servirVideo(false))
	mux.HandleFunc("GET /api/video/{tipo}/{id}/parte", a.handleParte)
	mux.HandleFunc("POST /api/reproducao", a.handleAssumir)

	mux.HandleFunc("GET /api/torrents", a.handleTorrents)
	mux.HandleFunc("POST /api/torrents", a.handleAdicionarTorrent)
	mux.HandleFunc("POST /api/torrents/atualizar", a.handleAtualizarTorrents)
	mux.HandleFunc("GET /api/torrents/{hash}", a.handleTorrent)
	mux.HandleFunc("DELETE /api/torrents/{hash}", a.handleApagarTorrent)
	mux.HandleFunc("POST /api/arquivos/{id}/baixar", a.handleBaixarArquivo)
	mux.HandleFunc("DELETE /api/arquivos/{id}/baixar", a.handleBaixarArquivo)
	mux.HandleFunc("POST /api/arquivos/{id}/visto", a.handleVisto)
	mux.HandleFunc("DELETE /api/arquivos/{id}/visto", a.handleVisto)
	mux.HandleFunc("GET /api/archive", a.handleBuscarNoArchive)
	mux.HandleFunc("GET /api/procurar", a.handleProcurarTorrents)
	mux.HandleFunc("POST /api/verificacoes", a.handleVerificar)
	mux.HandleFunc("GET /api/verificacoes", a.handleVerificacoes)
	mux.HandleFunc("GET /api/quadros/{nome}", a.handleQuadro)
	mux.HandleFunc("POST /api/opcoes/preparar", a.handlePrepararOpcao)
	mux.HandleFunc("GET /api/formatos", a.handleFormatos)
	mux.HandleFunc("GET /api/assinatura/downloads", a.handleDownloadsDaAssinatura)
	mux.HandleFunc("POST /api/assinatura/downloads/{chave}", a.handleBaixarDaAssinatura)
	mux.HandleFunc("DELETE /api/assinatura/downloads/{chave}", a.handleBaixarDaAssinatura)
	mux.HandleFunc("GET /api/prowlarr", a.handleProwlarr)
	mux.HandleFunc("GET /api/opensubtitles", a.handleOpenSubtitles)
	mux.HandleFunc("PUT /api/opensubtitles", a.handleGuardarOpenSubtitles)
	mux.HandleFunc("DELETE /api/opensubtitles", a.handleApagarOpenSubtitles)
	mux.HandleFunc("GET /api/legendas/{chave}", a.handleLegendas)
	mux.HandleFunc("PUT /api/legendas/{chave}", a.handleEscolherLegenda)
	mux.HandleFunc("DELETE /api/legendas/{chave}", a.handleEscolherLegenda)
	mux.HandleFunc("PATCH /api/legendas/{chave}", a.handleEscolherLegenda)
	mux.HandleFunc("GET /api/legenda/{arquivo}", a.servirLegenda(true))
	mux.HandleFunc("POST /api/legendas/{chave}/gerar", a.handleGerarLegenda)
	mux.HandleFunc("POST /api/legendas/{chave}/sincronizar", a.handleSincronizarLegenda)
	mux.HandleFunc("GET /api/acervo", a.handleEnviosAoAcervo) // S41: documentários para o Acervo
	mux.HandleFunc("POST /api/acervo", a.handleMandarAoAcervo)
	mux.HandleFunc("DELETE /api/acervo/{id}", a.handleCancelarEnvio)
	mux.HandleFunc("GET /api/flashcards", a.handleFlashcardsDisponivel)
	mux.HandleFunc("POST /api/flashcards", a.handleMandarParaOFlashcards)
	mux.HandleFunc("DELETE /api/legendas/{chave}/gerar", a.handleGerarLegenda)
	mux.HandleFunc("GET /api/tmdb", a.handleTMDB)
	mux.HandleFunc("PUT /api/tmdb", a.handleGuardarTMDB)
	mux.HandleFunc("DELETE /api/tmdb", a.handleApagarTMDB)
	mux.HandleFunc("GET /api/titulos", a.handleBuscarTitulos)
	mux.HandleFunc("GET /api/titulos/{tipo}/{id}", a.handleTitulo)
	mux.HandleFunc("GET /api/titulos/{tipo}/{id}/torrents", a.handleTorrentsDoTitulo)
	mux.HandleFunc("GET /api/titulos/{tipo}/{id}/torrents/aos-poucos", a.handleTorrentsAosPoucos)
	mux.HandleFunc("GET /api/titulos/{tipo}/{id}/onde", a.handleOndeAssistir)
	mux.HandleFunc("GET /api/titulos/serie/{id}/temporada/{temporada}", a.handleTemporadaDoTitulo)
	mux.HandleFunc("PUT /api/prowlarr", a.handleGuardarProwlarr)
	mux.HandleFunc("DELETE /api/prowlarr", a.handleApagarProwlarr)

	mux.HandleFunc("GET /api/meus-videos", a.handleMeusVideos) // S42: vídeos próprios, do Telegram
	mux.HandleFunc("POST /api/meus-videos/telegram", a.handlePedirAoTelegram)
	mux.HandleFunc("POST /api/meus-videos/telegram/rastrear", a.handleRastrear)
	mux.HandleFunc("DELETE /api/meus-videos/telegram/{id}", a.handleApagarPedido)
	mux.HandleFunc("POST /api/meus-videos/telegram/{id}/retomar", a.handleRetomarPedido)
	mux.HandleFunc("POST /api/meus-videos/salvos", a.handleSalvar)
	mux.HandleFunc("DELETE /api/meus-videos/salvos/{id}", a.handleApagarSalvo)
	mux.HandleFunc("PATCH /api/meus-videos/{id}", a.handleMudarMeuVideo)
	mux.HandleFunc("DELETE /api/meus-videos/{id}", a.handleApagarMeuVideo)
	mux.HandleFunc("GET /api/meus-videos/{id}/capa", a.handleCapaDoMeuVideo)

	mux.HandleFunc("GET /api/favoritos", a.handleFavoritos)
	mux.HandleFunc("GET /api/favoritos/chaves", a.handleChavesFavoritas)
	mux.HandleFunc("PUT /api/favoritos/{tipo}/{id}", a.handleMarcarFavorito)
	mux.HandleFunc("DELETE /api/favoritos/{tipo}/{id}", a.handleMarcarFavorito)

	mux.HandleFunc("GET /api/progresso", a.handleContinuar)
	mux.HandleFunc("PUT /api/progresso/{chave}", a.handleGuardarProgresso)
	mux.HandleFunc("DELETE /api/progresso/{chave}", a.handleApagarProgresso)

	mux.HandleFunc("GET /api/jogos", a.handleJogos)
	mux.HandleFunc("GET /api/canais/esportes", a.handleCanaisDeEsportes)
	mux.HandleFunc("GET /api/telas", a.handleTelas)
	mux.HandleFunc("POST /api/telas/procurar", a.handleProcurarTelas)
	mux.HandleFunc("GET /api/telas/sessoes", a.handleSessoes)
	mux.HandleFunc("PATCH /api/telas/{id}", a.handleMudarTela)
	mux.HandleFunc("DELETE /api/telas/{id}", a.handleApagarTela)
	mux.HandleFunc("POST /api/telas/{id}/tocar", a.handleTocarNaTela)
	mux.HandleFunc("GET /api/telas/{id}/estado", a.handleEstadoDaTela)
	mux.HandleFunc("POST /api/telas/{id}/comando", a.handleComandoNaTela)

	mux.HandleFunc("GET /api/sugestoes", a.handleSugestoes)
	mux.HandleFunc("PUT /api/sugestoes/{id}", a.handleGuardarSugestao)
	mux.HandleFunc("PATCH /api/sugestoes/{id}", a.handleMarcarSugestao)
	mux.HandleFunc("DELETE /api/sugestoes/{id}", a.handleApagarSugestao)
	mux.HandleFunc("GET /api/relatos", a.handleRelatos) // títulos para verificar (S37)
	mux.HandleFunc("PUT /api/relatos/{id}", a.handleGuardarRelato)
	mux.HandleFunc("PATCH /api/relatos/{id}", a.handleMarcarRelato)
	mux.HandleFunc("DELETE /api/relatos/{id}", a.handleApagarRelato)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		erroJSON(w, http.StatusNotFound, "não encontrado")
	})
	mux.Handle("/", spaHandler(web))
	return mux
}

func (a *app) handleHealth(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := a.db.PingContext(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "erro", "version": version})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version})
	}
}

func (a *app) handleAjustes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"mostrar_adulto": a.mostrarAdulto()})
}

func (a *app) handleMudarAjustes(w http.ResponseWriter, r *http.Request) {
	var corpo struct {
		MostrarAdulto *bool `json:"mostrar_adulto"`
	}
	if !lerCorpo(w, r, &corpo) {
		return
	}
	if corpo.MostrarAdulto != nil {
		valor := "0"
		if *corpo.MostrarAdulto {
			valor = "1"
		}
		if err := a.guardarAjuste("mostrar_adulto", valor); err != nil {
			falha(w, err)
			return
		}
	}
	a.handleAjustes(w, r)
}

// ---------- Utilidades ----------

// spaHandler entrega os arquivos do frontend; caminhos desconhecidos recebem o index.html.
func spaHandler(web fs.FS) http.Handler {
	files := http.FileServerFS(web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if _, err := fs.Stat(web, name); err != nil {
				http.ServeFileFS(w, r, web, "index.html")
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}

func lerCorpo(w http.ResponseWriter, r *http.Request, destino any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(destino); err != nil {
		erroJSON(w, http.StatusBadRequest, "Pedido inválido.")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func erroJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"erro": msg})
}

func falha(w http.ResponseWriter, err error) {
	log.Print(err)
	erroJSON(w, http.StatusInternalServerError, err.Error())
}
