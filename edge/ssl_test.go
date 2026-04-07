package edge

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBlockUntilCertificateIsReady(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")

	ch := make(chan struct{})

	go func() {
		ch <- struct{}{}
		BlockUntilCertificateIsReady(certPath, keyPath, 10*time.Millisecond)
		close(ch)
	}()

	// Wait until the goroutine starts
	<-ch

	// Block because the certificates are not ready
	select {
	case <-ch:
		t.FailNow()
	case <-time.After(100 * time.Millisecond):
	}

	// Create the certificates
	err := os.WriteFile(certPath, []byte("dummy cert"), 0644)
	require.NoError(t, err)

	err = os.WriteFile(keyPath, []byte("dummy key"), 0644)
	require.NoError(t, err)

	// There should not be a block anymore
	select {
	case _, ok := <-ch:
		require.False(t, ok)
	case <-time.After(100 * time.Millisecond):
		t.FailNow()
	}
}
