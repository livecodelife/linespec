package provenance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
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
// Format: [prov-YYYY-NNN], [prov-YYYY-XXXXXXXX], or with service suffix
func (g *Git) ExtractProvenanceIDs(message string) []string {
	// Match both legacy sequential format (prov-YYYY-NNN) and new crypto random format (prov-YYYY-XXXXXXXX)
	pattern := regexp.MustCompile(`\[prov-\d{4}-(?:\d{3}|[a-f0-9]{8})(?:-[a-z0-9-]+)?\]`)
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
	// Escape square brackets for git grep (otherwise interpreted as character range)
	cmd := exec.Command("git", "log", "--all", "--grep", fmt.Sprintf("\\[%s\\]", recordID), "--format=%H")
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

// isRecordFile checks if a file path matches a record's file path
// Handles path normalization (relative vs absolute)
func isRecordFile(filePath string, record *Record) bool {
	// Get the base filename from both paths
	fileBase := filepath.Base(filePath)
	recordBase := filepath.Base(record.FilePath)

	// Compare base filenames
	return fileBase == recordBase
}

// isHashManifest reports whether filePath is the system-managed hash manifest.
// The manifest is updated automatically on every complete and must never be
// subject to scope enforcement.
func isHashManifest(filePath string) bool {
	return filepath.ToSlash(filePath) == ".linespec/hash_manifest.json"
}

// isFileForbiddenForRecord checks if a file is in the record's forbidden_scope
func isFileForbiddenForRecord(filePath string, record *Record) (bool, error) {
	for _, pattern := range record.ForbiddenScope {
		matches, err := MatchPattern(filePath, pattern)
		if err != nil {
			return false, err
		}
		if matches {
			return true, nil
		}
	}
	return false, nil
}
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

// GetHeadSHA returns the SHA of the current HEAD commit
func (g *Git) GetHeadSHA() (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	if g.RepoRoot != "" {
		cmd.Dir = g.RepoRoot
	}

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get HEAD SHA: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// CommitRecord stages the given files and creates a commit with the provided message.
// Used by commands that have commit_on_status_change enabled.
func (g *Git) CommitRecord(message string, filePaths ...string) error {
	args := append([]string{"add"}, filePaths...)
	add := exec.Command("git", args...)
	if g.RepoRoot != "" {
		add.Dir = g.RepoRoot
	}
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("git add failed: %w\n%s", err, out)
	}

	commit := exec.Command("git", "commit", "-m", message)
	if g.RepoRoot != "" {
		commit.Dir = g.RepoRoot
	}
	commit.Stdout = os.Stdout
	commit.Stderr = os.Stderr
	if err := commit.Run(); err != nil {
		return fmt.Errorf("git commit failed: %w", err)
	}
	return nil
}

// Unstage removes the given paths from the index, leaving working-tree contents
// untouched. Used to undo the `git add` performed by an auto-commit when a
// status-change transition is rolled back after a rejected commit.
func (g *Git) Unstage(filePaths ...string) error {
	if len(filePaths) == 0 {
		return nil
	}
	args := append([]string{"reset", "--quiet", "HEAD", "--"}, filePaths...)
	cmd := exec.Command("git", args...)
	if g.RepoRoot != "" {
		cmd.Dir = g.RepoRoot
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git reset failed: %w\n%s", err, out)
	}
	return nil
}

// GetFilesChangedSince returns files that have changed between two SHAs
func (g *Git) GetFilesChangedSince(fromSHA, toSHA string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", fmt.Sprintf("%s..%s", fromSHA, toSHA))
	if g.RepoRoot != "" {
		cmd.Dir = g.RepoRoot
	}

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get files changed since %s: %w", fromSHA, err)
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

// GetStagedFiles returns files staged for commit
func (g *Git) GetStagedFiles() ([]string, error) {
	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	if g.RepoRoot != "" {
		cmd.Dir = g.RepoRoot
	}

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get staged files: %w", err)
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

// GetWorkingTreeChanges returns every file that differs from HEAD — staged and
// unstaged combined (`git diff --name-only HEAD`). It is the "what am I working
// on" signal the advice engine uses for ambient `next` when no intended files are
// given.
func (g *Git) GetWorkingTreeChanges() ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", "HEAD")
	if g.RepoRoot != "" {
		cmd.Dir = g.RepoRoot
	}

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get working tree changes: %w", err)
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

// ReadCommitMessageFile reads the commit message from a file
func (g *Git) ReadCommitMessageFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read commit message file: %w", err)
	}

	return strings.TrimSpace(string(data)), nil
}

// CommitChecker checks commits for provenance violations
type CommitChecker struct {
	Git          *Git
	Loader       *Loader
	ExcludePaths []string // patterns for files exempt from all provenance enforcement
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

	allFiles, err := c.Git.GetModifiedFiles(commit)
	if err != nil {
		return nil, err
	}

	// Filter excluded files so they cannot trigger scope or tag violations.
	var files []string
	for _, f := range allFiles {
		if !IsPathExcluded(f, c.ExcludePaths) {
			files = append(files, f)
		}
	}

	var violations []Violation

	for _, recordID := range recordIDs {
		record, exists := c.Loader.GetRecord(recordID)
		if !exists {
			// Unknown record ID, skip scope check for this record
			// (This allows new record creation to pass)
			continue
		}

		// Determine whether this record was already implemented immediately
		// before this commit (i.e. sealed by an earlier commit), by reading the
		// record's own file as it stood at commit^. This is commit-relative,
		// unlike record.Status (which reflects the Loader's current/final view of
		// the record) — historical audit (CheckCommit/CheckRange) must judge a
		// record's pre-seal lifecycle commits (draft creation, open transition,
		// in-progress implementation work) by what was true about the record at
		// the time, not by what it eventually became. Using record.Status here
		// would flag every one of those commits as violations once the record is
		// later implemented.
		implementedBefore, err := c.wasRecordImplementedAtRef(commit+"^", record)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not determine prior record status: %v\n", err)
			implementedBefore = true // fail closed: preserve the immutability guarantee
		}

		for _, file := range files {
			// The hash manifest is system-managed and exempt from scope enforcement,
			// regardless of the governing record's status. Must run before the
			// implemented-record guard below, or every completion commit (which
			// stages the manifest alongside the record) reports a false violation.
			if isHashManifest(file) {
				continue
			}

			// Allow a file belonging to a record whose `implements` field equals
			// this record's ID (e.g. an imprint created while working under this
			// blueprint), without requiring that path in this record's
			// affected_scope. Must also run before the implemented-record guard,
			// since the blueprint may already be implemented by the time the
			// imprint's own completion commit lands.
			if isImplements, err := c.isImplementsFileForCommit(commit, file, record.ID); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not check implements relationship: %v\n", err)
			} else if isImplements {
				continue
			}

			// Implemented records are immutable - no further commits may touch
			// them, aside from the exemptions already handled above.
			if implementedBefore {
				violations = append(violations, Violation{
					RecordID: recordID,
					File:     "",
					Commit:   commit,
					Message:  fmt.Sprintf("%s is already implemented - cannot commit with this ID. Create a new record or supersede this one.", recordID),
				})
				continue
			}

			// The record had not yet been sealed before this commit, so its own
			// file may change freely here — draft creation, the open transition,
			// in-progress edits, or the completion transition itself — subject
			// only to forbidden_scope.
			if isRecordFile(file, record) {
				isForbidden, err := isFileForbiddenForRecord(file, record)
				if err != nil {
					return nil, err
				}
				if !isForbidden {
					continue // Allowed - record's own file, not yet sealed
				}
				// If forbidden, fall through to violation
			}

			// Allow supersession transition: old record being marked superseded by record.ID
			// in a historical commit. Mirrors the same exception in CheckStaged.
			if record.Supersedes != "" && record.Supersedes != "null" {
				oldRecordID := strings.TrimSuffix(filepath.Base(file), ".yml")
				if oldRecordID == record.Supersedes {
					isSupersession, err := c.isSupersessionTransitionForCommit(commit, file, record.ID)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Warning: could not check supersession transition: %v\n", err)
					} else if isSupersession {
						continue // Allowed - valid supersession transition of old record
					}
				}
			}

			// Check if file is in scope
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

// CheckStaged checks staged files against provenance records referenced in a commit message
func (c *CommitChecker) CheckStaged(messageFile string, commitTagRequired bool) ([]Violation, error) {
	// Read the commit message
	var message string
	var err error
	if messageFile != "" {
		message, err = c.Git.ReadCommitMessageFile(messageFile)
		if err != nil {
			return nil, err
		}
	} else {
		// Fallback to HEAD commit message if no file provided
		message, err = c.Git.GetCommitMessage("HEAD")
		if err != nil {
			return nil, err
		}
	}

	recordIDs := c.Git.ExtractProvenanceIDs(message)

	// Get staged files before the commit-tag check so we can filter out excluded paths.
	files, err := c.Git.GetStagedFiles()
	if err != nil {
		return nil, err
	}

	// Filter excluded files out of the staged list.
	var nonExcluded []string
	for _, f := range files {
		if !IsPathExcluded(f, c.ExcludePaths) {
			nonExcluded = append(nonExcluded, f)
		}
	}
	files = nonExcluded

	if len(recordIDs) == 0 {
		// No provenance IDs in commit message.
		// If all staged files are excluded the commit-tag requirement is waived.
		if commitTagRequired && len(files) > 0 {
			return []Violation{
				{
					RecordID: "",
					File:     "",
					Commit:   "staged",
					Message:  "Commit tag required but no provenance ID found in message",
				},
			}, nil
		}
		return nil, nil
	}

	if len(files) == 0 {
		// No non-excluded staged files, nothing to check
		return nil, nil
	}

	var violations []Violation

	for _, recordID := range recordIDs {
		record, exists := c.Loader.GetRecord(recordID)
		if !exists {
			// Unknown record ID, skip scope check for this record
			// (This allows new record creation to pass)
			continue
		}

		// Partition staged files: the hash manifest and files belonging to a
		// record whose `implements` field equals this record's ID (e.g. an
		// imprint created while working under this blueprint) are exempt from
		// all scope enforcement. This must happen before the implemented-record
		// guard below, or every completion commit (which stages the manifest
		// alongside the record) and every imprint-creation commit reports a
		// false violation.
		var enforceableFiles []string
		for _, file := range files {
			if isHashManifest(file) {
				continue
			}
			if isImplements, err := c.isImplementsFile(file, record.ID); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not check implements relationship: %v\n", err)
			} else if isImplements {
				continue
			}
			enforceableFiles = append(enforceableFiles, file)
		}

		if len(enforceableFiles) == 0 {
			// Every staged file referencing this record is exempt (hash manifest
			// and/or implements-linked records) - nothing left to enforce, and in
			// particular no reason to demand a completion transition that isn't
			// actually part of this commit.
			continue
		}

		// Check if the record is already implemented
		// Implemented records are immutable - no new commits should reference them
		if record.Status == StatusImplemented {
			// Check if this is the completion transition (open → implemented)
			// which is the only allowed operation on an implemented record's file
			isCompletion := false
			for _, file := range enforceableFiles {
				if isRecordFile(file, record) {
					isComp, err := c.isCompletionTransition(file)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Warning: could not check completion transition: %v\n", err)
					} else if isComp {
						isCompletion = true
						break
					}
				}
			}

			if !isCompletion {
				violations = append(violations, Violation{
					RecordID: recordID,
					File:     "",
					Commit:   "staged",
					Message:  fmt.Sprintf("%s is already implemented - cannot commit with this ID. Create a new record or supersede this one.", recordID),
				})
				continue
			}
		}

		for _, file := range enforceableFiles {
			// Allow draft and open records to modify their own YAML file.
			// Draft records are in active planning — fields (including affected_scope)
			// may be freely adjusted before the record is opened for enforcement.
			if (record.Status == StatusOpen || record.Status == StatusDraft) && isRecordFile(file, record) {
				// Check if the record file itself is explicitly in forbidden_scope
				isForbidden, err := isFileForbiddenForRecord(file, record)
				if err != nil {
					return nil, err
				}
				if !isForbidden {
					continue // Allowed - open record modifying its own file (not forbidden)
				}
				// If forbidden, fall through to violation
			}

			// NEW: Allow completion transition (open → implemented) for the record's own file
			// This handles the case where the record file on disk has been modified by
			// `linespec provenance complete` to status: implemented, but the Loader still
			// reads from disk. We detect the transition by comparing HEAD vs staged.
			if isRecordFile(file, record) {
				// Check if this is a completion transition
				isCompletionTransition, err := c.isCompletionTransition(file)
				if err != nil {
					// If we can't determine, fall through to normal scope check
					fmt.Fprintf(os.Stderr, "Warning: could not check completion transition: %v\n", err)
				} else if isCompletionTransition {
					// This is a completion transition - check if file is not forbidden
					isForbidden, err := isFileForbiddenForRecord(file, record)
					if err != nil {
						return nil, err
					}
					if !isForbidden {
						continue // Allowed - completion transition of record's own file
					}
				}
			}

			// Allow supersession transition: the old record being marked as superseded by record.ID.
			// When `linespec provenance create --supersedes <old-id>` runs, both the new record file
			// and the old record file (updated with superseded_by + status:superseded) are staged
			// together. The commit is tagged with the new record ID, so we allow the old record file
			// when the new record's supersedes field matches it and the transition is valid.
			if record.Supersedes != "" && record.Supersedes != "null" {
				oldRecordID := strings.TrimSuffix(filepath.Base(file), ".yml")
				if oldRecordID == record.Supersedes {
					isSupersession, err := c.isSupersessionTransition(file, record.ID)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Warning: could not check supersession transition: %v\n", err)
					} else if isSupersession {
						continue // Allowed - valid supersession transition of old record
					}
				}
			}

			// Check if file is in scope
			inScope, err := record.IsInScope(file)
			if err != nil {
				return nil, err
			}

			if !inScope {
				violations = append(violations, Violation{
					RecordID: recordID,
					File:     file,
					Commit:   "staged",
					Message:  fmt.Sprintf("%s forbids changes to %s", recordID, file),
				})
			}
		}
	}

	return violations, nil
}

