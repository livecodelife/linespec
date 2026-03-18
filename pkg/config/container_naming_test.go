package config

import (
	"testing"
)

func TestContainerNaming_GetDatabaseContainer(t *testing.T) {
	cn := &ContainerNaming{
		DatabaseContainer: "{{ .ServiceName }}-db",
	}

	params := ContainerNameParams{
		ServiceName: "todo-service",
	}

	result := cn.GetDatabaseContainer(params)
	expected := "todo-service-db"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestContainerNaming_GetKafkaContainer(t *testing.T) {
	cn := &ContainerNaming{
		KafkaContainer: "{{ .ServiceName }}-kafka",
	}

	params := ContainerNameParams{
		ServiceName: "todo-service",
	}

	result := cn.GetKafkaContainer(params)
	expected := "todo-service-kafka"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestContainerNaming_GetProxyContainer(t *testing.T) {
	cn := &ContainerNaming{
		ProxyContainer: "{{ .ServiceName }}-proxy-{{ .Type }}-{{ .SpecName }}",
	}

	params := ContainerNameParams{
		ServiceName: "todo-service",
		SpecName:    "create-user",
		Type:        "db",
	}

	result := cn.GetProxyContainer(params)
	expected := "todo-service-proxy-db-create-user"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestContainerNaming_GetAppContainer(t *testing.T) {
	cn := &ContainerNaming{
		AppContainer: "{{ .ServiceName }}-app-{{ .SpecName }}",
	}

	params := ContainerNameParams{
		ServiceName: "todo-service",
		SpecName:    "create-user",
	}

	result := cn.GetAppContainer(params)
	expected := "todo-service-app-create-user"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestContainerNaming_GetMigrateContainer(t *testing.T) {
	cn := &ContainerNaming{
		MigrateContainer: "migrate-{{ .ServiceName }}",
	}

	params := ContainerNameParams{
		ServiceName: "todo-service",
	}

	result := cn.GetMigrateContainer(params)
	expected := "migrate-todo-service"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestContainerNaming_GetNetworkName(t *testing.T) {
	cn := &ContainerNaming{
		NetworkName: "{{ .ServiceName }}-net",
	}

	params := ContainerNameParams{
		ServiceName: "todo-service",
	}

	result := cn.GetNetworkName(params)
	expected := "todo-service-net"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestContainerNaming_GetMountPaths(t *testing.T) {
	cn := &ContainerNaming{
		ProjectMountPath:  "/custom/project",
		RegistryMountPath: "/custom/registry",
	}

	if cn.GetProjectMountPath() != "/custom/project" {
		t.Errorf("Expected project mount path /custom/project, got %s", cn.GetProjectMountPath())
	}

	if cn.GetRegistryMountPath() != "/custom/registry" {
		t.Errorf("Expected registry mount path /custom/registry, got %s", cn.GetRegistryMountPath())
	}
}

func TestContainerNaming_Defaults(t *testing.T) {
	cn := &ContainerNaming{}

	// Test that defaults are applied
	params := ContainerNameParams{
		ServiceName: "todo-service",
		SpecName:    "test-spec",
		Type:        "http",
	}

	if cn.GetDatabaseContainer(params) != "linespec-shared-db" {
		t.Errorf("Expected default database container name, got %s", cn.GetDatabaseContainer(params))
	}

	if cn.GetKafkaContainer(params) != "linespec-shared-kafka" {
		t.Errorf("Expected default kafka container name, got %s", cn.GetKafkaContainer(params))
	}

	if cn.GetProjectMountPath() != "/app/project" {
		t.Errorf("Expected default project mount path, got %s", cn.GetProjectMountPath())
	}

	if cn.GetRegistryMountPath() != "/app/registry" {
		t.Errorf("Expected default registry mount path, got %s", cn.GetRegistryMountPath())
	}
}

func TestSubstituteTemplate(t *testing.T) {
	tests := []struct {
		template string
		params   ContainerNameParams
		expected string
	}{
		{
			template: "{{ .ServiceName }}-db",
			params:   ContainerNameParams{ServiceName: "myapp"},
			expected: "myapp-db",
		},
		{
			template: "{{ .ServiceName }}-{{ .SpecName }}-{{ .Type }}",
			params:   ContainerNameParams{ServiceName: "svc", SpecName: "spec", Type: "http"},
			expected: "svc-spec-http",
		},
		{
			template: "static-name",
			params:   ContainerNameParams{ServiceName: "ignored"},
			expected: "static-name",
		},
		{
			template: "{{ .ServiceName }}",
			params:   ContainerNameParams{},
			expected: "",
		},
	}

	for _, tt := range tests {
		result := substituteTemplate(tt.template, tt.params)
		if result != tt.expected {
			t.Errorf("substituteTemplate(%q, %+v) = %q, expected %q",
				tt.template, tt.params, result, tt.expected)
		}
	}
}
