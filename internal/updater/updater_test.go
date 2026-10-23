package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// feedOpts describes the single-release GitHub feed stubFeed serves.
type feedOpts struct {
	tag               string
	prerelease        bool
	binary            []byte
	checksumOK        bool
	omitPlatformAsset bool // release carries no binary asset for this platform
}

// stubFeed serves a one-release GitHub feed whose binary and checksum assets
// self-host on the same server, and points ReleasesURL at it.
func stubFeed(t *testing.T, o feedOpts) *httptest.Server {
	t.Helper()
	if o.binary == nil {
		o.binary = []byte("fake milk binary")
	}
	sum := sha256.Sum256(o.binary)
	sumHex := hex.EncodeToString(sum[:])
	if !o.checksumOK {
		sumHex = strings.Repeat("0", 64)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch {
		case strings.HasSuffix(r.URL.Path, ".sha256"):
			fmt.Fprintf(w, "%s  %s\n", sumHex, assetName())
		case strings.Contains(r.URL.Path, "/assets/"):
			_, _ = w.Write(o.binary)
		default:
			assets := ""
			if !o.omitPlatformAsset {
				assets = fmt.Sprintf(`,{"name":%q,"browser_download_url":%q},{"name":%q,"browser_download_url":%q}`,
					assetName(), base+"/assets/"+assetName(),
					checksumAssetName(), base+"/assets/"+checksumAssetName())
			} else {
				assets = `,{"name":"milk-some-other-platform","browser_download_url":"` + base + `/assets/x"}`
			}
			fmt.Fprintf(w, `[{"tag_name":%q,"name":"rel","html_url":%q,"prerelease":%v,"assets":[%s]}]`,
				strings.TrimPrefix(o.tag, ""), base+"/releases/"+o.tag, o.prerelease, strings.TrimPrefix(assets, ","))
		}
	}))
	t.Cleanup(srv.Close)
	old := ReleasesURL
	ReleasesURL = srv.URL + "/releases"
	t.Cleanup(func() { ReleasesURL = old })
	return srv
}

func TestCheckLatest_NewerReleaseMatched(t *testing.T) {
	stubFeed(t, feedOpts{tag: "v2.0.0", checksumOK: true})

	rel, err := CheckLatest(context.Background(), "v1.0.0", false)
	if err != nil {
		t.Fatalf("CheckLatest: %v", err)
	}
	if rel == nil {
		t.Fatal("release = nil, want v2.0.0")
	}
	if rel.Tag != "v2.0.0" {
		t.Errorf("Tag = %q, want v2.0.0", rel.Tag)
	}
	if rel.AssetURL == "" || rel.ChecksumURL == "" {
		t.Errorf("asset URLs not resolved: %+v", rel)
	}
}

func TestCheckLatest_UpToDateNil(t *testing.T) {
	stubFeed(t, feedOpts{tag: "v1.0.0", checksumOK: true})

	rel, err := CheckLatest(context.Background(), "v1.0.0", false)
	if err != nil {
		t.Fatalf("CheckLatest: %v", err)
	}
	if rel != nil {
		t.Errorf("release = %+v, want nil when already at the newest tag", rel)
	}
}

func TestCheckLatest_PrereleaseGated(t *testing.T) {
	stubFeed(t, feedOpts{tag: "v2.0.0", prerelease: true, checksumOK: true})

	rel, err := CheckLatest(context.Background(), "v1.0.0", false)
	if err != nil {
		t.Fatalf("CheckLatest: %v", err)
	}
	if rel != nil {
		t.Errorf("prerelease shown on the stable channel: %+v", rel)
	}

	rel, err = CheckLatest(context.Background(), "v1.0.0", true)
	if err != nil {
		t.Fatalf("CheckLatest (prerelease): %v", err)
	}
	if rel == nil || rel.Tag != "v2.0.0" {
		t.Errorf("release = %+v, want v2.0.0 on the prerelease channel", rel)
	}
}

func TestCheckLatest_NoPlatformAssetSkipped(t *testing.T) {
	stubFeed(t, feedOpts{tag: "v2.0.0", checksumOK: true, omitPlatformAsset: true})

	rel, err := CheckLatest(context.Background(), "v1.0.0", false)
	if err != nil {
		t.Fatalf("CheckLatest: %v", err)
	}
	if rel != nil {
		t.Errorf("release without a %s asset returned: %+v", assetName(), rel)
	}
}

