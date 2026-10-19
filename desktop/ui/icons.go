package ui

import (
	"gioui.org/widget"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

func mustIcon(data []byte) *widget.Icon {
	ic, err := widget.NewIcon(data)
	if err != nil {
		panic(err)
	}
	return ic
}

// Material icons, the set gio-x and gioview use.
var (
	icAdd      = mustIcon(icons.ContentAdd)
	icSend     = mustIcon(icons.ContentSend)
	icStop     = mustIcon(icons.AVStop)
	icFolder   = mustIcon(icons.FileFolderOpen)
	icSettings = mustIcon(icons.ActionSettings)
	icSearch   = mustIcon(icons.ActionSearch)
	icDelete   = mustIcon(icons.ActionDelete)
	icEdit     = mustIcon(icons.EditorModeEdit)
	icMore     = mustIcon(icons.NavigationMoreHoriz)
	icClose    = mustIcon(icons.NavigationClose)
	icFile     = mustIcon(icons.ActionDescription)
	icTool     = mustIcon(icons.ActionBuild)
	icCopy     = mustIcon(icons.ContentContentCopy)
	icOpen     = mustIcon(icons.ActionOpenInNew)
	icAttach   = mustIcon(icons.EditorAttachFile)
	icImage    = mustIcon(icons.ImageImage)
	icPanel    = mustIcon(icons.ActionViewColumn)
	icWarn     = mustIcon(icons.AlertWarning)
	icDone     = mustIcon(icons.ActionDone)
	icExpand   = mustIcon(icons.NavigationExpandMore)
	icChevron  = mustIcon(icons.NavigationChevronRight)
	icCode     = mustIcon(icons.ActionCode)
	icPeople   = mustIcon(icons.SocialPeople)
	icQueue    = mustIcon(icons.AVQueue)
	icDiff     = mustIcon(icons.ActionCompareArrows)
	icChat     = mustIcon(icons.CommunicationChatBubbleOutline)
	icInfo     = mustIcon(icons.ActionInfoOutline)
)
