package plugins

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLoadManifestWithSignature(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.sh")
	require.NoError(t, os.WriteFile(entry, []byte("echo ok"), 0o755)) //nolint:gosec // executable test script
	digest := sha256.Sum256([]byte("echo ok"))

	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)

	mf := Manifest{
		Name:       "demo-plugin",
		Version:    "1.0.0",
		Entrypoint: "main.sh",
		Digest:     hex.EncodeToString(digest[:]),
		Signer:     "dev",
	}
	sig, err := SignManifest(&mf, priv)
	require.NoError(t, err)
	mf.Signature = sig

	manifestBytes, err := yaml.Marshal(mf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.yaml"), manifestBytes, 0o600))

	store := NewTrustStore()
	store.Register("dev", pub)

	loaded, err := LoadManifest(filepath.Join(dir, "manifest.yaml"), WithTrustStore(store), WithRoot(dir))
	require.NoError(t, err)
	require.True(t, loaded.Trusted)
	require.Equal(t, entry, loaded.EntrypointAbs)
}

func TestLoadManifestUnsignedTrustedWithoutStore(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.sh")
	require.NoError(t, os.WriteFile(entry, []byte("echo ok"), 0o755)) //nolint:gosec // executable test script
	digest := sha256.Sum256([]byte("echo ok"))

	mf := Manifest{
		Name:       "unsigned",
		Version:    "1.0.0",
		Entrypoint: "main.sh",
		Digest:     hex.EncodeToString(digest[:]),
	}
	data, err := yaml.Marshal(mf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.yaml"), data, 0o600))

	loaded, err := LoadManifest(filepath.Join(dir, "manifest.yaml"))
	require.NoError(t, err)
	require.True(t, loaded.Trusted)
}

func TestLoadManifestRejectsDigestMismatch(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.sh")
	original := []byte("echo ok")
	require.NoError(t, os.WriteFile(entry, original, 0o755)) //nolint:gosec // executable test script
	digest := sha256.Sum256(original)

	mf := Manifest{
		Name:       "demo",
		Version:    "1.0.0",
		Entrypoint: "main.sh",
		Digest:     hex.EncodeToString(digest[:]),
	}
	data, err := yaml.Marshal(mf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.yaml"), data, 0o600))

	require.NoError(t, os.WriteFile(entry, []byte("echo tampered"), 0o755)) //nolint:gosec // executable test script

	_, err = LoadManifest(filepath.Join(dir, "manifest.yaml"))
	require.Error(t, err)
}

func TestFindManifestPrefersYaml(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.yml"), []byte("{}"), 0o600))
	path, err := FindManifest(dir)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "manifest.yml"), path)
}

func TestFindManifestMissingReturnsError(t *testing.T) {
	dir := t.TempDir()
	_, err := FindManifest(dir)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrManifestNotFound)
}

func TestDiscoverManifestsLoadsAllAndSkipsMissing(t *testing.T) {
	root := t.TempDir()

	// plugin with manifest
	p1 := filepath.Join(root, "p1")
	require.NoError(t, os.MkdirAll(p1, 0o755))
	entry1 := filepath.Join(p1, "main.sh")
	require.NoError(t, os.WriteFile(entry1, []byte("echo one"), 0o755)) //nolint:gosec // executable test script
	d1 := sha256.Sum256([]byte("echo one"))
	m1 := Manifest{Name: "p1", Version: "1.0.0", Entrypoint: "main.sh", Digest: hex.EncodeToString(d1[:])}
	data1, err := yaml.Marshal(m1)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(p1, "manifest.yaml"), data1, 0o600))

	// plugin with manifest
	p2 := filepath.Join(root, "p2")
	require.NoError(t, os.MkdirAll(p2, 0o755))
	entry2 := filepath.Join(p2, "main.sh")
	require.NoError(t, os.WriteFile(entry2, []byte("echo two"), 0o755)) //nolint:gosec // executable test script
	d2 := sha256.Sum256([]byte("echo two"))
	m2 := Manifest{Name: "p2", Version: "1.0.0", Entrypoint: "main.sh", Digest: hex.EncodeToString(d2[:])}
	data2, err := yaml.Marshal(m2)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(p2, "manifest.yaml"), data2, 0o600))

	// directory without manifest should be skipped
	require.NoError(t, os.MkdirAll(filepath.Join(root, "empty"), 0o755))

	store := NewTrustStore()
	store.AllowUnsigned(true)

	manifests, err := DiscoverManifests(root, store)
	require.NoError(t, err)
	require.Len(t, manifests, 2)
	require.Equal(t, "p1", manifests[0].Name)
	require.Equal(t, "p2", manifests[1].Name)
}

func TestDiscoverManifestsMissingDir(t *testing.T) {
	store := NewTrustStore()
	store.AllowUnsigned(true)
	manifests, err := DiscoverManifests(filepath.Join(t.TempDir(), "not-exist"), store)
	require.NoError(t, err)
	require.Nil(t, manifests)
}

func TestNormalizeStringsHelper(t *testing.T) {
	values := []string{"  Foo ", "foo", "BAR", ""}
	out := normalizeStrings(values)
	require.Equal(t, []string{"bar", "foo"}, out)
}

