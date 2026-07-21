package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/pflag"
)

// flagInfo is the shell-agnostic description of a single mping flag,
// extracted from the FlagSet that registerFlags produces. It is the
// intermediate representation every shell template renders from, so adding a
// flag to registerFlags is the only thing needed to keep completion in sync.
type flagInfo struct {
	long     string
	short    string
	usage    string
	takesArg bool
}

// fileCompletionFlags are long flag names whose value is a filesystem path;
// their shell templates delegate to the shell's own file-path completion.
var fileCompletionFlags = map[string]bool{
	"file":        true,
	"output":      true,
	"json-output": true,
}

// interfaceCompletionFlags are long flag names whose value is a network
// interface name; their shell templates delegate to the hidden
// `mping __complete-interfaces` helper (see runCompleteInterfaces).
var interfaceCompletionFlags = map[string]bool{
	"interface": true,
}

// newCompletionFlagSet builds a throwaway FlagSet carrying mping's full flag
// surface (via registerFlags — the single source of truth also used by
// parseArgs) plus the --help/-h flag pflag would otherwise add implicitly.
// It exists purely for introspection; nothing is ever parsed into it.
func newCompletionFlagSet() *pflag.FlagSet {
	fs := pflag.NewFlagSet("mping", pflag.ContinueOnError)
	var cfg config
	var th thresholdFlags
	registerFlags(fs, &cfg, &th)
	fs.BoolP("help", "h", false, "help for mping")
	return fs
}

// collectFlags extracts a flagInfo per flag from fs in a deterministic
// (lexicographic, matching pflag's default VisitAll order) sequence.
func collectFlags(fs *pflag.FlagSet) []flagInfo {
	var flags []flagInfo
	fs.VisitAll(func(f *pflag.Flag) {
		flags = append(flags, flagInfo{
			long:     f.Name,
			short:    f.Shorthand,
			usage:    f.Usage,
			takesArg: f.Value.Type() != "bool",
		})
	})
	sort.Slice(flags, func(i, j int) bool { return flags[i].long < flags[j].long })
	return flags
}

// generateCompletion renders the completion script for shell ("bash", "zsh",
// or "fish") from fs's registered flags.
func generateCompletion(shell string, fs *pflag.FlagSet) (string, error) {
	flags := collectFlags(fs)
	switch shell {
	case "bash":
		return bashCompletion(flags), nil
	case "zsh":
		return zshCompletion(flags), nil
	case "fish":
		return fishCompletion(flags), nil
	default:
		return "", fmt.Errorf("unsupported shell %q (want bash, zsh, or fish)", shell)
	}
}

// valueHint returns the bash/fish "what completes this flag's value" marker:
// file completion, interface-name completion (via the hidden
// __complete-interfaces helper), or "" for flags whose value isn't
// completable (numbers, free-form strings, or bools that take no value).
func valueHint(f flagInfo) string {
	switch {
	case !f.takesArg:
		return ""
	case fileCompletionFlags[f.long]:
		return "file"
	case interfaceCompletionFlags[f.long]:
		return "interface"
	default:
		return "none"
	}
}

