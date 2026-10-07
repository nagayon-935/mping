package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	if !strings.Contains(bash, "complete -o filenames -F _mping mping") {
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
		if !strings.Contains(out.String(), "complete -o filenames -F _mping mping") {
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

func TestBashCompletionCandidates(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is unavailable")
	}
	dir := t.TempDir()
	for _, name := range []string{"hosts list.yaml", "hosts.yaml", "literal[1].yaml", "-Ienfile.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// An absolute command path makes the test independent of installed mping.
	command := filepath.Join(dir, "mping")
	if err := os.WriteFile(command, []byte("#!/bin/sh\n[ \"$1\" = __complete-interfaces ] || exit 1\nprintf '%s\\n' en0 en1 lo0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	script, err := generateCompletion("bash", newCompletionFlagSet())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		words []string
		want  []string
	}{
		{"long flag", []string{"--inter"}, []string{"--interface", "--interval"}},
		{"command", []string{"comp"}, []string{"completion"}},
		{"shells", []string{"completion", ""}, []string{"bash", "zsh", "fish", "--help"}},
		{"shell prefix", []string{"completion", "z"}, []string{"zsh"}},
		{"no arguments after shell", []string{"completion", "bash", ""}, nil},
		{"file with spaces", []string{"-f", "hosts l"}, []string{"hosts list.yaml"}},
		{"output path", []string{"--output", "hosts l"}, []string{"hosts list.yaml"}},
		{"json path", []string{"-j", "hosts l"}, []string{"hosts list.yaml"}},
		{"equals unsplit", []string{"--file=hosts l"}, []string{"--file=hosts list.yaml"}},
		{"equals split", []string{"--file", "=", "hosts l"}, []string{"hosts list.yaml"}},
		{"equals joined to flag", []string{"--file=", "hosts l"}, []string{"hosts list.yaml"}},
		{"attached file", []string{"-fhosts l"}, []string{"-fhosts list.yaml"}},
		{"literal glob", []string{"-f", "literal"}, []string{"literal[1].yaml"}},
		{"filename resembling flag", []string{"-f", "-Ien"}, []string{"-Ienfile.yaml"}},
		{"interface", []string{"-I", "en"}, []string{"en0", "en1"}},
		{"equals interface", []string{"--interface=en"}, []string{"--interface=en0", "--interface=en1"}},
		{"attached interface", []string{"-Ien"}, []string{"-Ien0", "-Ien1"}},
		{"numeric value", []string{"--interval", "--inter"}, nil},
		{"free form value", []string{"--http", "--inter"}, nil},
		{"value resembling attached flag", []string{"--http", "-Ien"}, nil},
		{"value resembling equals flag", []string{"--http", "--interface=en"}, nil},
		{"after end of options", []string{"--", "--inter"}, nil},
		{"after host", []string{"example.com", "--inter"}, []string{"--interface", "--interval"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			words := append([]string{command}, tt.words...)
			var quoted []string
			for _, word := range words {
				quoted = append(quoted, shellQuote(word))
			}
			harness := fmt.Sprintf("\nCOMP_WORDS=(%s)\nCOMP_CWORD=%d\n_mping\nfor candidate in \"${COMPREPLY[@]}\"; do printf '%%s\\n' \"$candidate\"; done\n", strings.Join(quoted, " "), len(words)-1)
			cmd := exec.Command(bash, "--noprofile", "--norc", "-c", script+harness)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("bash: %v\n%s", err, out)
			}
			var got []string
			if len(out) > 0 {
				got = strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("candidates = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCompletionShellSyntax(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			path, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is unavailable", shell)
			}
			fs := newCompletionFlagSet()
			fs.String("quote-test", "", "quotes ' and \\ plus [brackets]: $HOME `literal`")
			script, err := generateCompletion(shell, fs)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(path, "-n")
			cmd.Stdin = strings.NewReader(script)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("invalid %s syntax: %v\n%s", shell, err, out)
			}
		})
	}
}

func TestZshCompletionLoad(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is unavailable")
	}
	dir := t.TempDir()
	script, err := generateCompletion("zsh", newCompletionFlagSet())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "_mping")
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"source", "autoload"} {
		t.Run(mode, func(t *testing.T) {
			// Capture the specs passed to _arguments to check loading and
			// quoting at runtime without needing an interactive terminal.
			harness := `_arguments() { printf '%s\n' "$@"; }
_describe() { :; }
compdef() { :; }
words=(mping --dscp '')
CURRENT=3
`
			if mode == "source" {
				harness += "source " + shellQuote(path) + "\n_mping\n"
			} else {
				harness += "fpath=(" + shellQuote(dir) + " $fpath)\nautoload -Uz _mping\n_mping\n"
			}
			cmd := exec.Command(zsh, "-f", "-c", harness)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("zsh %s: %v\n%s", mode, err, out)
			}
			for _, needle := range []string{`'dscp\:'`, "*--http=", "*-H+", "--file=", "-f+", ":interface:_mping_interfaces"} {
				if !strings.Contains(string(out), needle) {
					t.Errorf("zsh %s lost spec %q:\n%s", mode, needle, out)
				}
			}
		})
	}
}

func TestRunCompletionHelp(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		var out, errOut bytes.Buffer
		if code := run([]string{"completion", flag}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "Usage:") || errOut.Len() != 0 {
			t.Fatalf("completion %s: code=%d stdout=%q stderr=%q", flag, code, out.String(), errOut.String())
		}
	}
	_, _, _, usage, _ := parseArgs([]string{"--help"})
	if !strings.Contains(usage, "mping completion bash|zsh|fish") {
		t.Fatalf("main help does not expose completion: %s", usage)
	}
}

type failingCompletionWriter struct{}

func (failingCompletionWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("write failed")
}

func TestRunCompletionWriteError(t *testing.T) {
	var errOut bytes.Buffer
	if code := runCompletion([]string{"bash"}, failingCompletionWriter{}, &errOut); code == 0 || !strings.Contains(errOut.String(), "write failed") {
		t.Fatalf("code=%d stderr=%q", code, errOut.String())
	}
}
