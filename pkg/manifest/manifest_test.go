package manifest

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func makeTar(files map[string][]byte) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, data := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(data))})
		tw.Write(data)
	}
	tw.Close()
	return buf.Bytes()
}

func TestParseVersionSuffix(t *testing.T) {
	cases := []struct{ url, wantBase, wantVer string }{
		{"https://example.com/manifest.json@v3", "https://example.com/manifest.json", "v3"},
		{"https://example.com/manifest.json", "https://example.com/manifest.json", ""},
		{"https://user@example.com/manifest.json", "https://user@example.com/manifest.json", ""},
		{"https://example.com/path@v1/manifest.json", "https://example.com/path@v1/manifest.json", ""},
	}
	for _, tc := range cases {
		base, ver := parseVersionSuffix(tc.url)
		if base != tc.wantBase || ver != tc.wantVer {
			t.Errorf("parseVersionSuffix(%q) = (%q, %q), want (%q, %q)",
				tc.url, base, ver, tc.wantBase, tc.wantVer)
		}
	}
}

func TestVerifyRootHash(t *testing.T) {
	provHash := testHash([]byte("provenance bytes"))
	specsHash := testHash([]byte("specs bytes"))
	// canonical order: provenance, specs
	goodRoot := testHash([]byte(provHash + specsHash))

	mv := ManifestVersion{
		RootHash: goodRoot,
		Layers: map[string]ManifestLayer{
			"provenance": {SHA256: provHash},
			"specs":      {SHA256: specsHash},
		},
	}
	if err := verifyRootHash(mv); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mv.RootHash = "badhash"
	if err := verifyRootHash(mv); err == nil {
		t.Error("expected error for wrong root_hash")
	}
}

func TestVerifyLayerHash(t *testing.T) {
	data := []byte("layer contents")
	good := testHash(data)
	if err := verifyLayerHash("test", data, good); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := verifyLayerHash("test", data, "badhash"); err == nil {
		t.Error("expected error for wrong hash")
	}
}

func buildManifest(version, rootHash string, layers map[string]ManifestLayer) []byte {
	m := Manifest{
		Latest: version,
		Versions: map[string]ManifestVersion{
			version: {RootHash: rootHash, Layers: layers},
		},
	}
	b, _ := json.Marshal(m)
	return b
}

func TestFetch_RootHashMismatchAbortsBeforeLayerDownload(t *testing.T) {
	layerDownloaded := false
	layerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		layerDownloaded = true
		w.Write([]byte("layer data"))
	}))
	defer layerSrv.Close()

	layerHash := testHash([]byte("layer data"))
	manifestBytes := buildManifest("v1", "bad_root_hash", map[string]ManifestLayer{
		"provenance": {SHA256: layerHash, URL: layerSrv.URL},
	})
	mSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(manifestBytes)
	}))
	defer mSrv.Close()

	_, err := Fetch(mSrv.URL, "")
	if err == nil {
		t.Fatal("expected error for root_hash mismatch")
	}
	if layerDownloaded {
		t.Error("layer was downloaded despite root_hash mismatch — must abort before fetching")
	}
}

func TestFetch_LayerHashMismatch(t *testing.T) {
	// Serve tampered data but declare a hash for "original data"
	goodHash := testHash([]byte("original data"))
	rootH := testHash([]byte(goodHash)) // only one layer in canonical order

	layerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("tampered data"))
	}))
	defer layerSrv.Close()

	manifestBytes := buildManifest("v1", rootH, map[string]ManifestLayer{
		"provenance": {SHA256: goodHash, URL: layerSrv.URL},
	})
	mSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(manifestBytes)
	}))
	defer mSrv.Close()

	_, err := Fetch(mSrv.URL, "")
	if err == nil {
		t.Fatal("expected error for layer hash mismatch")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("error should mention hash mismatch, got: %v", err)
	}
}