func bashCompletion(flags []flagInfo) string {
	var b strings.Builder
	b.WriteString("# bash completion for mping\n")
	b.WriteString("# Install: source <(mping completion bash)\n")
	b.WriteString("_mping() {\n")
	b.WriteString("    local cur prev opts\n")
	b.WriteString("    COMPREPLY=()\n")
	b.WriteString("    cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	b.WriteString("    prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n")

	var optWords []string
	for _, f := range flags {
		optWords = append(optWords, "--"+f.long)
		if f.short != "" {
			optWords = append(optWords, "-"+f.short)
		}
	}
	b.WriteString("    opts=\"" + strings.Join(optWords, " ") + "\"\n\n")

	b.WriteString("    case \"$prev\" in\n")
	for _, f := range flags {
		hint := valueHint(f)
		if hint == "" || hint == "none" {
			continue
		}
		names := "--" + f.long
		if f.short != "" {
			names = "-" + f.short + "|" + names
		}
		switch hint {
		case "file":
			b.WriteString("        " + names + ")\n")
			b.WriteString("            COMPREPLY=( $(compgen -f -- \"$cur\") )\n")
			b.WriteString("            return 0\n")
			b.WriteString("            ;;\n")
		case "interface":
			b.WriteString("        " + names + ")\n")
			b.WriteString("            COMPREPLY=( $(compgen -W \"$(mping __complete-interfaces)\" -- \"$cur\") )\n")
			b.WriteString("            return 0\n")
			b.WriteString("            ;;\n")
		}
	}
	b.WriteString("    esac\n\n")

	b.WriteString("    if [[ \"$cur\" == -* ]]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"$opts\" -- \"$cur\") )\n")
	b.WriteString("        return 0\n")
	b.WriteString("    fi\n")
	b.WriteString("}\n")
	b.WriteString("complete -F _mping mping\n")
	return b.String()
}

func zshCompletion(flags []flagInfo) string {
	var b strings.Builder
	b.WriteString("#compdef mping\n")
	b.WriteString("# zsh completion for mping\n")
	b.WriteString("# Install: mping completion zsh > \"${fpath[1]}/_mping\"\n\n")
	b.WriteString("_mping() {\n")
	b.WriteString("    _arguments \\\n")
	for _, f := range flags {
		spec := zshArgSpec(f)
		b.WriteString("        " + spec + " \\\n")
	}
	b.WriteString("        '*:host:_hosts'\n")
	b.WriteString("}\n\n")
	b.WriteString("_mping \"$@\"\n")
	return b.String()
}

// zshArgSpec renders one flag's `_arguments` spec line, e.g.:
//
//	'(-i --interval)'{-i,--interval}'[ping interval in ms]:value:'
func zshArgSpec(f flagInfo) string {
	desc := zshEscape(f.usage)
	var namePart string
	if f.short != "" {
		namePart = fmt.Sprintf("'(-%s --%s)'{-%s,--%s}", f.short, f.long, f.short, f.long)
	} else {
		namePart = fmt.Sprintf("'--%s'", f.long)
	}

	action := ""
	switch valueHint(f) {
	case "file":
		action = ":file:_files"
	case "interface":
		action = ":interface:(${(f)\"$(mping __complete-interfaces)\"})"
	case "none":
		action = ":value:"
	}
	return fmt.Sprintf("%s'[%s]%s'", namePart, desc, action)
}

// zshEscape escapes characters that are meaningful inside a zsh _arguments
// spec string ('[...]' description field): ':' separates the description
// from the action, and unescaped '[' / ']' would prematurely close the
// description field.
func zshEscape(s string) string {
	r := strings.NewReplacer(
		`:`, `\:`,
		`[`, `\[`,
		`]`, `\]`,
	)
	return r.Replace(s)
}

func fishCompletion(flags []flagInfo) string {
	var b strings.Builder
	b.WriteString("# fish completion for mping\n")
	b.WriteString("# Install: mping completion fish > ~/.config/fish/completions/mping.fish\n\n")
	for _, f := range flags {
		var line strings.Builder
		line.WriteString("complete -c mping")
		if f.short != "" {
			line.WriteString(" -s " + f.short)
		}
		line.WriteString(" -l " + f.long)
		switch valueHint(f) {
		case "file":
			line.WriteString(" -r -F")
		case "interface":
			line.WriteString(" -r -f -a '(mping __complete-interfaces)'")
		case "none":
			line.WriteString(" -r -f")
		}
		line.WriteString(" -d '" + fishEscape(f.usage) + "'")
		b.WriteString(line.String())
		b.WriteString("\n")
	}
	return b.String()
}

// fishEscape escapes single quotes inside a fish `-d '...'` description so
// the literal string isn't terminated early.
func fishEscape(s string) string {
	return strings.ReplaceAll(s, `'`, `\'`)
}

// runCompletion implements `mping completion <shell>`, writing the generated
// script to out on success or a usage message to errOut on failure.
func runCompletion(args []string, out, errOut io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errOut, "Usage: mping completion bash|zsh|fish")
		return 1
	}
	script, err := generateCompletion(args[0], newCompletionFlagSet())
	if err != nil {
		fmt.Fprintf(errOut, "Error: %v\n", err)
		fmt.Fprintln(errOut, "Usage: mping completion bash|zsh|fish")
		return 1
	}
	fmt.Fprint(out, script)
	return 0
}

// runCompleteInterfaces implements the hidden `mping __complete-interfaces`
// helper the generated scripts call for -I/--interface value completion. It
// lists network interface names one per line via the netInterfaces seam
// (cmd/main/netdetect.go), the same injection point getInterfaceMTU uses.
func runCompleteInterfaces(out io.Writer) int {
	ifaces, err := netInterfaces()
	if err != nil {
		return 1
	}
	for _, iface := range ifaces {
		fmt.Fprintln(out, iface.Name)
	}
	return 0
}
