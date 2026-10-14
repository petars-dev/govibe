package codegraph

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/cayleygraph/quad"
	"gopkg.in/yaml.v3"
)

// sensitiveRe marks a key or environment variable whose value must never reach
// the graph: no default, no value.
var sensitiveRe = regexp.MustCompile(`(?i)key|secret|password|token|dsn`)

// envName is what the config loader accepts as a variable in `${NAME:default}`.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// configIndex is the settings the project's settings files declare, the Go struct
// fields they bind to, and which declared names the code reads.
type configIndex struct {
	keys  []*cfgKey // declared and referenced leaf keys, sorted by key
	envs  []*envInfo
	uses  []cfgUse // function or package -> key
	reads []envUse // function -> environment variable

	keyByPath map[string]*cfgKey
	envByName map[string]*envInfo
	groups    map[string]bool // dotted paths that are not leaves
	file      string          // the settings file walkYAML is reading
	origin    string          // whose file that is: the project's or the framework's (its manifest)
}

type cfgKey struct {
	key       string
	line      int
	env       string // environment variable, "" for a static value
	def       string
	hasDef    bool
	sensitive bool
	field     string // Config.Server.Port, when bound to a struct field
	declared  bool   // present in a settings file
	file      string // the settings file that declares it ("" for a framework setting)
	origin    string // whose setting it is: of the file that declares it last
	// framework is set when the framework's own defaults declare the key too: an application
	// that overrides one does not read it (the framework does), so it is not "declared and unused".
	framework bool
}

type envInfo struct {
	name      string
	def       string
	hasDef    bool
	sensitive bool
	declared  bool // appears as ${NAME} in env.yml
	origin    string
	seen      bool
}

type cfgUse struct {
	fn  quad.IRI // "" for a package-level use
	dir string
	key string
}

type envUse struct {
	fn   quad.IRI
	name string
}

// configStats are the settings files the project has, of those its profile lists
// (lowest layer first, as the config loader merges them: a key declared again by a
// later file replaces the earlier declaration).
func (w *world) configStats() []sourceFile {
	prof, err := w.profile()
	if err != nil {
		return nil
	}
	var out []sourceFile
	for _, rel := range prof.Config {
		if sf, ok := statRegular(w.root, rel, maxConfigBytes); ok {
			out = append(out, sf)
		}
	}
	return out
}

func (w *world) configIndex() (*configIndex, error) {
	return w.cfgIdx.get(func() (*configIndex, error) {
		ci, err := w.code()
		if err != nil {
			return nil, fmt.Errorf("code index: %w", err)
		}
		return buildConfigIndex(w, ci)
	})
}

func buildConfigIndex(w *world, ci *codeIndex) (*configIndex, error) {
	cx := &configIndex{
		keyByPath: map[string]*cfgKey{},
		envByName: map[string]*envInfo{},
		groups:    map[string]bool{},
	}
	if len(w.configStats()) == 0 {
		// No settings file: the layer is skipped and writes nothing, so nothing may resolve
		// against keys it does not have (the framework's, from the manifest, included).
		return cx, nil
	}

	// The framework's own settings lie under the project's: its manifest first, then
	// the project's files, each declaration replacing the one below it.
	if mf := w.x.opts.Manifest; mf != nil && len(mf.ConfigYAML) > 0 {
		var doc yaml.Node
		if err := yaml.Unmarshal(mf.ConfigYAML, &doc); err != nil {
			return nil, fmt.Errorf("framework manifest: %w", err)
		}
		if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
			cx.origin = OriginFramework
			cx.walkYAML(doc.Content[0], "")
		}
	}
	cx.origin = OriginApp
	for _, sf := range w.configStats() {
		b, err := os.ReadFile(sf.abs)
		if err != nil {
			return nil, err
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", sf.rel, err)
		}
		if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
			cx.file = sf.rel
			cx.walkYAML(doc.Content[0], "")
		}
	}

	goToKey, keyToGo, fieldGroups := configFields(ci)
	if mf := w.x.opts.Manifest; mf != nil {
		// The framework's typed Config: a chain `cfg.Server.Port` reads `server.port`.
		// A struct of the project's own with the same field wins.
		for goPath, key := range mf.ConfigFields {
			if _, own := goToKey[goPath]; !own {
				goToKey[goPath], keyToGo[key] = key, "Config."+goPath
			}
		}
	}
	for k, f := range keyToGo {
		if kk := cx.keyByPath[k]; kk != nil {
			kk.field = f
		}
	}
	for g := range fieldGroups {
		cx.groups[g] = true
	}

	// Usage: selector chains on the config struct, and dynamic lookups by key.
	seenUse := map[string]bool{}
	use := func(fn quad.IRI, dir, path string) {
		for _, k := range cx.resolveKeys(path, keyToGo, fieldGroups) {
			key := string(fn) + "\x00" + dir + "\x00" + k.key
			if !seenUse[key] {
				seenUse[key] = true
				cx.uses = append(cx.uses, cfgUse{fn: fn, dir: dir, key: k.key})
			}
		}
	}
	for _, ch := range ci.cfgChains {
		// The longest prefix of the chain that names a field: cfg.Server.Port.String
		// is a use of server.port.
		for n := len(ch.names); n > 0; n-- {
			if path, ok := goToKey[strings.Join(ch.names[:n], ".")]; ok {
				use(ch.fn, ch.dir, path)
				break
			}
		}
	}
	for _, call := range ci.cfgCalls {
		use(call.fn, call.dir, strings.TrimSuffix(call.key, ".*"))
	}
	for _, er := range ci.envReads {
		cx.env(er.name) // read but not necessarily declared
		if er.fn != "" {
			cx.reads = append(cx.reads, envUse{fn: er.fn, name: er.name})
		}
	}

	for _, k := range cx.keyByPath {
		cx.keys = append(cx.keys, k)
	}
	sort.Slice(cx.keys, func(i, j int) bool { return cx.keys[i].key < cx.keys[j].key })
	for _, e := range cx.envByName {
		if e.sensitive {
			e.def, e.hasDef = "", false
		}
		cx.envs = append(cx.envs, e)
	}
	sort.Slice(cx.envs, func(i, j int) bool { return cx.envs[i].name < cx.envs[j].name })
	return cx, nil
}