func TestFetch_VersionSuffix(t *testing.T) {
	provData := []byte("prov data")
	provHash := testHash(provData)
	rootH := testHash([]byte(provHash))

	layerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(provData)
	}))
	defer layerSrv.Close()

	m := Manifest{
		Latest: "v2",
		Versions: map[string]ManifestVersion{
			"v1": {
				RootHash: rootH,
				Layers:   map[string]ManifestLayer{"provenance": {SHA256: provHash, URL: layerSrv.URL}},
			},
			"v2": {RootHash: "irrelevant"},
		},
	}
	manifestBytes, _ := json.Marshal(m)
	mSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(manifestBytes)
	}))
	defer mSrv.Close()

	// @v1 suffix should pin to v1, not latest (v2)
	got, err := Fetch(mSrv.URL+"@v1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Version != "v1" {
		t.Errorf("expected version v1, got %s", got.Version)
	}
}

func TestFetch_FlagVersionOverride(t *testing.T) {
	provData := []byte("prov bytes")
	provHash := testHash(provData)
	rootH := testHash([]byte(provHash))

	layerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(provData)
	}))
	defer layerSrv.Close()

	m := Manifest{
		Latest: "v2",
		Versions: map[string]ManifestVersion{
			"v1": {
				RootHash: rootH,
				Layers:   map[string]ManifestLayer{"provenance": {SHA256: provHash, URL: layerSrv.URL}},
			},
			"v2": {RootHash: "irrelevant"},
		},
	}
	manifestBytes, _ := json.Marshal(m)
	mSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(manifestBytes)
	}))
	defer mSrv.Close()

	got, err := Fetch(mSrv.URL, "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Version != "v1" {
		t.Errorf("expected version v1, got %s", got.Version)
	}
}

func TestExtractTar_PreservesRelativePaths(t *testing.T) {
	files := map[string][]byte{
		"provenance/prov-2026-abc.yml": []byte("id: prov-2026-abc"),
		"provenance/prov-2026-def.yml": []byte("id: prov-2026-def"),
	}
	tarData := makeTar(files)

	dest := t.TempDir()
	if err := extractTar(tarData, dest); err != nil {
		t.Fatalf("extractTar: %v", err)
	}

	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Errorf("missing file %s: %v", name, err)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("file %s: got %q, want %q", name, got, want)
		}
	}
}

func TestExtractTar_RejectsTraversal(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.WriteHeader(&tar.Header{Name: "../escape.txt", Mode: 0644, Size: 3})
	tw.Write([]byte("bad"))
	tw.Close()

	dest := t.TempDir()
	if err := extractTar(buf.Bytes(), dest); err == nil {
		t.Error("expected error for path traversal, got nil")
	}
}

func TestIsLocalPath(t *testing.T) {
	cases := []struct {
		loc   string
		local bool
	}{
		{"http://example.com/manifest.json", false},
		{"https://example.com/manifest.json", false},
		{"/abs/path/manifest.json", true},
		{"./relative/manifest.json", true},
		{"manifest.json", true},
	}
	for _, tc := range cases {
		if got := isLocalPath(tc.loc); got != tc.local {
			t.Errorf("isLocalPath(%q) = %v, want %v", tc.loc, got, tc.local)
		}
	}
}

func TestFetch_LocalManifest_AutoResolvesLayers(t *testing.T) {
	dir := t.TempDir()

	provData := makeTar(map[string][]byte{"prov-2026-aaa.yml": []byte("id: prov-2026-aaa")})
	promptData := makeTar(map[string][]byte{"prompt.md": []byte("# hello")})
	provHash := testHash(provData)
	promptHash := testHash(promptData)
	rootH := testHash([]byte(provHash + promptHash)) // canonical order: provenance, prompt

	// Write artifacts using publish naming convention
	os.WriteFile(filepath.Join(dir, "linespec-provenance-v1.tar"), provData, 0644)
	os.WriteFile(filepath.Join(dir, "linespec-prompt-v1.tar"), promptData, 0644)

	// Write manifest with empty URLs
	manifestBytes := buildManifest("v1", rootH, map[string]ManifestLayer{
		"provenance": {SHA256: provHash, URL: ""},
		"prompt":     {SHA256: promptHash, URL: ""},
	})
	manifestPath := filepath.Join(dir, "linespec.manifest.json")
	os.WriteFile(manifestPath, manifestBytes, 0644)

	got, err := Fetch(manifestPath, "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Version != "v1" {
		t.Errorf("expected version v1, got %s", got.Version)
	}
	if len(got.Layers["provenance"]) == 0 {
		t.Error("expected provenance layer data")
	}
	if len(got.Layers["prompt"]) == 0 {
		t.Error("expected prompt layer data")
	}
}

