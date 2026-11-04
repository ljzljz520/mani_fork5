package dao

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alajmo/mani/core"
)

const (
	TargetKindProject  = "project"
	TargetKindWorktree = "worktree"
)

// PathTarget is a single config entity (a project or one of its
// worktrees) that claims a working directory, together with its
// physical location and the config file it originates from.
type PathTarget struct {
	Kind        string
	ProjectName string
	Worktree    Worktree

	// Path is the configured, lexically cleaned absolute path.
	Path string
	// RealPath is the symlink-resolved working directory. Missing tail
	// components are re-appended after the resolved existing prefix.
	RealPath string
	// OwnerRealPath is the owning project's real path (worktree targets).
	OwnerRealPath string

	// Config provenance.
	Source string
	Line   int
}

func (t PathTarget) toClaim() core.PathClaim {
	claim := core.PathClaim{
		Kind:        t.Kind,
		ProjectName: t.ProjectName,
		Path:        t.Path,
		Source:      t.Source,
		Line:        t.Line,
	}
	if t.Kind == TargetKindWorktree {
		claim.WorktreePath = t.Worktree.Path
		claim.Branch = t.Worktree.Branch
	}

	return claim
}

// BuildPathTargets expands projects (and optionally their worktrees)
// into path targets, resolving each claimed directory to its physical
// location while retaining the config source.
//
// baseDir resolves relative project paths; production configs already
// carry absolute project paths, but manually built configs may be
// relative.
func BuildPathTargets(baseDir string, projects []Project, includeWorktrees bool) ([]PathTarget, error) {
	targets := []PathTarget{}

	for _, project := range projects {
		projectAbs, err := core.GetAbsolutePath(baseDir, project.Path, project.Name)
		if err != nil {
			return nil, err
		}
		projectAbs = filepath.Clean(projectAbs)

		projectReal, err := core.ResolveRealPath(projectAbs)
		if err != nil {
			return nil, err
		}

		targets = append(targets, PathTarget{
			Kind:        TargetKindProject,
			ProjectName: project.Name,
			Path:        projectAbs,
			RealPath:    projectReal,
			Source:      project.GetContext(),
			Line:        project.GetContextLine(),
		})

		if !includeWorktrees {
			continue
		}

		for _, wt := range project.WorktreeList {
			var wtAbs string
			if filepath.IsAbs(wt.Path) {
				wtAbs = filepath.Clean(wt.Path)
			} else {
				wtAbs = filepath.Join(projectAbs, wt.Path)
			}

			wtReal, err := core.ResolveRealPath(wtAbs)
			if err != nil {
				return nil, err
			}

			targets = append(targets, PathTarget{
				Kind:          TargetKindWorktree,
				ProjectName:   project.Name,
				Worktree:      wt,
				Path:          wtAbs,
				RealPath:      wtReal,
				OwnerRealPath: projectReal,
				Source:        wt.GetContext(),
				Line:          wt.GetContextLine(),
			})
		}
	}

	return targets, nil
}

// DetectWorkingDirConflicts groups targets by their resolved working
// directory and flags every directory claimed by two or more targets.
//
// A project and its linked worktrees intentionally share the same Git
// common/management directory but check out different working
// directories, which is legal; identity is therefore the physical
// working directory only. Nested directories (a project inside another
// project's directory) are also legal since they resolve differently.
func DetectWorkingDirConflicts(targets []PathTarget) []core.PathConflict {
	groups := make(map[string][]PathTarget)
	order := []string{}
	for _, target := range targets {
		if _, ok := groups[target.RealPath]; !ok {
			order = append(order, target.RealPath)
		}
		groups[target.RealPath] = append(groups[target.RealPath], target)
	}

	conflicts := []core.PathConflict{}
	for _, realPath := range order {
		group := groups[realPath]
		if len(group) < 2 {
			continue
		}

		claims := make([]core.PathClaim, len(group))
		for i, target := range group {
			claims[i] = target.toClaim()
		}

		conflicts = append(conflicts, core.PathConflict{
			RealPath: realPath,
			Reason:   core.PathConflictDuplicate,
			Message: fmt.Sprintf(
				"%d config targets resolve to the same working directory",
				len(group),
			),
			Claims: claims,
		})
	}

	return conflicts
}

