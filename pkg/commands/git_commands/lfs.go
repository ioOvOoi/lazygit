package git_commands

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jesseduffield/lazygit/pkg/commands/models"
	"github.com/samber/lo"
	"github.com/spf13/afero"
)

// LfsCommands wraps the subset of `git lfs` we integrate with. The focus is the
// file-locking workflow that binary-heavy projects (e.g. Unreal Engine) rely on
// to coordinate edits to unmergeable assets.
type LfsCommands struct {
	*GitCommon

	enabledOnce sync.Once
	enabled     bool

	currentUserOnce sync.Once
	currentUser     string
}

func NewLfsCommands(gitCommon *GitCommon) *LfsCommands {
	return &LfsCommands{GitCommon: gitCommon}
}

type lfsLockJSON struct {
	Id    string `json:"id"`
	Path  string `json:"path"`
	Owner struct {
		Name string `json:"name"`
	} `json:"owner"`
	LockedAt string `json:"locked_at"`
}

// Enabled reports whether it's worth talking to git-lfs at all: git-lfs must be
// installed and the repo must actually track something through the lfs filter.
// The result is cached for the session, since neither condition changes under
// us in practice and the check would otherwise run on every locks refresh.
func (self *LfsCommands) Enabled() bool {
	self.enabledOnce.Do(func() {
		self.enabled = self.installed() && self.repoUsesLfs()
	})
	return self.enabled
}

func (self *LfsCommands) installed() bool {
	err := self.cmd.New(NewGitCmd("lfs").Arg("version").ToArgv()).DontLog().Run()
	return err == nil
}

// repoUsesLfs returns true when `git lfs track` lists at least one tracked
// pattern. That command only reads .gitattributes, so it never touches the
// network, which matters because Enabled() gates the lock queries that do.
func (self *LfsCommands) repoUsesLfs() bool {
	output, err := self.cmd.New(NewGitCmd("lfs").Arg("track").ToArgv()).DontLog().RunWithOutput()
	if err != nil {
		return false
	}

	inTrackedSection := false
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "Listing tracked patterns") {
			inTrackedSection = true
			continue
		}
		if strings.HasPrefix(line, "Listing excluded patterns") {
			inTrackedSection = false
			continue
		}
		if inTrackedSection && strings.TrimSpace(line) != "" {
			return true
		}
	}
	return false
}

// Locks returns the file locks reported by the lfs lock server. It contacts the
// remote, so callers should invoke it deliberately (on demand / after an
// action) rather than on every background refresh.
func (self *LfsCommands) Locks() ([]*models.LfsLock, error) {
	if !self.Enabled() {
		return nil, nil
	}

	output, err := self.cmd.New(NewGitCmd("lfs").Arg("locks", "--json").ToArgv()).
		DontLog().RunWithOutput()
	if err != nil {
		return nil, err
	}

	var parsed []lfsLockJSON
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		return nil, err
	}

	currentUser := self.getCurrentUser()
	return lo.Map(parsed, func(lock lfsLockJSON, _ int) *models.LfsLock {
		return &models.LfsLock{
			Id:       lock.Id,
			Path:     lock.Path,
			Owner:    lock.Owner.Name,
			LockedAt: lock.LockedAt,
			Mine:     currentUser != "" && lock.Owner.Name == currentUser,
		}
	}), nil
}

// MarkTrackedFiles sets IsLfsTracked on each file that git resolves to the lfs
// filter (via .gitattributes). It's a no-op when lfs isn't in use, so non-lfs
// repos never spawn the check-attr process.
func (self *LfsCommands) MarkTrackedFiles(files []*models.File) {
	if !self.Enabled() || len(files) == 0 {
		return
	}

	paths := lo.Map(files, func(file *models.File, _ int) string {
		return file.Path
	})

	cmdObj := self.cmd.New(NewGitCmd("check-attr").Arg("filter", "-z", "--stdin").ToArgv()).DontLog()
	cmdObj.SetStdin(strings.Join(paths, "\x00"))
	output, err := cmdObj.RunWithOutput()
	if err != nil {
		self.Log.Debugf("lfs: check-attr failed: %v", err)
		return
	}

	// Output is NUL-separated triples: path, "filter", value.
	tracked := make(map[string]bool)
	fields := strings.Split(output, "\x00")
	for i := 0; i+2 < len(fields); i += 3 {
		if fields[i+2] == "lfs" {
			tracked[fields[i]] = true
		}
	}

	for _, file := range files {
		if tracked[file.Path] {
			file.IsLfsTracked = true
		}
	}
}

func (self *LfsCommands) getCurrentUser() string {
	self.currentUserOnce.Do(func() {
		output, err := self.cmd.New(NewGitCmd("config").Arg("user.name").ToArgv()).
			DontLog().RunWithOutput()
		if err == nil {
			self.currentUser = strings.TrimSpace(output)
		}
	})
	return self.currentUser
}

