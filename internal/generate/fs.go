package generate

import "io/fs"

// fsReadDir lists the names in a directory of the project.
func fsReadDir(p *Project, dir string) ([]string, error) {
	entries, err := fs.ReadDir(p.fsys, dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out, nil
}
