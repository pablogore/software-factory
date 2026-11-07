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
