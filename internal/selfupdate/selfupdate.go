// Package selfupdate implements `muxray update`: an explicit, opt-in in-place
// upgrade that downloads a published release from GitHub Releases, verifies its
// checksum, and atomically replaces the running binary.
//
// This is the ONLY network path in muxray and it is user-initiated. It only
// DOWNLOADS release assets — it sends no pane content, prompts, telemetry, or
// environment anywhere. muxray's "no telemetry egress" guarantee (see the
// telemetry package) is unchanged; `update` is the explicit, opt-in exception a
// user runs on purpose, exactly like `brew upgrade` or `go install`.
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultRepo is the GitHub owner/name muxray updates from.
const DefaultRepo = "dandriscoll/muxray"

// maxAsset bounds a downloaded asset so a hostile/corrupt response can't exhaust
// memory. Release tarballs are well under this.
const maxAsset = 64 << 20 // 64 MiB

// AssetName is the release archive name for a version/platform, matching
// scripts/build-release.sh (e.g. "muxray_v0.1.1_linux_amd64.tar.gz").
func AssetName(version, goos, goarch string) string {
	return fmt.Sprintf("muxray_%s_%s_%s.tar.gz", version, goos, goarch)
}

// DownloadBase is the release-asset base URL for a version.
func DownloadBase(repo, version string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s", repo, version)
}

// LatestTag resolves the latest release tag (e.g. "v0.1.1") via the GitHub API.
func LatestTag(ctx context.Context, client *http.Client, repo string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	body, err := get(ctx, client, url)
	if err != nil {
		return "", err
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("parse release metadata: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("no published release found for %s", repo)
	}
	return rel.TagName, nil
}

// Download fetches a URL's bytes (bounded).
func Download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	return get(ctx, client, url)
}

func get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxAsset))
}

// VerifyChecksum confirms asset's sha256 matches the entry for assetName in a
// `sha256sum`-style checksums file (lines like "<hex>  ./<name>" or "<hex>  <name>").
func VerifyChecksum(asset []byte, assetName string, checksums []byte) error {
	sum := sha256.Sum256(asset)
	got := hex.EncodeToString(sum[:])
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "./")
		if name == assetName {
			if !strings.EqualFold(fields[0], got) {
				return fmt.Errorf("checksum mismatch for %s: have %s, want %s", assetName, got, fields[0])
			}
			return nil
		}
	}
	return fmt.Errorf("no checksum entry for %s", assetName)
}

// ExtractBinary returns the named binary's bytes from a .tar.gz release archive.
func ExtractBinary(targz []byte, binName string) ([]byte, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(targz)))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == binName {
			return io.ReadAll(io.LimitReader(tr, maxAsset))
		}
	}
	return nil, fmt.Errorf("%s not found in archive", binName)
}

// Replace atomically replaces the binary at execPath with data, preserving the
// path. It writes a temp file in the same directory (so the rename is atomic on
// the same filesystem) then renames over the target — which is safe even while
// the old binary is running on Unix.
func Replace(execPath string, data []byte) error {
	dir := filepath.Dir(execPath)
	tmp, err := os.CreateTemp(dir, ".muxray-update-*")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write update: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpName, execPath); err != nil {
		return fmt.Errorf("replace %s: %w", execPath, err)
	}
	return nil
}
