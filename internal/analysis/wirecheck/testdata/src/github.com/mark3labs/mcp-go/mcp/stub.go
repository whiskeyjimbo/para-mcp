package mcp

// CallToolRequest is the stub for analysistest.
type CallToolRequest struct{}

func (r CallToolRequest) GetString(key, def string) string  { return "" }
func (r CallToolRequest) RequireString(key string) (string, error) { return "", nil }
func (r CallToolRequest) GetInt(key string, def int) int    { return 0 }
