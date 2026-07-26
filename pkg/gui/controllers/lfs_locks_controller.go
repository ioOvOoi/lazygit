package controllers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jesseduffield/lazygit/pkg/commands/models"
	"github.com/jesseduffield/lazygit/pkg/gui/context"
	"github.com/jesseduffield/lazygit/pkg/gui/style"
	"github.com/jesseduffield/lazygit/pkg/gui/types"
	"github.com/jesseduffield/lazygit/pkg/gocui"
	"github.com/jesseduffield/lazygit/pkg/utils"
)
type LfsLocksController struct {
	baseController
	*ListControllerTrait[*models.LfsLock]
	c *ControllerCommon
}

var _ types.IController = &LfsLocksController{}

func NewLfsLocksController(
	c *ControllerCommon,
) *LfsLocksController {
	return &LfsLocksController{
		baseController: baseController{},
		ListControllerTrait: NewListControllerTrait(
			c,
			c.Contexts().LfsLocks,
			c.Contexts().LfsLocks.GetSelected,
			c.Contexts().LfsLocks.GetSelectedItems,
		),
		c: c,
	}
}

func (self *LfsLocksController) GetKeybindings(opts types.KeybindingsOpts) []*types.Binding {
	return []*types.Binding{
		{
			Keys:              opts.GetKeys(opts.Config.Universal.Remove),
			Handler:           self.withItems(self.unlockMultiple),
			GetDisabledReason: self.require(self.itemRangeSelected()),
			Description:       self.c.Tr.LfsUnlock,
			Tooltip:           self.c.Tr.LfsUnlockTooltip,
			DisplayOnScreen:   true,
		},
	}
}

func (self *LfsLocksController) GetOnFocus() func(types.OnFocusOpts) {
	return func(types.OnFocusOpts) {
		// Locks live on the remote lock server, so pull a fresh view whenever the
		// panel comes into focus rather than relying on the last background load.
		self.c.Refresh(types.RefreshOptions{Scope: []types.RefreshableView{types.LFS_LOCKS}})
	}
}

func (self *LfsLocksController) GetOnRenderToMain() func() {
	return func() {
		var task types.UpdateTask
		lock := self.context().GetSelected()
		if lock == nil {
			task = types.NewRenderStringTask(self.c.Tr.LfsNoLocks)
		} else {
			task = types.NewRenderStringTask(self.lockSummary(lock))
		}

		self.c.RenderToMainViews(types.RefreshMainOpts{
			Pair: self.c.MainViewPairs().Normal,
			Main: &types.ViewUpdateOpts{
				Title: self.c.Tr.LfsLocksTitle,
				Task:  task,
			},
		})
	}
}

func (self *LfsLocksController) lockSummary(lock *models.LfsLock) string {
	owner := lock.Owner
	if lock.Mine {
		owner = owner + " " + self.c.Tr.LfsLockOwnerYou
	}

	lines := [][]string{
		{style.FgCyan.Sprint(self.c.Tr.LfsLockPathColumn), lock.Path},
		{style.FgCyan.Sprint(self.c.Tr.LfsLockOwnerColumn), owner},
	}
	if lock.LockedAt != "" {
		lines = append(lines, []string{style.FgCyan.Sprint(self.c.Tr.LfsLockLockedAtColumn), lock.LockedAt})
	}

	rendered, _ := utils.RenderDisplayStrings(lines, nil)
	return strings.Join(rendered, "\n")
}

func (self *LfsLocksController) context() *context.LfsLocksContext {
	return self.c.Contexts().LfsLocks
}

func (self *LfsLocksController) unlockMultiple(locks []*models.LfsLock) error {
	// Separate locks into owned and non-owned
	var nonOwned []*models.LfsLock
	for _, lock := range locks {
		if !lock.Mine {
			nonOwned = append(nonOwned, lock)
		}
	}

	// If there are non-owned locks, confirm before force-unlocking
	if len(nonOwned) > 0 {
		var paths []string
		for _, lock := range nonOwned {
			paths = append(paths, lock.Path)
		}

		self.c.Confirm(types.ConfirmOpts{
			Title: self.c.Tr.LfsForceUnlockTitle,
			Prompt: utils.ResolvePlaceholderString(
				self.c.Tr.LfsForceUnlockMultiplePrompt,
				map[string]string{
					"count":  fmt.Sprintf("%d", len(nonOwned)),
					"paths":  strings.Join(paths, ", "),
				},
			),
			HandleConfirm: func() error {
				return self.doUnlockMultiple(locks)
			},
		})
		return nil
	}

	// All locks are owned by the user, unlock directly
	return self.doUnlockMultiple(locks)
}

func (self *LfsLocksController) doUnlockMultiple(locks []*models.LfsLock) error {
	return self.c.WithWaitingStatus(self.c.Tr.LfsUnlockingStatus, func(gocui.Task) error {
		self.c.LogAction(self.c.Tr.Actions.LfsUnlock)

		var errs []error
		for _, lock := range locks {
			if err := self.c.Git().Lfs.Unlock(lock.Path); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", lock.Path, err))
			}
		}

		self.c.Refresh(types.RefreshOptions{Scope: []types.RefreshableView{types.LFS_LOCKS, types.FILES}})

		if len(errs) > 0 {
			return fmt.Errorf("failed to unlock %d file(s): %w", len(errs), errors.Join(errs...))
		}
		return nil
	})
}