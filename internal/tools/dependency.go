package tools

import (
	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/mcp"
	"github.com/yanulabs/goray/internal/render"
)

func (t *toolset) explainDependency() mcp.Tool {
	return mcp.Tool{
		Name:        "explain_dependency",
		Annotations: readOnlyAnnotations(),
		Description: "Exact files, imports, and referenced symbols behind a module or package dependency edge, including blank and dot imports.",
		InputSchema: object(map[string]any{
			"from":          propString("Source module name or package path"),
			"to":            propString("Target module name or package path"),
			"include_tests": propBool("Include *_test.go imports and usages (default: false)"),
			"limit":         propInt("Max symbol usage sites (default: unlimited)"),
		}, "from", "to"),
		Run: t.indexed(func(idx *index.Index, args map[string]any) (string, bool) {
			from := strArg(args, "from", "")
			to := strArg(args, "to", "")
			if from == "" || to == "" {
				return "both 'from' and 'to' are required", true
			}
			explanation, err := idx.ExplainDependency(
				from,
				to,
				boolArg(args, "include_tests", false),
				intArg(args, "limit", 0),
			)
			if err != nil {
				return err.Error(), true
			}
			out := struct {
				index.DependencyExplanation
				IndexState index.State `json:"index_state"`
			}{DependencyExplanation: explanation, IndexState: t.src.Status().IndexState}
			return render.JSON(out), false
		}),
	}
}
