// Package updater provides update-check and self-update logic for the milk CLI.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// ErrWindowsManual is returned by Apply as a last resort on Windows when even
// staging failed; the caller should print the temp file path. The normal
// Windows path replaces the running binary by renaming it aside (see
// Apply/StartupUpdate) and does not produce this error.
var ErrWindowsManual = errors.New("cannot replace running binary on Windows; download saved to temp path")

// ErrWindowsStaged is returned by Apply when the in-place replacement was
// denied (antivirus, exclusive lock) but the verified download was staged as
// dest.new for StartupUpdate to swap in at the next start. It is a deferred
// success — callers must not treat it as a failure, but must tell the user the
// new version becomes active on next start. The staged path is appended.
var ErrWindowsStaged = errors.New("update staged; it will be applied on next start")

// renameFile is os.Rename, a seam so tests can force the rename-denied path
// Windows guards against (locked image, antivirus) without needing Windows.
var renameFile = os.Rename

// ReleasesURL is the GitHub releases API endpoint CheckLatest fetches. A
// variable so tests can point it at a stub release feed — the only seam the
// fetch path needs (asset URLs are taken from the response's
// browser_download_url, so a stub can self-host those too).
var ReleasesURL = "https://api.github.com/repos/scoutme/milk/releases"

// Release holds the fields of a GitHub release that the updater cares about.
type Release struct {
	Tag          string `json:"tag_name"`
	Name         string `json:"name"`
	HTMLURL      string `json:"html_url"`
	AssetURL     string `json:"-"` // resolved from assets array
	ChecksumURL  string `json:"-"` // resolved from assets array
	IsPrerelease bool   `json:"prerelease"`
}

