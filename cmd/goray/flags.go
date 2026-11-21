package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

type usageError struct {
	message string
}

func (e *usageError) Error() string { return e.message }

func usagef(format string, args ...any) error {
	return &usageError{message: fmt.Sprintf(format, args...)}
}

// quietFlagSet keeps the standard library parser from writing its own usage
// text; goray prints aligned help and concise errors itself.
func quietFlagSet(fs *flag.FlagSet) {
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
}

// parseFlagSet reorders flags around positional arguments, then parses them.
// Go's flag package stops at the first positional, which makes commands like
// "goray export html . -o report.html" surprising.
func parseFlagSet(fs *flag.FlagSet, args []string) ([]string, error) {
	normalized, err := normalizeArgs(fs, args)
	if err != nil {
		return nil, err
	}
	if err := fs.Parse(normalized); err != nil {
		if err == flag.ErrHelp {
			return nil, usagef("help requested for %s", fs.Name())
		}
		return nil, usagef("%s", err)
	}
	return fs.Args(), nil
}

func normalizeArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	bools := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		bools[f.Name] = isBoolFlag(f)
	})
	bools["h"] = true
	bools["help"] = true

	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	endOfFlags := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if endOfFlags {
			positionals = append(positionals, arg)
			continue
		}
		if arg == "--" {
			endOfFlags = true
			continue
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			positionals = append(positionals, arg)
			continue
		}
		name, inlineValue := splitFlagToken(arg)
		isBool, known := bools[name]
		if !known {
			return nil, usagef("unknown flag %q", arg)
		}
		flags = append(flags, arg)
		if isBool || inlineValue {
			continue
		}
		if i+1 >= len(args) {
			return nil, usagef("flag needs an argument: %s", arg)
		}
		i++
		flags = append(flags, args[i])
	}

	out := make([]string, 0, len(flags)+1+len(positionals))
	out = append(out, flags...)
	out = append(out, "--")
	out = append(out, positionals...)
	return out, nil
}

func splitFlagToken(arg string) (name string, inlineValue bool) {
	token := strings.TrimPrefix(arg, "-")
	token = strings.TrimPrefix(token, "-")
	if i := strings.IndexByte(token, '='); i >= 0 {
		return token[:i], true
	}
	return token, false
}

func isBoolFlag(f *flag.Flag) bool {
	type boolFlag interface {
		IsBoolFlag() bool
	}
	value, ok := f.Value.(boolFlag)
	return ok && value.IsBoolFlag()
}

func wantsHelp(args []string, valueFlags map[string]bool) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return false
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			continue
		}
		name, inlineValue := splitFlagToken(arg)
		if name == "h" || name == "help" {
			return true
		}
		if valueFlags[name] && !inlineValue {
			i++
		}
	}
	return false
}

func singlePath(args []string, command string) (string, error) {
	if len(args) > 1 {
		return "", usagef("unexpected argument %q: %s accepts at most one path", args[1], command)
	}
	if len(args) == 1 {
		return args[0], nil
	}
	return ".", nil
}
