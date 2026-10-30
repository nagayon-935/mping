package privilege

import (
	"os"
	"testing"
)

func TestCapabilitiesReflectTheProcess(t *testing.T) {
	caps, err := capabilities()
	if err != nil {
		t.Fatal(err)
	}
	// root holds every capability; an ordinary test run holds none.
	if (os.Geteuid() == 0) != (caps != "") {
		t.Fatalf("euid %d, capabilities %q", os.Geteuid(), caps)
	}
}