// isCompletionTransition checks if the file is transitioning from open to implemented
// by comparing the HEAD version with the staged version
func (c *CommitChecker) isCompletionTransition(filePath string) (bool, error) {
	return c.isCompletionTransitionBetween("HEAD", "", filePath)
}

// wasRecordImplementedAtRef reports whether record's own YAML file already showed
// status: implemented as of ref (typically "commit^", the parent of the commit
// under audit). This is commit-relative, unlike record.Status (which reflects the
// Loader's current/final view) — CheckCommit/CheckRange use it so a record's
// pre-seal lifecycle commits are judged by what was true about the record at the
// time, not by what it eventually became.
func (c *CommitChecker) wasRecordImplementedAtRef(ref string, record *Record) (bool, error) {
	// record.FilePath is absolute (set by the Loader); git show needs a path
	// relative to the repo root.
	relPath := record.FilePath
	if c.Git.RepoRoot != "" {
		if rel, err := filepath.Rel(c.Git.RepoRoot, record.FilePath); err == nil {
			relPath = rel
		}
	}

	cmd := exec.Command("git", "show", ref+":"+filepath.ToSlash(relPath))
	if c.Git.RepoRoot != "" {
		cmd.Dir = c.Git.RepoRoot
	}
	output, err := cmd.Output()
	if err != nil {
		// File did not exist yet at ref (e.g. this commit creates the record) -
		// it cannot have been implemented.
		return false, nil
	}

	var rec Record
	if err := yaml.Unmarshal(output, &rec); err != nil {
		return false, fmt.Errorf("failed to parse record at %s: %w", ref, err)
	}

	return rec.Status == StatusImplemented, nil
}