// readWorktreePointer parses a linked worktree's .git file.
//
// Returns:
//   - adminDir: administrative directory the "gitdir:" pointer targets
//   - standalone: true when .git is a directory (standalone repository)
//   - problem: non-empty when the .git entry is missing or malformed
func readWorktreePointer(wtReal string) (adminDir string, standalone bool, problem string) {
	gitPath := filepath.Join(wtReal, ".git")

	info, err := os.Stat(gitPath)
	if err != nil {
		return "", false, "" // no .git entry: not a checkout
	}

	if info.IsDir() {
		return "", true, "" // standalone repository
	}

	content, err := os.ReadFile(gitPath)
	if err != nil {
		return "", false, "unreadable .git file"
	}

	pointer, found := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir: ")
	if !found {
		return "", false, "malformed .git pointer (expected `gitdir: <path>`)"
	}

	if !filepath.IsAbs(pointer) {
		pointer = filepath.Join(wtReal, pointer)
	}

	return filepath.Clean(pointer), false, ""
}

// VerifyLinkedWorktree verifies that an existing directory wtReal is a
// linked git worktree whose administrative metadata belongs to the
// owner repository at ownerReal. Returns an empty reason when the
// ownership is valid.
//
// Linked worktree on-disk layout:
//
//	<wt>/.git                                  file: "gitdir: <owner>/.git/worktrees/<name>"
//	<owner>/.git/worktrees/<name>/gitdir       back-reference to <wt>/.git
//	<owner>/.git/worktrees/<name>/commondir    (optional) -> <owner>/.git
func VerifyLinkedWorktree(ownerReal string, wtReal string) (reason string, detail string) {
	adminDir, standalone, problem := readWorktreePointer(wtReal)
	if problem != "" {
		return core.PathConflictInvalidWorktree, problem
	}
	if standalone {
		return core.PathConflictForeignRepository, "`.git` is a directory (standalone repository)"
	}
	if adminDir == "" {
		return core.PathConflictNotCheckout, "no `.git` entry found"
	}

	adminReal, err := core.ResolveRealPath(adminDir)
	if err != nil {
		return core.PathConflictInvalidWorktree, fmt.Sprintf("cannot resolve worktree metadata `%s`", adminDir)
	}

	// The administrative directory must live below <owner>/.git/worktrees.
	worktreesRoot := filepath.Join(ownerReal, ".git", "worktrees")
	rel, err := filepath.Rel(worktreesRoot, adminReal)
	outsideRoot := err != nil ||
		rel == "." ||
		rel == ".." ||
		filepath.IsAbs(rel) ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator))
	if outsideRoot {
		ownerGitDir := filepath.Join(ownerReal, ".git")
		return core.PathConflictForeignWorktree, fmt.Sprintf(
			"worktree metadata `%s` does not belong to `%s`",
			adminReal,
			ownerGitDir,
		)
	}

	// Back-reference: <adminDir>/gitdir must point at <wt>/.git.
	wantGitFile, err := core.ResolveRealPath(filepath.Join(wtReal, ".git"))
	if err != nil {
		return core.PathConflictInvalidWorktree, err.Error()
	}

	backRefRaw, err := os.ReadFile(filepath.Join(adminReal, "gitdir"))
	if err != nil {
		return core.PathConflictInvalidWorktree, "worktree metadata is missing the `gitdir` back-reference"
	}

	backRefPath := strings.TrimSpace(string(backRefRaw))
	if !filepath.IsAbs(backRefPath) {
		backRefPath = filepath.Join(adminReal, backRefPath)
	}

	backRefReal, err := core.ResolveRealPath(backRefPath)
	if err != nil {
		return core.PathConflictInvalidWorktree, fmt.Sprintf("cannot resolve back-reference `%s`", backRefPath)
	}

	if backRefReal != wantGitFile {
		return core.PathConflictInvalidWorktree, fmt.Sprintf(
			"back-reference points to `%s`, expected `%s`",
			backRefReal,
			wantGitFile,
		)
	}

	// Commondir (optional): must resolve to the owner's .git directory.
	commonRaw, err := os.ReadFile(filepath.Join(adminReal, "commondir"))
	if err == nil {
		commonPath := strings.TrimSpace(string(commonRaw))
		if !filepath.IsAbs(commonPath) {
			commonPath = filepath.Join(adminReal, commonPath)
		}

		commonReal, err := core.ResolveRealPath(commonPath)
		if err != nil {
			return core.PathConflictInvalidWorktree, fmt.Sprintf("cannot resolve common directory `%s`", commonPath)
		}

		wantCommon, err := core.ResolveRealPath(filepath.Join(ownerReal, ".git"))
		if err != nil {
			return core.PathConflictInvalidWorktree, err.Error()
		}

		if commonReal != wantCommon {
			return core.PathConflictForeignWorktree, fmt.Sprintf(
				"common directory is `%s`, expected `%s`",
				commonReal,
				wantCommon,
			)
		}
	}

	return "", ""
}