func TestApply_VerifiesChecksumAndReplaces(t *testing.T) {
	if testing.Short() {
		t.Skip("replaces a file on disk")
	}
	binary := []byte("new milk binary")
	stubFeed(t, feedOpts{tag: "v2.0.0", binary: binary, checksumOK: true})
	rel, err := CheckLatest(context.Background(), "v1.0.0", false)
	if err != nil || rel == nil {
		t.Fatalf("CheckLatest = (%+v, %v)", rel, err)
	}

	dest := filepath.Join(t.TempDir(), "milk-bin")
	if err := Apply(context.Background(), rel, dest, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(binary) {
		t.Errorf("dest content = %q, want %q", got, binary)
	}
}

func TestApply_ChecksumMismatchAbortsWithoutTouchingDest(t *testing.T) {
	stubFeed(t, feedOpts{tag: "v2.0.0", checksumOK: false})
	rel, err := CheckLatest(context.Background(), "v1.0.0", false)
	if err != nil || rel == nil {
		t.Fatalf("CheckLatest = (%+v, %v)", rel, err)
	}

	dest := filepath.Join(t.TempDir(), "milk-bin")
	err = Apply(context.Background(), rel, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("Apply error = %v, want a checksum mismatch", err)
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("dest exists after a failed checksum — the download must not land")
	}
}

func TestVersionCmpAndNewerThan(t *testing.T) {
	// versionCmp compares already-normalized numeric versions; newerThan is
	// the public entry point that strips "v" and maps "dev" first.
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.10.0", "1.9.0", 1},
		{"1.2", "1.2.0", 0},
		{"0.9.0", "0.10.0", -1},
	} {
		if got := versionCmp(tc.a, tc.b); got != tc.want {
			t.Errorf("versionCmp(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"v2.0.0", "1.9.9", true},
		{"v1.0.0", "v1.0.0", false},
		{"v0.0.1", "dev", true},
		{"dev", "v0.0.1", false},
	} {
		if got := newerThan(tc.a, tc.b); got != tc.want {
			t.Errorf("newerThan(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// --- Windows replace/stage/startup dance ---
//
// The dance is plain rename bookkeeping that runs on any OS; what is
// Windows-specific is only *why* it exists (an executing image is locked
// against writes but can be renamed). The renameFile seam forces the
// denied-rename cases without needing Windows.

// stubRename redirects os.Rename through fn; restored via t.Cleanup.
func stubRename(t *testing.T, fn func(from, to string) error) {
	t.Helper()
	old := renameFile
	renameFile = fn
	t.Cleanup(func() { renameFile = old })
}

// denyTo makes renames whose destination ends with suffix fail.
func denyTo(suffix string) func(from, to string) error {
	return func(from, to string) error {
		if strings.HasSuffix(to, suffix) {
			return fmt.Errorf("access is denied (simulated): %s", to)
		}
		return os.Rename(from, to)
	}
}

func writeUpDownFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func readFileContent(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func TestReplaceRunningWindows_InPlaceDance(t *testing.T) {
	dir := t.TempDir()
	dest := writeUpDownFile(t, dir, "milk.exe", "old-binary")
	tmp := writeUpDownFile(t, dir, ".milk-update-x", "new-binary")

	if err := replaceRunningWindows(tmp, dest); err != nil {
		t.Fatalf("replaceRunningWindows: %v", err)
	}
	if got := readFileContent(t, dest); got != "new-binary" {
		t.Errorf("dest = %q, want new-binary", got)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("tmp still exists after rename into place")
	}
	// The best-effort .old cleanup succeeds while nothing executes it (on
	// Windows the running process keeps it; the test FS doesn't).
	if _, err := os.Stat(dest + ".old"); !os.IsNotExist(err) {
		t.Errorf("dest.old left behind although removable")
	}
}

func TestReplaceRunningWindows_StageWhenRenameAsideDenied(t *testing.T) {
	dir := t.TempDir()
	dest := writeUpDownFile(t, dir, "milk.exe", "old-binary")
	tmp := writeUpDownFile(t, dir, ".milk-update-x", "new-binary")
	stubRename(t, denyTo(".old"))

	err := replaceRunningWindows(tmp, dest)
	if !errors.Is(err, ErrWindowsStaged) {
		t.Fatalf("err = %v, want ErrWindowsStaged", err)
	}
	if got := readFileContent(t, dest); got != "old-binary" {
		t.Errorf("dest = %q, want the old binary untouched", got)
	}
	if got := readFileContent(t, dest+".new"); got != "new-binary" {
		t.Errorf("staged = %q, want new-binary", got)
	}
	if !strings.Contains(err.Error(), dest+".new") {
		t.Errorf("error does not carry the staged path: %v", err)
	}
}

func TestReplaceRunningWindows_StageWhenMoveIntoPlaceDenied(t *testing.T) {
	dir := t.TempDir()
	dest := writeUpDownFile(t, dir, "milk.exe", "old-binary")
	tmp := writeUpDownFile(t, dir, ".milk-update-x", "new-binary")
	// Only the tmp→dest move is denied; the aside-rename and its restore work.
	stubRename(t, func(from, to string) error {
		if from == tmp && to == dest {
			return fmt.Errorf("access is denied (simulated): %s", to)
		}
		return os.Rename(from, to)
	})

	err := replaceRunningWindows(tmp, dest)
	if !errors.Is(err, ErrWindowsStaged) {
		t.Fatalf("err = %v, want ErrWindowsStaged", err)
	}
	// The old image must be restored — the install leaves a working binary.
	if got := readFileContent(t, dest); got != "old-binary" {
		t.Errorf("dest = %q, want the restored old binary", got)
	}
	if got := readFileContent(t, dest+".new"); got != "new-binary" {
		t.Errorf("staged = %q, want new-binary", got)
	}
	if _, err := os.Stat(dest + ".old"); !os.IsNotExist(err) {
		t.Errorf("dest.old left behind after restore")
	}
}

func TestReplaceRunningWindows_ManualWhenStagingDenied(t *testing.T) {
	dir := t.TempDir()
	dest := writeUpDownFile(t, dir, "milk.exe", "old-binary")
	tmp := writeUpDownFile(t, dir, ".milk-update-x", "new-binary")
	// Both the aside-rename and staging are denied — the genuinely manual path.
	stubRename(t, func(from, to string) error {
		if strings.HasSuffix(to, ".old") || strings.HasSuffix(to, ".new") {
			return fmt.Errorf("access is denied (simulated): %s", to)
		}
		return os.Rename(from, to)
	})

	err := replaceRunningWindows(tmp, dest)
	if !errors.Is(err, ErrWindowsManual) {
		t.Fatalf("err = %v, want ErrWindowsManual", err)
	}
	if !strings.Contains(err.Error(), tmp) {
		t.Errorf("error does not carry the temp path for manual replace: %v", err)
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Errorf("downloaded binary must survive for the manual path: %v", err)
	}
	if got := readFileContent(t, dest); got != "old-binary" {
		t.Errorf("dest = %q, want the old binary untouched", got)
	}
}

func TestStartupUpdate_SwapsStagedInstall(t *testing.T) {
	dir := t.TempDir()
	dest := writeUpDownFile(t, dir, "milk.exe", "old-binary")
	writeUpDownFile(t, dir, "milk.exe.new", "new-binary")

	if err := startupUpdate(dest); err != nil {
		t.Fatalf("startupUpdate: %v", err)
	}
	if got := readFileContent(t, dest); got != "new-binary" {
		t.Errorf("dest = %q, want the staged new binary swapped in", got)
	}
	if _, err := os.Stat(dest + ".new"); !os.IsNotExist(err) {
		t.Errorf("staged file left behind")
	}
	if _, err := os.Stat(dest + ".old"); !os.IsNotExist(err) {
		t.Errorf("old image left behind although removable")
	}
}

func TestStartupUpdate_RecoveryHoleMissingDest(t *testing.T) {
	dir := t.TempDir()
	// A previous swap's restore failed: dest is gone, only the staged file
	// remains — it must move into place instead of wedging.
	writeUpDownFile(t, dir, "milk.exe.new", "new-binary")

	if err := startupUpdate(filepath.Join(dir, "milk.exe")); err != nil {
		t.Fatalf("startupUpdate: %v", err)
	}
	if got := readFileContent(t, filepath.Join(dir, "milk.exe")); got != "new-binary" {
		t.Errorf("dest = %q, want the staged new binary", got)
	}
}

func TestStartupUpdate_CleansLeftoverOldImage(t *testing.T) {
	dir := t.TempDir()
	dest := writeUpDownFile(t, dir, "milk.exe", "current-binary")
	writeUpDownFile(t, dir, "milk.exe.old", "previous-cycle")

	if err := startupUpdate(dest); err != nil {
		t.Fatalf("startupUpdate: %v", err)
	}
	if _, err := os.Stat(dest + ".old"); !os.IsNotExist(err) {
		t.Errorf("dest.old from a previous cycle not removed")
	}
	if got := readFileContent(t, dest); got != "current-binary" {
		t.Errorf("dest = %q, want the current binary untouched", got)
	}
}

func TestStartupUpdate_DeniedSwapKeepsStagedForNextStart(t *testing.T) {
	dir := t.TempDir()
	dest := writeUpDownFile(t, dir, "milk.exe", "old-binary")
	writeUpDownFile(t, dir, "milk.exe.new", "new-binary")
	stubRename(t, func(from, to string) error {
		// Only the staged file's move into place is denied; the aside-rename
		// and its restore must keep working.
		if strings.HasSuffix(from, ".new") && to == dest {
			return fmt.Errorf("access is denied (simulated): %s", to)
		}
		return os.Rename(from, to)
	})
	err := startupUpdate(dest)
	if err == nil {
		t.Fatal("startupUpdate = nil, want the denied-swap error")
	}
	if got := readFileContent(t, dest); got != "old-binary" {
		t.Errorf("dest = %q, want the old binary restored", got)
	}
	if _, err := os.Stat(dest + ".new"); err != nil {
		t.Errorf("staged file must survive for the next start: %v", err)
	}
	if _, err := os.Stat(dest + ".old"); !os.IsNotExist(err) {
		t.Errorf("dest.old left behind after the restore")
	}
}
