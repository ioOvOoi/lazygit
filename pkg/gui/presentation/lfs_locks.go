package presentation

import (
	"github.com/jesseduffield/lazygit/pkg/commands/models"
	"github.com/jesseduffield/lazygit/pkg/gui/style"
	"github.com/jesseduffield/lazygit/pkg/i18n"
	"github.com/jesseduffield/lazygit/pkg/theme"
	"github.com/samber/lo"
)

func GetLfsLockListDisplayStrings(locks []*models.LfsLock, tr *i18n.TranslationSet) [][]string {
	return lo.Map(locks, func(lock *models.LfsLock, _ int) []string {
		return getLfsLockDisplayStrings(lock, tr)
	})
}

func getLfsLockDisplayStrings(lock *models.LfsLock, tr *i18n.TranslationSet) []string {
	owner := lock.Owner
	if lock.Mine {
		owner = owner + " " + tr.LfsLockOwnerYou
	}

	ownerColor := style.FgYellow
	if lock.Mine {
		ownerColor = style.FgGreen
	}

	return []string{
		theme.DefaultTextColor.Sprint(lock.Path),
		ownerColor.Sprint(owner),
	}
}
