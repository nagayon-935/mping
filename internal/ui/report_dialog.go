package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// saveDialog is owned by the UI loop. The session worker performs capture
// and file I/O, then delivers its result through the UI mailbox.
type saveDialog struct {
	app        *tview.Application
	root       *tview.Pages
	session    *uiSession
	save       func(string, string, uint64) error
	restore    func()
	notify     func(string, bool)
	form       *tview.Form
	status     *tview.TextView
	open       bool
	selectedID uint64
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
	d.status = tview.NewTextView().SetDynamicColors(true).SetText("Tab: Next field | Enter: Choose / Save | Esc: Cancel | Existing files are preserved")
	d.form = tview.NewForm()
	d.form.AddInputField("Path", "mping-"+time.Now().Format("20060102-150405.000")+".txt", 0, nil, nil)
	d.form.AddDropDown("Format", []string{"Text", "JSON"}, 0, func(option string, index int) {
		path := d.form.GetFormItem(0).(*tview.InputField)
		ext := filepath.Ext(path.GetText())
		if ext == ".txt" || ext == ".json" {
			newExt := ".txt"
			if index == 1 {
				newExt = ".json"
			}
			path.SetText(strings.TrimSuffix(path.GetText(), ext) + newExt)
		}
	})
	d.form.AddButton("Save", d.submit).AddButton("Cancel", d.close)
	d.form.SetCancelFunc(d.close)
	title := " Save session report "
	if selectedID != 0 {
		title = fmt.Sprintf(" Save target #%d report ", selectedID)
	}
	d.form.SetBorder(true).SetTitle(title)
	pane := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(d.form, 0, 1, true).AddItem(d.status, 2, 0, false)
	d.root.AddPage("saveReport", pane, true, true).SwitchToPage("saveReport")
	d.app.SetFocus(d.form)
}

func (d *saveDialog) close() {
	d.open = false
	d.root.RemovePage("saveReport")
	d.restore()
}

func (d *saveDialog) submit() {
	path := strings.TrimSpace(d.form.GetFormItem(0).(*tview.InputField).GetText())
	if path == "" {
		d.status.SetText("[red]Enter a file path[-]")
		return
	}
	index, _ := d.form.GetFormItem(1).(*tview.DropDown).GetCurrentOption()
	format := "text"
	if index == 1 {
		format = "json"
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
		d.status.SetText("[yellow]Operation queue full; please try again[-]")
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
	return event
}
