package mcpserver

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kirg0/d9c/internal/agent"
)

// Resource URIs.
const (
	hostsURI         = "d9c://hosts"
	logsURITemplate  = "d9c://containers/{id}/logs"
	containersPrefix = "d9c://containers/"
	logsSuffix       = "/logs"
	jsonMIME         = "application/json"
)

// addResources registers the read-only resources: the saved hosts and the
// recent logs of a container, both as the envelope a tool call would return.
func (s *Server) addResources() {
	s.mcp.AddResource(&mcp.Resource{
		URI:         hostsURI,
		Name:        "hosts",
		Title:       "Saved hosts",
		Description: "The saved hosts and the write tier the policy admits on each (as d9c_hosts returns).",
		MIMEType:    jsonMIME,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return s.readResource(ctx, req.Params.URI, "hosts ls", nil)
	})
	s.mcp.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: logsURITemplate,
		Name:        "container-logs",
		Title:       "Container logs",
		Description: "The last 200 log lines of a container (ID or name), as d9c_logs returns them. " + agent.UntrustedNote,
		MIMEType:    jsonMIME,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		id, ok := logsTarget(req.Params.URI)
		if !ok {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		return s.readResource(ctx, req.Params.URI, "logs", agent.Args{"container": id})
	})
}

// logsTarget extracts the container of a d9c://containers/{id}/logs URI.
func logsTarget(uri string) (string, bool) {
	rest, ok := strings.CutPrefix(uri, containersPrefix)
	if !ok {
		return "", false
	}
	rest, ok = strings.CutSuffix(rest, logsSuffix)
	if !ok || rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	id, err := url.PathUnescape(rest)
	return id, err == nil && id != ""
}

// readResource runs the read operation name and returns its envelope as the
// resource content. A missing object is the protocol's resource-not-found
// error; any other failure carries the d9c error text and hint.
func (s *Server) readResource(ctx context.Context, uri, name string, args agent.Args) (*mcp.ReadResourceResult, error) {
	op, ok := s.opts.Registry.Lookup(name)
	if !ok {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	rt, res, err := s.run(ctx, op, args, agent.Call{Ceiling: s.ceiling}, nil)
	if err != nil {
		if e := agent.Classify(err); e.Code == agent.CodeNotFound {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		return nil, errors.New(agent.Classify(err).Error())
	}
	env := agent.NewEnvelope(s.opts.Label, rt, res)
	if op.Local {
		env.Runtime = ""
	}
	r := jsonResult(env, false)
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI: uri, MIMEType: jsonMIME, Text: r.Content[0].(*mcp.TextContent).Text,
	}}}, nil
}
