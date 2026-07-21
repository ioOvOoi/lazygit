package git_commands

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/jesseduffield/lazygit/pkg/commands/models"
	"github.com/samber/lo"
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

func (self *LfsCommands) Unlock(path string) error {
	return self.cmd.New(NewGitCmd("lfs").Arg("unlock", "--").Arg(path).ToArgv()).Run()
}

func (self *LfsCommands) UnlockForce(path string) error {
	return self.cmd.New(NewGitCmd("lfs").Arg("unlock", "--force", "--").Arg(path).ToArgv()).Run()
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
