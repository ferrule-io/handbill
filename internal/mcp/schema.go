package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mjquinlan2000/agent-dash/internal/sites"
)

// Limits on tool input (docs/design.md#mcp-tools).
const (
	maxRequestBodyBytes = 16 << 20 // MCP request body
	maxFilesPerCall     = 1000
	maxReadBytes        = 1 << 20 // read_file
	defaultVersionLimit = 50
	maxVersionLimit     = 500
)

const (
	sitePattern = `^[a-z2-7]{12}$`
	uuidPattern = `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`
)

// toolError is a failure message the model can act on.
type toolError struct{ msg string }

func (e *toolError) Error() string { return e.msg }

func toolErrorf(format string, args ...any) error {
	return &toolError{msg: fmt.Sprintf(format, args...)}
}

// annotations: every tool is closed-world. Read-only tools set only
// ReadOnlyHint; the others state destructive and idempotent explicitly.
func readOnly(title string) *mcpsdk.ToolAnnotations {
	return &mcpsdk.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: new(false)}
}

func mutating(title string, destructive, idempotent bool) *mcpsdk.ToolAnnotations {
	return &mcpsdk.ToolAnnotations{Title: title, DestructiveHint: new(destructive), IdempotentHint: idempotent, OpenWorldHint: new(false)}
}

// schemaFor infers In's input schema (additional properties are refused)
// and applies patch for what struct tags cannot express.
func schemaFor[In any](patch func(*jsonschema.Schema)) *jsonschema.Schema {
	s, err := jsonschema.For[In](nil)
	if err != nil {
		panic(err)
	}
	if patch != nil {
		patch(s)
	}
	return s
}

// addTool registers a typed tool. Errors from toolErrorf and
// *sites.InputError reach the model; anything else is logged with the tool
// name and reported as an internal error.
func addTool[In, Out any](s *mcpsdk.Server, env *Env, t *mcpsdk.Tool, patch func(*jsonschema.Schema), h func(context.Context, In) (Out, error)) {
	t.InputSchema = schemaFor[In](patch)
	mcpsdk.AddTool(s, t, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in In) (*mcpsdk.CallToolResult, Out, error) {
		out, err := h(ctx, in)
		if err == nil {
			return nil, out, nil
		}
		var zero Out
		var te *toolError
		var ie *sites.InputError
		switch {
		case errors.As(err, &te):
			return nil, zero, te
		case errors.As(err, &ie):
			return nil, zero, errors.New(ie.Msg)
		}
		env.Deps.Logger.Error("mcp tool failed", "tool", t.Name, "err", err)
		return nil, zero, errors.New("internal error, try again")
	})
}

// Schema patches.

func prop(s *jsonschema.Schema, name string) *jsonschema.Schema {
	p, ok := s.Properties[name]
	if !ok {
		panic("mcp: schema has no property " + name)
	}
	return p
}

func sitePatch(s *jsonschema.Schema) { prop(s, "site").Pattern = sitePattern }

func uuidPatch(name string) func(*jsonschema.Schema) {
	return func(s *jsonschema.Schema) { prop(s, name).Pattern = uuidPattern }
}

func modePatch(s *jsonschema.Schema) { prop(s, "mode").Enum = []any{"replace", "merge"} }

func titlePatch(s *jsonschema.Schema) {
	t := prop(s, "title")
	t.MinLength, t.MaxLength = new(1), new(sites.MaxTitleLen)
}

func descriptionPatch(s *jsonschema.Schema) {
	prop(s, "description").MaxLength = new(sites.MaxDescriptionLen)
}

func filesPatch(s *jsonschema.Schema) {
	f := prop(s, "files")
	f.MaxItems = new(maxFilesPerCall)
	f.Items = fileSchema()
}

func deletePathsPatch(s *jsonschema.Schema) {
	d := prop(s, "delete_paths")
	d.MaxItems = new(maxFilesPerCall)
	d.Items.MinLength, d.Items.MaxLength = new(1), new(sites.MaxPathLen)
}

func patches(ps ...func(*jsonschema.Schema)) func(*jsonschema.Schema) {
	return func(s *jsonschema.Schema) {
		for _, p := range ps {
			p(s)
		}
	}
}

// fileInput is one file of publish_site or upload_files.
type fileInput struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	Encoding    string `json:"encoding,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

func fileSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:                 "object",
		Required:             []string{"path", "content"},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
		Properties: map[string]*jsonschema.Schema{
			"path": {Type: "string", MinLength: new(1), MaxLength: new(sites.MaxPathLen),
				Description: `Relative path from the site root, e.g. "index.html", "css/app.css".`},
			"content": {Type: "string",
				Description: "File contents: the text itself for encoding utf8, or base64 for encoding base64."},
			"encoding":     {Type: "string", Enum: []any{"utf8", "base64"}, Default: []byte(`"utf8"`)},
			"content_type": {Type: "string", MaxLength: new(200), Description: "Optional: overrides the type inferred from the extension."},
		},
	}
}

// decodeFiles turns tool input into service files. Paths go to the service
// unchanged; sites.CleanPath validates them.
func decodeFiles(in []fileInput) ([]sites.File, error) {
	out := make([]sites.File, len(in))
	for i, f := range in {
		out[i] = sites.File{Path: f.Path, ContentType: f.ContentType}
		switch f.Encoding {
		case "", "utf8":
			out[i].Body = []byte(f.Content)
		case "base64":
			b, err := decodeBase64(f.Content)
			if err != nil {
				return nil, toolErrorf("files[%d].content is not valid base64", i)
			}
			out[i].Body = b
		default:
			return nil, toolErrorf("files[%d].encoding must be utf8 or base64", i)
		}
	}
	return out, nil
}

// decodeBase64 drops whitespace, then accepts padded or unpadded standard
// base64.
func decodeBase64(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, s)
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}

// ts formats a time as RFC 3339 UTC.
func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }
