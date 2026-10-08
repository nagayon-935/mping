package web

import "testing"

func TestSourceHasNoProviderBeforeSet(t *testing.T) {
	src := NewSource()

	p, reloading, _ := src.load()

	if p != nil {
		t.Fatalf("provider = %v, want nil", p)
	}
	if reloading {
		t.Fatal("reloading = true before any provider was set")
	}
}

func TestSourceSetInstallsProviderAndClearsReloading(t *testing.T) {
	src := NewSource()
	first, _ := providerWithTarget("a.example")
	second, _ := providerWithTarget("b.example")
	src.Set(first)
	src.MarkReloading()

	src.Set(second)
	p, reloading, _ := src.load()

	if p != second {
		t.Fatalf("provider = %v, want second provider", p)
	}
	if reloading {
		t.Fatal("reloading = true after Set")
	}
}

func TestSourceMarkReloadingKeepsLastProvider(t *testing.T) {
	src := NewSource()
	p, _ := providerWithTarget("a.example")
	src.Set(p)

	src.MarkReloading()
	got, reloading, _ := src.load()

	if got != p {
		t.Fatalf("provider = %v, want the last provider to stay readable", got)
	}
	if !reloading {
		t.Fatal("reloading = false after MarkReloading")
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

	if !(v0 < v1 && v1 < v2) {
		t.Fatalf("versions = %d, %d, %d; want strictly increasing", v0, v1, v2)
	}
}
