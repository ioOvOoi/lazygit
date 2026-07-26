package controllers

import (
	"strings"

	"github.com/jesseduffield/lazygit/pkg/commands/models"
	"github.com/jesseduffield/lazygit/pkg/gui/context"
	"github.com/jesseduffield/lazygit/pkg/gui/style"
	"github.com/jesseduffield/lazygit/pkg/gui/types"
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
			Handler:           self.withItem(self.unlock),
			GetDisabledReason: self.require(self.singleItemSelected()),
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

func (self *LfsLocksController) unlock(lock *models.LfsLock) error {
	if lock.Mine {
		return self.doUnlock(lock, false)
	}

	// Someone else holds this lock; releasing it needs --force and can disrupt
	// their work, so make the user confirm before we override it.
	self.c.Confirm(types.ConfirmOpts{
		Title: self.c.Tr.LfsForceUnlockTitle,
		Prompt: utils.ResolvePlaceholderString(
			self.c.Tr.LfsForceUnlockPrompt,
			map[string]string{
				"path":  lock.Path,
				"owner": lock.Owner,
			},
		),
		HandleConfirm: func() error {
			return self.doUnlock(lock, true)
		},
	})
	return nil
}

func (self *LfsLocksController) doUnlock(lock *models.LfsLock, force bool) error {
	self.c.LogAction(self.c.Tr.Actions.LfsUnlock)

	var err error
	if force {
		err = self.c.Git().Lfs.UnlockForce(lock.Path)
	} else {
		err = self.c.Git().Lfs.Unlock(lock.Path)
	}

	self.c.Refresh(types.RefreshOptions{Scope: []types.RefreshableView{types.LFS_LOCKS, types.FILES}})
	return err
}
