package cli

import (
	"context"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dandriscoll/muxray/internal/schema"
	"github.com/dandriscoll/muxray/internal/selfupdate"
	"github.com/dandriscoll/muxray/internal/version"
)

type updateResponse struct {
	schema.Envelope
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version,omitempty"`
	TargetVersion  string `json:"target_version"`
	Updated        bool   `json:"updated"`
	InstallPath    string `json:"install_path,omitempty"`
	Note           string `json:"note"`
}

// cmdUpdate performs an explicit, opt-in in-place upgrade from GitHub Releases.
// It only downloads release assets; it sends no pane content or telemetry.
func cmdUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", true, "emit JSON (default)")
	text := fs.Bool("text", false, "emit a terse human summary instead")
	pin := fs.String("version", "", "install this exact version (e.g. v0.1.1) instead of the latest release")
	check := fs.Bool("check", false, "report current and latest versions without installing")
	if err := fs.Parse(args); err != nil {
		return flagError("update", true, err)
	}
	wantJSON := !*text && *jsonOut

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 30 * time.Second}
	current := version.Version

	// Resolve the latest tag when we need it (no pin, or --check).
	latest := ""
	if *pin == "" || *check {
		t, err := selfupdate.LatestTag(ctx, client, selfupdate.DefaultRepo)
		if err != nil {
			return emitError(wantJSON, "update", &cmdError{class: "network_error", message: err.Error(),
				hint: "check your network connection, or pass --version <vX.Y.Z> to pin a release", exit: ExitInternal})
		}
		latest = t
	}
	target := *pin
	if target == "" {
		target = latest
	}

	mkResp := func(updated bool, path, note string) updateResponse {
		return updateResponse{
			Envelope:       schema.NewEnvelope("update", version.Version),
			CurrentVersion: current, LatestVersion: latest, TargetVersion: target,
			Updated: updated, InstallPath: path, Note: note,
		}
	}

	if *check {
		note := "update available: " + current + " -> " + latest
		if current == latest {
			note = "already on the latest release (" + current + ")"
		}
		return emit(wantJSON, mkResp(false, "", note), func() string { return note })
	}

	if current == target {
		note := "already on " + target + "; nothing to do"
		return emit(wantJSON, mkResp(false, "", note), func() string { return note })
	}

	exe, err := os.Executable()
	if err != nil {
		return emitError(wantJSON, "update", &cmdError{class: "no_executable_path", message: err.Error(),
			hint: "could not locate the running binary to replace it", exit: ExitInternal})
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}

	asset := selfupdate.AssetName(target, runtime.GOOS, runtime.GOARCH)
	base := selfupdate.DownloadBase(selfupdate.DefaultRepo, target)

	tgz, err := selfupdate.Download(ctx, client, base+"/"+asset)
	if err != nil {
		return emitError(wantJSON, "update", &cmdError{class: "download_failed", message: err.Error(),
			hint: "verify the version exists for this platform (" + runtime.GOOS + "/" + runtime.GOARCH + ") at github.com/" + selfupdate.DefaultRepo + "/releases", exit: ExitInternal})
	}
	sums, err := selfupdate.Download(ctx, client, base+"/checksums.txt")
	if err != nil {
		return emitError(wantJSON, "update", &cmdError{class: "download_failed", message: "could not fetch checksums.txt: " + err.Error(),
			hint: "the release may be incomplete; try again or pin a different --version", exit: ExitInternal})
	}
	if err := selfupdate.VerifyChecksum(tgz, asset, sums); err != nil {
		return emitError(wantJSON, "update", &cmdError{class: "checksum_mismatch", message: err.Error(),
			hint: "the download did not match the published checksum; aborting without changing the binary", exit: ExitInternal})
	}
	bin, err := selfupdate.ExtractBinary(tgz, "muxray")
	if err != nil {
		return emitError(wantJSON, "update", &cmdError{class: "extract_failed", message: err.Error(), exit: ExitInternal})
	}
	if err := selfupdate.Replace(exe, bin); err != nil {
		return emitError(wantJSON, "update", &cmdError{class: "replace_failed", message: err.Error(),
			hint: "the install location may not be writable by this user; re-run with sufficient permissions (e.g. sudo) or reinstall", exit: ExitInternal})
	}

	note := "updated " + current + " -> " + target
	return emit(wantJSON, mkResp(true, exe, note), func() string { return note + " (" + exe + ")" })
}
