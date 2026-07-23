package ui

import "testing"

// TestMonitorPane_Refresh_SkipsSetTextWhenUnchanged guards the P5 fix:
// refresh() must still call render() every tick (so the pane's underlying
// data stays current), but should skip the tview SetText call when the
// rendered content is byte-for-byte identical to the previous tick.
func TestMonitorPane_Refresh_SkipsSetTextWhenUnchanged(t *testing.T) {
	renderCalls := 0
	mp := newMonitorPane(true, " Test Monitor ", func(availW int) string {
		renderCalls++
		return "same content"
	})
	mp.view.SetRect(0, 0, 80, 10)

	mp.refresh()
	firstText := mp.view.GetText(false)

	mp.refresh()

	if renderCalls != 2 {
		t.Errorf("render() call count: got %d, want 2 (render must still run every tick)", renderCalls)
	}
	if mp.lastText != "same content" {
		t.Errorf("lastText: got %q, want %q", mp.lastText, "same content")
	}
	if got := mp.view.GetText(false); got != firstText {
		t.Errorf("view text changed unexpectedly: got %q, want %q", got, firstText)
	}
}

// TestMonitorPane_Refresh_UpdatesSetTextWhenChanged verifies refresh() still
// applies new content when render() output differs from the last tick.
func TestMonitorPane_Refresh_UpdatesSetTextWhenChanged(t *testing.T) {
	seq := []string{"first", "second"}
	i := 0
	mp := newMonitorPane(true, " Test Monitor ", func(availW int) string {
		text := seq[i]
		if i < len(seq)-1 {
			i++
		}
		return text
	})
	mp.view.SetRect(0, 0, 80, 10)

	mp.refresh()
	if got := mp.view.GetText(false); got != "first" {
		t.Fatalf("after first refresh: got %q, want %q", got, "first")
	}

	mp.refresh()
	if got := mp.view.GetText(false); got != "second" {
		t.Fatalf("after second refresh: got %q, want %q", got, "second")
	}
	if mp.lastText != "second" {
		t.Errorf("lastText: got %q, want %q", mp.lastText, "second")
	}
}

// TestMonitorPane_Refresh_DisabledIsNoOp confirms the pre-existing disabled
// short-circuit still works after adding lastText caching.
func TestMonitorPane_Refresh_DisabledIsNoOp(t *testing.T) {
	renderCalls := 0
	mp := newMonitorPane(false, " Test Monitor ", func(availW int) string {
		renderCalls++
		return "unused"
	})

	mp.refresh()

	if renderCalls != 0 {
		t.Errorf("render() should not be called when pane is disabled, got %d calls", renderCalls)
	}
	if mp.view != nil {
		t.Error("disabled pane should have a nil view")
	}
}