// walkYAML records every scalar below a mapping as a leaf key.
func (cx *configIndex) walkYAML(n *yaml.Node, prefix string) {
	if n.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		path := k.Value
		if prefix != "" {
			path = prefix + "." + k.Value
		}
		if v.Kind == yaml.MappingNode {
			cx.groups[path] = true
			cx.walkYAML(v, path)
			continue
		}
		key := &cfgKey{key: path, line: k.Line, declared: true, file: cx.file, origin: cx.origin}
		if cx.origin == OriginFramework {
			key.file, key.line = "", 0 // the framework's file is not in this tree
		}
		sensitive := sensitiveRe.MatchString(path)
		if v.Kind == yaml.ScalarNode {
			if name, def, ok := placeholder(v.Value); ok {
				// A key is sensitive if either its own name or its variable's is.
				sensitive = sensitive || sensitiveRe.MatchString(name)
				key.env = name
				e := cx.env(name)
				if cx.origin == OriginApp || !e.seen {
					e.origin = cx.origin // the project's own declaration takes the variable over
				}
				e.seen = true
				e.declared = true
				e.sensitive = e.sensitive || sensitive
				if def != "" && !e.hasDef {
					e.def, e.hasDef = def, true
				}
				if def != "" {
					key.def, key.hasDef = def, true
				}
			} else if v.Value != "" {
				key.def, key.hasDef = v.Value, true // a static value
			}
		}
		// A sensitive setting has no default and no value in the graph, ever.
		key.sensitive = sensitive
		if sensitive {
			key.def, key.hasDef = "", false
		}
		if prev := cx.keyByPath[path]; prev != nil && prev.framework || cx.origin == OriginFramework {
			key.framework = true
		}
		cx.keyByPath[path] = key
	}
}

// placeholder parses `${NAME}` or `${NAME:default}` the way config.Load does.
func placeholder(s string) (name, def string, ok bool) {
	if !strings.HasPrefix(s, "${") || !strings.HasSuffix(s, "}") {
		return "", "", false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(s, "${"), "}")
	name, def, _ = strings.Cut(inner, ":")
	if !envName.MatchString(name) {
		return "", "", false
	}
	return name, def, true
}

func (cx *configIndex) env(name string) *envInfo {
	e := cx.envByName[name]
	if e == nil {
		e = &envInfo{name: name, sensitive: sensitiveRe.MatchString(name), origin: OriginApp}
		cx.envByName[name] = e
	}
	return e
}

