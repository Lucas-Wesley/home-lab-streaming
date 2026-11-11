package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAmbienteDoClaudeUsaAContaDoHub(t *testing.T) {
	arquivo := filepath.Join(t.TempDir(), "token")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "do-env")
	t.Setenv("CLAUDE_TOKEN_ARQUIVO", arquivo)

	if ambienteDoClaude() != nil {
		t.Error("sem o arquivo, devia valer o token do .env")
	}
	os.WriteFile(arquivo, []byte("\n"), 0o600)
	if ambienteDoClaude() != nil {
		t.Error("com o arquivo vazio, devia valer o token do .env")
	}
	os.WriteFile(arquivo, []byte("da-conta-ativa\n"), 0o600)
	cmd := exec.Command("sh", "-c", "printf %s \"$CLAUDE_CODE_OAUTH_TOKEN\"")
	cmd.Env = ambienteDoClaude()
	saida, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(saida)) != "da-conta-ativa" {
		t.Errorf("o Claude recebeu %q (%v)", saida, err)
	}
}
