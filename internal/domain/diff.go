package domain

// ChangeKind is what a job's diff did to one file.
type ChangeKind string

// The kinds of change the gate tells apart. A rename is a deletion and an
// addition, so a protected file cannot leave by being moved.
const (
	ChangeAdded    ChangeKind = "added"
	ChangeModified ChangeKind = "modified"
	ChangeDeleted  ChangeKind = "deleted"
)

// RepoKind is how a changed path is a git repo of its own rather than a
// file. The gate doesn't look inside one, so any file can hide in it.
type RepoKind string

// The kinds of repo a change can be; a plain file has none.
const (
	// RepoSubmodule is a gitlink: a commit of another repo kept in the
	// tree, with or without an entry in .gitmodules.
	RepoSubmodule RepoKind = "submodule"
	// RepoNested is an untracked folder with its own .git.
	RepoNested RepoKind = "nested"
)

// FileChange is one file a job's diff against its base touches, by its
// slash-separated path from the repo root. Repo says when the path is a
// repo of its own; a nested one's path ends in "/".
type FileChange struct {
	Path string
	Kind ChangeKind
	Repo RepoKind
}

// IndexFlag is a mark git's index can keep on a tracked file that makes git
// stop looking at its changes in the worktree, so no diff shows them.
type IndexFlag string

// The index flags the gate refuses: either one hides a file's changes.
const (
	FlagSkipWorktree    IndexFlag = "skip-worktree"
	FlagAssumeUnchanged IndexFlag = "assume-unchanged"
)

// FlaggedFile is one file a worktree's index marks with Flag, by its
// slash-separated path from the repo root.
type FlaggedFile struct {
	Path string
	Flag IndexFlag
}
