package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The "Example LineSpec Tests" CI job runs migrations for every service config
// it discovers, so every example app image must exist before the first example
// suite runs. Suite order must stay unchanged.

type ciStep struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`
}

type ciWorkflow struct {
	Jobs map[string]struct {
		Name  string   `yaml:"name"`
		Steps []ciStep `yaml:"steps"`
	} `yaml:"jobs"`
}

func TestCIBuildsAllExampleImagesBeforeFirstSuite(t *testing.T) {
	var wf ciWorkflow
	if err := yaml.Unmarshal([]byte(readRepoFile(t, "../../.github/workflows/ci.yml")), &wf); err != nil {
		t.Fatalf("parse ci.yml: %v", err)
	}

	var steps []ciStep
	found := false
	for _, job := range wf.Jobs {
		if job.Name == "Example LineSpec Tests" {
			steps, found = job.Steps, true
			break
		}
	}
	if !found {
		t.Fatal(`job "Example LineSpec Tests" not found in ci.yml`)
	}

	firstSuite := -1
	for i, s := range steps {
		if strings.Contains(s.Run, "./linespec test examples/") {
			firstSuite = i
			break
		}
	}
	if firstSuite < 0 {
		t.Fatal("no step running `./linespec test examples/` found")
	}

	images := []struct{ image, dir string }{
		{"user-service", "examples/user-service/"},
		{"todo-api", "examples/todo-api/"},
		{"order-service", "examples/order-service/"},
		{"order-events-service", "examples/multi-db-service/"},
	}
	for _, img := range images {
		idx := -1
		for i, s := range steps {
			if strings.Contains(s.Run, "docker build") && strings.Contains(s.Run, img.image+":latest") && strings.Contains(s.Run, img.dir) {
				idx = i
				break
			}
		}
		if idx < 0 {
			t.Errorf("no docker build step for image %s (%s) in the Example LineSpec Tests job", img.image, img.dir)
			continue
		}
		if idx > firstSuite {
			t.Errorf("image %s is built too late: step %q (#%d) comes after the first suite step %q (#%d)",
				img.image, steps[idx].Name, idx, steps[firstSuite].Name, firstSuite)
		}
	}

	wantOrder := []string{
		"examples/user-linespecs/",
		"examples/todo-linespecs/",
		"examples/order-linespecs/",
		"examples/multi-db-linespecs/",
	}
	var gotOrder []string
	for _, s := range steps {
		if !strings.Contains(s.Run, "./linespec test examples/") {
			continue
		}
		for _, w := range wantOrder {
			if strings.Contains(s.Run, w) {
				gotOrder = append(gotOrder, w)
			}
		}
	}
	if strings.Join(gotOrder, ",") != strings.Join(wantOrder, ",") {
		t.Errorf("suite order changed: got %v, want %v", gotOrder, wantOrder)
	}
}
