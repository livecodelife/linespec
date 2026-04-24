package provenance

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/livecodelife/linespec/pkg/config"
)

// CacheManager handles fetching and storing remote provenance directories.
// Each configured shared repo is cached under ~/.linespec/cache/<sha256-of-url>/provenance/.
// A sidecar .meta file records when the cache was last populated for TTL checks.
type CacheManager struct {
	SharedRepos     []config.SharedRepoConfig
	CacheTTLMinutes int
	cacheBaseDir    string
}

// cacheMeta is stored as JSON alongside each cached repo's provenance directory.
type cacheMeta struct {
	FetchedAt   time.Time `json:"fetched_at"`
	RecordCount int       `json:"record_count"`
	RepoName    string    `json:"repo_name"`
	RepoURL     string    `json:"repo_url"`
	RepoRef     string    `json:"repo_ref"`
}

// NewCacheManager creates a CacheManager rooted at ~/.linespec/cache.
func NewCacheManager(repos []config.SharedRepoConfig, ttlMinutes int) *CacheManager {
	homeDir, _ := os.UserHomeDir()
	return &CacheManager{
		SharedRepos:     repos,
		CacheTTLMinutes: ttlMinutes,
		cacheBaseDir:    filepath.Join(homeDir, ".linespec", "cache"),
	}
}

// repoCacheRoot returns the base cache directory for a repo (parent of provenance/).
func (c *CacheManager) repoCacheRoot(repo config.SharedRepoConfig) string {
	hash := sha256.Sum256([]byte(repo.URL))
	return filepath.Join(c.cacheBaseDir, fmt.Sprintf("%x", hash))
}

// ProvenanceDir returns the path where a repo's provenance records are cached.
func (c *CacheManager) ProvenanceDir(repo config.SharedRepoConfig) string {
	return filepath.Join(c.repoCacheRoot(repo), "provenance")
}

// metaPath returns the path to the sidecar meta file for a repo.
func (c *CacheManager) metaPath(repo config.SharedRepoConfig) string {
	return filepath.Join(c.repoCacheRoot(repo), ".meta")
}

// ttl returns the effective TTL duration for cache freshness checks.
func (c *CacheManager) ttl() time.Duration {
	minutes := c.CacheTTLMinutes
	if minutes <= 0 {
		minutes = 60
	}
	return time.Duration(minutes) * time.Minute
}

// IsFresh reports whether the cached data for a repo was fetched within the TTL window.
func (c *CacheManager) IsFresh(repo config.SharedRepoConfig) bool {
	meta, err := c.readMeta(repo)
	if err != nil {
		return false
	}
	return time.Since(meta.FetchedAt) < c.ttl()
}

// LoadedDirs returns the provenance cache directories for all repos that have
// a populated cache (fresh or stale). These can be passed directly to the Loader.
func (c *CacheManager) LoadedDirs() []string {
	var dirs []string
	for _, repo := range c.SharedRepos {
		dir := c.ProvenanceDir(repo)
		if _, err := os.Stat(dir); err == nil {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// Sync fetches the provenance directory from a remote repo, unpacks it into the
// cache, and writes the .meta sidecar. Returns the record count and whether the
// cache was already fresh (skipped). Uses git archive where supported (SSH and
// some HTTPS hosts); falls back to a depth-1 sparse checkout for GitHub-style
// HTTPS remotes that reject git archive.
func (c *CacheManager) Sync(repo config.SharedRepoConfig, force bool) (int, bool, error) {
	if !force && c.IsFresh(repo) {
		meta, _ := c.readMeta(repo)
		return meta.RecordCount, true, nil
	}

	ref := repo.Ref
	if ref == "" {
		ref = "main"
	}

	provDir := c.ProvenanceDir(repo)
	if err := os.MkdirAll(provDir, 0755); err != nil {
		return 0, false, fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Try git archive first — works for SSH remotes and some HTTPS servers.
	count, err := c.fetchViaArchive(repo, ref, provDir)
	if err != nil {
		// Fall back to sparse checkout for HTTPS remotes (e.g. GitHub) that
		// do not support git archive over HTTP.
		count, err = c.fetchViaSparseCheckout(repo, ref, provDir)
		if err != nil {
			return 0, false, err
		}
	}

	if err := c.writeMeta(repo, count); err != nil {
		return count, false, fmt.Errorf("warning: failed to write cache meta for %s: %w", repo.Name, err)
	}
	return count, false, nil
}

// repoDir returns the subdirectory within the remote repo that contains provenance
// records. Defaults to "provenance" if not explicitly configured.
func repoDir(repo config.SharedRepoConfig) string {
	if repo.Dir != "" {
		return repo.Dir
	}
	return "provenance"
}

// fetchViaArchive fetches using git archive --remote, which works for SSH and
// some HTTPS hosts. Returns an error if the remote rejects git archive.
func (c *CacheManager) fetchViaArchive(repo config.SharedRepoConfig, ref, provDir string) (int, error) {
	dir := repoDir(repo)
	cmd := exec.Command("git", "archive", "--remote="+repo.URL, ref, dir+"/")
	pr, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("git archive failed for %s: %w", repo.Name, err)
	}
	// Capture stderr for a helpful error message on non-zero exit.
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("git archive failed for %s: %w", repo.Name, err)
	}

	// Remove stale contents before writing fresh data.
	if entries, _ := os.ReadDir(provDir); len(entries) > 0 {
		for _, entry := range entries {
			_ = os.Remove(filepath.Join(provDir, entry.Name()))
		}
	}

	count, unpackErr := unpackProvenance(pr, provDir)
	if waitErr := cmd.Wait(); waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return 0, fmt.Errorf("git archive failed for %s: %s",
				repo.Name, strings.TrimSpace(stderrBuf.String()))
		}
		return 0, fmt.Errorf("git archive failed for %s: %w", repo.Name, waitErr)
	}
	if unpackErr != nil {
		return 0, fmt.Errorf("failed to unpack archive for %s: %w", repo.Name, unpackErr)
	}
	return count, nil
}

