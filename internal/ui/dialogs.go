package ui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/report"
	"github.com/nagayon-935/mping/internal/stats"
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
	path          *tview.InputField
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
	d.status = tview.NewTextView().SetText("Enter: Save | Tab: Move focus | Esc: Cancel\nExisting files are preserved").SetTextColor(tcell.ColorWhite)
	d.status.SetBackgroundColor(tcell.ColorBlack)
	d.formatPreview = tview.NewTextView().SetTextColor(tcell.ColorWhite).SetWrap(false)
	d.formatPreview.SetBackgroundColor(tcell.ColorBlack)
	d.form = tview.NewForm().
		SetLabelColor(tcell.ColorWhite).
		SetFieldStyle(tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)).
		SetButtonStyle(tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)).
		SetButtonActivatedStyle(tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorWhite))
	d.form.SetBackgroundColor(tcell.ColorBlack)
	d.configurePathField()
	d.form.AddButton("Save", d.submit).AddButton("Cancel", d.close)
	d.form.SetCancelFunc(d.close)
	title := " Save session report "
	if selectedID != 0 {
		title = fmt.Sprintf(" Save target #%d report ", selectedID)
	}
	content := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(d.path, 3, 0, true).
		AddItem(d.formatPreview, 2, 0, false).
		AddItem(d.form, 3, 0, false).
		AddItem(nil, 0, 1, false)
	content.SetBackgroundColor(tcell.ColorBlack)
	content.SetBorder(true).SetBorderPadding(1, 1, 2, 2).SetTitle(title).SetTitleColor(tcell.ColorWhite).SetBorderColor(tcell.ColorWhite)
	pane := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(content, 0, 1, true).AddItem(d.status, 2, 0, false)
	d.root.AddPage("saveReport", pane, true, true).SwitchToPage("saveReport")
	d.app.SetFocus(d.path)
}

// configurePathField keeps the format preview and focus styling in sync with the input.
func (d *saveDialog) configurePathField() {
	const jsonPreview = "Format: JSON (.json)"
	d.path = tview.NewInputField().SetText("mping-" + time.Now().Format("20060102-150405.000") + ".json").SetChangedFunc(func(path string) {
		_, format, err := report.PathFormat(path)
		if err != nil {
			d.formatPreview.SetText("Format: Use a .json or .csv file name")
		} else if format == "csv" {
			d.formatPreview.SetText("Format: CSV (.csv), one row per target")
		} else {
			d.formatPreview.SetText(jsonPreview)
		}
	})
	d.path.SetBackgroundColor(tcell.ColorBlack)
	d.path.SetBorder(true).SetTitle(" 保存先 (Path) ").SetTitleColor(tcell.ColorWhite).SetBorderColor(tcell.ColorWhite)
	d.path.SetFocusFunc(func() {
		d.path.SetTitle(" 保存先 (Path) / 入力中 ")
		d.path.SetFieldStyle(tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorWhite))
	})
	d.path.SetBlurFunc(func() {
		d.path.SetTitle(" 保存先 (Path) ")
		d.path.SetFieldStyle(tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack))
	})
	d.formatPreview.SetText(jsonPreview)
}

func (d *saveDialog) close() {
	d.open = false
	d.root.RemovePage("saveReport")
	d.restore()
}

func (d *saveDialog) submit() {
	path, format, err := report.PathFormat(d.path.GetText())
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
		d.status.SetText(queueFullMessage)
		return
	}
	d.close()
	d.notify("Saving report: "+path, false)
}

// handle keeps global shortcuts from intercepting paths while the save form is open.
func (d *saveDialog) handle(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyEscape {
		d.close()
		return nil
	}
	if event.Key() == tcell.KeyTab || event.Key() == tcell.KeyBacktab {
		fields := []tview.Primitive{d.path, d.form.GetButton(0), d.form.GetButton(1)}
		for i, field := range fields {
			if d.app.GetFocus() == field {
				step := 1
				if event.Key() == tcell.KeyBacktab {
					step = len(fields) - 1
				}
				d.app.SetFocus(fields[(i+step)%len(fields)])
				return nil
			}
		}
	}
	if event.Key() == tcell.KeyEnter && d.app.GetFocus() == d.path {
		d.submit()
		return nil
	}
	return event
}

// deleteDialog freezes the identity shown to the user. Refreshes may change
// the table selection, but confirmation always refers to this same target.
type deleteDialog struct {
	app     *tview.Application
	root    *tview.Pages
	open    bool
	last    bool
	quit    func()
	confirm func(uint64, string)
	restore func(tview.Primitive)
}

func newDeleteDialog(app *tview.Application, root *tview.Pages, confirm func(uint64, string), restore func(tview.Primitive)) *deleteDialog {
	return &deleteDialog{app: app, root: root, confirm: confirm, restore: restore}
}

func (d *deleteDialog) show(target *stats.TargetStats, last bool) {
	if d.open || target == nil {
		return
	}
	v := target.GetViewWindow(0)
	id, host := v.ID, v.Host
	focus := d.app.GetFocus()
	ip := v.IP
	if ip == "" {
		ip = "未解決"
	}
	d.last = last
	message := fmt.Sprintf("このホストを削除しますか？\n\nホスト: %s\n対象: #%d\nIP: %s\n\n再追加すると、新しい測定として開始します。\n\nTab / ←→: 選択  Enter: 確定  Esc: キャンセル", tview.Escape(host), id, tview.Escape(ip))
	buttons := []string{"キャンセル", "削除"}
	if last {
		message = fmt.Sprintf("最後のホストです。\n\nホスト: %s\nIP: %s\n\n終了するには q キーを押してください。\n\nEsc / Enter: 測定画面へ戻る", tview.Escape(host), tview.Escape(ip))
		buttons = []string{"戻る"}
	}
	d.open = true
	modal := newStyledModal(" ホスト削除 ", message, buttons, func(index int, label string) {
		if !d.open {
			return
		}
		d.open = false
		d.root.RemovePage("deleteConfirm")
		d.restore(focus)
		if !last && index == 1 {
			d.confirm(id, host)
		}
	})
	// Keep the underlying page visible and refreshing while the dialog is open.
	d.root.AddPage("deleteConfirm", modal, true, true)
	d.app.SetFocus(modal)
}

func (d *deleteDialog) handle(event *tcell.EventKey) *tcell.EventKey {
	if d.last && event.Key() == tcell.KeyRune && event.Rune() == 'q' && d.quit != nil {
		d.quit()
		return nil
	}
	// Ignore repeated d and all other global shortcuts until the dialog closes.
	return handleModalKey(event)
}

// webAccessDialog shows the control URL only on explicit request, independently
// of Log retention.
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
