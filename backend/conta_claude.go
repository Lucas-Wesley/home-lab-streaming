package main

import (
	"os"
	"strings"
)

// A conta do Claude que o Hub escolheu (H11 do Hub) fica num arquivo montado só para leitura
// (CLAUDE_TOKEN_ARQUIVO). Ele é lido a cada chamada: trocar de conta no Hub vale na próxima tarefa,
// sem reiniciar o app. Sem o arquivo, vale o CLAUDE_CODE_OAUTH_TOKEN do .env.

// ambienteDoClaude devolve o ambiente da chamada ao Claude, com o token da conta ativa; nil herda o do app.
func ambienteDoClaude() []string {
	arquivo := os.Getenv("CLAUDE_TOKEN_ARQUIVO")
	if arquivo == "" {
		return nil
	}
	b, err := os.ReadFile(arquivo)
	token := strings.TrimSpace(string(b))
	if err != nil || token == "" {
		return nil
	}
	// Repetida, a variável vale pelo último valor: o do arquivo passa na frente do .env.
	return append(os.Environ(), "CLAUDE_CODE_OAUTH_TOKEN="+token)
}
