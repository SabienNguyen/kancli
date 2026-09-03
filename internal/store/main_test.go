package store

import (
	"fmt"
	"os"
	"testing"
)

// TestMain fails the package if a test leaves a store open. An open store
// holds board.db, and on Windows an open handle makes the temp directory
// undeletable, so every test that opens a store must close it.
func TestMain(m *testing.M) {
	code := m.Run()
	if n := OpenStores(); n != 0 && code == 0 {
		fmt.Fprintf(os.Stderr, "%d store(s) left open by the tests; every store must be closed\n", n)
		code = 1
	}
	os.Exit(code)
}
