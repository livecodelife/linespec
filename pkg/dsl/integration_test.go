package dsl

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParser_AllLineSpecs(t *testing.T) {
	dirs := []string{"../../examples/user-linespecs", "../../examples/todo-linespecs", "../../examples/notification-linespecs"}

	for _, dir := range dirs {
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("Failed to read directory %s: %v", dir, err)
		}

		for _, file := range files {
			if filepath.Ext(file.Name()) != ".linespec" {
				continue
			}

			t.Run(file.Name(), func(t *testing.T) {
				filePath := filepath.Join(dir, file.Name())
				tokens, err := LexFile(filePath)
				if err != nil {
					t.Fatalf("LexFile failed for %s: %v", filePath, err)
				}

				parser := NewParser(tokens)
				_, err = parser.Parse(file.Name())
				if err != nil {
					t.Fatalf("Parse failed for %s: %v", filePath, err)
				}
			})
		}
	}
}
