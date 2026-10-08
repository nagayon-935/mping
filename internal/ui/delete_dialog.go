package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
	"github.com/rivo/tview"
)

// deleteDialog freezes the identity shown to the user. Refreshes may change
// the table selection, but confirmation always refers to this same target.
type deleteDialog struct {
	app     *tview.Application
	root    *tview.Pages
	modal   *tview.Modal
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
	v := target.GetView()
	id, host := v.ID, v.Host
	focus := d.app.GetFocus()
	ip, dscp := v.IP, v.DSCP
	if ip == "" {
		ip = "未解決"
	}
	if dscp == "" {
		dscp = "既定"
	}
	d.last = last
	message := fmt.Sprintf("このホストを削除しますか？\n\nホスト: %s\n対象: #%d\nIP: %s\nDSCP: %s\n\n再追加すると、新しい測定として開始します。\n\nTab / ←→: 選択  Enter: 確定  Esc: キャンセル", tview.Escape(host), id, tview.Escape(ip), tview.Escape(dscp))
	buttons := []string{"キャンセル", "削除"}
	if last {
		message = fmt.Sprintf("最後のホストです。\n\nホスト: %s\nIP: %s\n\n終了するには q キーを押してください。\n\nEsc / Enter: 測定画面へ戻る", tview.Escape(host), tview.Escape(ip))
		buttons = []string{"戻る"}
	}
	d.open = true
	d.modal = tview.NewModal().
		SetBackgroundColor(tcell.ColorBlack).
		SetTextColor(tcell.ColorWhite).
		SetButtonStyle(tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)).
		SetButtonActivatedStyle(tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorWhite)).
		SetText(message).
		AddButtons(buttons).
		SetFocus(0).
		SetDoneFunc(func(index int, label string) {
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
	d.modal.Box.SetBackgroundColor(tcell.ColorBlack)
	d.modal.SetTitle(" ホスト削除 ").SetTitleColor(tcell.ColorWhite).SetBorderColor(tcell.ColorWhite)
	// Keep the underlying page visible and refreshing while the dialog is open.
	d.root.AddPage("deleteConfirm", d.modal, true, true)
	d.app.SetFocus(d.modal)
}

func (d *deleteDialog) handle(event *tcell.EventKey) *tcell.EventKey {
	if d.last && event.Key() == tcell.KeyRune && event.Rune() == 'q' && d.quit != nil {
		d.quit()
		return nil
	}
	switch event.Key() {
	case tcell.KeyTab, tcell.KeyBacktab, tcell.KeyLeft, tcell.KeyRight, tcell.KeyEnter, tcell.KeyEscape, tcell.KeyCtrlC:
		return event
	}
	// Ignore repeated d and all other global shortcuts until the dialog closes.
	return nil
}