// ghRelease is the raw GitHub API shape used only for JSON unmarshalling.
type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Name       string    `json:"name"`
	HTMLURL    string    `json:"html_url"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// assetName returns the expected binary asset name for the current platform.
func assetName() string {
	if runtime.GOOS == "windows" {
		return "milk-windows-amd64.exe"
	}
	return fmt.Sprintf("milk-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// checksumAssetName returns the expected checksum asset name for the current
// platform.
func checksumAssetName() string {
	return assetName() + ".sha256"
}

// CheckLatest fetches the list of releases from GitHub and returns the newest
// release that is newer than currentVersion, or nil if already up to date.
//
// When includePrerelease is false, pre-release entries are skipped.
// If no binary asset matching the current platform is found for a release,
// that release is skipped.
// The string "dev" is treated as "0.0.0" so any real release appears newer.
func CheckLatest(ctx context.Context, currentVersion string, includePrerelease bool) (*Release, error) {
	url := ReleasesURL
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("updater: build request: %w", err)
	}
	req.Header.Set("User-Agent", "milk-updater/"+currentVersion)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("updater: fetch releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("updater: GitHub API returned %d", resp.StatusCode)
	}

	var raw []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("updater: decode releases: %w", err)
	}

	want := assetName()
	wantCS := checksumAssetName()

	for _, r := range raw {
		if r.Prerelease && !includePrerelease {
			continue
		}

		var binaryURL, csURL string
		for _, a := range r.Assets {
			switch a.Name {
			case want:
				binaryURL = a.BrowserDownloadURL
			case wantCS:
				csURL = a.BrowserDownloadURL
			}
		}
		if binaryURL == "" {
			// No matching binary for this platform; skip.
			continue
		}

		if !newerThan(r.TagName, currentVersion) {
			// Already at this version or newer; because releases are ordered
			// newest-first, no point checking further.
			return nil, nil
		}

		return &Release{
			Tag:          r.TagName,
			Name:         r.Name,
			HTMLURL:      r.HTMLURL,
			AssetURL:     binaryURL,
			ChecksumURL:  csURL,
			IsPrerelease: r.Prerelease,
		}, nil
	}

	return nil, nil
}

// Apply downloads and installs the release binary over dest.
//
// progress is called with (bytesDownloaded, totalBytes); the first call is
// always (0, contentLength). If contentLength is unknown, totalBytes is -1.
//
// On Windows, Apply renames the running image aside (Windows locks an
// executing binary against writes but allows renaming it — the loader opens
// the image with FILE_SHARE_DELETE), moves the verified download into place,
// and lets the running process keep executing the renamed old image; the
// leftover dest.old is removed by StartupUpdate at the next start. If any
// rename is denied, the download is staged as dest.new instead and
// ErrWindowsStaged is returned — StartupUpdate applies it lazily on the next
// start. Only when staging also fails does Apply return ErrWindowsManual with
// the temp path embedded — callers should print it to the user.
//
// On all other platforms, the temp file is atomically renamed over dest.
func Apply(ctx context.Context, r *Release, dest string, progress func(done, total int64)) error {
	dir := filepath.Dir(dest)

	// Create temp file in the same directory so Rename is atomic.
	tmp, err := os.CreateTemp(dir, ".milk-update-*")
	if err != nil {
		return fmt.Errorf("updater: create temp file: %w", err)
	}
	tmpName := tmp.Name()

	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	// Download the binary.
	if err := downloadTo(ctx, r.AssetURL, tmp, progress); err != nil {
		cleanup()
		return fmt.Errorf("updater: download binary: %w", err)
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("updater: close temp file: %w", err)
	}

	// Verify checksum if available.
	if r.ChecksumURL != "" {
		if err := verifyChecksum(ctx, tmpName, r.ChecksumURL); err != nil {
			os.Remove(tmpName)
			return fmt.Errorf("updater: checksum mismatch: %w", err)
		}
	}

	if runtime.GOOS == "windows" {
		return replaceRunningWindows(tmpName, dest)
	}

	// chmod +x before replacing.
	if err := os.Chmod(tmpName, 0755); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("updater: chmod: %w", err)
	}

	if err := os.Rename(tmpName, dest); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("updater: rename into place: %w", err)
	}

	return nil
}

// replaceRunningWindows installs the verified download tmpName over dest
// while dest is very likely the running executable.
//
// Windows denies writes to an executing image but allows renaming it (the
// loader opens the image with FILE_SHARE_DELETE), so the standard self-update
// dance works: rename the current image aside to dest+".old", move the new
// binary into place, and let this process keep executing the renamed image
// until it exits — dest holds the new version for the next start. The .old
// file cannot be deleted while we still run it; StartupUpdate removes it at
// the next start (or the start after, if another milk instance was running).
//
// When any rename is denied (antivirus, exclusive lock, Program Files ACLs),
// the download is staged as dest+".new" for StartupUpdate to swap in lazily
// on the next start (ErrWindowsStaged). Only if even staging fails does it
// fall back to ErrWindowsManual so the caller keeps the hand-replace path.
func replaceRunningWindows(tmpName, dest string) error {
	// Step 1: rename the running image aside. os.Rename replaces an
	// existing dest+".old" (leftover from a crashed cycle) when it isn't
	// itself locked by a still-running instance.
	if err := renameFile(dest, dest+".old"); err != nil {
		return stageWindows(tmpName, dest, fmt.Errorf("rename running binary aside: %w", err))
	}
	// Step 2: move the new binary into place.
	if err := renameFile(tmpName, dest); err != nil {
		// Put the old image back so the install leaves a working binary.
		_ = renameFile(dest+".old", dest)
		return stageWindows(tmpName, dest, fmt.Errorf("move new binary into place: %w", err))
	}
	// Step 3: best-effort cleanup — fails while this process still executes
	// the (renamed) old image; StartupUpdate retries at the next start.
	_ = os.Remove(dest + ".old")
	return nil
}

// stageWindows parks tmpName at dest+".new" as the deferred-install payload.
// cause explains why the in-place dance was denied and is kept in the error
// chain for diagnostics.
func stageWindows(tmpName, dest string, cause error) error {
	if err := renameFile(tmpName, dest+".new"); err != nil {
		return fmt.Errorf("%w (%v; staging also failed: %v): %s", ErrWindowsManual, cause, err, tmpName)
	}
	return fmt.Errorf("%w (%v): %s", ErrWindowsStaged, cause, dest+".new")
}

// StartupUpdate applies deferred self-update bookkeeping at process start:
// it swaps a staged dest.new into place and clears a leftover dest.old from a
// previous cycle. Best-effort by contract — callers ignore the error and it
// must never block startup.
func StartupUpdate() error {
	dest, err := CurrentBinaryPath()
	if err != nil {
		return err
	}
	return startupUpdate(dest)
}

// startupUpdate is StartupUpdate's body over an explicit dest (the test seam
// — tests must not resolve or touch the real running test binary).
//
// Staged swap: dest.new exists because replaceRunningWindows could not rename
// the running image. Renaming here is allowed for the same reason — we are
// executing dest — so dest moves to dest.old and dest.new becomes dest: this
// process keeps running the old image (now dest.old) and the NEXT start runs
// the new binary. The freshly renamed dest.old stays locked while we run, so
// cleanup is retried every start.
func startupUpdate(dest string) error {
	if _, err := os.Stat(dest + ".new"); err == nil {
		// Guard the hole case: a previous swap's restore may have failed,
		// leaving dest missing — then the staged file just moves into place.
		if _, statErr := os.Stat(dest); statErr == nil {
			if err := renameFile(dest, dest+".old"); err != nil {
				return fmt.Errorf("updater: move running binary aside: %w", err)
			}
		}
		if err := renameFile(dest+".new", dest); err != nil {
			// Put the old image back and leave the staged file for the
			// next start to retry.
			_ = renameFile(dest+".old", dest)
			return fmt.Errorf("updater: apply staged update: %w", err)
		}
	}
	// Remove the previous cycle's old image (unlocked now that the process
	// which renamed it is gone; still-running siblings keep it locked and
	// this retries every start). No-op on platforms that never create it.
	_ = os.Remove(dest + ".old")
	return nil
}

// CurrentBinaryPath returns the path of the running executable, resolving
// any symlinks.
func CurrentBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("updater: resolve executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("updater: eval symlinks: %w", err)
	}
	return resolved, nil
}

// downloadTo streams the resource at url into dst, calling progress as data
// arrives.
func downloadTo(ctx context.Context, url string, dst io.Writer, progress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}

	total := resp.ContentLength // -1 if unknown
	if progress != nil {
		progress(0, total)
	}

	buf := make([]byte, 32*1024)
	var done int64
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// verifyChecksum downloads the checksum file at csURL, parses the first
// hex-encoded SHA256 it finds, and compares it against the file at path.
func verifyChecksum(ctx context.Context, path, csURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, csURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("checksum server returned %d", resp.StatusCode)
	}

	csBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// The file is typically "<hex>  filename" or just "<hex>".
	fields := strings.Fields(string(csBytes))
	if len(fields) == 0 {
		return fmt.Errorf("empty checksum file")
	}
	expected := strings.ToLower(fields[0])

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))

	if got != expected {
		return fmt.Errorf("expected %s, got %s", expected, got)
	}
	return nil
}

// newerThan reports whether version a is strictly newer than version b.
// Both strings may have a leading "v". "dev" is treated as "0.0.0".
func newerThan(a, b string) bool {
	return versionCmp(normalizeVersion(a), normalizeVersion(b)) > 0
}

// normalizeVersion strips a leading "v" and maps "dev" to "0.0.0".
func normalizeVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	if v == "dev" || v == "" {
		return "0.0.0"
	}
	return v
}

// versionCmp compares two dot-separated integer version strings.
// Returns -1, 0, or 1.
func versionCmp(a, b string) int {
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")

	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}

	for i := 0; i < maxLen; i++ {
		var ia, ib int
		if i < len(partsA) {
			ia, _ = strconv.Atoi(partsA[i])
		}
		if i < len(partsB) {
			ib, _ = strconv.Atoi(partsB[i])
		}
		if ia < ib {
			return -1
		}
		if ia > ib {
			return 1
		}
	}
	return 0
}
