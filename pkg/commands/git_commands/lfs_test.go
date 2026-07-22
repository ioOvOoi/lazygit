package git_commands

import (
	"testing"

	"github.com/go-errors/errors"
	"github.com/jesseduffield/lazygit/pkg/commands/models"
	"github.com/jesseduffield/lazygit/pkg/commands/oscommands"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
)

func TestLfsLocks(t *testing.T) {
	locksJSON := `[` +
		`{"id":"101","path":"Content/Maps/Main.umap","owner":{"name":"alice"},"locked_at":"2024-01-02T03:04:05Z"},` +
		`{"id":"102","path":"Content/Meshes/Rock.uasset","owner":{"name":"bob"},"locked_at":"2024-01-03T00:00:00Z"}` +
		`]`

	runner := oscommands.NewFakeRunner(t).
		ExpectGitArgs([]string{"lfs", "version"}, "git-lfs/3.7.1", nil).
		ExpectGitArgs([]string{"lfs", "track"}, "Listing tracked patterns\n    *.uasset (.gitattributes)\nListing excluded patterns\n", nil).
		ExpectGitArgs([]string{"lfs", "locks", "--json"}, locksJSON, nil).
		ExpectGitArgs([]string{"config", "user.name"}, "alice\n", nil)
	instance := buildLfsCommands(commonDeps{runner: runner})

	locks, err := instance.Locks()
	assert.NoError(t, err)
	assert.Len(t, locks, 2)

	assert.Equal(t, "Content/Maps/Main.umap", locks[0].Path)
	assert.Equal(t, "alice", locks[0].Owner)
	assert.True(t, locks[0].Mine)

	assert.Equal(t, "Content/Meshes/Rock.uasset", locks[1].Path)
	assert.Equal(t, "bob", locks[1].Owner)
	assert.False(t, locks[1].Mine)

	runner.CheckForMissingCalls()
}

// When git-lfs isn't installed we must not fall through to hitting the lock
// server; Locks returns an empty result and stops after the version probe.
func TestLfsLocksNotInstalled(t *testing.T) {
	runner := oscommands.NewFakeRunner(t).
		ExpectGitArgs([]string{"lfs", "version"}, "", errors.New("git: 'lfs' is not a git command"))
	instance := buildLfsCommands(commonDeps{runner: runner})

	locks, err := instance.Locks()
	assert.NoError(t, err)
	assert.Nil(t, locks)

	runner.CheckForMissingCalls()
}

// A repo that isn't tracking anything through the lfs filter shouldn't query
// the lock server either.
func TestLfsLocksRepoNotUsingLfs(t *testing.T) {
	runner := oscommands.NewFakeRunner(t).
		ExpectGitArgs([]string{"lfs", "version"}, "git-lfs/3.7.1", nil).
		ExpectGitArgs([]string{"lfs", "track"}, "Listing tracked patterns\nListing excluded patterns\n", nil)
	instance := buildLfsCommands(commonDeps{runner: runner})

	locks, err := instance.Locks()
	assert.NoError(t, err)
	assert.Nil(t, locks)

	runner.CheckForMissingCalls()
}

func TestLfsMarkTrackedFiles(t *testing.T) {
	// git check-attr -z output is NUL-separated (path, "filter", value) triples.
	checkAttrOutput := "Content/Hero.uasset\x00filter\x00lfs\x00" +
		"README.md\x00filter\x00unspecified\x00"

	runner := oscommands.NewFakeRunner(t).
		ExpectGitArgs([]string{"lfs", "version"}, "git-lfs/3.7.1", nil).
		ExpectGitArgs([]string{"lfs", "track"}, "Listing tracked patterns\n    *.uasset (.gitattributes)\nListing excluded patterns\n", nil).
		ExpectGitArgs([]string{"check-attr", "filter", "-z", "--stdin"}, checkAttrOutput, nil)
	instance := buildLfsCommands(commonDeps{runner: runner})

	files := []*models.File{
		{Path: "Content/Hero.uasset"},
		{Path: "README.md"},
	}
	instance.MarkTrackedFiles(files)

	assert.True(t, files[0].IsLfsTracked)
	assert.False(t, files[1].IsLfsTracked)

	runner.CheckForMissingCalls()
}