func TestFetch_LocalManifest_VersionSuffix(t *testing.T) {
	dir := t.TempDir()

	provData := makeTar(map[string][]byte{"prov-2026-aaa.yml": []byte("id: prov-2026-aaa")})
	provHash := testHash(provData)
	rootH := testHash([]byte(provHash))

	os.WriteFile(filepath.Join(dir, "linespec-provenance-v2.tar"), provData, 0644)

	m := Manifest{
		Latest: "v2",
		Versions: map[string]ManifestVersion{
			"v1": {RootHash: "irrelevant"},
			"v2": {
				RootHash: rootH,
				Layers:   map[string]ManifestLayer{"provenance": {SHA256: provHash, URL: ""}},
			},
		},
	}
	manifestBytes, _ := json.Marshal(m)
	manifestPath := filepath.Join(dir, "linespec.manifest.json")
	os.WriteFile(manifestPath, manifestBytes, 0644)

	got, err := Fetch(manifestPath+"@v2", "")
	if err != nil {
		t.Fatalf("Fetch with @version suffix: %v", err)
	}
	if got.Version != "v2" {
		t.Errorf("expected version v2, got %s", got.Version)
	}
}

func TestFetch_LocalManifest_HashMismatchAborts(t *testing.T) {
	dir := t.TempDir()

	provData := makeTar(map[string][]byte{"prov-2026-aaa.yml": []byte("id: prov-2026-aaa")})
	provHash := testHash(provData)
	rootH := testHash([]byte(provHash))

	// Write tampered artifact
	os.WriteFile(filepath.Join(dir, "linespec-provenance-v1.tar"), []byte("tampered"), 0644)

	manifestBytes := buildManifest("v1", rootH, map[string]ManifestLayer{
		"provenance": {SHA256: provHash, URL: ""},
	})
	os.WriteFile(filepath.Join(dir, "linespec.manifest.json"), manifestBytes, 0644)

	_, err := Fetch(filepath.Join(dir, "linespec.manifest.json"), "")
	if err == nil {
		t.Fatal("expected error for hash mismatch on local artifact")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("expected hash mismatch error, got: %v", err)
	}
}

func TestFetch_RemoteManifest_EmptyURLErrors(t *testing.T) {
	provHash := testHash([]byte("prov data"))
	rootH := testHash([]byte(provHash))

	manifestBytes := buildManifest("v1", rootH, map[string]ManifestLayer{
		"provenance": {SHA256: provHash, URL: ""},
	})
	mSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(manifestBytes)
	}))
	defer mSrv.Close()

	_, err := Fetch(mSrv.URL, "")
	if err == nil {
		t.Fatal("expected error for empty layer URL on remote manifest")
	}
	if !strings.Contains(err.Error(), "has no URL") {
		t.Errorf("expected 'has no URL' error, got: %v", err)
	}
}

func TestFetch_PropagatesNameFromManifest(t *testing.T) {
	provData := makeTar(map[string][]byte{"prov-2026-aaa.yml": []byte("id: prov-2026-aaa")})
	provHash := testHash(provData)
	rootH := testHash([]byte(provHash))

	layerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(provData)
	}))
	defer layerSrv.Close()

	m := Manifest{
		Name:   "my-service",
		Latest: "v1",
		Versions: map[string]ManifestVersion{
			"v1": {
				RootHash: rootH,
				Layers:   map[string]ManifestLayer{"provenance": {SHA256: provHash, URL: layerSrv.URL}},
			},
		},
	}
	manifestBytes, _ := json.Marshal(m)
	mSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(manifestBytes)
	}))
	defer mSrv.Close()

	got, err := Fetch(mSrv.URL, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "my-service" {
		t.Errorf("expected Name %q, got %q", "my-service", got.Name)
	}
}

