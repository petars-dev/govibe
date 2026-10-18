package generate

import (
	"fmt"
	"go/format"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config makes the app's typed config package: a struct that mirrors the app's own
// section of env.yml (the section named after the app, or --section), with Load.
func Config(p *Project, o Options) (*Result, error) {
	section := o.Section
	if section == "" {
		section = strings.ReplaceAll(strings.ReplaceAll(p.Name, "-", "_"), ".", "_")
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(section) {
		return nil, fmt.Errorf("--section %q: a section is a lower-case key of env.yml", section)
	}
	tree, file := p.configSection(section)
	fields, leaves := configFields(tree, 1)
	data := struct {
		common
		Section, Fields, SampleYAML, SampleCheck string
	}{common: p.common(), Section: section, Fields: fields}
	data.SampleYAML = strconv.Quote(sampleYAML(section, tree))
	if len(leaves) > 0 {
		data.SampleCheck = fmt.Sprintf("got.%s != %s", leaves[0].field, leaves[0].sampleGo)
	}
	r := &Result{Kind: "config"}
	for _, t := range []struct{ tmpl, file string }{{"config", "config.go"}, {"config_test", "config_test.go"}} {
		b, err := render(t.tmpl, data)
		if err != nil {
			return nil, err
		}
		if b, err = format.Source(b); err != nil {
			return nil, fmt.Errorf("%s: %w", t.tmpl, err)
		}
		if err := p.create(r, "config/"+t.file, b, o); err != nil {
			return nil, err
		}
	}
	switch {
	case tree == nil:
		r.note("env.yml declares no %q section yet: add one (keys as ${ENV:default} placeholders), then add the fields to config.Config", section)
	default:
		r.note("%d keys of the %q section of %s are bound; keep the struct and the file in step", len(leaves), section, file)
	}
	r.note("read it in the wiring: cfg, err := config.Load(rt.Config)")
	return r, nil
}

// configSection reads the section from the last settings file of the profile that
// declares it; nil when none does.
func (p *Project) configSection(section string) (map[string]any, string) {
	files := p.Profile.Config
	for i := len(files) - 1; i >= 0; i-- {
		b, err := p.read(files[i])
		if err != nil {
			continue
		}
		var doc map[string]any
		if yaml.Unmarshal(b, &doc) != nil {
			continue
		}
		if sec, ok := doc[section].(map[string]any); ok {
			return sec, files[i]
		}
	}
	return nil, ""
}

// leaf is one scalar key of the section, as a struct field.
type leaf struct {
	field    string // Go path from the root struct: Smtp.Host
	sampleGo string // the sample value as a Go literal, for the test
}

var placeholderRe = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*(?::(.*))?\}$`)

// configFields renders the struct fields for a section, nested maps as nested structs.
func configFields(tree map[string]any, depth int) (string, []leaf) {
	keys := make([]string, 0, len(tree))
	for k := range tree {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	var leaves []leaf
	indent := strings.Repeat("\t", depth)
	for _, k := range keys {
		name := fieldName(k)
		if name == "" {
			continue
		}
		switch v := tree[k].(type) {
		case map[string]any:
			inner, sub := configFields(v, depth+1)
			fmt.Fprintf(&b, "%s%s struct {\n%s%s} `yaml:%q`\n", indent, name, inner, indent, k)
			for _, l := range sub {
				leaves = append(leaves, leaf{field: name + "." + l.field, sampleGo: l.sampleGo})
			}
		default:
			typ, sample := goType(v)
			fmt.Fprintf(&b, "%s%s %s `yaml:%q`\n", indent, name, typ, k)
			leaves = append(leaves, leaf{field: name, sampleGo: sample})
		}
	}
	return b.String(), leaves
}

// fieldName is the Go field for a key: smtp_host -> SmtpHost.
func fieldName(key string) string {
	n, err := ParseName(key)
	if err != nil {
		return ""
	}
	return n.Pascal
}

// goType is the Go type of a value as env.yml writes it (a placeholder's default decides),
// and a sample value of that type as a Go literal.
func goType(v any) (string, string) {
	s := fmt.Sprint(v)
	if m := placeholderRe.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
		s = m[1]
	}
	switch {
	case v == nil:
		return "string", `"x"`
	case s == "true" || s == "false":
		return "bool", "true"
	}
	if _, err := strconv.Atoi(s); err == nil {
		return "int", "7"
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return "float64", "7.5"
	}
	return "string", `"x"`
}

// sampleYAML is a settings document with a sample value of each key, for the test.
func sampleYAML(section string, tree map[string]any) string {
	var b strings.Builder
	b.WriteString(section + ":\n")
	writeSample(&b, tree, 1)
	return b.String()
}

func writeSample(b *strings.Builder, tree map[string]any, depth int) {
	keys := make([]string, 0, len(tree))
	for k := range tree {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	indent := strings.Repeat("  ", depth)
	for _, k := range keys {
		if fieldName(k) == "" {
			continue
		}
		switch v := tree[k].(type) {
		case map[string]any:
			fmt.Fprintf(b, "%s%s:\n", indent, k)
			writeSample(b, v, depth+1)
		default:
			_, sample := goType(v)
			fmt.Fprintf(b, "%s%s: %s\n", indent, k, strings.Trim(sample, `"`))
		}
	}
}
