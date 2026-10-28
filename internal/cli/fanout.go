package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/kirg0/d9c/internal/agent"
)

// ShapeHosts applies shape (--fields, --limit) to the data of every host that
// answered, in place, and returns the items key and table columns for text
// rendering, taken from the first such host (every host ran the same
// operation, so they share the data type).
func ShapeHosts(results []agent.HostResult, shape agent.Shape) (key string, cols []string, err error) {
	first := true
	for i := range results {
		r := &results[i]
		if r.Error != nil {
			continue
		}
		if first {
			key, cols, first = agent.ItemsKey(r.Data), agent.ItemFields(r.Data), false
			if len(shape.Fields) > 0 {
				cols = shape.Fields
			}
		}
		res, err := shape.Apply(agent.Result{Data: r.Data, Truncated: r.Truncated, Hint: r.Hint})
		if err != nil {
			return "", nil, err
		}
		r.Data, r.Truncated, r.Hint = res.Data, res.Truncated, res.Hint
	}
	return key, cols, nil
}

// WriteHosts renders the envelope of a fan-out, whose Data is the
// []agent.HostResult already shaped by ShapeHosts: JSON as is, NDJSON one host
// per line, text one section per host. Notices go to warn as in WriteResult.
func WriteHosts(w, warn io.Writer, env agent.Envelope, key string, cols []string, f Format) error {
	results, _ := env.Data.([]agent.HostResult)
	switch f {
	case FormatNDJSON:
		enc := newEncoder(w)
		for _, r := range results {
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
		if env.Truncated || env.Partial {
			return enc.Encode(truncatedRecord{Truncated: env.Truncated, Partial: env.Partial, Hint: env.Hint})
		}
		return nil
	case FormatText:
		for i, r := range results {
			if i > 0 {
				if _, err := io.WriteString(w, "\n"); err != nil {
					return err
				}
			}
			if r.Error != nil {
				if _, err := fmt.Fprintf(w, "# %s: %s: %s\n", sanitize(r.Host), r.Error.Code, sanitize(r.Error.Message)); err != nil {
					return err
				}
				continue
			}
			if _, err := fmt.Fprintf(w, "# %s (%s)\n", sanitize(r.Host), r.Runtime); err != nil {
				return err
			}
			if err := writeText(w, r.Data, key, cols); err != nil {
				return err
			}
		}
		var heads []string
		if env.Partial {
			heads = append(heads, "partial failure")
		}
		if env.Truncated {
			heads = append(heads, "truncated")
		}
		if len(heads) > 0 {
			_, err := fmt.Fprintln(warn, joinNotice(strings.Join(heads, ", "), env.Hint))
			return err
		}
		return nil
	default:
		return newEncoder(w).Encode(env)
	}
}