func TestFetch_NameEmptyWhenNotSet(t *testing.T) {
	provData := makeTar(map[string][]byte{"prov-2026-aaa.yml": []byte("id: prov-2026-aaa")})
	provHash := testHash(provData)
	rootH := testHash([]byte(provHash))

	layerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(provData)
	}))
	defer layerSrv.Close()

	m := Manifest{
		Latest: "v1",
		Versions: map[string]ManifestVersion{
			"v1": {
				RootHash: rootH,
				Layers:   map[string]ManifestLayer{"provenance": {SHA256: provHash, URL: layerSrv.URL}},
			},
		},
	}
	manifestBytes, _ := json.Marshal(m)
	mSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(manifestBytes)
	}))
	defer mSrv.Close()

	got, err := Fetch(mSrv.URL, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "" {
		t.Errorf("expected empty Name for manifest without name field, got %q", got.Name)
	}
}

func TestExtractSpecs_StripsCommonTopLevelPrefix(t *testing.T) {
	files := map[string][]byte{
		"linespec.manifest/specs/foo.linespec": []byte("test spec"),
		"linespec.manifest/specs/sub/bar.linespec": []byte("nested spec"),
		"linespec.manifest/prompt.md": []byte("# prompt"),
	}
	dest := t.TempDir()
	if err := ExtractSpecs(makeTar(files), dest); err != nil {
		t.Fatalf("ExtractSpecs: %v", err)
	}
	want := map[string]string{
		"specs/foo.linespec":     "test spec",
		"specs/sub/bar.linespec": "nested spec",
		"prompt.md":              "# prompt",
	}
	for path, wantContent := range want {
		got, err := os.ReadFile(filepath.Join(dest, path))
		if err != nil {
			t.Errorf("missing file %s: %v", path, err)
			continue
		}
		if string(got) != wantContent {
			t.Errorf("file %s: got %q, want %q", path, got, wantContent)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "linespec.manifest")); !os.IsNotExist(err) {
		t.Error("linespec.manifest/ subdirectory should not exist after extraction")
	}
}

func TestExtractSpecs_NoStrippingWhenFlatPaths(t *testing.T) {
	files := map[string][]byte{
		"specs/foo.linespec": []byte("flat spec"),
		"prompt.md":          []byte("# prompt"),
	}
	dest := t.TempDir()
	if err := ExtractSpecs(makeTar(files), dest); err != nil {
		t.Fatalf("ExtractSpecs: %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(dest, "specs/foo.linespec")); err != nil {
		t.Errorf("expected specs/foo.linespec at root: %v", err)
	}
}

func TestExtractSpecs_NoStrippingWhenMixedPrefixes(t *testing.T) {
	files := map[string][]byte{
		"dirA/file1.txt": []byte("a"),
		"dirB/file2.txt": []byte("b"),
	}
	dest := t.TempDir()
	if err := ExtractSpecs(makeTar(files), dest); err != nil {
		t.Fatalf("ExtractSpecs: %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(dest, "dirA/file1.txt")); err != nil {
		t.Errorf("expected dirA/file1.txt: %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(dest, "dirB/file2.txt")); err != nil {
		t.Errorf("expected dirB/file2.txt: %v", err)
	}
}

func TestExtractProvenance(t *testing.T) {
	files := map[string][]byte{
		"prov-2026-aaa.yml": []byte("id: prov-2026-aaa"),
	}
	dest := t.TempDir()
	if err := ExtractProvenance(makeTar(files), dest); err != nil {
		t.Fatalf("ExtractProvenance: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "prov-2026-aaa.yml")); err != nil {
		t.Errorf("expected file not found: %v", err)
	}
}
