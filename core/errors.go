package core

import (
	"fmt"
	"os"
	"strings"

	"github.com/gookit/color"
)

type ConfigEnvFailed struct {
	Name string
	Err  string
}

func (c *ConfigEnvFailed) Error() string {
	return fmt.Sprintf("failed to evaluate env `%s` \n  %s", c.Name, c.Err)
}

type AlreadyManiDirectory struct {
	Dir string
}

func (c *AlreadyManiDirectory) Error() string {
	return fmt.Sprintf("`%s` is already a mani directory\n", c.Dir)
}

type ZeroNotAllowed struct {
	Name string
}

func (c *ZeroNotAllowed) Error() string {
	return fmt.Sprintf("invalid value for %s, cannot be 0", c.Name)
}

type FailedToOpenFile struct {
	Name string
}

func (f *FailedToOpenFile) Error() string {
	return fmt.Sprintf("failed to open `%s`", f.Name)
}

type FailedToParsePath struct {
	Name string
}

func (f *FailedToParsePath) Error() string {
	return fmt.Sprintf("failed to parse path `%s`", f.Name)
}

type PathDoesNotExist struct {
	Path string
}

func (p *PathDoesNotExist) Error() string {
	return fmt.Sprintf("path `%s` does not exist", p.Path)
}

type TagNotFound struct {
	Tags []string
}

func (c *TagNotFound) Error() string {
	tags := "`" + strings.Join(c.Tags, "`, `") + "`"
	return fmt.Sprintf("cannot find tags %s", tags)
}

type DirNotFound struct {
	Dirs []string
}

func (c *DirNotFound) Error() string {
	dirs := "`" + strings.Join(c.Dirs, "`, `") + "`"
	return fmt.Sprintf("cannot find paths %s", dirs)
}

type NoTargets struct{}

func (c *NoTargets) Error() string {
	return "no matching projects found"
}

type ProjectNotFound struct {
	Name []string
}

func (c *ProjectNotFound) Error() string {
	projects := "`" + strings.Join(c.Name, "`, `") + "`"
	return fmt.Sprintf("cannot find projects %s", projects)
}

type TaskNotFound struct {
	Name []string
}

func (c *TaskNotFound) Error() string {
	tasks := "`" + strings.Join(c.Name, "`, `") + "`"
	return fmt.Sprintf("cannot find tasks %s", tasks)
}

type ThemeNotFound struct {
	Name string
}

func (c *ThemeNotFound) Error() string {
	return fmt.Sprintf("cannot find theme `%s`", c.Name)
}

type SpecNotFound struct {
	Name string
}

func (c *SpecNotFound) Error() string {
	return fmt.Sprintf("cannot find spec `%s`", c.Name)
}

type SpecOutputError struct {
	Name   string
	Output string
}

func (c *SpecOutputError) Error() string {
	return fmt.Sprintf("invalid output for spec `%s`, found `%s`, expected one of: stream, table, html, markdown", c.Name, c.Output)
}

type TargetNotFound struct {
	Name string
}

func (c *TargetNotFound) Error() string {
	return fmt.Sprintf("cannot find target `%s`", c.Name)
}

type TargetTagsExprError struct {
	Name string
	Err  error
}

func (c *TargetTagsExprError) Error() string {
	return fmt.Sprintf("invalid tags_expr for target `%s`, %s", c.Name, c.Err.Error())
}

type TagExprInvalid struct {
	Expression string
}

func (c *TagExprInvalid) Error() string {
	return fmt.Sprintf("invalid tags expression: %s", c.Expression)
}

type ConfigNotFound struct {
	Names []string
}

func (f *ConfigNotFound) Error() string {
	return fmt.Sprintf("cannot find any configuration file %v in current directory or any of the parent directories", f.Names)
}

type WorktreePathRequired struct{}

func (c *WorktreePathRequired) Error() string {
	return "worktree path is required"
}

type FailedToCreateWorktree struct {
	Path   string
	Output string
	Err    error
}

func (c *FailedToCreateWorktree) Error() string {
	return fmt.Sprintf("failed to create worktree `%s`: %s - %s", c.Path, c.Err, c.Output)
}

