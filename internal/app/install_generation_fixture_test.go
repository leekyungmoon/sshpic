package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSettledTestInstallGeneration(t *testing.T, cacheDir string) {
	t.Helper()
	directory := filepath.Join(cacheDir, windowsInstallStateDir)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	ledger := installGenerationLedger{
		Version: installGenerationVersion,
		Owner:   installGenerationOwner,
		State:   installGenerationStateDone,
		Token:   strings.Repeat("1", 32),
	}
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, installGenerationLedgerFile), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