// isImplementsFile checks whether the staged version of filePath is a record
// whose `implements` field equals recordID.
func (c *CommitChecker) isImplementsFile(filePath, recordID string) (bool, error) {
	return c.isImplementsFileBetween("", filePath, recordID)
}

// isImplementsFileForCommit checks whether filePath, as committed, is a record
// whose `implements` field equals recordID.
func (c *CommitChecker) isImplementsFileForCommit(commit, filePath, recordID string) (bool, error) {
	return c.isImplementsFileBetween(commit, filePath, recordID)
}

// isImplementsFileBetween checks whether the record YAML at afterRef:filePath
// has an `implements` field equal to recordID. Used to allow a commit tagged
// with a blueprint's ID to create or edit the YAML of a record that implements
// it (e.g. an imprint written while working on the blueprint), without
// requiring that path in the blueprint's affected_scope. For staged
// comparison, pass empty string as afterRef to use ":filepath" syntax.
func (c *CommitChecker) isImplementsFileBetween(afterRef, filePath, recordID string) (bool, error) {
	var afterRefSpec string
	if afterRef == "" {
		afterRefSpec = ":" + filePath
	} else {
		afterRefSpec = afterRef + ":" + filePath
	}

	cmd := exec.Command("git", "show", afterRefSpec)
	if c.Git.RepoRoot != "" {
		cmd.Dir = c.Git.RepoRoot
	}
	output, err := cmd.Output()
	if err != nil {
		// File doesn't exist at afterRef (e.g. deleted) - not an implements file.
		return false, nil
	}

	var candidate Record
	if err := yaml.Unmarshal(output, &candidate); err != nil {
		// Not parseable as a record - not an implements file.
		return false, nil
	}

	return candidate.Implements != "" && candidate.Implements == recordID, nil
}