type FailedToRemoveWorktree struct {
	Path   string
	Output string
	Err    error
}

func (c *FailedToRemoveWorktree) Error() string {
	return fmt.Sprintf("failed to remove worktree `%s`: %s - %s", c.Path, c.Err, c.Output)
}

type ConfigErr struct {
	Msg string
}

func (f *ConfigErr) Error() string {
	return f.Msg
}

// Path ownership preflight conflict reasons.
const (
	// Two or more config targets resolve to the same working directory.
	PathConflictDuplicate = "duplicate-working-dir"
	// An existing worktree directory contains a standalone .git directory
	// instead of a linked worktree pointer.
	PathConflictForeignRepository = "foreign-repository"
	// An existing worktree directory is a linked worktree of another
	// repository (its administrative dir does not belong to the owner).
	PathConflictForeignWorktree = "foreign-worktree"
	// An existing worktree directory is not a git checkout at all.
	PathConflictNotCheckout = "not-a-checkout"
	// A worktree .git pointer or its administrative metadata is malformed
	// or points somewhere else.
	PathConflictInvalidWorktree = "invalid-worktree-pointer"
)

// PathClaim describes a single config entity that claims a working
// directory (a project or one of its worktrees) along with the config
// file it originates from.
type PathClaim struct {
	Kind string // "project" | "worktree"

	ProjectName  string
	WorktreePath string
	Branch       string

	// Configured (lexically cleaned, not symlink-resolved) absolute path.
	Path string

	// Config file the entity originates from (provenance).
	Source string
	Line   int
}

// PathConflict describes a single path ownership violation. Claims always
// contains every config entity involved so the full set of conflicts can
// be reported at once.
type PathConflict struct {
	// RealPath is the symlink-resolved working directory.
	RealPath string

	Reason  string
	Message string
	Claims  []PathClaim
}

type PathConflictError struct {
	Conflicts []PathConflict
}

func formatPathClaim(c PathClaim) string {
	if c.Kind == "worktree" {
		return fmt.Sprintf(
			"worktree `%s` (branch `%s`) of project `%s` resolves to `%s`",
			c.WorktreePath,
			c.Branch,
			c.ProjectName,
			c.Path,
		)
	}

	return fmt.Sprintf("project `%s` resolves to `%s`", c.ProjectName, c.Path)
}

func (e *PathConflictError) Error() string {
	var b strings.Builder

	errPrefix := color.FgRed.Sprintf("error")
	ptrPrefix := color.FgBlue.Sprintf("-->")

	for i, conflict := range e.Conflicts {
		if i > 0 {
			b.WriteString("\n")
		}

		if conflict.Reason == PathConflictDuplicate {
			fmt.Fprintf(&b, "%s: %s `%s`\n", errPrefix, conflict.Message, conflict.RealPath)
		} else {
			fmt.Fprintf(&b, "%s: %s\n", errPrefix, conflict.Message)
			if conflict.RealPath != "" {
				fmt.Fprintf(&b, "           working directory `%s`\n", conflict.RealPath)
			}
		}

		for _, claim := range conflict.Claims {
			if claim.Source != "" {
				if claim.Line > 0 {
					fmt.Fprintf(&b, "  %s %s:%d\n", ptrPrefix, claim.Source, claim.Line)
				} else {
					fmt.Fprintf(&b, "  %s %s\n", ptrPrefix, claim.Source)
				}
			}

			fmt.Fprintf(&b, "    %s\n", formatPathClaim(claim))
		}
	}

	return b.String()
}

func CheckIfError(err error) {
	if err != nil {
		switch err.(type) {
		case *ConfigErr, *PathConflictError:
			// Errors are already mapped with `error:` prefix
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "%s: %v\n", color.FgRed.Sprintf("error"), err)
			os.Exit(1)
		}
	}
}

func Exit(err error) {
	switch err := err.(type) {
	case *ConfigErr, *PathConflictError:
		// Errors are already mapped with `error:` prefix
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "%s: %v\n", color.FgRed.Sprintf("error"), err)
		os.Exit(1)
	}
}
