package plugins

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTrustStoreVerifiesSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	store := NewTrustStore()
	store.Register("dev", pub)

	mf := &Manifest{Name: "demo", Version: "1.0.0", Entrypoint: "main", Digest: hex.EncodeToString(make([]byte, sha256.Size)), Signer: "dev"}
	payload, err := CanonicalManifestBytes(mf)
	require.NoError(t, err)
	hash := sha256.Sum256(payload)
	mf.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, hash[:]))

	err = store.Verify(mf, payload)
	require.NoError(t, err)
}

func TestTrustStoreBlocksDigest(t *testing.T) {
	store := NewTrustStore()
	store.AllowUnsigned(true)
	store.BlockDigest("deadbeef")

	mf := &Manifest{Name: "demo", Version: "1.0.0", Entrypoint: "main", Digest: "deadbeef"}
	payload, err := CanonicalManifestBytes(mf)
	require.NoError(t, err)
	err = store.Verify(mf, payload)
	require.Error(t, err)
}

func TestVerifyNilStoreAndManifest(t *testing.T) {
	var ts *TrustStore
	err := ts.Verify(&Manifest{}, []byte("payload"))
	require.Error(t, err)
	store := NewTrustStore()
	require.Error(t, store.Verify(nil, []byte("payload")))
}

func TestVerifyUnknownSignerAndBadSignature(t *testing.T) {
	store := NewTrustStore()
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	store.Register("dev", pub)
	mf := &Manifest{Name: "demo", Version: "1.0.0", Entrypoint: "main", Digest: hex.EncodeToString(make([]byte, sha256.Size)), Signer: "unknown", Signature: ""}
	payload, err := CanonicalManifestBytes(mf)
	require.NoError(t, err)
	require.Error(t, store.Verify(mf, payload))

	// bad base64
	mf.Signer = "dev"
	mf.Signature = "***"
	require.Error(t, store.Verify(mf, payload))

	// wrong signature
	hash := sha256.Sum256(payload)
	mf.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, hash[:]))
	require.NoError(t, store.Verify(mf, payload))
	sig := make([]byte, ed25519.SignatureSize)
	copy(sig, hash[:])
	mf.Signature = base64.StdEncoding.EncodeToString(sig)
	require.Error(t, store.Verify(mf, payload))
}

func TestVerifyAllowsUnsignedWhenConfigured(t *testing.T) {
	store := NewTrustStore()
	store.AllowUnsigned(true)
	mf := &Manifest{Name: "demo", Version: "1.0.0", Entrypoint: "main", Digest: hex.EncodeToString(make([]byte, sha256.Size))}
	payload, err := CanonicalManifestBytes(mf)
	require.NoError(t, err)
	require.NoError(t, store.Verify(mf, payload))
}