// isSupersessionTransition checks if the file is transitioning from open to superseded
// with superseded_by == supersedingID, by comparing HEAD vs staged.
func (c *CommitChecker) isSupersessionTransition(filePath, supersedingID string) (bool, error) {
	return c.isSupersessionTransitionBetween("HEAD", "", filePath, supersedingID)
}

// isSupersessionTransitionForCommit checks if the file is transitioning from open to superseded
// by comparing the parent commit with the given commit.
func (c *CommitChecker) isSupersessionTransitionForCommit(commit, filePath, supersedingID string) (bool, error) {
	return c.isSupersessionTransitionBetween(commit+"^", commit, filePath, supersedingID)
}

// isSupersessionTransitionBetween checks if a provenance record file transitions from open
// to superseded with superseded_by == supersedingID between beforeRef and afterRef.
// For staged comparison, pass empty string as afterRef to use ":filepath" syntax.
func (c *CommitChecker) isSupersessionTransitionBetween(beforeRef, afterRef, filePath, supersedingID string) (bool, error) {
	cmd := exec.Command("git", "show", beforeRef+":"+filePath)
	if c.Git.RepoRoot != "" {
		cmd.Dir = c.Git.RepoRoot
	}
	beforeOutput, err := cmd.Output()
	if err != nil {
		return false, nil
	}

	var beforeRecord Record
	if err := yaml.Unmarshal(beforeOutput, &beforeRecord); err != nil {
		return false, fmt.Errorf("failed to parse before record: %w", err)
	}

	if beforeRecord.Status != StatusImplemented {
		return false, nil
	}

	var afterRefSpec string
	if afterRef == "" {
		afterRefSpec = ":" + filePath
	} else {
		afterRefSpec = afterRef + ":" + filePath
	}

	cmd = exec.Command("git", "show", afterRefSpec)
	if c.Git.RepoRoot != "" {
		cmd.Dir = c.Git.RepoRoot
	}
	afterOutput, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to read after file: %w", err)
	}

	var afterRecord Record
	if err := yaml.Unmarshal(afterOutput, &afterRecord); err != nil {
		return false, fmt.Errorf("failed to parse after record: %w", err)
	}

	return afterRecord.Status == StatusSuperseded && afterRecord.SupersededBy == supersedingID, nil
}