func (self *LfsCommands) Lock(path string) error {
	return self.cmd.New(NewGitCmd("lfs").Arg("lock", "--").Arg(path).ToArgv()).Run()
}

// UntrackedLargeFiles returns the staged files that are at least thresholdBytes
// in size but aren't tracked through the lfs filter — the ones at risk of
// bloating the repo if committed as plain git objects (a common mistake with
// large binary assets). Returns nil when lfs isn't in use for this repo.
func (self *LfsCommands) UntrackedLargeFiles(files []*models.File, thresholdBytes int64) []*models.File {
	if !self.Enabled() || thresholdBytes <= 0 {
		return nil
	}

	worktree := self.repoPaths.WorktreePath()
	return lo.Filter(files, func(file *models.File, _ int) bool {
		if !file.HasStagedChanges || file.IsLfsTracked || file.Deleted {
			return false
		}
		info, err := self.Fs.Stat(filepath.Join(worktree, file.Path))
		if err != nil {
			return false
		}
		return info.Size() >= thresholdBytes
	})
}

// TrackAndRestage adds each file to lfs tracking (by extension, or by exact path
// when there's no extension) and re-stages the files so their staged content
// becomes an lfs pointer rather than the raw blob.
func (self *LfsCommands) TrackAndRestage(files []*models.File) error {
	seen := make(map[string]bool)
	patterns := []string{}
	for _, file := range files {
		pattern := lfsTrackPatternForPath(file.Path)
		if !seen[pattern] {
			seen[pattern] = true
			patterns = append(patterns, pattern)
		}
	}

	for _, pattern := range patterns {
		if err := self.cmd.New(NewGitCmd("lfs").Arg("track").Arg(pattern).ToArgv()).Run(); err != nil {
			return err
		}
	}

	paths := []string{".gitattributes"}
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return runGitCmdOnPaths("add", paths, self.cmd)
}

func lfsTrackPatternForPath(path string) string {
	if ext := filepath.Ext(path); ext != "" {
		return "*" + ext
	}
	return path
}

func (self *LfsCommands) Unlock(path string) error {
	return self.cmd.New(NewGitCmd("lfs").Arg("unlock", "--").Arg(path).ToArgv()).Run()
}

func (self *LfsCommands) UnlockForce(path string) error {
	return self.cmd.New(NewGitCmd("lfs").Arg("unlock", "--force", "--").Arg(path).ToArgv()).Run()
}

// unlockOnPushFilePath is where we persist the paths whose locks should be
// released the next time the user pushes. It lives in the worktree's git dir so
// the intent, recorded at commit time, survives until push (and a restart in
// between).
func (self *LfsCommands) unlockOnPushFilePath() string {
	return filepath.Join(self.repoPaths.WorktreeGitDirPath(), "lazygit-lfs-unlock-on-push")
}

// MarkForUnlockOnPush records that the given paths' locks should be released on
// the next push, merging with any already-pending paths.
func (self *LfsCommands) MarkForUnlockOnPush(paths []string) error {
	if len(paths) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	merged := []string{}
	for _, path := range append(self.PendingUnlockOnPush(), paths...) {
		if path != "" && !seen[path] {
			seen[path] = true
			merged = append(merged, path)
		}
	}

	content := strings.Join(merged, "\n") + "\n"
	return afero.WriteFile(self.Fs, self.unlockOnPushFilePath(), []byte(content), 0o644)
}

// PendingUnlockOnPush returns the paths whose locks are scheduled to be released
// on the next push.
func (self *LfsCommands) PendingUnlockOnPush() []string {
	data, err := afero.ReadFile(self.Fs, self.unlockOnPushFilePath())
	if err != nil {
		return nil
	}

	return lo.Filter(strings.Split(strings.TrimSpace(string(data)), "\n"),
		func(line string, _ int) bool { return line != "" })
}

// UnlockPendingOnPush releases the locks recorded for release on push (best
// effort, only the ones we own) and clears the pending list. Meant to be called
// after a successful push.
func (self *LfsCommands) UnlockPendingOnPush() {
	if !self.Enabled() {
		return
	}

	pending := self.PendingUnlockOnPush()
	if len(pending) == 0 {
		return
	}

	for _, path := range pending {
		self.UnlockOwnedQuietly(path)
	}

	if err := self.Fs.Remove(self.unlockOnPushFilePath()); err != nil {
		self.Log.Debugf("lfs: could not clear pending unlock-on-push list: %v", err)
	}
}

// UnlockOwnedQuietly attempts to release a lock we hold on the given path,
// swallowing the error if we don't actually own it (or aren't the lock server's
// idea of the owner). It's used on commit, where unlocking is a convenience and
// must never turn a successful commit into a surfaced failure.
func (self *LfsCommands) UnlockOwnedQuietly(path string) {
	if !self.Enabled() {
		return
	}
	if err := self.Unlock(path); err != nil {
		self.Log.Debugf("lfs: could not unlock %s on commit: %v", path, err)
	}
}
