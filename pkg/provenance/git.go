package provenance

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Git provides git operations for provenance integration
type Git struct {
	RepoRoot string
}

// NewGit creates a new Git helper
func NewGit(repoRoot string) *Git {
	return &Git{RepoRoot: repoRoot}
}

// GetModifiedFiles returns files modified in a commit or commit range
func (g *Git) GetModifiedFiles(commit string) ([]string, error) {
	cmd := exec.Command("git", "diff-tree", "--no-commit-id", "--name-only", "-r", commit)
	if g.RepoRoot != "" {
		cmd.Dir = g.RepoRoot
	}

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get modified files: %w", err)
	}

	files := strings.Split(string(output), "\n")
	var result []string
	for _, f := range files {
		f = strings.TrimSpace(f)
		if f != "" {
			result = append(result, f)
		}
	}

	return result, nil
}

// GetCommitMessage returns the commit message for a given commit
func (g *Git) GetCommitMessage(commit string) (string, error) {
	cmd := exec.Command("git", "log", "-1", "--format=%B", commit)
	if g.RepoRoot != "" {
		cmd.Dir = g.RepoRoot
	}

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get commit message: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// ExtractProvenanceIDs extracts provenance record IDs from a commit message
// Format: [prov-YYYY-NNN]
func (g *Git) ExtractProvenanceIDs(message string) []string {
	pattern := regexp.MustCompile(`\[prov-\d{4}-\d{3}\]`)
	matches := pattern.FindAllString(message, -1)

	var ids []string
	for _, match := range matches {
		// Remove brackets
		id := strings.Trim(match, "[]")
		ids = append(ids, id)
	}

	return ids
}

// GetCommitsInRange returns all commits between two references
func (g *Git) GetCommitsInRange(from, to string) ([]string, error) {
	cmd := exec.Command("git", "log", "--format=%H", fmt.Sprintf("%s..%s", from, to))
	if g.RepoRoot != "" {
		cmd.Dir = g.RepoRoot
	}

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get commits in range: %w", err)
	}

	commits := strings.Split(string(output), "\n")
	var result []string
	for _, c := range commits {
		c = strings.TrimSpace(c)
		if c != "" {
			result = append(result, c)
		}
	}

	return result, nil
}

// GetCommitsForRecord returns all commits that reference a given record ID
func (g *Git) GetCommitsForRecord(recordID string) ([]string, error) {
	cmd := exec.Command("git", "log", "--all", "--grep", fmt.Sprintf("[%s]", recordID), "--format=%H")
	if g.RepoRoot != "" {
		cmd.Dir = g.RepoRoot
	}

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get commits for record: %w", err)
	}

	commits := strings.Split(string(output), "\n")
	var result []string
	for _, c := range commits {
		c = strings.TrimSpace(c)
		if c != "" {
			result = append(result, c)
		}
	}

	return result, nil
}

// GetFilesChangedInCommits returns all files changed across a set of commits
func (g *Git) GetFilesChangedInCommits(commits []string) ([]string, error) {
	fileSet := make(map[string]bool)

	for _, commit := range commits {
		files, err := g.GetModifiedFiles(commit)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			fileSet[f] = true
		}
	}

	var result []string
	for f := range fileSet {
		result = append(result, f)
	}

	return result, nil
}

// GetGitEmail returns the configured git user email
func (g *Git) GetGitEmail() (string, error) {
	cmd := exec.Command("git", "config", "user.email")
	if g.RepoRoot != "" {
		cmd.Dir = g.RepoRoot
	}

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get git email: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// CommitChecker checks commits for provenance violations
type CommitChecker struct {
	Git    *Git
	Loader *Loader
}

// NewCommitChecker creates a new commit checker
func NewCommitChecker(git *Git, loader *Loader) *CommitChecker {
	return &CommitChecker{
		Git:    git,
		Loader: loader,
	}
}

// Violation represents a forbidden scope violation
type Violation struct {
	RecordID string
	File     string
	Commit   string
	Message  string
}

// CheckCommit checks a single commit for violations
func (c *CommitChecker) CheckCommit(commit string) ([]Violation, error) {
	message, err := c.Git.GetCommitMessage(commit)
	if err != nil {
		return nil, err
	}

	recordIDs := c.Git.ExtractProvenanceIDs(message)
	if len(recordIDs) == 0 {
		// No provenance IDs in commit, nothing to check
		return nil, nil
	}

	files, err := c.Git.GetModifiedFiles(commit)
	if err != nil {
		return nil, err
	}

	var violations []Violation

	for _, recordID := range recordIDs {
		record, exists := c.Loader.GetRecord(recordID)
		if !exists {
			// Unknown record ID, skip
			continue
		}

		for _, file := range files {
			inScope, err := record.IsInScope(file)
			if err != nil {
				return nil, err
			}

			if !inScope {
				violations = append(violations, Violation{
					RecordID: recordID,
					File:     file,
					Commit:   commit,
					Message:  fmt.Sprintf("%s forbids changes to %s", recordID, file),
				})
			}
		}
	}

	return violations, nil
}

// CheckRange checks a range of commits for violations
func (c *CommitChecker) CheckRange(from, to string) ([]Violation, error) {
	commits, err := c.Git.GetCommitsInRange(from, to)
	if err != nil {
		return nil, err
	}

	var allViolations []Violation
	for _, commit := range commits {
		violations, err := c.CheckCommit(commit)
		if err != nil {
			return nil, err
		}
		allViolations = append(allViolations, violations...)
	}

	return allViolations, nil
}

// AutoPopulateScope populates affected_scope from git commits for a record
func (c *CommitChecker) AutoPopulateScope(record *Record) error {
	if record.ScopeMode() == "allowlist" {
		// Already in allowlist mode, don't auto-populate
		return nil
	}

	commits, err := c.Git.GetCommitsForRecord(record.ID)
	if err != nil {
		return err
	}

	if len(commits) == 0 {
		return nil
	}

	files, err := c.Git.GetFilesChangedInCommits(commits)
	if err != nil {
		return err
	}

	// Merge new files into existing affected_scope
	existingSet := make(map[string]bool)
	for _, f := range record.AffectedScope {
		existingSet[f] = true
	}

	for _, f := range files {
		if !existingSet[f] {
			record.AffectedScope = append(record.AffectedScope, f)
			existingSet[f] = true
		}
	}

	return nil
}