// isCompletionTransitionBetween checks if the file is transitioning from open to implemented
// by comparing the beforeRef version with the afterRef version.
// For staged comparison, pass empty string as afterRef to use ":filepath" syntax.
// For commit comparison, pass the commit SHA as afterRef.
func (c *CommitChecker) isCompletionTransitionBetween(beforeRef, afterRef, filePath string) (bool, error) {
	// Read the before version (what was there before)
	cmd := exec.Command("git", "show", beforeRef+":"+filePath)
	if c.Git.RepoRoot != "" {
		cmd.Dir = c.Git.RepoRoot
	}
	beforeOutput, err := cmd.Output()
	if err != nil {
		// If we can't read beforeRef, assume it's a new file (not a completion transition)
		return false, nil
	}

	var beforeRecord Record
	if err := yaml.Unmarshal(beforeOutput, &beforeRecord); err != nil {
		return false, fmt.Errorf("failed to parse before record: %w", err)
	}

	// Read the after version
	var afterRefSpec string
	if afterRef == "" {
		// Staged file: use ":filepath" syntax
		afterRefSpec = ":" + filePath
	} else {
		// Commit: use "commit:filepath" syntax
		afterRefSpec = afterRef + ":" + filePath
	}

	cmd = exec.Command("git", "show", afterRefSpec)
	if c.Git.RepoRoot != "" {
		cmd.Dir = c.Git.RepoRoot
	}
	afterOutput, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to read after file: %w", err)
	}

	var afterRecord Record
	if err := yaml.Unmarshal(afterOutput, &afterRecord); err != nil {
		return false, fmt.Errorf("failed to parse after record: %w", err)
	}

	// Check if this is a completion transition:
	// beforeRef has status: open or draft (both of which `complete` accepts as a
	// sealing source) AND afterRef has status: implemented
	beforeIsSealable := beforeRecord.Status == StatusOpen || beforeRecord.Status == StatusDraft
	return beforeIsSealable && afterRecord.Status == StatusImplemented, nil
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