func TestValidateManifestFieldsErrors(t *testing.T) {
	require.Error(t, validateManifestFields(nil))
	// bad name
	require.Error(t, validateManifestFields(&Manifest{Name: "UPPER", Version: "1.0.0", Entrypoint: "main", Digest: strings.Repeat("0", 64)}))
	// bad version
	require.Error(t, validateManifestFields(&Manifest{Name: "ok", Version: "not-semver", Entrypoint: "main", Digest: strings.Repeat("0", 64)}))
	// missing entrypoint
	require.Error(t, validateManifestFields(&Manifest{Name: "ok", Version: "1.0.0", Digest: strings.Repeat("0", 64)}))
	// bad digest length
	require.Error(t, validateManifestFields(&Manifest{Name: "ok", Version: "1.0.0", Entrypoint: "main", Digest: "short"}))
	// bad digest hex
	require.Error(t, validateManifestFields(&Manifest{Name: "ok", Version: "1.0.0", Entrypoint: "main", Digest: strings.Repeat("g", 64)}))
}

func TestSecureJoinPreventsTraversalAndAbs(t *testing.T) {
	base := filepath.Join(t.TempDir(), "plugin")
	require.NoError(t, os.MkdirAll(base, 0o755))
	good, err := secureJoin(base, "main.sh")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(good, base))
	_, err = secureJoin(base, "../evil.sh")
	require.Error(t, err)
	_, err = secureJoin(base, "/abs/path")
	require.Error(t, err)
}

func TestComputeDigestMatchesSHA256(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f.txt")
	require.NoError(t, os.WriteFile(file, []byte("hello"), 0o600))
	d, err := computeDigest(file)
	require.NoError(t, err)
	expected := sha256.Sum256([]byte("hello"))
	require.Equal(t, hex.EncodeToString(expected[:]), d)
}

func TestComputeDigestMissingFile(t *testing.T) {
	_, err := computeDigest(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
}

func TestIsSemVerHelper(t *testing.T) {
	require.True(t, IsSemVer("1.2.3"))
	require.True(t, IsSemVer("v1.2.3-beta"))
	require.False(t, IsSemVer(""))
	require.False(t, IsSemVer("not-semver"))
}

func TestCanonicalManifestBytesDeterministic(t *testing.T) {
	m := &Manifest{
		Name:       "demo",
		Version:    "1.0.0",
		Entrypoint: "main.sh",
		Capabilities: []string{
			"b",
			"a",
		},
		Metadata: map[string]string{
			"z": "1",
			"a": "2",
		},
		Digest: "abcd",
		Signer: "dev",
	}
	first, err := CanonicalManifestBytes(m)
	require.NoError(t, err)
	second, err := CanonicalManifestBytes(m)
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func TestDiscoverManifestsRespectsTrustStore(t *testing.T) {
	root := t.TempDir()
	plug := filepath.Join(root, "trusted")
	require.NoError(t, os.MkdirAll(plug, 0o755))
	entry := filepath.Join(plug, "main.sh")
	require.NoError(t, os.WriteFile(entry, []byte("echo ok"), 0o755)) //nolint:gosec // executable test script
	hash := sha256.Sum256([]byte("echo ok"))
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	mf := Manifest{
		Name:       "trusted",
		Version:    "1.0.0",
		Entrypoint: "main.sh",
		Digest:     hex.EncodeToString(hash[:]),
		Signer:     "dev",
	}
	sig, err := SignManifest(&mf, priv)
	require.NoError(t, err)
	mf.Signature = sig
	data, err := yaml.Marshal(mf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(plug, "manifest.yaml"), data, 0o644))
	store := NewTrustStore()
	store.Register("dev", pub)
	manifests, err := DiscoverManifests(root, store)
	require.NoError(t, err)
	require.Len(t, manifests, 1)
	require.True(t, manifests[0].Trusted)
}

func TestDiscoverManifestsPropagatesLoadError(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "bad")
	require.NoError(t, os.MkdirAll(bad, 0o755))
	// create manifest with impossible digest
	entry := filepath.Join(bad, "main.sh")
	require.NoError(t, os.WriteFile(entry, []byte("echo bad"), 0o755))
	mf := Manifest{Name: "bad", Version: "1.0.0", Entrypoint: "main.sh", Digest: strings.Repeat("0", 10)}
	data, err := yaml.Marshal(mf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(bad, "manifest.yaml"), data, 0o644))
	store := NewTrustStore()
	store.AllowUnsigned(true)
	_, err = DiscoverManifests(root, store)
	require.Error(t, err)
}

func TestLoadManifestPathIsDirAndDecodeError(t *testing.T) {
	dir := t.TempDir()
	// when pointing at a directory
	_, err := LoadManifest(dir)
	require.Error(t, err)

	file := filepath.Join(dir, "manifest.yaml")
	require.NoError(t, os.WriteFile(file, []byte("::invalid"), 0o644))
	_, err = LoadManifest(file)
	require.Error(t, err)
}

func TestLoadManifestEntryEscapesRoot(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.sh")
	require.NoError(t, os.WriteFile(entry, []byte("echo ok"), 0o755))
	d := sha256.Sum256([]byte("echo ok"))
	mf := Manifest{
		Name:       "escape",
		Version:    "1.0.0",
		Entrypoint: "../evil.sh",
		Digest:     hex.EncodeToString(d[:]),
	}
	data, err := yaml.Marshal(mf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.yaml"), data, 0o644))
	_, err = LoadManifest(filepath.Join(dir, "manifest.yaml"), WithRoot(dir))
	require.Error(t, err)
}
