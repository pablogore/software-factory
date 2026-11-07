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

// FileChange is one file a job's diff against its base touches, by its
// slash-separated path from the repo root.
type FileChange struct {
	Path string
	Kind ChangeKind
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
