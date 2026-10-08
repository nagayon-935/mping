package ui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/report"
	"github.com/rivo/tview"
)

// saveDialog is owned by the UI loop. The session worker performs capture
// and file I/O, then delivers its result through the UI mailbox.
type saveDialog struct {
	app           *tview.Application
	root          *tview.Pages
	session       *uiSession
	save          func(string, string, uint64) error
	restore       func()
	notify        func(string, bool)
	form          *tview.Form
	status        *tview.TextView
	formatPreview *tview.TextView
	open          bool
	selectedID    uint64
}

func newSaveDialog(app *tview.Application, root *tview.Pages, session *uiSession, save func(string, string, uint64) error, restore func(), notify func(string, bool)) *saveDialog {
	return &saveDialog{app: app, root: root, session: session, save: save, restore: restore, notify: notify}
}

func (d *saveDialog) show(selectedID uint64) {
	if d.save == nil {
		return
	}
	d.selectedID = selectedID
	d.open = true
	d.status = tview.NewTextView().SetText("Tab: Next field | Enter: Save | Esc: Cancel | Existing files are preserved").SetTextColor(tcell.ColorWhite)
	d.status.SetBackgroundColor(tcell.ColorBlack)
	d.formatPreview = tview.NewTextView().SetTextColor(tcell.ColorWhite).SetWrap(false)
	d.formatPreview.SetBackgroundColor(tcell.ColorBlack)
	d.form = tview.NewForm().
		SetLabelColor(tcell.ColorWhite).
		SetFieldStyle(tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)).
		SetButtonStyle(tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)).
		SetButtonActivatedStyle(tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorWhite))
	d.form.SetBackgroundColor(tcell.ColorBlack)
	d.form.AddInputField("Path (.txt/.json)", "mping-"+time.Now().Format("20060102-150405.000")+".txt", 0, nil, func(path string) {
		_, format, err := report.PathFormat(path)
		if err != nil {
			d.formatPreview.SetText("Format: Use a .txt or .json file name")
		} else if format == "json" {
			d.formatPreview.SetText("Format: JSON (.json)")
		} else {
			d.formatPreview.SetText("Format: Text (.txt)")
		}
	})
	d.formatPreview.SetText("Format: Text (.txt)")
	d.form.AddButton("Save", d.submit).AddButton("Cancel", d.close)
	d.form.SetCancelFunc(d.close)
	title := " Save session report "
	if selectedID != 0 {
		title = fmt.Sprintf(" Save target #%d report ", selectedID)
	}
	d.form.SetBorder(true).SetTitle(title).SetTitleColor(tcell.ColorWhite).SetBorderColor(tcell.ColorWhite)
	pane := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(d.form, 0, 1, true).AddItem(d.formatPreview, 1, 0, false).AddItem(d.status, 2, 0, false)
	d.root.AddPage("saveReport", pane, true, true).SwitchToPage("saveReport")
	d.app.SetFocus(d.form)
}

func (d *saveDialog) close() {
	d.open = false
	d.root.RemovePage("saveReport")
	d.restore()
}

func (d *saveDialog) submit() {
	path, format, err := report.PathFormat(d.form.GetFormItem(0).(*tview.InputField).GetText())
	if err != nil {
		d.status.SetText(err.Error())
		return
	}
	id := d.selectedID
	if !d.session.Submit(func() {
		err := d.save(path, format, id)
		d.session.Post(func() {
			if err != nil {
				d.notify("Save report: "+err.Error(), true)
			} else {
				d.notify("Saved report: "+path, false)
			}
		})
	}) {
		d.status.SetText("Operation queue full; please try again")
		return
	}
	d.close()
	d.notify("Saving report: "+path, false)
}

// While a save form is open, global shortcuts must not intercept typed paths.
func (d *saveDialog) handle(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyEscape {
		d.close()
		return nil
	}
	if event.Key() == tcell.KeyEnter && d.app.GetFocus() == d.form.GetFormItem(0) {
		d.submit()
		return nil
	}
	return event
}
