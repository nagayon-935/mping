package ui

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/nagayon-935/mping/internal/stats"
)

func TestDeleteConfirmationDoesNotReplaceDisappearingTarget(t *testing.T) {
	a, b := stats.NewTargetStats("same"), stats.NewTargetStats("same")
	type targetSet struct{ targets []*stats.TargetStats }
	var source atomic.Pointer[targetSet]
	source.Store(&targetSet{[]*stats.TargetStats{a, b}})
	refreshed := make(chan struct{})
	var once sync.Once
	deleted := make(chan uint64, 1)
	h := startTUI(t, RunOptions{
		Targets: []*stats.TargetStats{a, b},
		TargetSource: func() TargetSet {
			targets := source.Load().targets
			if len(targets) == 1 {
				once.Do(func() { close(refreshed) })
			}
			return TargetSet{Targets: targets}
		},
		OnDeleteTarget: func(id uint64) error { deleted <- id; return errors.New("selected target is no longer active") },
	}, 140, 45)
	h.wait("Ping Monitor")
	h.key(tcell.KeyDown)
	h.key(tcell.KeyEnter)
	h.wait("Host Details")
	h.press('d')
	h.wait("このホストを削除しますか？")
	source.Store(&targetSet{[]*stats.TargetStats{a}})
	b.RecordEvent("test", "removed elsewhere")
	recvWithin(t, refreshed, "the target list refresh")
	h.key(tcell.KeyRight)
	h.key(tcell.KeyEnter)
	if id := recvWithin(t, deleted, "the delete confirmation callback"); id != b.ID() {
		t.Fatal("confirmation substituted another target")
	}
	h.wait("Delete target: selected target is no longer active")
	h.wait("Ping Monitor")
	h.quit()
}
