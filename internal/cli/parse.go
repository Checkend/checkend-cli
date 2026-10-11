package cli

import (
	"fmt"
	"strings"
)

// Kind is the type of an option.
type Kind int

const (
	// String takes a value: --name value or --name=value.
	String Kind = iota
	// Bool takes no value.
	Bool
	// List takes a value and can be given more than once.
	List
)

type parsed struct {
	values map[string]string
	lists  map[string][]string
	bools  map[string]bool
	args   []string
}

// parse reads options and positional arguments, strictly: an unknown option, a missing value
// or a value for a flag is an error. Only -h has a short form.
func parse(argv []string, options map[string]Kind) (parsed, error) {
	p := parsed{values: map[string]string{}, lists: map[string][]string{}, bools: map[string]bool{}}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case arg == "--":
			p.args = append(p.args, argv[i+1:]...)
			return p, nil
		case arg == "-h":
			p.bools["help"] = true
		case strings.HasPrefix(arg, "--"):
			name, value, hasValue := strings.Cut(arg[2:], "=")
			kind, ok := options[name]
			if !ok || name == "" {
				return p, fmt.Errorf("Unknown option '--%s'.", name)
			}
			if kind == Bool {
				if hasValue {
					return p, fmt.Errorf("Option '--%s' does not take a value.", name)
				}
				p.bools[name] = true
				continue
			}
			if !hasValue {
				if i+1 >= len(argv) {
					return p, fmt.Errorf("Option '--%s <value>' needs a value.", name)
				}
				value = argv[i+1]
				if len(value) > 1 && strings.HasPrefix(value, "-") {
					return p, fmt.Errorf("Option '--%s' needs a value, not %q. For a value that starts with a dash, use --%s=%s.", name, value, name, value)
				}
				i++
			}
			if kind == List {
				p.lists[name] = append(p.lists[name], value)
			}
			p.values[name] = value
		case len(arg) > 1 && strings.HasPrefix(arg, "-"):
			return p, fmt.Errorf("Unknown option '%s'.", arg)
		default:
			p.args = append(p.args, arg)
		}
	}
	return p, nil
}
