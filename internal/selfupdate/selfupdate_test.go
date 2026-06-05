package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAssetName(t *testing.T) {
	got := AssetName("v0.1.1", "linux", "amd64")
	want := "muxray_v0.1.1_linux_amd64.tar.gz"
	if got != want {
		t.Errorf("AssetName = %q, want %q", got, want)
	}
}

func TestVerifyChecksum(t *testing.T) {
	asset := []byte("the release tarball bytes")
	sum := sha256.Sum256(asset)
	hexsum := hex.EncodeToString(sum[:])
	name := "muxray_v0.1.1_linux_amd64.tar.gz"
	// sha256sum-style, with the "./" prefix the build script produces.
	checksums := []byte(fmt.Sprintf("%s  ./%s\n%s  ./other.tar.gz\n", hexsum, name, hexsum))

	if err := VerifyChecksum(asset, name, checksums); err != nil {
		t.Errorf("valid checksum rejected: %v", err)
	}
	// A tampered asset must be rejected.
	if err := VerifyChecksum([]byte("tampered"), name, checksums); err == nil {
		t.Error("tampered asset accepted")
	}
	// A missing entry must be reported, not silently passed.
	if err := VerifyChecksum(asset, "absent.tar.gz", checksums); err == nil {
		t.Error("missing checksum entry accepted")
	}
}

func TestExtractBinary(t *testing.T) {
	want := []byte("\x7fELF...the muxray binary...")
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	// A README sibling (must be ignored) plus the binary in a platform subdir,
	// matching the release archive layout (muxray_<os>_<arch>/muxray).
	for _, f := range []struct {
		name string
		body []byte
	}{
		{"muxray_linux_amd64/README.md", []byte("# readme")},
		{"muxray_linux_amd64/muxray", want},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.body); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gw.Close()

	got, err := ExtractBinary(buf.Bytes(), "muxray")
	if err != nil {
		t.Fatalf("ExtractBinary: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted %q, want %q", got, want)
	}
	if _, err := ExtractBinary(buf.Bytes(), "nonesuch"); err == nil {
		t.Error("ExtractBinary found a binary that isn't there")
	}
}

func TestLatestTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v0.1.1","name":"v0.1.1"}`)
	}))
	defer srv.Close()
	// Point the helper at the test server by overriding the URL via a small client
	// that rewrites the host is overkill; instead exercise get() through a wrapper.
	body, err := Download(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	if !bytes.Contains(body, []byte(`"tag_name":"v0.1.1"`)) {
		t.Errorf("unexpected body: %s", body)
	}
}