// VerifyExistingWorktrees checks every existing worktree target on disk.
// A directory merely existing is not proof of ownership: it must be a
// linked worktree of the owning project. Directories that do not exist
// yet are left alone (sync is expected to create them).
func VerifyExistingWorktrees(targets []PathTarget) []core.PathConflict {
	conflicts := []core.PathConflict{}

	for _, target := range targets {
		if target.Kind != TargetKindWorktree {
			continue
		}

		if _, err := os.Stat(target.RealPath); err != nil {
			continue // not created yet
		}

		reason, detail := VerifyLinkedWorktree(target.OwnerRealPath, target.RealPath)
		if reason == "" {
			continue
		}

		var message string
		switch reason {
		case core.PathConflictForeignRepository:
			message = fmt.Sprintf(
				"existing worktree directory of project `%s` is a standalone git repository, not a linked worktree of `%s`",
				target.ProjectName,
				target.OwnerRealPath,
			)
		case core.PathConflictForeignWorktree:
			message = fmt.Sprintf(
				"existing worktree directory of project `%s` belongs to another repository, not `%s`",
				target.ProjectName,
				target.OwnerRealPath,
			)
		case core.PathConflictNotCheckout:
			message = fmt.Sprintf(
				"existing worktree directory of project `%s` is not a git checkout, cannot claim it for `%s`",
				target.ProjectName,
				target.OwnerRealPath,
			)
		default:
			message = fmt.Sprintf(
				"existing worktree directory of project `%s` has invalid worktree metadata, not a linked worktree of `%s`",
				target.ProjectName,
				target.OwnerRealPath,
			)
		}
		if detail != "" {
			message += " (" + detail + ")"
		}

		conflicts = append(conflicts, core.PathConflict{
			RealPath: target.RealPath,
			Reason:   reason,
			Message:  message,
			Claims:   []core.PathClaim{target.toClaim()},
		})
	}

	return conflicts
}

// CheckPathOwnership is the shared path ownership preflight used by
// run, exec, TUI and sync. Working-directory identity is always
// determined via ResolveRealPath; worktree claims and on-disk ownership
// verification are additionally enabled for sync.
//
// All conflicts are returned in a single error so callers report them
// before performing any mutation.
func CheckPathOwnership(
	baseDir string,
	projects []Project,
	includeWorktrees bool,
	verifyOnDisk bool,
) error {
	targets, err := BuildPathTargets(baseDir, projects, includeWorktrees)
	if err != nil {
		return err
	}

	conflicts := DetectWorkingDirConflicts(targets)
	if verifyOnDisk {
		conflicts = append(conflicts, VerifyExistingWorktrees(targets)...)
	}

	if len(conflicts) > 0 {
		return &core.PathConflictError{Conflicts: conflicts}
	}

	return nil
}