func TestLfsUntrackedLargeFiles(t *testing.T) {
	fs := afero.NewMemMapFs()
	assert.NoError(t, afero.WriteFile(fs, "big.bin", make([]byte, 2*1024*1024), 0o644))
	assert.NoError(t, afero.WriteFile(fs, "small.txt", []byte("hi"), 0o644))

	runner := oscommands.NewFakeRunner(t).
		ExpectGitArgs([]string{"lfs", "version"}, "git-lfs/3.7.1", nil).
		ExpectGitArgs([]string{"lfs", "track"}, "Listing tracked patterns\n    *.uasset (.gitattributes)\nListing excluded patterns\n", nil)
	instance := buildLfsCommands(commonDeps{runner: runner, fs: fs, repoPaths: MockRepoPaths(".")})

	files := []*models.File{
		{Path: "big.bin", HasStagedChanges: true},
		{Path: "small.txt", HasStagedChanges: true},
		// already lfs-tracked, so skipped regardless of size
		{Path: "tracked.uasset", HasStagedChanges: true, IsLfsTracked: true},
		// not staged, so skipped
		{Path: "big.bin", HasStagedChanges: false},
	}

	result := instance.UntrackedLargeFiles(files, 1*1024*1024)
	assert.Len(t, result, 1)
	assert.Equal(t, "big.bin", result[0].Path)

	runner.CheckForMissingCalls()
}

func TestLfsTrackAndRestage(t *testing.T) {
	runner := oscommands.NewFakeRunner(t).
		ExpectGitArgs([]string{"lfs", "track", "*.uasset"}, "", nil).
		ExpectGitArgs([]string{"lfs", "track", "*.bin"}, "", nil).
		ExpectGitArgs([]string{"add", "--", ".gitattributes", "a.uasset", "b.uasset", "c.bin"}, "", nil)
	instance := buildLfsCommands(commonDeps{runner: runner})

	err := instance.TrackAndRestage([]*models.File{
		{Path: "a.uasset"},
		{Path: "b.uasset"}, // same extension: pattern de-duplicated
		{Path: "c.bin"},
	})
	assert.NoError(t, err)

	runner.CheckForMissingCalls()
}

func TestLfsContentOps(t *testing.T) {
	runner := oscommands.NewFakeRunner(t).
		ExpectGitArgs([]string{"lfs", "pull"}, "", nil).
		ExpectGitArgs([]string{"lfs", "pull", "--include=Content/Hero.uasset"}, "", nil).
		ExpectGitArgs([]string{"lfs", "fetch", "--all"}, "", nil).
		ExpectGitArgs([]string{"lfs", "checkout"}, "", nil)
	instance := buildLfsCommands(commonDeps{runner: runner})

	assert.NoError(t, instance.Pull())
	assert.NoError(t, instance.PullPath("Content/Hero.uasset"))
	assert.NoError(t, instance.Fetch())
	assert.NoError(t, instance.Checkout())

	runner.CheckForMissingCalls()
}

func TestLfsUnlockOnPushRoundTrip(t *testing.T) {
	instance := buildLfsCommands(commonDeps{fs: afero.NewMemMapFs()})

	assert.Empty(t, instance.PendingUnlockOnPush())

	assert.NoError(t, instance.MarkForUnlockOnPush([]string{"a.uasset", "b.uasset"}))
	// Marking again merges and de-duplicates rather than appending blindly.
	assert.NoError(t, instance.MarkForUnlockOnPush([]string{"b.uasset", "c.uasset"}))

	assert.Equal(t, []string{"a.uasset", "b.uasset", "c.uasset"}, instance.PendingUnlockOnPush())
}
