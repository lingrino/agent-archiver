package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

type mockTool struct {
	name   string
	result string
	err    error
}

func (m *mockTool) Name() string        { return m.name }
func (m *mockTool) Description() string  { return "mock tool" }
func (m *mockTool) InputSchema() anthropic.ToolInputSchemaParam {
	return anthropic.ToolInputSchemaParam{
		Properties: map[string]interface{}{},
	}
}
func (m *mockTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return m.result, m.err
}

func TestRegistryNames(t *testing.T) {
	r := NewRegistry(
		&mockTool{name: "tool_a"},
		&mockTool{name: "tool_b"},
		&mockTool{name: "tool_c"},
	)

	names := r.Names()
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}
	if names[0] != "tool_a" || names[1] != "tool_b" || names[2] != "tool_c" {
		t.Errorf("unexpected names: %v", names)
	}
}

func TestRegistryExecute(t *testing.T) {
	r := NewRegistry(&mockTool{name: "test", result: "hello"})

	result, err := r.Execute(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestRegistryExecuteUnknown(t *testing.T) {
	r := NewRegistry()

	_, err := r.Execute(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

func TestRegistryAnthropicTools(t *testing.T) {
	r := NewRegistry(
		&mockTool{name: "tool_a"},
		&mockTool{name: "tool_b"},
	)

	tools := r.AnthropicTools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].OfTool.Name != "tool_a" {
		t.Errorf("expected tool_a, got %s", tools[0].OfTool.Name)
	}
	if tools[1].OfTool.Name != "tool_b" {
		t.Errorf("expected tool_b, got %s", tools[1].OfTool.Name)
	}
}
