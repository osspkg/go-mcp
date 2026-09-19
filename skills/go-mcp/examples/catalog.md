# Tools, resources, and prompts

Register catalog entries before calling `Run`; the first request seals the
registry.

```go
if err := server.RegisterResource(mcp.Resource{
	URI: "memo://welcome", Name: "welcome", Text: "Hello from a resource",
}); err != nil {
	return err
}

if err := server.RegisterResourceTemplate(mcp.ResourceTemplate{
	URITemplate: "memo://people/{name}", Name: "person",
	Handler: func(ctx context.Context, request mcp.ResourceRequest) (mcp.Resource, error) {
		name := request.Variables["name"]
		return mcp.Resource{URI: request.URI, Name: name, Text: "Hello, " + name}, nil
	},
}); err != nil {
	return err
}

if err := server.RegisterPrompt(mcp.Prompt{
	Name: "welcome",
	Messages: []mcp.PromptMessage{{Role: "user", Text: "Welcome!"}},
}); err != nil {
	return err
}
```

Use a `Prompt.Handler` instead of `Messages` for a dynamic prompt. Keep the
handler context-aware and validate its argument map before producing messages.
The dispatcher exposes `resources/list`, `resources/templates/list`,
`resources/read`, `prompts/list`, and `prompts/get`.
