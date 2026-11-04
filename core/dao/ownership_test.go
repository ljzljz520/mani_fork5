package dao

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alajmo/mani/core"
)

// --- on-disk worktree metadata helpers (hermetic, no git binary) ---

// writeLinkedWorktree lays out the on-disk metadata git uses for a linked
// worktree:
//
//	<wt>/.git                              -> "gitdir: <owner>/.git/worktrees/<name>"
//	<owner>/.git/worktrees/<name>/gitdir   -> back-reference
//	<owner>/.git/worktrees/<name>/commondir-> "../../"
func writeLinkedWorktree(t *testing.T, owner, wt, name string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(owner, ".git", "worktrees", name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}

	adminDir := filepath.Join(owner, ".git", "worktrees", name)

	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+adminDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "gitdir"), []byte(filepath.Join(wt, ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adminDir, "commondir"), []byte("../../\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeStandaloneRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func real(t *testing.T, path string) string {
	t.Helper()
	r, err := core.ResolveRealPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// --- ResolveRealPath ---

func TestResolveRealPath_LexicalAliases(t *testing.T) {
	base := t.TempDir()

	candidates := []string{
		filepath.Join(base, "shared"),
		filepath.Join(base, "shared") + string(filepath.Separator),
		filepath.Join(base, ".", "shared"),
		filepath.Join(base, "nested", "..", "shared"),
		filepath.Join(base, "shared", "sub", ".."),
	}

	want := filepath.Join(real(t, base), "shared")
	for _, candidate := range candidates {
		got, err := core.ResolveRealPath(candidate)
		if err != nil {
			t.Fatalf("ResolveRealPath(%q): %v", candidate, err)
		}
		if got != want {
			t.Fatalf("ResolveRealPath(%q) = %q, want %q", candidate, got, want)
		}
	}
}

func TestResolveRealPath_SymlinkedExistingParent(t *testing.T) {
	base := t.TempDir()

	physical := filepath.Join(base, "workspace")
	if err := os.MkdirAll(physical, 0o755); err != nil {
		t.Fatal(err)
	}

	alias := filepath.Join(base, "link")
	if err := os.Symlink(physical, alias); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	// Target directory does not exist yet, the symlinked parent does.
	got, err := core.ResolveRealPath(filepath.Join(alias, "project-x"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := core.ResolveRealPath(filepath.Join(physical, "project-x"))
	if err != nil {
		t.Fatal(err)
	}

	if got != want {
		t.Fatalf("symlinked parent not resolved: %q != %q", got, want)
	}
}

// --- BuildPathTargets + DetectWorkingDirConflicts ---

func TestBuildPathTargets_ProvenanceAndRealPaths(t *testing.T) {
	base := t.TempDir()

	projects := []Project{
		{
			Name:        "alpha",
			Path:        filepath.Join(base, "alpha"),
			context:     filepath.Join(base, "main.yaml"),
			contextLine: 3,
			WorktreeList: []Worktree{
				{Path: "../alpha-wt", Branch: "feat", context: filepath.Join(base, "main.yaml"), contextLine: 6},
			},
		},
	}

	targets, err := BuildPathTargets(base, projects, true)
	if err != nil {
		t.Fatal(err)
	}

	if len(targets) != 2 {
		t.Fatalf("expected 2 targets (project + worktree), got %d", len(targets))
	}

	wt := targets[1]
	if wt.Kind != TargetKindWorktree {
		t.Fatalf("expected worktree target, got %q", wt.Kind)
	}
	if wt.OwnerRealPath != targets[0].RealPath {
		t.Fatalf("worktree owner %q != project real path %q", wt.OwnerRealPath, targets[0].RealPath)
	}
	if wt.Source != filepath.Join(base, "main.yaml") || wt.Line != 6 {
		t.Fatalf("worktree provenance lost: %s:%d", wt.Source, wt.Line)
	}
	if wt.RealPath != filepath.Join(real(t, base), "alpha-wt") {
		t.Fatalf("worktree path resolved to %q", wt.RealPath)
	}
}

func TestDetectWorkingDirConflicts_NestedDirsAreLegal(t *testing.T) {
	base := t.TempDir()

	projects := []Project{
		{Name: "root", Path: base},
		{Name: "frontend", Path: filepath.Join(base, "frontend")},
		{Name: "app", Path: filepath.Join(base, "frontend", "app")},
	}

	targets, err := BuildPathTargets(base, projects, false)
	if err != nil {
		t.Fatal(err)
	}

	if conflicts := DetectWorkingDirConflicts(targets); len(conflicts) != 0 {
		t.Fatalf("nested directories must be legal, got %d conflicts", len(conflicts))
	}
}

func TestDetectWorkingDirConflicts_LexicalPathAliases(t *testing.T) {
	base := t.TempDir()

	projects := []Project{
		{Name: "a", Path: filepath.Join(base, "repo"), context: filepath.Join(base, "a.yaml"), contextLine: 1},
		{Name: "b", Path: filepath.Join(base, "repo", "..", "repo"), context: filepath.Join(base, "b.yaml"), contextLine: 2},
		{Name: "c", Path: filepath.Join(base, "other"), context: filepath.Join(base, "c.yaml"), contextLine: 3},
	}

	targets, err := BuildPathTargets(base, projects, false)
	if err != nil {
		t.Fatal(err)
	}

	conflicts := DetectWorkingDirConflicts(targets)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}

	if len(conflicts[0].Claims) != 2 {
		t.Fatalf("expected 2 claims, got %d", len(conflicts[0].Claims))
	}

	sources := map[string]bool{}
	for _, claim := range conflicts[0].Claims {
		sources[claim.Source] = true
	}
	if !sources[filepath.Join(base, "a.yaml")] || !sources[filepath.Join(base, "b.yaml")] {
		t.Fatalf("conflict must preserve both config sources: %+v", conflicts[0].Claims)
	}
}

func TestDetectWorkingDirConflicts_SymlinkAlias(t *testing.T) {
	base := t.TempDir()

	physical := filepath.Join(base, "ws")
	if err := os.MkdirAll(physical, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physical, filepath.Join(base, "link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	// Both targets are not cloned yet: only the symlinked parent exists.
	projects := []Project{
		{Name: "a", Path: filepath.Join(physical, "p")},
		{Name: "b", Path: filepath.Join(base, "link", "p")},
	}

	targets, err := BuildPathTargets(base, projects, false)
	if err != nil {
		t.Fatal(err)
	}

	if conflicts := DetectWorkingDirConflicts(targets); len(conflicts) != 1 {
		t.Fatalf("symlink aliases must collide, got %d conflicts", len(conflicts))
	}
}

func TestDetectWorkingDirConflicts_WorktreeVsProject(t *testing.T) {
	base := t.TempDir()

	projects := []Project{
		{
			Name:        "owner",
			Path:        filepath.Join(base, "repo"),
			context:     filepath.Join(base, "main.yaml"),
			contextLine: 1,
			WorktreeList: []Worktree{
				{Path: "../shared", Branch: "feat"},
			},
		},
		{Name: "other", Path: filepath.Join(base, "shared"), context: filepath.Join(base, "other.yaml"), contextLine: 8},
	}

	targets, err := BuildPathTargets(base, projects, true)
	if err != nil {
		t.Fatal(err)
	}

	conflicts := DetectWorkingDirConflicts(targets)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}

	kinds := map[string]bool{}
	for _, claim := range conflicts[0].Claims {
		kinds[claim.Kind] = true
	}
	if !kinds[TargetKindProject] || !kinds[TargetKindWorktree] {
		t.Fatalf("expected project + worktree claims, got %+v", kinds)
	}
}

func TestDetectWorkingDirConflicts_WorktreePathEqualsOwner(t *testing.T) {
	base := t.TempDir()

	projects := []Project{
		{
			Name:    "owner",
			Path:    filepath.Join(base, "repo"),
			context: filepath.Join(base, "main.yaml"),
			WorktreeList: []Worktree{
				{Path: ".", Branch: "feat"},
			},
		},
	}

	targets, err := BuildPathTargets(base, projects, true)
	if err != nil {
		t.Fatal(err)
	}

	if conflicts := DetectWorkingDirConflicts(targets); len(conflicts) != 1 {
		t.Fatalf("worktree claiming the project dir itself must conflict, got %d", len(conflicts))
	}
}

func TestDetectWorkingDirConflicts_LegitWorktreeSiblingsShareCommonDir(t *testing.T) {
	base := t.TempDir()

	projects := []Project{
		{
			Name:    "owner",
			Path:    filepath.Join(base, "repo"),
			context: filepath.Join(base, "main.yaml"),
			WorktreeList: []Worktree{
				{Path: "../repo-wt-a", Branch: "feat-a"},
				{Path: "../repo-wt-b", Branch: "feat-b"},
			},
		},
	}

	targets, err := BuildPathTargets(base, projects, true)
	if err != nil {
		t.Fatal(err)
	}

	// Different working directories sharing the same git common dir: legal.
	if conflicts := DetectWorkingDirConflicts(targets); len(conflicts) != 0 {
		t.Fatalf("legit linked worktrees must not conflict, got %d", len(conflicts))
	}
}

// --- existing worktree directory ownership ---

func TestVerifyExistingWorktrees_ValidLinkedWorktree(t *testing.T) {
	base := real(t, t.TempDir())
	owner := filepath.Join(base, "repo")
	wt := filepath.Join(base, "repo-wt")
	writeLinkedWorktree(t, owner, wt, "feat")

	targets := []PathTarget{{
		Kind:          TargetKindWorktree,
		ProjectName:   "owner",
		RealPath:      wt,
		OwnerRealPath: owner,
	}}

	if conflicts := VerifyExistingWorktrees(targets); len(conflicts) != 0 {
		t.Fatalf("valid linked worktree must pass ownership check: %+v", conflicts)
	}

	// Direct verifier: empty reason means valid.
	if reason, _ := VerifyLinkedWorktree(owner, wt); reason != "" {
		t.Fatalf("expected valid ownership, got %s", reason)
	}
}

func TestVerifyExistingWorktrees_MissingDirectoryIsSkipped(t *testing.T) {
	base := real(t, t.TempDir())

	targets := []PathTarget{{
		Kind:          TargetKindWorktree,
		ProjectName:   "owner",
		RealPath:      filepath.Join(base, "not-created-yet"),
		OwnerRealPath: filepath.Join(base, "repo"),
	}}

	if conflicts := VerifyExistingWorktrees(targets); len(conflicts) != 0 {
		t.Fatalf("missing worktree dirs are created later by sync, got %+v", conflicts)
	}
}

func TestVerifyExistingWorktrees_StandaloneRepository(t *testing.T) {
	base := real(t, t.TempDir())
	owner := filepath.Join(base, "repo")
	foreign := filepath.Join(base, "repo-wt")
	writeStandaloneRepo(t, foreign)

	targets := []PathTarget{{
		Kind:          TargetKindWorktree,
		ProjectName:   "owner",
		RealPath:      foreign,
		OwnerRealPath: owner,
	}}

	conflicts := VerifyExistingWorktrees(targets)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Reason != core.PathConflictForeignRepository {
		t.Fatalf("expected foreign-repository, got %q", conflicts[0].Reason)
	}
}

func TestVerifyExistingWorktrees_WorktreeOfAnotherRepository(t *testing.T) {
	base := real(t, t.TempDir())
	ownerA := filepath.Join(base, "repo-a")
	ownerB := filepath.Join(base, "repo-b")
	wt := filepath.Join(base, "shared-wt")

	// On disk it is a valid linked worktree, but of repo-b, not repo-a.
	writeLinkedWorktree(t, ownerB, wt, "feat")

	targets := []PathTarget{{
		Kind:          TargetKindWorktree,
		ProjectName:   "owner-a",
		RealPath:      wt,
		OwnerRealPath: ownerA,
	}}

	conflicts := VerifyExistingWorktrees(targets)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Reason != core.PathConflictForeignWorktree {
		t.Fatalf("expected foreign-worktree, got %q", conflicts[0].Reason)
	}
}

func TestVerifyExistingWorktrees_NotACheckout(t *testing.T) {
	base := real(t, t.TempDir())
	owner := filepath.Join(base, "repo")
	plain := filepath.Join(base, "repo-wt")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, "random.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	targets := []PathTarget{{
		Kind:          TargetKindWorktree,
		ProjectName:   "owner",
		RealPath:      plain,
		OwnerRealPath: owner,
	}}

	conflicts := VerifyExistingWorktrees(targets)
	if len(conflicts) != 1 || conflicts[0].Reason != core.PathConflictNotCheckout {
		t.Fatalf("expected not-a-checkout conflict, got %+v", conflicts)
	}
}

func TestVerifyExistingWorktrees_BrokenBackReference(t *testing.T) {
	base := real(t, t.TempDir())
	owner := filepath.Join(base, "repo")
	wt := filepath.Join(base, "repo-wt")
	writeLinkedWorktree(t, owner, wt, "feat")

	// Corrupt the back-reference.
	adminDir := filepath.Join(owner, ".git", "worktrees", "feat")
	if err := os.WriteFile(filepath.Join(adminDir, "gitdir"), []byte(filepath.Join(base, "somewhere-else", ".git")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reason, _ := VerifyLinkedWorktree(owner, wt)
	if reason != core.PathConflictInvalidWorktree {
		t.Fatalf("expected invalid-worktree-pointer, got %q", reason)
	}
}

// --- imported config provenance ---

func TestCheckPathOwnership_ImportedConfigPreservesSources(t *testing.T) {
	dir := t.TempDir()

	mainYAML := `import:
  - ext.yaml

projects:
  alpha:
    path: shared
    worktrees:
      - path: w
        branch: feat
`
	extYAML := `projects:
  beta:
    path: ./shared/
`
	if err := os.WriteFile(filepath.Join(dir, "mani.yaml"), []byte(mainYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ext.yaml"), []byte(extYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	config, err := ReadConfig(filepath.Join(dir, "mani.yaml"), "", false)
	if err != nil {
		t.Fatal(err)
	}

	alpha, err := config.GetProject("alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := config.GetProject("beta")
	if err != nil {
		t.Fatal(err)
	}

	// Each target remembers its own config file.
	if alpha.GetContext() != filepath.Join(dir, "mani.yaml") {
		t.Fatalf("alpha source = %q", alpha.GetContext())
	}
	if beta.GetContext() != filepath.Join(dir, "ext.yaml") {
		t.Fatalf("beta source = %q", beta.GetContext())
	}
	if len(alpha.WorktreeList) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(alpha.WorktreeList))
	}
	if alpha.WorktreeList[0].GetContext() != filepath.Join(dir, "mani.yaml") ||
		alpha.WorktreeList[0].GetContextLine() <= 0 {
		t.Fatalf("worktree provenance missing: %s:%d",
			alpha.WorktreeList[0].GetContext(),
			alpha.WorktreeList[0].GetContextLine(),
		)
	}

	// Both imported entities resolve to the same future directory.
	err = CheckPathOwnership(dir, config.ProjectList, false, false)
	if err == nil {
		t.Fatal("expected conflict between imported config entities")
	}

	conflictErr, ok := err.(*core.PathConflictError)
	if !ok {
		t.Fatalf("expected PathConflictError, got %T", err)
	}
	if len(conflictErr.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflictErr.Conflicts))
	}

	sources := map[string]bool{}
	for _, claim := range conflictErr.Conflicts[0].Claims {
		sources[claim.Source] = true
		if claim.Line <= 0 {
			t.Fatalf("claim missing config line: %+v", claim)
		}
	}
	if !sources[filepath.Join(dir, "mani.yaml")] || !sources[filepath.Join(dir, "ext.yaml")] {
		t.Fatalf("conflict must cite both config files: %+v", conflictErr.Conflicts[0].Claims)
	}
}

// --- target replacement / final filtered selection ---

func TestCheckPathOwnership_FollowsFinalProjectSelection(t *testing.T) {
	base := t.TempDir()

	config := Config{
		Dir: base,
		ProjectList: []Project{
			{Name: "alpha", Path: filepath.Join(base, "shared"), context: "a.yaml"},
			{Name: "beta", Path: filepath.Join(base, "shared"), context: "b.yaml"},
			{Name: "gamma", Path: filepath.Join(base, "gamma"), context: "c.yaml"},
		},
	}

	// Only one of the conflicting targets selected (e.g. after a target
	// gets replaced by runtime flags): nothing executes twice.
	selected, err := config.FilterProjects(false, false, []string{"alpha"}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPathOwnership(base, selected, false, false); err != nil {
		t.Fatalf("single-target selection must pass: %v", err)
	}

	// Both conflicting targets selected: preflight refuses.
	selected, err = config.FilterProjects(false, false, []string{"alpha", "beta"}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPathOwnership(base, selected, false, false); err == nil {
		t.Fatal("expected conflict when both aliases are selected")
	}
}

// --- end-to-end check combining duplicate detection and disk verification ---

func TestCheckPathOwnership_ReportsAllConflictsBeforeMutation(t *testing.T) {
	base := real(t, t.TempDir())

	foreign := filepath.Join(base, "foreign-wt")
	writeStandaloneRepo(t, foreign)

	projects := []Project{
		{
			Name:    "a",
			Path:    filepath.Join(base, "dup"),
			context: filepath.Join(base, "mani.yaml"),
			WorktreeList: []Worktree{
				{Path: "../foreign-wt", Branch: "feat", context: filepath.Join(base, "mani.yaml")},
			},
		},
		{Name: "b", Path: filepath.Join(base, "dup"), context: filepath.Join(base, "ext.yaml")},
	}

	err := CheckPathOwnership(base, projects, true, true)
	if err == nil {
		t.Fatal("expected conflicts")
	}

	conflictErr, ok := err.(*core.PathConflictError)
	if !ok {
		t.Fatalf("expected PathConflictError, got %T", err)
	}

	// Both the duplicate working dir and the foreign worktree must be
	// reported together so sync can surface everything before mutating.
	reasons := map[string]bool{}
	for _, c := range conflictErr.Conflicts {
		reasons[c.Reason] = true
	}
	if !reasons[core.PathConflictDuplicate] || !reasons[core.PathConflictForeignRepository] {
		t.Fatalf("expected duplicate + foreign-repository conflicts, got %+v", reasons)
	}
}