// resolveKeys returns the keys a use of path means: the leaf itself, every leaf
// below a group, or, for a path the code names that env.yml does not declare, a
// new undeclared key.
func (cx *configIndex) resolveKeys(path string, keyToGo map[string]string, fieldGroups map[string]bool) []*cfgKey {
	if k := cx.keyByPath[path]; k != nil {
		return []*cfgKey{k}
	}
	var out []*cfgKey
	for _, k := range cx.keyByPath {
		if strings.HasPrefix(k.key, path+".") {
			out = append(out, k)
		}
	}
	if len(out) == 0 && !cx.groups[path] && !fieldGroups[path] {
		// Read but not declared.
		k := &cfgKey{key: path, sensitive: sensitiveRe.MatchString(path), field: keyToGo[path], origin: OriginApp}
		cx.keyByPath[path] = k
		return []*cfgKey{k}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// configFields maps the Go fields of the application's `Config` struct (the
// struct named Config in the package named config) to their yaml keys, from the
// yaml struct tags: "Server.Port" -> "server.port".
func configFields(ci *codeIndex) (goToKey, keyToGo map[string]string, groups map[string]bool) {
	goToKey, keyToGo, groups = map[string]string{}, map[string]string{}, map[string]bool{}
	dirs := ci.pkgNamed("config")
	for _, d := range dirs {
		t := ci.typeNamed(d, "Config")
		if t == nil || t.fields == nil {
			continue
		}
		var walk func(fs []fieldInfo, goPrefix, keyPrefix string, depth int)
		walk = func(fs []fieldInfo, goPrefix, keyPrefix string, depth int) {
			if depth > 6 {
				return
			}
			for _, f := range fs {
				name := strings.Split(f.tag.Get("yaml"), ",")[0]
				if name == "-" {
					continue
				}
				if name == "" {
					name = strings.ToLower(f.name)
				}
				goPath, keyPath := goPrefix+f.name, keyPrefix+name
				goToKey[goPath] = keyPath
				keyToGo[keyPath] = "Config." + goPath
				inner := f.inline
				if inner == nil {
					if nt := ci.typeNamed(d, strings.TrimLeft(f.typ, "*")); nt != nil && nt.fields != nil {
						inner = nt.fields
					}
				}
				if inner != nil {
					groups[keyPath] = true
					walk(inner, goPath+".", keyPath+".", depth+1)
				}
			}
		}
		walk(t.fields, "", "", 0)
		break
	}
	return goToKey, keyToGo, groups
}

type configLayer struct{}

func (configLayer) name() string   { return "config" }
func (configLayer) deps() []string { return []string{"code"} }

func (configLayer) fingerprint(w *world) (string, error) {
	if sfs := w.configStats(); len(sfs) > 0 {
		return fingerprint(sfs), nil
	}
	return "absent", nil
}

func (configLayer) build(ctx context.Context, w *world) (*layerOutput, error) {
	prof, err := w.profile()
	if err != nil {
		return nil, err
	}
	if len(w.configStats()) == 0 {
		// Not the same as a file with no settings in it: with nothing to read, a rule
		// about declared settings has not looked, and must not read as clean.
		return &layerOutput{
			skipped: fmt.Sprintf("no settings file found (looked for %s); set `config:` in govibe.yml if the project keeps it elsewhere", strings.Join(prof.Config, ", ")),
			clear:   true,
		}, nil
	}
	cx, err := w.configIndex()
	if err != nil {
		return nil, err
	}
	ci, err := w.code()
	if err != nil {
		return nil, fmt.Errorf("code index: %w", err)
	}

	set := newQuadSet("config")
	for _, k := range cx.keys {
		id := cfgID(k.key)
		set.add(id, PredType, KindConfigKey)
		set.str(id, PredName, k.key)
		set.flag(id, PredSensitive, k.sensitive)
		set.flag(id, PredDeclared, k.declared)
		set.str(id, PredOrigin, k.origin)
		if k.framework {
			set.flag(id, PredFrameworkKey, true)
		}
		if k.declared && k.file != "" {
			set.str(id, PredPath, k.file)
			set.num(id, PredLine, k.line)
		}
		if k.hasDef && !k.sensitive {
			set.str(id, PredDefault, k.def)
		}
		if k.field != "" {
			set.str(id, PredField, k.field)
		}
		if k.env != "" {
			set.add(id, PredReadsEnv, envID(k.env))
		}
	}
	for _, e := range cx.envs {
		id := envID(e.name)
		set.add(id, PredType, KindEnvVar)
		set.str(id, PredName, e.name)
		set.flag(id, PredSensitive, e.sensitive)
		set.flag(id, PredDeclared, e.declared)
		set.str(id, PredOrigin, e.origin)
		if e.hasDef && !e.sensitive {
			set.str(id, PredDefault, e.def)
		}
	}
	for _, u := range cx.uses {
		if u.fn != "" {
			set.add(u.fn, PredUsesConfig, cfgID(u.key))
		}
		if ci.pkgs[u.dir] != nil {
			set.add(pkgID(u.dir), PredUsesConfig, cfgID(u.key))
		}
	}
	for _, r := range cx.reads {
		set.add(r.fn, PredReadsEnv, envID(r.name))
	}
	return &layerOutput{quads: set.quads}, nil
}
