package configloader

import (
	"errors"
	"fmt"
	"net"
	"os"
	"reflect"
	"time"
	"unicode"

	"github.com/hashicorp/go-multierror"
	"github.com/joho/godotenv"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type Option interface {
	pflag.Value
	Parse(interface{}) (interface{}, error)
}

var (
	optionType     = reflect.TypeOf((*Option)(nil)).Elem()
	pflagValueType = reflect.TypeOf((*pflag.Value)(nil)).Elem()
)

func optionDecodeHook() mapstructure.DecodeHookFunc {
	return func(from, to reflect.Value) (interface{}, error) {
		if from.Type() == to.Type() {
			return from.Interface(), nil
		}
		if option, ok := castTo(to, optionType); ok {
			return option.(Option).Parse(from.Interface())
		}
		return from.Interface(), nil
	}
}

func convertFieldName(name string, delim rune, replaceKeyDelim bool, conv func(rune) rune) string {
	var result []rune
	prevUpper := false
	for _, r := range name {
		if r == keyDelimRune {
			prevUpper = false
			if replaceKeyDelim {
				result = append(result, delim)
			} else {
				result = append(result, keyDelimRune)
			}
		} else if unicode.IsUpper(r) {
			if !prevUpper {
				prevUpper = true
				if len(result) > 0 {
					result = append(result, delim)
				}
			}
			result = append(result, conv(r))
		} else {
			prevUpper = false
			result = append(result, conv(r))
		}
	}
	return string(result)
}

func deref(v reflect.Value) reflect.Value {
	if v.Type().Kind() == reflect.Ptr {
		return deref(v.Elem())
	}
	return v
}

func getStruct(v reflect.Value) reflect.Value {
	v = deref(v)
	if v.Type().Kind() == reflect.Struct {
		return v
	}
	return reflect.Value{}
}

func castTo(val reflect.Value, iface reflect.Type) (interface{}, bool) {
	val = deref(val)
	if val.Type().Implements(iface) {
		return val.Interface(), true
	}
	if val.CanAddr() {
		val = val.Addr()
		if val.Type().Implements(iface) {
			return val.Interface(), true
		}
	}
	return nil, false
}

type fieldOpts struct {
	name                 string
	flagName             string
	shortFlagName        string
	usage                string
	envName              string
	envNameSetExplicitly bool
	configName           string
	defaultValue         reflect.Value
}

type prefixes struct{ field, flag, env, config string }

func (p *prefixes) apply(o *fieldOpts) {
	o.name = p.field + o.name
	o.flagName = p.flag + o.flagName
	o.envName = p.env + o.envName
	o.configName = p.config + o.configName
}

func (p *prefixes) extend(o *fieldOpts) {
	p.field += o.name
	p.flag += o.flagName
	p.env += o.envName
	p.config += o.configName
}

type fieldSet map[string]*fieldOpts

func (s fieldSet) Parse(struc reflect.Value) {
	for i := 0; i < struc.NumField(); i++ {
		s.parse(prefixes{}, struc, i)
	}
}

func (s fieldSet) parse(prefix prefixes, struc reflect.Value, fieldI int) {
	field := struc.Type().Field(fieldI)
	if !field.IsExported() {
		return
	}
	val := struc.Field(fieldI)
	o := &fieldOpts{
		name:         field.Name,
		defaultValue: val,
	}
	nested := getStruct(val)
	if nested.IsValid() {
		if _, ok := castTo(val, optionType); ok {
			nested = reflect.Value{}
		} else {
			o.name += keyDelimString
		}
	}
	if shorthand, ok := field.Tag.Lookup("short"); ok {
		o.shortFlagName = shorthand
	}
	if usage, ok := field.Tag.Lookup("usage"); ok {
		o.usage = usage
	}
	if flagName, ok := field.Tag.Lookup("flag"); ok {
		o.flagName = flagName
	}
	if o.flagName == "" {
		o.flagName = convertFieldName(o.name, '-', true, unicode.ToUpper)
	}
	if envName, ok := field.Tag.Lookup("env"); ok {
		o.envName = envName
		o.envNameSetExplicitly = true
	}
	if o.envName == "" {
		o.envName = convertFieldName(o.name, '_', true, unicode.ToUpper)
		o.envNameSetExplicitly = false
	}
	if configName, ok := field.Tag.Lookup("config"); ok {
		o.configName = configName
	}
	if o.configName == "" {
		o.configName = convertFieldName(o.name, '_', false, unicode.ToLower)
	}
	if nested.IsValid() {
		o.configName += keyDelimString
		prefix.extend(o)
		for i := 0; i < nested.Type().NumField(); i++ {
			s.parse(prefix, nested, i)
		}
	} else {
		prefix.apply(o)
		s[o.name] = o
	}
}

func registerFlag(flags *pflag.FlagSet, opts *fieldOpts) (*pflag.Flag, error) {
	switch val := opts.defaultValue.Interface().(type) {
	case int:
		flags.IntP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case int8:
		flags.Int8P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case int16:
		flags.Int16P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case int32:
		flags.Int32P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case int64:
		flags.Int64P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case uint:
		flags.UintP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case uint8:
		flags.Uint8P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case uint16:
		flags.Uint16P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case uint32:
		flags.Uint32P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case uint64:
		flags.Uint64P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case float32:
		flags.Float32P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case float64:
		flags.Float64P(opts.flagName, opts.shortFlagName, val, opts.usage)
	case bool:
		flags.BoolP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case string:
		flags.StringP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case time.Duration:
		flags.DurationP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case net.IP:
		flags.IPP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case net.IPNet:
		flags.IPNetP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case net.IPMask:
		flags.IPMaskP(opts.flagName, opts.shortFlagName, val, opts.usage)

	case []int:
		flags.IntSliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []int32:
		flags.Int32SliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []int64:
		flags.Int64SliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []uint:
		flags.UintSliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []float32:
		flags.Float32SliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []float64:
		flags.Float64SliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []bool:
		flags.BoolSliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []string:
		flags.StringSliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []time.Duration:
		flags.DurationSliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	case []net.IP:
		flags.IPSliceP(opts.flagName, opts.shortFlagName, val, opts.usage)
	default:
		pflagVal, ok := castTo(opts.defaultValue, pflagValueType)
		if !ok {
			return nil, fmt.Errorf("unsupported field type: %T", val)
		}
		flags.VarP(pflagVal.(pflag.Value), opts.flagName, opts.shortFlagName, opts.usage)
	}
	return flags.Lookup(opts.flagName), nil
}

type Loader struct {
	LoadFromArgs bool
	Args         []string

	LoadFromEnv  bool
	EnvFilePaths []string
	EnvPrefix    string

	LoadFromConfigFile  bool
	ConfigPath          string
	ConfigType          string
	ConfigArg           string
	ConfigArgShorthand  string
	ConfigEnv           string
	ConfigEnvOmitPrefix bool

	vip *viper.Viper
}

// The non-printable characters should ensure that these don't conflict with any
// sensible user-supplied values.
const (
	configPathViperKey  = "_\u0007_CONFIG_PATH_\u0007_"
	mapstructureTagName = "_\u0007_MAPSTRUCTURE_TAG_\u0007_"
	keyDelimRune        = '\u0000'
	keyDelimString      = string(keyDelimRune)
)

func New() *Loader {
	return &Loader{
		LoadFromArgs: true,
		Args:         os.Args,

		LoadFromEnv:  true,
		EnvFilePaths: []string{".env"},

		LoadFromConfigFile: true,
		ConfigArg:          "config",
		ConfigEnv:          "CONFIG",
	}
}

func (l *Loader) Load(dst interface{}) error {
	dstVal := reflect.ValueOf(dst)
	if dstVal.Kind() != reflect.Ptr || dstVal.Elem().Kind() != reflect.Struct {
		return fatalErr(errors.New("dst must be a pointer to a struct"))
	}
	fieldSet := fieldSet{}
	fieldSet.Parse(dstVal.Elem())
	l.vip = viper.NewWithOptions(viper.KeyDelimiter(keyDelimString))
	l.setDefaults(fieldSet)
	var errs *multierror.Error
	if l.LoadFromArgs {
		errs = multierror.Append(errs, l.loadFromArgs(fieldSet))
	}
	if l.LoadFromEnv {
		errs = multierror.Append(errs, l.loadFromEnv(fieldSet))
	}
	if l.LoadFromConfigFile {
		errs = multierror.Append(errs, l.loadFromConfigFile(fieldSet))
	}
	err := l.vip.Unmarshal(dst, func(c *mapstructure.DecoderConfig) {
		c.TagName = mapstructureTagName
		c.DecodeHook = mapstructure.ComposeDecodeHookFunc(
			optionDecodeHook(),
			mapstructure.StringToTimeDurationHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
		)
	})
	return multierror.Append(errs, fatalErr(err)).ErrorOrNil()
}

func (l *Loader) setDefaults(opts fieldSet) {
	for _, opt := range opts {
		l.vip.SetDefault(opt.name, opt.defaultValue.Interface())
	}
}

func (l *Loader) loadFromArgs(opts fieldSet) error {
	if len(l.Args) == 0 {
		return argsErr(errors.New("program name is missing from Args"))
	}
	flags := pflag.NewFlagSet(l.Args[0], pflag.ContinueOnError)
	for _, opt := range opts {
		flag, err := registerFlag(flags, opt)
		if err != nil {
			return argsErr(err)
		}
		l.vip.BindPFlag(opt.name, flag)
	}
	if l.LoadFromConfigFile && l.ConfigArg != "" && flags.Lookup(l.ConfigArg) == nil {
		flags.StringP(l.ConfigArg, l.ConfigArgShorthand, l.ConfigPath, "Config file path")
		l.vip.BindPFlag(configPathViperKey, flags.Lookup(l.ConfigArg))
	}
	return argsErr(flags.Parse(l.Args[1:]))
}

func (l *Loader) loadFromEnv(opts fieldSet) error {
	prefix := l.EnvPrefix
	if prefix != "" {
		prefix += "_"
	}
	for _, opt := range opts {
		if opt.envNameSetExplicitly {
			l.vip.BindEnv(opt.name, opt.envName)
		} else {
			l.vip.BindEnv(opt.name, prefix+opt.envName)
		}
	}
	if l.LoadFromConfigFile && l.ConfigEnv != "" {
		if l.ConfigEnvOmitPrefix {
			l.vip.BindEnv(configPathViperKey, l.ConfigEnv)
		} else {
			l.vip.BindEnv(configPathViperKey, prefix+l.ConfigEnv)
		}
	}
	l.vip.AllowEmptyEnv(true)
	var errs *multierror.Error
	if len(l.EnvFilePaths) > 0 {
		for _, path := range l.EnvFilePaths {
			errs = multierror.Append(errs, envErr(godotenv.Load(path)))
		}
	}
	return errs.ErrorOrNil()
}

func (l *Loader) loadFromConfigFile(opts fieldSet) error {
	configPath := l.vip.GetString(configPathViperKey)
	if configPath == "" {
		return nil
	}
	l.vip.SetConfigFile(configPath)
	l.vip.SetConfigType(l.ConfigType)
	if err := l.vip.ReadInConfig(); err != nil {
		return fileErr(err)
	}
	for _, opt := range opts {
		if l.vip.IsSet(opt.configName) {
			l.vip.SetDefault(opt.name, l.vip.Get(opt.configName))
		}
	}
	return nil
}

type FatalErr struct{ error }

func fatalErr(err error) error {
	if err == nil {
		return nil
	}
	return multierror.Append(FatalErr{multierror.Prefix(err, "load:")})
}

func (e FatalErr) Unwrap() error              { return errors.Unwrap(e.error) }
func (e FatalErr) Is(target error) bool       { return errors.Is(e.error, target) }
func (e FatalErr) As(target interface{}) bool { return errors.As(e.error, target) }

type ArgsErr struct{ error }

func argsErr(err error) error {
	if err == nil {
		return nil
	}
	return ArgsErr{multierror.Prefix(err, "arguments:")}
}

func (e ArgsErr) Unwrap() error              { return errors.Unwrap(e.error) }
func (e ArgsErr) Is(target error) bool       { return errors.Is(e.error, target) }
func (e ArgsErr) As(target interface{}) bool { return errors.As(e.error, target) }

type EnvErr struct{ error }

func envErr(err error) error {
	if err == nil {
		return nil
	}
	return EnvErr{multierror.Prefix(err, "env:")}
}

func (e EnvErr) Unwrap() error              { return errors.Unwrap(e.error) }
func (e EnvErr) Is(target error) bool       { return errors.Is(e.error, target) }
func (e EnvErr) As(target interface{}) bool { return errors.As(e.error, target) }

type FileErr struct{ error }

func fileErr(err error) error {
	if err == nil {
		return nil
	}
	return FileErr{multierror.Prefix(err, "config file:")}
}

func (e FileErr) Unwrap() error              { return errors.Unwrap(e.error) }
func (e FileErr) Is(target error) bool       { return errors.Is(e.error, target) }
func (e FileErr) As(target interface{}) bool { return errors.As(e.error, target) }
