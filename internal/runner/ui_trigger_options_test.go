package runner

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestUIRunTriggerOptionsMatchBackend menjaga dropdown "Trigger" di halaman Runs
// tetap sepadan dengan enum backend (runTriggers). QA menemukan UI menawarkan
// "api", yang tidak pernah dipakai run mana pun dan ditolak parseRunFilter
// sebagai filter tidak sah — pilihan itu hanya menghasilkan daftar kosong.
func TestUIRunTriggerOptionsMatchBackend(t *testing.T) {
	appPath := filepath.Join("..", "..", "cmd", "cronsoul", "ui", "app.js")
	src, err := os.ReadFile(appPath)
	if err != nil {
		t.Fatalf("gagal membaca %s: %v", appPath, err)
	}

	block := regexp.MustCompile(`\btrigOpts=\[(.*?)\];`).FindSubmatch(src)
	if block == nil {
		t.Fatalf("blok trigOpts tidak ditemukan di %s", appPath)
	}

	// [['','All triggers'],['manual','Manual'],...] → ambil nilai (elemen pertama).
	pairs := regexp.MustCompile(`\['([^']*)','`).FindAllStringSubmatch(string(block[1]), -1)
	if len(pairs) == 0 {
		t.Fatalf("tidak ada opsi yang terbaca dari trigOpts: %s", block[1])
	}

	uiValues := map[string]bool{}
	for _, p := range pairs {
		uiValues[p[1]] = true
	}
	if !uiValues[""] {
		t.Error("opsi 'All triggers' (nilai kosong) harus tetap ada")
	}
	delete(uiValues, "")

	for value := range uiValues {
		if !runTriggers[value] {
			t.Errorf("UI menawarkan trigger %q yang tidak dikenal backend %v", value, sortedTriggers())
		}
	}
	for value := range runTriggers {
		if !uiValues[value] {
			t.Errorf("backend punya trigger %q tapi UI tidak menawarkannya", value)
		}
	}
}

func sortedTriggers() []string {
	out := make([]string, 0, len(runTriggers))
	for k := range runTriggers {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
