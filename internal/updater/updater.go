package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultRepo     = "haimingx/aiswap"
	defaultAPIBase  = "https://api.github.com"
	maxBinarySize   = 64 << 20
	maxDownloadSize = 128 << 20
)

type ReleaseInfo struct {
	TagName string `json:"tag_name"`
}

type CheckResult struct {
	CurrentVersion string
	LatestVersion  string
	UpdateAvail    bool
}

type Updater struct {
	Repo       string
	HTTPClient *http.Client
	APIBase    string // Defaults to https://api.github.com
}

func New(repo string) *Updater {
	if repo == "" {
		repo = DefaultRepo
	}
	return &Updater{
		Repo:       repo,
		HTTPClient: &http.Client{Timeout: 90 * time.Second},
		APIBase:    defaultAPIBase,
	}
}

func (u *Updater) LatestVersion(ctx context.Context) (string, error) {
	// Prefer gh CLI when targeting default GitHub API (supports private repos).
	if strings.TrimRight(u.APIBase, "/") == defaultAPIBase {
		if tag, err := u.latestViaGH(ctx); err == nil && tag != "" {
			return tag, nil
		}
	}
	url := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimRight(u.APIBase, "/"), u.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "aiswap-updater/1.0")
	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github release api returned http-%d (for private repos, authenticate with 'gh auth login')", resp.StatusCode)
	}
	var info ReleaseInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		return "", fmt.Errorf("decode release metadata: %w", err)
	}
	if info.TagName == "" {
		return "", errors.New("empty tag_name in latest release")
	}
	return info.TagName, nil
}

func (u *Updater) latestViaGH(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", err
	}
	ghCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ghCtx, "gh", "release", "view", "-R", u.Repo, "--json", "tagName", "-q", ".tagName")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (u *Updater) Check(ctx context.Context, current string) (*CheckResult, error) {
	latest, err := u.LatestVersion(ctx)
	if err != nil {
		return nil, err
	}
	return &CheckResult{
		CurrentVersion: current,
		LatestVersion:  latest,
		UpdateAvail:    isNewerVersion(current, latest),
	}, nil
}

func isHomebrewManaged(binaryPath string) bool {
	return strings.Contains(filepath.ToSlash(binaryPath), "/Cellar/aiswap/")
}

func isNewerVersion(current, latest string) bool {
	cur := strings.TrimPrefix(strings.TrimSpace(current), "v")
	lat := strings.TrimPrefix(strings.TrimSpace(latest), "v")
	if lat == "" || cur == lat {
		return false
	}
	cParts, cOK := parseSemver(cur)
	lParts, lOK := parseSemver(lat)
	if !cOK || !lOK {
		// Non-semver current builds (e.g. "dev") can always upgrade to a tagged release.
		return !cOK && lOK
	}
	for i := 0; i < 3; i++ {
		if lParts[i] != cParts[i] {
			return lParts[i] > cParts[i]
		}
	}
	return false
}

func parseSemver(s string) ([3]int, bool) {
	if idx := strings.IndexAny(s, "-+"); idx >= 0 {
		s = s[:idx]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

func (u *Updater) Upgrade(ctx context.Context, current, targetBinary string) (*CheckResult, error) {
	if targetBinary == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve current executable: %w", err)
		}
		targetBinary, err = filepath.EvalSymlinks(exe)
		if err != nil {
			targetBinary = exe
		}
	}
	if isHomebrewManaged(targetBinary) {
		return nil, errors.New("aiswap is managed by Homebrew; run 'brew upgrade aiswap' instead")
	}

	chk, err := u.Check(ctx, current)
	if err != nil {
		return nil, err
	}
	if !chk.UpdateAvail {
		return chk, nil
	}

	asset := fmt.Sprintf("aiswap-%s-%s-%s.tar.gz", chk.LatestVersion, runtime.GOOS, runtime.GOARCH)
	tmpDir, err := os.MkdirTemp("", "aiswap-upgrade-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, asset)
	checksumsPath := filepath.Join(tmpDir, "checksums.txt")

	if err := u.downloadAsset(ctx, chk.LatestVersion, asset, archivePath); err != nil {
		return nil, err
	}
	if err := u.downloadAsset(ctx, chk.LatestVersion, "checksums.txt", checksumsPath); err != nil {
		return nil, fmt.Errorf("download checksums.txt: %w", err)
	}
	if err := VerifyChecksum(archivePath, checksumsPath, asset); err != nil {
		return nil, err
	}

	newBinBytes, err := extractBinaryFromTarGz(archivePath, "aiswap")
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(targetBinary)
	tmpBin, err := os.CreateTemp(dir, ".aiswap-upgrade-*")
	if err != nil {
		return nil, fmt.Errorf("create temp binary in %s: %w", dir, err)
	}
	tmpPath := tmpBin.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpBin.Write(newBinBytes); err != nil {
		_ = tmpBin.Close()
		return nil, err
	}
	if err := tmpBin.Chmod(0o755); err != nil {
		_ = tmpBin.Close()
		return nil, err
	}
	if err := tmpBin.Sync(); err != nil {
		_ = tmpBin.Close()
		return nil, err
	}
	if err := tmpBin.Close(); err != nil {
		return nil, err
	}

	if runtime.GOOS == "darwin" {
		if err := exec.CommandContext(ctx, "codesign", "--force", "--sign", "-", tmpPath).Run(); err != nil {
			return nil, fmt.Errorf("codesign upgraded binary: %w", err)
		}
	}

	if err := os.Rename(tmpPath, targetBinary); err != nil {
		return nil, fmt.Errorf("replace binary %s: %w", targetBinary, err)
	}
	return chk, nil
}

func (u *Updater) downloadAsset(ctx context.Context, version, asset, dest string) error {
	if strings.TrimRight(u.APIBase, "/") == defaultAPIBase {
		if _, err := exec.LookPath("gh"); err == nil {
			ghCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ghCtx, "gh", "release", "download", version, "-R", u.Repo, "-p", asset, "-O", dest)
			if err := cmd.Run(); err == nil {
				return nil
			}
		}
	}
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", u.Repo, version, asset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := u.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s returned http-%d", asset, resp.StatusCode)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(out, io.LimitReader(resp.Body, maxDownloadSize+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > maxDownloadSize {
		return fmt.Errorf("download %s exceeds maximum allowed size (%d bytes)", asset, maxDownloadSize)
	}
	return nil
}

func VerifyChecksum(filePath, checksumsPath, assetName string) error {
	raw, err := os.ReadFile(checksumsPath)
	if err != nil {
		return err
	}
	var expected string
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && (fields[1] == assetName || strings.TrimPrefix(fields[1], "*") == assetName) {
			expected = strings.ToLower(fields[0])
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("checksum entry for %s not found in %s", assetName, checksumsPath)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expected {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", assetName, expected, actual)
	}
	return nil
}

func extractBinaryFromTarGz(archivePath, name string) ([]byte, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gunzip %s: %w", archivePath, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar %s: %w", archivePath, err)
		}
		if filepath.Base(hdr.Name) == name && hdr.Typeflag == tar.TypeReg {
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, io.LimitReader(tr, maxBinarySize+1)); err != nil {
				return nil, err
			}
			if buf.Len() > maxBinarySize {
				return nil, fmt.Errorf("binary %q in %s exceeds maximum allowed size (%d bytes)", name, archivePath, maxBinarySize)
			}
			return buf.Bytes(), nil
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive %s", name, archivePath)
}