// StaleScopeWarning represents a warning about files in scope that haven't changed since sealing
type StaleScopeWarning struct {
	RecordID string
	File     string
	Message  string
}

// CheckForStaleScopeWarnings checks implemented records for files in affected_scope
// that haven't actually changed since the record was sealed
func (c *CommitChecker) CheckForStaleScopeWarnings(record *Record, changedFiles []string) []StaleScopeWarning {
	var warnings []StaleScopeWarning

	// Only check implemented records with sealed_at_sha
	if record.Status != StatusImplemented || record.SealedAtSHA == "" {
		return warnings
	}

	// Get files that have actually changed since sealing
	filesChangedSinceSeal, err := c.Git.GetFilesChangedSince(record.SealedAtSHA, "HEAD")
	if err != nil {
		// If we can't determine, assume all files have changed (safer, fewer false positives)
		return warnings
	}

	// Build a set of files that have changed since sealing
	changedSinceSeal := make(map[string]bool)
	for _, f := range filesChangedSinceSeal {
		changedSinceSeal[f] = true
	}

	// For each file being changed in this commit
	for _, changedFile := range changedFiles {
		// Check if this file is in the record's affected_scope
		inScope, err := record.IsInScope(changedFile)
		if err != nil || !inScope {
			continue
		}

		// Check if this specific file has changed since sealing
		if !changedSinceSeal[changedFile] {
			// Wording is sourced from the single engine-owned helper (next.go)
			// so the commit-time warning never drifts from `next` or the skill.
			shortSHA := record.SealedAtSHA[:7]
			warning := HintStaleScope(changedFile, record.ID, shortSHA)
			warnings = append(warnings, StaleScopeWarning{
				RecordID: record.ID,
				File:     changedFile,
				Message:  warning,
			})
		}
	}

	return warnings
}
