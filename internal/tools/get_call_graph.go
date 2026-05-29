package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hypoballad/virgil/internal/repository"
)

const callGraphDefaultDepth = 3
const callGraphMaxDepth = 5
const callGraphMaxNodes = 100

// GetCallGraphTool は関数からの呼び出しを再帰的に辿り Mermaid 図を生成する。
type GetCallGraphTool struct {
	calls *repository.CallRepository
}

func NewGetCallGraphTool(calls *repository.CallRepository) *GetCallGraphTool {
	return &GetCallGraphTool{calls: calls}
}

func (t *GetCallGraphTool) Name() string {
	return "get_call_graph"
}

func (t *GetCallGraphTool) Description() string {
	return "Generate a Mermaid diagram showing what functions a given function calls (recursively up to depth N). " +
		"Use this to understand the structure of a function's behavior. " +
		"Returns a Mermaid graph that visualizes the call tree. " +
		"For reverse lookup (who calls X), use get_callers instead."
}

func (t *GetCallGraphTool) Definition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: FunctionDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		},
	}
}

func (t *GetCallGraphTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "The starting function name (root of the call graph)",
			},
			"depth": map[string]interface{}{
				"type":        "integer",
				"description": fmt.Sprintf("How deep to recurse (default %d, max %d)", callGraphDefaultDepth, callGraphMaxDepth),
			},
		},
		"required": []string{"name"},
	}
}

func (t *GetCallGraphTool) IsMutating() bool {
	return false
}

type getCallGraphArgs struct {
	Name  string `json:"name"`
	Depth int    `json:"depth,omitempty"`
}

func (t *GetCallGraphTool) Execute(ctx context.Context, argsJSON json.RawMessage) (*Result, error) {
	var args getCallGraphArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return ErrorResult(fmt.Sprintf("invalid arguments: %v", err)), nil
	}

	name := strings.TrimSpace(args.Name)
	if name == "" {
		return ErrorResult("name is required"), nil
	}
	if t.calls == nil {
		return ErrorResult("call graph repository is not available"), nil
	}

	depth := normalizeCallGraphDepth(args.Depth)

	return &Result{
		IsError: false,
		Content: BuildCallGraphReport(t.calls, name, depth),
	}, nil
}

type callNode struct {
	Name     string
	Receiver string
}

func (n callNode) Key() string {
	if n.Receiver != "" {
		return n.Receiver + "." + n.Name
	}
	return n.Name
}

func (n callNode) Display() string {
	if n.Receiver != "" {
		return fmt.Sprintf("%s.%s", n.Receiver, n.Name)
	}
	return n.Name
}

// BuildCallGraphReport は呼び出しグラフを Markdown + Mermaid で整形する。
func BuildCallGraphReport(calls *repository.CallRepository, startName string, depth int) string {
	var sb strings.Builder

	depth = normalizeCallGraphDepth(depth)
	sb.WriteString(fmt.Sprintf("# Call Graph from `%s`\n\n", startName))
	sb.WriteString(fmt.Sprintf("Depth: %d (max %d, nodes max %d)\n\n", depth, callGraphMaxDepth, callGraphMaxNodes))

	type edge struct {
		from callNode
		to   callNode
	}

	visited := make(map[string]bool)
	var edges []edge
	nodeCount := 0
	truncated := false

	startNode := callNode{Name: startName}

	type queueItem struct {
		node  callNode
		depth int
	}
	queue := []queueItem{{node: startNode, depth: 0}}
	visited[startNode.Key()] = true
	nodeCount = 1

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= depth {
			continue
		}

		records, err := calls.FindOutgoing(current.node.Name, current.node.Receiver, 50)
		if err != nil {
			sb.WriteString(fmt.Sprintf("Error querying calls: %v\n", err))
			return sb.String()
		}

		for _, r := range records {
			child := callNode{Name: r.CalleeName, Receiver: r.CalleeReceiver}

			edges = append(edges, edge{from: current.node, to: child})

			if visited[child.Key()] {
				continue
			}
			visited[child.Key()] = true
			nodeCount++

			if nodeCount > callGraphMaxNodes {
				truncated = true
				continue
			}

			queue = append(queue, queueItem{node: child, depth: current.depth + 1})
		}
	}

	if len(edges) == 0 {
		sb.WriteString(fmt.Sprintf("No outgoing calls found from `%s`.\n\n", startName))
		sb.WriteString("Possible reasons:\n")
		sb.WriteString("- The function exists but doesn't call anything (leaf function)\n")
		sb.WriteString("- The function name is misspelled\n")
		sb.WriteString("- The function is in a non-indexed file\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Nodes: %d, Edges: %d", nodeCount, len(edges)))
	if truncated {
		sb.WriteString(fmt.Sprintf(" (truncated at %d nodes)", callGraphMaxNodes))
	}
	sb.WriteString("\n\n")

	sb.WriteString("```mermaid\n")
	sb.WriteString("graph TD\n")

	edgeSet := make(map[string]bool)
	var uniqueEdges []edge
	for _, e := range edges {
		key := e.from.Key() + "->" + e.to.Key()
		if edgeSet[key] {
			continue
		}
		edgeSet[key] = true
		uniqueEdges = append(uniqueEdges, e)
	}

	sort.Slice(uniqueEdges, func(i, j int) bool {
		if uniqueEdges[i].from.Key() != uniqueEdges[j].from.Key() {
			return uniqueEdges[i].from.Key() < uniqueEdges[j].from.Key()
		}
		return uniqueEdges[i].to.Key() < uniqueEdges[j].to.Key()
	})

	for _, e := range uniqueEdges {
		sb.WriteString(fmt.Sprintf("    %s[\"%s\"] --> %s[\"%s\"]\n",
			mermaidID(e.from.Key()), e.from.Display(),
			mermaidID(e.to.Key()), e.to.Display(),
		))
	}

	sb.WriteString("```\n\n")

	sb.WriteString("**Next steps:**\n")
	sb.WriteString("- To find callers of a specific function: `get_callers(name=\"FUNCTION_NAME\")`\n")
	sb.WriteString("- To increase depth: `get_call_graph(name=\"" + startName + "\", depth=5)`\n")

	return sb.String()
}

func normalizeCallGraphDepth(depth int) int {
	if depth <= 0 {
		return callGraphDefaultDepth
	}
	if depth > callGraphMaxDepth {
		return callGraphMaxDepth
	}
	return depth
}

// mermaidID は Mermaid のノードIDとして使える文字列に変換する。
func mermaidID(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			sb.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			sb.WriteRune(r)
		case r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	return sb.String()
}
