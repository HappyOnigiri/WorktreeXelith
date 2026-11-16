package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/HappyOnigiri/WorktreeX/internal/config"
	"github.com/HappyOnigiri/WorktreeX/internal/discovery"
	"github.com/HappyOnigiri/WorktreeX/internal/domain"
	"github.com/HappyOnigiri/WorktreeX/internal/gitx"
)

// git-lfs は fetch 設定で除外した path を cache の有無に関係なく pointer のまま書くため、
// 見積りはその path の cache を要求せず、実体化検証の対象からも外す。
func TestEstimateCapacityLeavesLFSFetchExcludedPathsAsPointers(t *testing.T) {
	if _, err := exec.LookPath("git-lfs"); err != nil {
		t.Skip("git-lfs is not installed")
	}
	t.Parallel()
	for _, test := range []struct {
		name      string
		lfsconfig string
		gitConfig []string
		wantPaths []string
		excluded  int
	}{
		{name: "lfsconfig exclude", lfsconfig: "[lfs]\n\tfetchexclude = excluded\n", wantPaths: []string{"kept.bin"}, excluded: 1},
		{name: "git config overrides lfsconfig", lfsconfig: "[lfs]\n\tfetchexclude = excluded\n", gitConfig: []string{"lfs.fetchexclude", "kept.bin"}, wantPaths: []string{"excluded/large.bin"}, excluded: 1},
		{name: "include limits fetch", gitConfig: []string{"lfs.fetchinclude", "kept.bin"}, wantPaths: []string{"kept.bin"}, excluded: 1},
		{name: "exclude everything", lfsconfig: "[lfs]\n\tfetchexclude = *\n", excluded: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := t.TempDir()
			gitCommand(t, repository, "init", "-b", "main")
			gitCommand(t, repository, "config", "user.email", "wx@example.invalid")
			gitCommand(t, repository, "config", "user.name", "wx")
			files := map[string]string{
				".gitattributes":     "*.bin filter=lfs\n",
				"excluded/large.bin": "version https://git-lfs.github.com/spec/v1\noid sha256:" + strings.Repeat("b", 64) + "\nsize 123\n",
				"kept.bin":           "version https://git-lfs.github.com/spec/v1\noid sha256:" + strings.Repeat("c", 64) + "\nsize 456\n",
			}
			if test.lfsconfig != "" {
				files[".lfsconfig"] = test.lfsconfig
			}
			for name, content := range files {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(repository, name)), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(repository, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			gitCommand(t, repository, "add", ".")
			gitCommand(t, repository, "commit", "-m", "lfs pointers")
			if len(test.gitConfig) != 0 {
				gitCommand(t, repository, append([]string{"config"}, test.gitConfig...)...)
			}
			oid := capacityGitOutput(t, repository, "rev-parse", "HEAD")
			common := capacityGitOutput(t, repository, "rev-parse", "--path-format=absolute", "--git-common-dir")
			repo := discovery.Repository{ID: "repo", MainPath: domain.CanonicalPath(repository), CommonDir: domain.CanonicalPath(common), RelativePath: "."}
			cfg := config.Defaults()
			cfg.Storage.CopyMode = config.CopyModeCopy
			p := Preparer{Git: &gitx.Runner{Timeout: 10 * time.Second}, Config: cfg}
			estimate, err := p.EstimateCapacity(context.Background(), repo, oid)
			if err != nil {
				t.Fatal(err)
			}
			var paths []string
			for _, object := range estimate.LFS {
				paths = append(paths, object.Paths...)
			}
			if !reflect.DeepEqual(paths, test.wantPaths) || estimate.LFSFetchExcludedPaths != test.excluded {
				t.Fatalf("LFS paths=%v excluded=%d, want %v and %d", paths, estimate.LFSFetchExcludedPaths, test.wantPaths, test.excluded)
			}
			if estimate.MissingLFSObjects != len(test.wantPaths) {
				t.Fatalf("missing LFS objects=%d, want only fetched paths %v", estimate.MissingLFSObjects, test.wantPaths)
			}
		})
	}
}
