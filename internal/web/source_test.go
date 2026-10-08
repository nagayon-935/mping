package web

import "testing"

func TestSourceIsStartingWithNoProviderBeforeSet(t *testing.T) {
	src := NewSource()

	p, state, _ := src.load()

	if p != nil {
		t.Fatalf("provider = %v, want nil", p)
	}
	if state != StateStarting || src.State() != StateStarting {
		t.Fatalf("state = %q / %q, want %q", state, src.State(), StateStarting)
	}
}

func TestSourceStateTransitions(t *testing.T) {
	tests := []struct {
		name  string
		steps func(*Source, Provider)
		want  State
	}{
		{"set", func(s *Source, p Provider) { s.Set(p) }, StateRunning},
		{"reloading", func(s *Source, p Provider) { s.Set(p); s.MarkReloading() }, StateReloading},
		{"set after reloading", func(s *Source, p Provider) { s.Set(p); s.MarkReloading(); s.Set(p) }, StateRunning},
		{"stopped", func(s *Source, p Provider) { s.Set(p); s.MarkStopped() }, StateStopped},
		{"stopped after reloading", func(s *Source, p Provider) { s.Set(p); s.MarkReloading(); s.MarkStopped() }, StateStopped},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := NewSource()
			p, _ := providerWithTarget("a.example")

			tt.steps(src, p)

			if got := src.State(); got != tt.want {
				t.Fatalf("State() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSourceSetInstallsNewProvider(t *testing.T) {
	src := NewSource()
	first, _ := providerWithTarget("a.example")
	second, _ := providerWithTarget("b.example")
	src.Set(first)
	src.MarkReloading()

	src.Set(second)
	p, _, _ := src.load()

	if p != second {
		t.Fatalf("provider = %v, want second provider", p)
	}
}

func TestSourceMarkReloadingAndStoppedKeepLastProvider(t *testing.T) {
	for _, mark := range []func(*Source){(*Source).MarkReloading, (*Source).MarkStopped} {
		src := NewSource()
		p, _ := providerWithTarget("a.example")
		src.Set(p)

		mark(src)
		got, _, _ := src.load()

		if got != p {
			t.Fatalf("provider = %v, want the last provider to stay readable", got)
		}
	}
}

func TestSourceVersionAdvancesOnEveryTransition(t *testing.T) {
	src := NewSource()
	p, _ := providerWithTarget("a.example")
	_, _, v0 := src.load()

	src.Set(p)
	_, _, v1 := src.load()
	src.MarkReloading()
	_, _, v2 := src.load()
	src.MarkStopped()
	_, _, v3 := src.load()

	if !(v0 < v1 && v1 < v2 && v2 < v3) {
		t.Fatalf("versions = %d, %d, %d, %d; want strictly increasing", v0, v1, v2, v3)
	}
}

func TestSourceZeroValueIsUsable(t *testing.T) {
	var src Source
	p, _ := providerWithTarget("a.example")

	got, state, _ := src.load()
	src.Set(p)
	after, _, _ := src.load()

	if got != nil || state != StateStarting {
		t.Fatalf("zero Source load = (%v, %q), want (nil, %q)", got, state, StateStarting)
	}
	if after != p {
		t.Fatalf("provider after Set = %v, want p", after)
	}
}
