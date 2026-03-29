package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

// Tool represents a content extraction tool the agent can invoke.
type Tool interface {
	Name() string
	Description() string
	InputSchema() anthropic.ToolInputSchemaParam
	Execute(ctx context.Context, input json.RawMessage) (string, error)
}

// Registry holds registered tools and dispatches calls by name.
type Registry struct {
	tools map[string]Tool
	order []string
}

func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{
		tools: make(map[string]Tool, len(tools)),
	}
	for _, t := range tools {
		r.tools[t.Name()] = t
		r.order = append(r.order, t.Name())
	}
	return r
}

// Names returns the names of all registered tools in registration order.
func (r *Registry) Names() []string {
	return r.order
}

// AnthropicTools converts registered tools to SDK-compatible tool definitions.
func (r *Registry) AnthropicTools() []anthropic.ToolUnionParam {
	params := make([]anthropic.ToolUnionParam, 0, len(r.tools))
	for _, name := range r.order {
		t := r.tools[name]
		params = append(params, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name(),
				Description: anthropic.String(t.Description()),
				InputSchema: t.InputSchema(),
			},
		})
	}
	return params
}

// Execute dispatches a tool call by name.
func (r *Registry) Execute(ctx context.Context, name string, input json.RawMessage) (string, error) {
	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return t.Execute(ctx, input)
}
