package framework

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

//go:embed all:builtin
var builtinFS embed.FS

// Load returns all framework descriptions keyed by name.
// It merges built-in descriptions (embedded at compile time) with user-provided
// descriptions from userDir (typically .linespec/frameworks/). User descriptions
// with the same name override built-ins; unknown names are added alongside them.
func Load(userDir string) (map[string]*Description, error) {
	descs := make(map[string]*Description)

	if err := loadFromFS(builtinFS, "builtin", descs); err != nil {
		return nil, fmt.Errorf("load built-in frameworks: %w", err)
	}

	if userDir != "" {
		if _, err := os.Stat(userDir); err == nil {
			if err := loadFromFS(os.DirFS(userDir), ".", descs); err != nil {
				return nil, fmt.Errorf("load user frameworks from %s: %w", userDir, err)
			}
		}
	}

	return descs, nil
}

func loadFromFS(fsys fs.FS, dir string, into map[string]*Description) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yml" {
			continue
		}
		data, err := fs.ReadFile(fsys, filepath.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var d Description
		if err := yaml.Unmarshal(data, &d); err != nil {
			return fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		if d.Name == "" {
			return fmt.Errorf("%s: framework description missing required 'name' field", e.Name())
		}
		into[d.Name] = &d
	}
	return nil
}
