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
	repeat   bool
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
			repeat:   f.Value.Type() == "stringSlice",
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
	b.WriteString("    local cur prev opts flag prefix candidate i value_expected=0\n")
	b.WriteString("    COMPREPLY=()\n")
	b.WriteString("    cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	b.WriteString("    prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n")
	b.WriteString(`
    if [[ "${COMP_WORDS[1]}" == completion ]] && (( COMP_CWORD > 1 )); then
        if (( COMP_CWORD == 2 )); then
            COMPREPLY=( $(compgen -W 'bash zsh fish --help' -- "$cur") )
        fi
        return 0
    fi
    for (( i=1; i<COMP_CWORD; i++ )); do
        [[ "${COMP_WORDS[i]}" == -- ]] && return 0
    done
    flag="$prev"
    prefix=""
`)
	b.WriteString("    case \"$prev\" in\n")
	for _, f := range flags {
		if f.takesArg {
			names := "--" + f.long
			if f.short != "" {
				names = "-" + f.short + "|" + names
			}
			fmt.Fprintf(&b, "        %s) value_expected=1 ;;\n", names)
		}
	}
	b.WriteString("    esac\n")
	b.WriteString(`    if (( value_expected )); then
        [[ "$cur" == = ]] && cur=""
    else
    # Bash normally splits '=' into its own COMP_WORDS entry. Also support
    # shells with '=' removed from COMP_WORDBREAKS.
    if [[ "$cur" == --*=* ]]; then
        flag="${cur%%=*}"
        prefix="$flag="
        cur="${cur#*=}"
    elif [[ "$prev" == = ]] && (( COMP_CWORD > 1 )); then
        flag="${COMP_WORDS[COMP_CWORD-2]}"
    elif [[ "$cur" == = ]]; then
        cur=""
    elif [[ "$prev" == --*= ]]; then
        flag="${prev%=}"
    fi
`)
	b.WriteString("    case \"$cur\" in\n")
	for _, f := range flags {
		if f.short != "" && f.takesArg {
			fmt.Fprintf(&b, "        -%s?*) flag=-%s; prefix=-%s; cur=\"${cur:2}\" ;;\n", f.short, f.short, f.short)
		}
	}
	b.WriteString("    esac\n")
	b.WriteString("    fi\n")

	var optWords []string
	for _, f := range flags {
		optWords = append(optWords, "--"+f.long)
		if f.short != "" {
			optWords = append(optWords, "-"+f.short)
		}
	}
	b.WriteString("    opts=\"" + strings.Join(optWords, " ") + "\"\n\n")

	b.WriteString("    case \"$flag\" in\n")
	for _, f := range flags {
		hint := valueHint(f)
		if hint == "" {
			continue
		}
		names := "--" + f.long
		if f.short != "" {
			names = "-" + f.short + "|" + names
		}
		switch hint {
		case "file":
			b.WriteString("        " + names + ")\n")
			b.WriteString("            while IFS= read -r candidate; do\n")
			b.WriteString("                COMPREPLY+=( \"$prefix$candidate\" )\n")
			b.WriteString("            done < <(compgen -f -- \"$cur\")\n")
			b.WriteString("            return 0\n")
			b.WriteString("            ;;\n")
		case "interface":
			b.WriteString("        " + names + ")\n")
			b.WriteString("            while IFS= read -r candidate; do\n")
			b.WriteString("                [[ \"$candidate\" == \"$cur\"* ]] && COMPREPLY+=( \"$prefix$candidate\" )\n")
			b.WriteString("            done < <(\"${COMP_WORDS[0]}\" __complete-interfaces 2>/dev/null)\n")
			b.WriteString("            return 0\n")
			b.WriteString("            ;;\n")
		case "none":
			b.WriteString("        " + names + ") return 0 ;;\n")
		}
	}
	b.WriteString("    esac\n\n")

	b.WriteString("    if (( COMP_CWORD == 1 )); then\n")
	b.WriteString("        opts=\"completion $opts\"\n")
	b.WriteString("    fi\n")
	b.WriteString("    if [[ \"$cur\" == -* ]] || (( COMP_CWORD == 1 )); then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"$opts\" -- \"$cur\") )\n")
	b.WriteString("        return 0\n")
	b.WriteString("    fi\n")
	b.WriteString("}\n")
	b.WriteString("complete -o filenames -F _mping mping\n")
	return b.String()
}

