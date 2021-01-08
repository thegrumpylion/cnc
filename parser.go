package cli

import (
	"context"
	"encoding"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/scylladb/go-set/strset"
)

var defaultParser = NewParser()

// Parser is the cli parser
type Parser struct {
	tags           StructTags
	roots          []reflect.Value
	cmds           map[string]*command
	enums          map[reflect.Type]map[string]interface{}
	execTree       []interface{}
	globalsEnabled bool
	strategy       OnErrorStrategy
	argCase        Case
	envCase        Case
	cmdCase        Case
	argSplicer     Splicer
	envSplicer     Splicer
}

// NewParser create new parser
func NewParser(opts ...ParserOption) *Parser {
	p := &Parser{
		cmds:       map[string]*command{},
		enums:      map[reflect.Type]map[string]interface{}{},
		argCase:    CaseCamelLower,
		envCase:    CaseSnakeUpper,
		cmdCase:    CaseLower,
		argSplicer: SplicerDot,
		envSplicer: SplicerUnderscore,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *Parser) addRoot(in interface{}) *path {
	p.roots = append(p.roots, reflect.ValueOf(in))
	return &path{
		root: &p.roots[len(p.roots)-1],
	}
}

// NewRootCommand add new root command to defaultParser
func NewRootCommand(name string, arg interface{}) {
	defaultParser.NewRootCommand(name, arg)
}

// NewRootCommand add new root command to this parser
func (p *Parser) NewRootCommand(name string, arg interface{}) {
	t := reflect.TypeOf(arg)
	if t.Kind() != reflect.Ptr && t.Elem().Kind() != reflect.Struct {
		panic("not ptr to struct")
	}
	path := p.addRoot(arg)
	c := &command{
		path:   path,
		name:   name,
		subcmd: map[string]*command{},
		flags:  newFlagSet(),
	}
	p.cmds[name] = c
	p.walkStruct(c, t, path, "", "", false, strset.New())
}

// Eval marshal string args to struct using the defaultParser
func Eval(args []string) error {
	return defaultParser.Eval(args)
}

// Eval marshal string args to struct
func (p *Parser) Eval(args []string) error {

	values := map[*argument][]string{}

	c, ok := p.cmds[args[0]]
	// try base path
	if !ok {
		c, ok = p.cmds[filepath.Base(args[0])]
		if !ok {
			return ErrCommandNotFound(args[0])
		}
	}

	p.execTree = append(p.execTree, c.path.Get())

	args = args[1:]
	positional := false
	positionals := []string{}
	for i := 0; i < len(args); i++ {

		arg := args[i]

		if arg == "--" {
			positional = true
			continue
		}

		if positional {
			positionals = append(positionals, arg)
			continue
		}

		if !isFlag(arg) {
			if c.subcmd != nil {
				cc, ok := c.subcmd[arg]
				if !ok {
					return ErrCommandNotFound(arg)
				}
				if err := p.setValues(values); err != nil {
					return err
				}
				c = cc
				p.execTree = append(p.execTree, c.path.Get())
				continue
			}
			positionals = append(positionals, arg)
			continue
		}

		if arg == "-h" || arg == "--help" {
			// handle help
		}

		if arg == "--version" {
			// handle version
		}

		val := ""
		compositeFlag := false
		// get flag and value in case --flag=value
		if i := strings.Index(arg, "="); i != -1 {
			arg = arg[:i]
			val = arg[i+1:]
			compositeFlag = true
		}

		a := c.GetFlag(arg)
		if a == nil {
			return ErrNoSuchFlag(arg)
		}

		// handle arrays and slices
		if a.isArray || a.isSlice {
			if a.separate {
				// if is array and overflows
				if a.isArray && len(values[a]) == a.arrayLen {
					return errors.New("array over capacity")
				}
				if val == "" {
					val = args[i+1]
					i++
				}
				values[a] = append(values[a], val)
				continue
			}
			// clear array
			if a.isArray {
				for j := 0; j < a.arrayLen; j++ {
					val = args[i+1]
					if isFlag(val) {
						continue
					}
					values[a] = append(values[a], val)
					i++
				}
				continue
			}
			for _, val = range args[i+1:] {
				if isFlag(val) {
					continue
				}
				values[a] = append(values[a], val)
				i++
			}
			continue
		}

		// get the value in case --flag value
		if !compositeFlag {
			if a.isBool() {
				val = "true"
			} else {
				val = args[i+1]
				i++
			}
		}

		values[a] = []string{val}
	}

	if err := p.setValues(values); err != nil {
		return err
	}

	for _, a := range c.AllFlags() {
		if a.required && !a.isSet {
			return fmt.Errorf("required flag not set: %s", a.long)
		}
	}

	return nil
}

func (p *Parser) setValues(values map[*argument][]string) error {
	for a, s := range values {
		a.isSet = true
		// handle array
		if len(s) > 1 {
			if err := a.setArrayValue(s); err != nil {
				return err
			}
			continue
		}
		val := s[0]
		// handle encoding.TextUnmarshaler
		if tum, ok := a.path.Get().(encoding.TextUnmarshaler); ok {
			if err := tum.UnmarshalText([]byte(val)); err != nil {
				return err
			}
			continue
		}
		// handle enum
		if a.enum {
			em := p.enums[a.typ]
			a.setValue(em[strings.ToLower(val)])
			continue
		}
		// handle scalar
		if err := a.setScalarValue(val); err != nil {
			return err
		}
	}
	return nil
}

// OnErrorStrategy defines how errors are handled on execution
type OnErrorStrategy uint

const (
	// OnErrorBreak halt execution and return the error immediately
	OnErrorBreak OnErrorStrategy = iota
	// OnErrorPostRunners execute post runners in stack but break if post runner returns error.
	// LastErrorFromContext can be used to retrieve the error
	OnErrorPostRunners
	// OnErrorPostRunnersContinue execute post runners in stack ignoring errors. LastErrorFromContext
	// can be used to retrieve any error
	OnErrorPostRunnersContinue
	// OnErrorContinue ignore errors. LastErrorFromContext can be used to retrieve any error.
	OnErrorContinue
)

type lastErrorKey struct{}

// LastErrorFromContext get the last error in case the execution continues on errors
func LastErrorFromContext(ctx context.Context) error {
	return ctx.Value(lastErrorKey{}).(error)
}

// Execute the chain of commands
func Execute(ctx context.Context) error {
	return defaultParser.Execute(ctx)
}

// Execute the chain of commands
func (p *Parser) Execute(ctx context.Context) error {

	var err error
	lastCmd := len(p.execTree) - 1
	pPostRunners := []PersistentPostRunner{}

	for i, inf := range p.execTree {
		// PersistentPostRun pushed on a stack to run in a reverse order
		if rnr, ok := inf.(PersistentPostRunner); ok {
			pPostRunners = append([]PersistentPostRunner{rnr}, pPostRunners...)
		}
		// PersistentPreRun
		if rnr, ok := inf.(PersistentPreRunner); ok {
			err = rnr.PersistentPreRun(ctx)
			if err != nil {
				if !(p.strategy == OnErrorContinue) {
					break
				}
				ctx = context.WithValue(ctx, lastErrorKey{}, err)
			}
		}
		if i == lastCmd {
			// PreRun
			if rnr, ok := inf.(PreRunner); ok {
				err = rnr.PreRun(ctx)
				if err != nil {
					if !(p.strategy == OnErrorContinue) {
						break
					}
					ctx = context.WithValue(ctx, lastErrorKey{}, err)
				}
			}
			// Run
			if rnr, ok := inf.(Runner); ok {
				err = rnr.Run(ctx)
				if err != nil {
					if !(p.strategy == OnErrorContinue) {
						break
					}
					ctx = context.WithValue(ctx, lastErrorKey{}, err)
				}
			}
			// PostRun
			if rnr, ok := inf.(PostRunner); ok {
				err = rnr.PostRun(ctx)
				if err != nil {
					if !(p.strategy == OnErrorContinue) {
						break
					}
					ctx = context.WithValue(ctx, lastErrorKey{}, err)
				}
			}
		}
	}
	// check for error and strategy
	if err != nil && p.strategy == OnErrorBreak {
		return err
	}
	// PersistentPostRun
	for _, rnr := range pPostRunners {
		err = rnr.PersistentPostRun(ctx)
		if err != nil {
			if p.strategy == OnErrorPostRunners {
				return err
			}
			ctx = context.WithValue(ctx, lastErrorKey{}, err)
		}
	}
	return err
}

// RegisterEnum resgister an enum map to the default parser
func RegisterEnum(enumMap interface{}) {
	defaultParser.RegisterEnum(enumMap)
}

// RegisterEnum resgister an enum map. map must have string key and int/uint
// value. The value must also be a custom type e.g. type MyEnum uint32
func (p *Parser) RegisterEnum(enumMap interface{}) {
	v := reflect.ValueOf(enumMap)
	t := reflect.TypeOf(enumMap)
	if t.Kind() != reflect.Map {
		panic("enumMap must be a map")
	}

	// key is the string of enum
	if t.Key().Kind() != reflect.String {
		panic("enumMap key must be string")
	}

	// element is enum int/uint custom type
	te := t.Elem()
	if te.PkgPath() == "" {
		panic("enumMap element must be custom type")
	}
	if !(isInt(te) || isUint(te)) {
		panic("enumMap element must be int/uint")
	}

	enm := map[string]interface{}{}
	for _, k := range v.MapKeys() {
		enm[strings.ToLower(k.String())] = v.MapIndex(k).Interface()
	}

	p.enums[te] = enm
}

var textUnmarshaler = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()

func (p *Parser) walkStruct(c *command, t reflect.Type, pth *path, pfx, envpfx string, isArg bool, globals *strset.Set) {
	if isPtr(t) {
		t = t.Elem()
	}
	for i := 0; i < t.NumField(); i++ {

		// get field
		f := t.Field(i)
		fn := f.Name
		ft := f.Type

		// get and parse cli tag
		var tag *clitag
		tg := f.Tag.Get(p.tags.Cli)
		if tg == "-" {
			continue
		}
		tag = parseCliTag(tg)

		// compute arg name
		name := p.argCase.Parse(fn)
		if tag.long != "" {
			name = tag.long
		}
		if pfx != "" {
			name = p.argSplicer.Splice(pfx, name)
		}

		// compute env var name
		env := p.envCase.Parse(fn)
		if tag.long != "" {
			env = tag.env
		}
		if envpfx != "" {
			env = p.envSplicer.Splice(envpfx, env)
		}

		// create subpath for the current field
		spth := pth.Subpath(fn)

		if isStruct(ft) && !ft.Implements(textUnmarshaler) {
			// embedded struct parse as args of parent
			if f.Anonymous {
				p.walkStruct(c, ft, spth, pfx, envpfx, isArg, globals)
				continue
			}
			// we know is an arg so use the name as prefix
			if isArg {
				p.walkStruct(c, ft, spth, name, env, isArg, globals)
				continue
			}
			// is a ptr to struct but isArg in tag is set or
			// is normal struct so this is an arg
			if tag.isArg || !isPtr(ft) {
				p.walkStruct(c, ft, spth, name, env, true, globals)
				continue
			}
			// parse struct as a command
			cname := p.cmdCase.Parse(fn)
			if tag.cmd != "" {
				cname = tag.cmd
			}
			sc := c.AddSubcommand(cname, spth)
			p.walkStruct(sc, ft, spth, "", "", false, globals.Copy())
			continue
		}

		// check for global args propagation collision
		if p.globalsEnabled && tag.global {
			if globals.Has(name) {
				panic("global args propagation collision")
			}
			globals.Add(name)
		}

		// generate long and short flags
		long := "--" + name
		short := ""
		if tag.short != "" {
			if len(tag.short) != 1 {
				panic("wrong short tag: " + tag.short)
			}
			short = "-" + tag.short
		}

		// create arg and add to command
		a := &argument{
			path:       spth,
			typ:        ft,
			long:       long,
			short:      short,
			env:        env,
			required:   tag.required,
			positional: tag.positional,
			def:        f.Tag.Get(p.tags.Default),
			help:       f.Tag.Get(p.tags.Help),
		}
		c.AddArg(a)

		// get the underlaying type if pointer
		if isPtr(ft) {
			ft = ft.Elem()
		}

		if isArray(ft) {
			switch ft.Kind() {
			case reflect.Array:
				a.isArray = true
				a.arrayLen = ft.Len()
			case reflect.Slice:
				a.isSlice = true
				a.arrayLen = -1
			}
		}

		// check for enums
		if isInt(ft) || isUint(ft) {
			if _, ok := p.enums[ft]; ok {
				a.enum = true
			}
		}
	}
}

func isFlag(s string) bool {
	return strings.HasPrefix(s, "-") && strings.TrimLeft(s, "-") != ""
}
