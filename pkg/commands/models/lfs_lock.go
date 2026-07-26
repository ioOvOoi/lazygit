package models

// LfsLock represents a single git-lfs file lock, as reported by
// `git lfs locks`. Locks are how teams working with unmergeable binary assets
// (the typical Unreal Engine / game-dev workflow) coordinate who is allowed to
// edit a given file at a time.
type LfsLock struct {
	Id       string
	Path     string
	Owner    string
	LockedAt string
	// Mine is true when the lock is held by the current user. It's a
	// best-effort flag derived by comparing the owner against the local git
	// user name, since the lock server is the only authority on ownership.
	Mine bool
}

func (l *LfsLock) ID() string {
	return l.Id
}

func (l *LfsLock) Description() string {
	return l.Path
}
