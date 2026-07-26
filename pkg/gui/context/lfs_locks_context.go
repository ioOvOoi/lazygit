package context

import (
	"github.com/jesseduffield/lazygit/pkg/commands/models"
	"github.com/jesseduffield/lazygit/pkg/gui/presentation"
	"github.com/jesseduffield/lazygit/pkg/gui/types"
)

type LfsLocksContext struct {
	*FilteredListViewModel[*models.LfsLock]
	*ListContextTrait
}

var _ types.IListContext = (*LfsLocksContext)(nil)

func NewLfsLocksContext(c *ContextCommon) *LfsLocksContext {
	viewModel := NewFilteredListViewModel(
		func() []*models.LfsLock { return c.Model().LfsLocks },
		func(lock *models.LfsLock) []string {
			return []string{lock.Path, lock.Owner}
		},
	)

	getDisplayStrings := func(_ int, _ int) [][]string {
		return presentation.GetLfsLockListDisplayStrings(viewModel.GetItems(), c.Tr)
	}

	return &LfsLocksContext{
		FilteredListViewModel: viewModel,
		ListContextTrait: &ListContextTrait{
			Context: NewSimpleContext(NewBaseContext(NewBaseContextOpts{
				View:       c.Views().LfsLocks,
				WindowName: "files",
				Key:        LFS_LOCKS_CONTEXT_KEY,
				Kind:       types.SIDE_CONTEXT,
				Focusable:  true,
			})),
			ListRenderer: ListRenderer{
				list:              viewModel,
				getDisplayStrings: getDisplayStrings,
			},
			c: c,
		},
	}
}
