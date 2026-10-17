package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/moudlajs/waiverwatch/internal/league"
	"github.com/moudlajs/waiverwatch/internal/sleeper"
)

var update = flag.Bool("update", false, "rewrite testdata/tools.json")

const goldenTools = "testdata/tools.json"

type toolSchemas struct {
	In  json.RawMessage `json:"in"`
	Out json.RawMessage `json:"out,omitempty"`
}

func currentSchemas(t *testing.T) map[string]toolSchemas {
	t.Helper()
	ct, st := sdk.NewInMemoryTransports()
	ctx := context.Background()
	svc := league.NewService(sleeper.New("http://unused.invalid"), nil, nil, "me")
	if _, err := NewServer(svc, "test", nil).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]toolSchemas, len(res.Tools))
	for _, tl := range res.Tools {
		in, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		var o []byte
		if tl.OutputSchema != nil {
			if o, err = json.Marshal(tl.OutputSchema); err != nil {
				t.Fatal(err)
			}
		}
		out[tl.Name] = toolSchemas{In: in, Out: o}
	}
	return out
}

// claude.ai caches every tool's schemas until the user reconnects, and rejects answers that don't
// match them (#139): an existing tool's schemas must not change. New tools are fine.
func TestToolSchemasAreStable(t *testing.T) {
	got := currentSchemas(t)
	if *update {
		b, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenTools, append(b, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(goldenTools)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]toolSchemas
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	for name, w := range want {
		g, ok := got[name]
		switch {
		case !ok:
			t.Errorf("tool %s was removed: connected users still see it until they reconnect (#139)", name)
		case !jsonEqual(g.Out, w.Out):
			t.Errorf("tool %s: output schema changed; connectors that cached it will reject its answers (#139). "+
				"Report new data in an existing text field such as a note, or in a new tool", name)
		case !jsonEqual(g.In, w.In):
			t.Errorf("tool %s: input schema changed; cached connectors keep the old one until reconnect (#139). "+
				"A new optional argument is safe; then run go test ./internal/mcp -run TestToolSchemasAreStable -update", name)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("new tool %s: run go test ./internal/mcp -run TestToolSchemasAreStable -update", name)
		}
	}
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y bytes.Buffer
	if len(a) > 0 {
		if err := json.Compact(&x, a); err != nil {
			return false
		}
	}
	if len(b) > 0 {
		if err := json.Compact(&y, b); err != nil {
			return false
		}
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}
