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
	d.modal = newStyledModal(" Web UI access ", message, []string{"Copy URL", "Close"}, func(index int, _ string) {
		if index == 0 {
			d.copy(d.url)
			d.modal.SetText(message + "\n\nCopy requested. If blocked by your terminal, select the URL above.")
			return
		}
		d.open = false
		d.root.RemovePage("webAccess")
		d.restore(focus)
	})
	d.root.AddPage("webAccess", d.modal, true, true)
	d.app.SetFocus(d.modal)
}

func (d *webAccessDialog) handle(event *tcell.EventKey) *tcell.EventKey {
	return handleModalKey(event)
}