// fetchViaSparseCheckout fetches only the configured provenance subdirectory using a
// depth-1 partial clone with blob filtering and sparse checkout. This works
// universally including GitHub HTTPS URLs.
func (c *CacheManager) fetchViaSparseCheckout(repo config.SharedRepoConfig, ref, provDir string) (int, error) {
	dir := repoDir(repo)

	tmpDir, err := os.MkdirTemp("", "linespec-sync-*")
	if err != nil {
		return 0, fmt.Errorf("failed to create temp dir for %s: %w", repo.Name, err)
	}
	defer os.RemoveAll(tmpDir)

	run := func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Dir = tmpDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s %s failed for %s: %s", name, args[0], repo.Name, strings.TrimSpace(string(out)))
		}
		return nil
	}

	if err := run("git", "clone", "--depth=1", "--filter=blob:none", "--sparse",
		"--branch="+ref, repo.URL, "."); err != nil {
		return 0, err
	}
	// Sparse checkout the exact directory (git respects nested paths like "a/b").
	if err := run("git", "sparse-checkout", "set", "--no-cone", dir); err != nil {
		return 0, err
	}

	// Copy .yml files from the checked-out directory into the cache.
	srcDir := filepath.Join(tmpDir, filepath.FromSlash(dir))
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // repo has no such directory
		}
		return 0, fmt.Errorf("failed to read provenance dir from %s: %w", repo.Name, err)
	}

	// Remove stale contents before writing fresh data.
	if existing, _ := os.ReadDir(provDir); len(existing) > 0 {
		for _, entry := range existing {
			_ = os.Remove(filepath.Join(provDir, entry.Name()))
		}
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		src := filepath.Join(srcDir, name)
		dst := filepath.Join(provDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			return count, fmt.Errorf("failed to read %s from %s: %w", name, repo.Name, err)
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return count, fmt.Errorf("failed to write %s to cache: %w", name, err)
		}
		count++
	}
	return count, nil
}

// SyncAll refreshes all configured repos. force=true ignores TTL.
// Returns a slice of results in the same order as SharedRepos.
type SyncResult struct {
	Repo        config.SharedRepoConfig
	RecordCount int
	WasFresh    bool
	Err         error
}

func (c *CacheManager) SyncAll(force bool) []SyncResult {
	results := make([]SyncResult, len(c.SharedRepos))
	for i, repo := range c.SharedRepos {
		count, wasFresh, err := c.Sync(repo, force)
		results[i] = SyncResult{
			Repo:        repo,
			RecordCount: count,
			WasFresh:    wasFresh,
			Err:         err,
		}
	}
	return results
}

// readMeta reads the sidecar .meta file for a repo.
func (c *CacheManager) readMeta(repo config.SharedRepoConfig) (*cacheMeta, error) {
	data, err := os.ReadFile(c.metaPath(repo))
	if err != nil {
		return nil, err
	}
	var meta cacheMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// writeMeta writes the sidecar .meta file for a repo.
func (c *CacheManager) writeMeta(repo config.SharedRepoConfig, recordCount int) error {
	meta := cacheMeta{
		FetchedAt:   time.Now(),
		RecordCount: recordCount,
		RepoName:    repo.Name,
		RepoURL:     repo.URL,
		RepoRef:     repo.Ref,
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.metaPath(repo), data, 0644)
}

// unpackProvenance extracts .yml files from a tar archive into destDir,
// stripping the first path component (e.g. "provenance/" or "linespec-cli/").
// Returns the count of files written.
func unpackProvenance(r io.Reader, destDir string) (int, error) {
	tr := tar.NewReader(r)
	count := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return count, fmt.Errorf("tar read error: %w", err)
		}

		// Strip the leading directory component (whatever subdir was requested).
		name := hdr.Name
		if idx := strings.Index(name, "/"); idx >= 0 {
			name = name[idx+1:]
		}
		// Only extract flat files (no subdirs).
		if strings.Contains(name, "/") || name == "" {
			continue
		}
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}

		destPath := filepath.Join(destDir, name)
		f, err := os.Create(destPath)
		if err != nil {
			return count, fmt.Errorf("failed to create %s: %w", destPath, err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return count, fmt.Errorf("failed to write %s: %w", destPath, err)
		}
		f.Close()
		count++
	}
	return count, nil
}
