package main

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"testing"
)

// TestGenerateCompletion_AllShellsIncludeEveryFlag guards against drift
// between the CLI's actual flag surface (registerFlags) and the generated
// completion scripts: every long and short flag name collectFlags reports
// must appear somewhere in each shell's generated script.
func TestGenerateCompletion_AllShellsIncludeEveryFlag(t *testing.T) {
	fs := newCompletionFlagSet()
	flags := collectFlags(fs)
	if len(flags) == 0 {
		t.Fatal("collectFlags returned no flags — is registerFlags wired up?")
	}

	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			script, err := generateCompletion(shell, fs)
			if err != nil {
				t.Fatalf("generateCompletion(%q): %v", shell, err)
			}
			for _, f := range flags {
				// fish's native `complete -l <name>` syntax spells the long
				// name without a leading "--" (unlike bash/zsh), so the
				// literal substring differs by shell dialect.
				longNeedle := "--" + f.long
				if shell == "fish" {
					longNeedle = "-l " + f.long
				}
				if !strings.Contains(script, longNeedle) {
					t.Errorf("%s completion missing long flag %s", shell, longNeedle)
				}
				if f.short == "" {
					continue
				}
				shortNeedle := "-" + f.short
				if shell == "fish" {
					shortNeedle = "-s " + f.short
				}
				if !strings.Contains(script, shortNeedle) {
					t.Errorf("%s completion missing short flag %s (for --%s)", shell, shortNeedle, f.long)
				}
			}
		})
	}
}

func TestGenerateCompletion_ShellMarkers(t *testing.T) {
	fs := newCompletionFlagSet()

	bash, err := generateCompletion("bash", fs)
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	if !strings.Contains(bash, "complete -F _mping mping") {
		t.Errorf("bash completion missing registration marker; got:\n%s", bash)
	}

	zsh, err := generateCompletion("zsh", fs)
	if err != nil {
		t.Fatalf("zsh: %v", err)
	}
	if !strings.Contains(zsh, "#compdef mping") {
		t.Errorf("zsh completion missing #compdef marker; got:\n%s", zsh)
	}

	fish, err := generateCompletion("fish", fs)
	if err != nil {
		t.Fatalf("fish: %v", err)
	}
	if !strings.Contains(fish, "complete -c mping") {
		t.Errorf("fish completion missing complete -c mping marker; got:\n%s", fish)
	}
}

func TestGenerateCompletion_FileFlags(t *testing.T) {
	fs := newCompletionFlagSet()

	zsh, err := generateCompletion("zsh", fs)
	if err != nil {
		t.Fatalf("zsh: %v", err)
	}
	if !strings.Contains(zsh, "_files") {
		t.Errorf("zsh completion missing _files for file-taking flags; got:\n%s", zsh)
	}

	fish, err := generateCompletion("fish", fs)
	if err != nil {
		t.Fatalf("fish: %v", err)
	}
	if !strings.Contains(fish, "-l file") || !strings.Contains(fish, "-F") {
		t.Errorf("fish completion missing file-forcing (-F) for --file; got:\n%s", fish)
	}

	bash, err := generateCompletion("bash", fs)
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	if !strings.Contains(bash, "-f|--file") {
		t.Errorf("bash completion missing -f|--file case branch; got:\n%s", bash)
	}
}

func TestGenerateCompletion_InterfaceFlag(t *testing.T) {
	fs := newCompletionFlagSet()

	for _, shell := range []string{"bash", "zsh", "fish"} {
		script, err := generateCompletion(shell, fs)
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		if !strings.Contains(script, "__complete-interfaces") {
			t.Errorf("%s completion missing __complete-interfaces reference for -I/--interface; got:\n%s", shell, script)
		}
	}
}

func TestGenerateCompletion_UnsupportedShell(t *testing.T) {
	fs := newCompletionFlagSet()
	if _, err := generateCompletion("powershell", fs); err == nil {
		t.Error("expected error for unsupported shell, got nil")
	}
}

// TestGenerateCompletion_ZshDescriptionEscaped guards against a flag usage
// string breaking zsh's _arguments syntax, which treats bare ':' and
// unescaped '[' ']' as field separators. --http's usage ("URL(s) to
// health-check, e.g. https://example.com/health (comma-separated or
// repeated)") contains a literal ':' from "https://", making it a real
// (not synthetic) case that must be escaped.
func TestGenerateCompletion_ZshDescriptionEscaped(t *testing.T) {
	fs := newCompletionFlagSet()
	zsh, err := generateCompletion("zsh", fs)
	if err != nil {
		t.Fatalf("zsh: %v", err)
	}
	for line := range strings.SplitSeq(zsh, "\n") {
		if !strings.Contains(line, "--http") {
			continue
		}
		if strings.Contains(line, "https") && !strings.Contains(line, `\:`) {
			t.Errorf("zsh --http description has unescaped ':' which breaks _arguments: %s", line)
		}
		return
	}
	t.Fatal("did not find a --http line in zsh completion to check escaping")
}

func TestRunCompletion_Dispatch(t *testing.T) {
	t.Run("bash", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := run([]string{"completion", "bash"}, &out, &errOut)
		if code != 0 {
			t.Fatalf("expected exit 0, got %d (stderr: %s)", code, errOut.String())
		}
		if !strings.Contains(out.String(), "complete -F _mping mping") {
			t.Errorf("expected bash completion marker in stdout, got:\n%s", out.String())
		}
	})

	t.Run("missing shell arg", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := run([]string{"completion"}, &out, &errOut)
		if code == 0 {
			t.Fatal("expected non-zero exit for missing shell argument")
		}
	})

	t.Run("unknown shell", func(t *testing.T) {
		var out, errOut bytes.Buffer
		code := run([]string{"completion", "foo"}, &out, &errOut)
		if code == 0 {
			t.Fatal("expected non-zero exit for unknown shell")
		}
	})
}

func TestRunCompleteInterfaces(t *testing.T) {
	oldNetInterfaces := netInterfaces
	defer func() { netInterfaces = oldNetInterfaces }()

	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{{Name: "en0"}, {Name: "lo0"}}, nil
	}

	var out bytes.Buffer
	code := runCompleteInterfaces(&out)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	got := out.String()
	if !strings.Contains(got, "en0") || !strings.Contains(got, "lo0") {
		t.Errorf("expected interface names in output, got: %q", got)
	}
}

func TestRunCompleteInterfaces_ListError(t *testing.T) {
	oldNetInterfaces := netInterfaces
	defer func() { netInterfaces = oldNetInterfaces }()

	netInterfaces = func() ([]net.Interface, error) {
		return nil, fmt.Errorf("boom")
	}

	var out bytes.Buffer
	code := runCompleteInterfaces(&out)
	if code == 0 {
		t.Fatal("expected non-zero exit when netInterfaces fails")
	}
}
