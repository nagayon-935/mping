package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// The URL lives outside Log retention and is shown only on explicit request.
type webAccessDialog struct {
	app     *tview.Application
	root    *tview.Pages
	url     string
	copy    func(string)
	restore func(tview.Primitive)
	modal   *tview.Modal
	open    bool
}

func newWebAccessDialog(app *tview.Application, root *tview.Pages, url string, copy func(string), restore func(tview.Primitive)) *webAccessDialog {
	return &webAccessDialog{app: app, root: root, url: url, copy: copy, restore: restore}
}

func (d *webAccessDialog) show() {
	if d.open || d.url == "" {
		return
	}
	d.open = true
	focus := d.app.GetFocus()
	message := "Open this URL to enable browser controls:\n\n" + tview.Escape(d.url) + "\n\nEnter: Copy URL | Tab: Select | Esc: Close"
	d.modal = tview.NewModal().
		SetBackgroundColor(tcell.ColorBlack).
		SetTextColor(tcell.ColorWhite).
		SetButtonStyle(tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)).
		SetButtonActivatedStyle(tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorWhite)).
		SetText(message).
		AddButtons([]string{"Copy URL", "Close"}).
		SetDoneFunc(func(index int, _ string) {
			if index == 0 {
				d.copy(d.url)
				d.modal.SetText(message + "\n\nCopy requested. If blocked by your terminal, select the URL above.")
				return
			}
			d.open = false
			d.root.RemovePage("webAccess")
			d.restore(focus)
		})
	d.modal.Box.SetBackgroundColor(tcell.ColorBlack)
	d.modal.SetTitle(" Web UI access ").SetTitleColor(tcell.ColorWhite).SetBorderColor(tcell.ColorWhite)
	d.root.AddPage("webAccess", d.modal, true, true)
	d.app.SetFocus(d.modal)
}

func (d *webAccessDialog) handle(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyTab, tcell.KeyBacktab, tcell.KeyLeft, tcell.KeyRight, tcell.KeyEnter, tcell.KeyEscape, tcell.KeyCtrlC:
		return event
	}
	return nil
}
