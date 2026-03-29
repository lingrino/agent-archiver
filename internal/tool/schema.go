package tool

import (
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/invopop/jsonschema"
)

// GenerateSchema reflects a Go struct into an Anthropic tool input schema.
func GenerateSchema[T any]() anthropic.ToolInputSchemaParam {
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	var v T
	schema := reflector.Reflect(v)

	properties := make(map[string]interface{})
	if schema.Properties != nil {
		for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
			properties[pair.Key] = pair.Value
		}
	}

	return anthropic.ToolInputSchemaParam{
		Properties: properties,
		Required:   schema.Required,
	}
}
