package crypto

import (
	"testing"
)

func TestRunCryptoSelfTests(t *testing.T) {
	if err := RunCryptoSelfTests(); err != nil {
		t.Fatalf("RunCryptoSelfTests failed: %v", err)
	}
}

func TestZeroize(t *testing.T) {
	data := []byte("highly-sensitive-secret-key-material")
	Zeroize(data)
	for i, b := range data {
		if b != 0 {
			t.Errorf("byte at index %d was not zeroized, got %d", i, b)
		}
	}

	// Test nil and empty
	Zeroize(nil)
	Zeroize([]byte{})
}