func zshCompletion(flags []flagInfo) string {
	var b strings.Builder
	b.WriteString("#compdef mping\n")
	b.WriteString("# zsh completion for mping\n")
	b.WriteString("# Install after compinit: source <(mping completion zsh)\n\n")
	b.WriteString(`_mping_interfaces() {
    local -a interfaces
    interfaces=("${(@f)$("${words[1]}" __complete-interfaces 2>/dev/null)}")
    _describe 'network interface' interfaces
}

`)
	b.WriteString("_mping() {\n")
	b.WriteString(`    local -a shells commands
    shells=(bash zsh fish)
    commands=('completion:generate shell completion script')
    if [[ "${words[2]}" == completion ]] && (( CURRENT > 2 )); then
        if (( CURRENT == 3 )); then
            _describe 'shell' shells
            _arguments '--help[show completion usage]' '-h[show completion usage]'
        fi
        return
    fi
    if (( CURRENT == 2 )); then
        _describe 'command' commands
    fi
`)
	b.WriteString("    _arguments -s -S \\\n")
	for _, f := range flags {
		spec := zshArgSpec(f)
		b.WriteString("        " + spec + " \\\n")
	}
	b.WriteString("        '*:host:_hosts'\n")
	b.WriteString("}\n\n")
	b.WriteString(`if [[ "${funcstack[1]}" == _mping ]]; then
    _mping "$@"
else
    compdef _mping mping
fi
`)
	return b.String()
}

// zshArgSpec renders one flag's `_arguments` spec line, e.g.:
//
//	'(-i --interval)-i+[ping interval in ms]:value:'
func zshArgSpec(f flagInfo) string {
	desc := zshEscape(f.usage)

	action := ""
	switch valueHint(f) {
	case "file":
		action = ":file:_files"
	case "interface":
		action = ":interface:_mping_interfaces"
	case "none":
		action = ":value:"
	}
	exclude, repeat := "", ""
	if f.repeat {
		repeat = "*"
	} else if f.short != "" {
		exclude = fmt.Sprintf("(-%s --%s)", f.short, f.long)
	}
	longName, shortName := "--"+f.long, "-"+f.short
	if f.takesArg {
		longName += "="
		shortName += "+"
	}
	suffix := "[" + desc + "]" + action
	spec := shellQuote(exclude + repeat + longName + suffix)
	if f.short != "" {
		spec += " " + shellQuote(exclude+repeat+shortName+suffix)
	}
	return spec
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// zshEscape escapes characters that are meaningful inside a zsh _arguments
// spec string ('[...]' description field): ':' separates the description
// from the action, and unescaped '[' / ']' would prematurely close the
// description field.
func zshEscape(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
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
	b.WriteString(`function __mping_completion_command
    set -l tokens (commandline -opc)
    test (count $tokens) -ge 2; and test "$tokens[2]" = completion
end

complete -c mping -f
complete -c mping -n '__fish_use_subcommand' -a completion -d 'generate shell completion script'
complete -c mping -n '__mping_completion_command; and test (count (commandline -opc)) -eq 2' -a 'bash zsh fish'
complete -c mping -n '__mping_completion_command' -s h -l help -d 'show completion usage'
`)
	for _, f := range flags {
		var line strings.Builder
		line.WriteString("complete -c mping -n 'not __mping_completion_command'")
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
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
}

// runCompletion implements `mping completion <shell>`, writing the generated
// script to out on success or a usage message to errOut on failure.
func runCompletion(args []string, out, errOut io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(out, "Usage: mping completion bash|zsh|fish")
		return 0
	}
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
	if _, err := fmt.Fprint(out, script); err != nil {
		fmt.Fprintf(errOut, "Error writing completion script: %v\n", err)
		return 1
	}
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
