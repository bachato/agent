package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/md5"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"os"
	"testing"

	"github.com/portainer/agent"
	"github.com/portainer/portainer/api/filesystem"

	"github.com/stretchr/testify/require"
)

func generateKeyAndSignature(t *testing.T, message string) (hexKey, b64Sig string, privKey *ecdsa.PrivateKey) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	digest := md5.New()
	digest.Write([]byte(message))
	hash := digest.Sum(nil)

	r, s, err := ecdsa.Sign(rand.Reader, key, hash)
	require.NoError(t, err)

	keySize := key.Params().BitSize / 8
	sig := append(r.FillBytes(make([]byte, keySize)), s.FillBytes(make([]byte, keySize))...)
	b64Sig = base64.RawStdEncoding.EncodeToString(sig)

	pubKeyDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	hexKey = hex.EncodeToString(pubKeyDER)

	return hexKey, b64Sig, key
}

func TestIsAssociated_NoSecretOrKey(t *testing.T) {
	t.Parallel()

	svc := NewECDSAService("")
	require.False(t, svc.IsAssociated())
}

func TestIsAssociated_WithSecret(t *testing.T) {
	t.Parallel()

	svc := NewECDSAService("mysecret")
	require.True(t, svc.IsAssociated())
}

func TestIsAssociated_AfterSuccessfulVerify(t *testing.T) {
	t.Parallel()

	hexKey, b64Sig, _ := generateKeyAndSignature(t, agent.PortainerAgentSignatureMessage)
	svc := NewECDSAService("")

	require.False(t, svc.IsAssociated())

	valid, err := svc.VerifySignature(b64Sig, hexKey)
	require.NoError(t, err)
	require.True(t, valid)

	require.True(t, svc.IsAssociated())
}

func TestVerifySignature_Valid(t *testing.T) {
	t.Parallel()

	hexKey, b64Sig, _ := generateKeyAndSignature(t, agent.PortainerAgentSignatureMessage)
	svc := NewECDSAService("")

	valid, err := svc.VerifySignature(b64Sig, hexKey)
	require.NoError(t, err)
	require.True(t, valid)
}

func TestVerifySignature_WithSecret(t *testing.T) {
	t.Parallel()

	secret := "custom-secret"
	hexKey, b64Sig, _ := generateKeyAndSignature(t, secret)
	svc := NewECDSAService(secret)

	valid, err := svc.VerifySignature(b64Sig, hexKey)
	require.NoError(t, err)
	require.True(t, valid)
}

func TestVerifySignature_WrongMessage(t *testing.T) {
	t.Parallel()

	hexKey, b64Sig, _ := generateKeyAndSignature(t, "wrong-message")
	svc := NewECDSAService("")

	valid, err := svc.VerifySignature(b64Sig, hexKey)
	require.NoError(t, err)
	require.False(t, valid)
}

func TestVerifySignature_InvalidBase64Signature(t *testing.T) {
	t.Parallel()

	hexKey, _, _ := generateKeyAndSignature(t, agent.PortainerAgentSignatureMessage)
	svc := NewECDSAService("")

	_, err := svc.VerifySignature("not-valid-base64!!!", hexKey)
	require.Error(t, err)
}

func TestVerifySignature_InvalidHexKey(t *testing.T) {
	t.Parallel()

	_, b64Sig, _ := generateKeyAndSignature(t, agent.PortainerAgentSignatureMessage)
	svc := NewECDSAService("")

	_, err := svc.VerifySignature(b64Sig, "not-valid-hex!!!")
	require.Error(t, err)
}

func TestGenerateCertsForHost(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	svc := TLSService{}
	err := svc.GenerateCertsForHost("127.0.0.1")
	require.NoError(t, err)

	certPEM, err := os.ReadFile(filesystem.JoinPaths(dir, agent.TLSCertPath))
	require.NoError(t, err)

	keyPEM, err := os.ReadFile(filesystem.JoinPaths(dir, agent.TLSKeyPath))
	require.NoError(t, err)

	_, err = tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)

	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block)

	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	require.Contains(t, cert.DNSNames, "localhost")
	require.Equal(t, "127.0.0.1", cert.IPAddresses[0].String())
}
