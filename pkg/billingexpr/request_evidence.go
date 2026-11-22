package billingexpr

import (
	"fmt"
	"maps"
	"math"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

// RequestPricingEvidence contains only derived numeric/boolean request facts.
// The original expression remains authoritative for normalization and tracing.
// No body/header strings, objects, media, or credentials are retained.
type RequestPricingEvidence struct {
	Version        int                            `json:"version"`
	ExpressionHash string                         `json:"expression_hash"`
	Values         map[string]RequestPricingValue `json:"values"`
}

type RequestPricingValue struct {
	Kind    string  `json:"kind"`
	Number  float64 `json:"number,omitempty"`
	Integer int     `json:"integer,omitempty"`
	Boolean bool    `json:"boolean,omitempty"`
}

// CaptureRequestPricing evaluates request-only subtrees, including branches not
// reached by estimated usage. It refuses requests whose later pricing would
// require retaining raw strings or dynamically querying the discarded request.
func CaptureRequestPricing(expression string, request RequestInput) (*RequestPricingEvidence, error) {
	if _, err := CompileFromCache(expression); err != nil {
		return nil, err
	}
	_, body := ParseExprVersion(expression)
	tree, err := parser.Parse(body)
	if err != nil {
		return nil, err
	}
	trace := &requestRulePatcher{}
	ast.Walk(&tree.Node, trace)
	environment := maps.Clone(compileEnvPrototypeV1)
	maps.Copy(environment, requestProbeEnvironment(request))
	evidence := &RequestPricingEvidence{Version: 1, ExpressionHash: ExprHashString(expression), Values: map[string]RequestPricingValue{}}
	patcher := &requestEvidencePatcher{evidence: evidence, environment: environment, capture: true}
	ast.Walk(&tree.Node, patcher)
	if node := unretainedRequestProbe(tree.Node); node != nil {
		return nil, fmt.Errorf("request pricing dependency at position %d cannot be retained safely", node.Location().From)
	}
	return evidence, nil
}

type requestEvidencePatcher struct {
	evidence    *RequestPricingEvidence
	environment map[string]any
	capture     bool
}

func (p *requestEvidencePatcher) Visit(node *ast.Node) {
	if !p.capture {
		if value, ok := p.evidence.Values[(*node).String()]; ok {
			if replacement := value.node(); replacement != nil {
				ast.Patch(node, replacement)
			}
		}
		return
	}
	if unretainedRequestProbe(*node) == nil {
		return
	}
	// Runtime usage, clocks, tier callbacks, and local bindings remain live.
	// A local binding whose value is numeric is frozen at its own value node.
	if ast.Find(*node, func(n ast.Node) bool {
		if _, ok := n.(*ast.PointerNode); ok {
			return true
		}
		id, ok := n.(*ast.IdentifierNode)
		if !ok {
			return false
		}
		switch id.Value {
		case "param", "header", "has", "max", "min", "abs", "ceil", "floor":
			return false
		}
		return true
	}) != nil {
		return
	}
	source := (*node).String()
	program, err := expr.Compile(source, expr.Env(compileEnvPrototypeV1))
	if err != nil {
		return
	}
	value, err := expr.Run(program, p.environment)
	if err != nil {
		return
	}
	frozen := RequestPricingValue{}
	switch v := value.(type) {
	case nil:
		frozen.Kind = "nil"
	case bool:
		frozen.Kind = "bool"
		frozen.Boolean = v
	case int:
		frozen.Kind = "int"
		frozen.Integer = v
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return
		}
		frozen.Kind = "float"
		frozen.Number = v
	default:
		return
	}
	p.evidence.Values[source] = frozen
	ast.Patch(node, frozen.node())
}

func (v RequestPricingValue) node() ast.Node {
	switch v.Kind {
	case "nil":
		return &ast.NilNode{}
	case "bool":
		return &ast.BoolNode{Value: v.Boolean}
	case "int":
		return &ast.IntegerNode{Value: v.Integer}
	case "float":
		if !math.IsNaN(v.Number) && !math.IsInf(v.Number, 0) {
			return &ast.FloatNode{Value: v.Number}
		}
	}
	return nil
}

func unretainedRequestProbe(node ast.Node) ast.Node {
	return ast.Find(node, func(n ast.Node) bool {
		id, ok := n.(*ast.IdentifierNode)
		return ok && (id.Value == "param" || id.Value == "header")
	})
}

func runEntryWithRequest(entry *cachedEntry, expression string, params TokenParams, request RequestInput) (float64, TraceResult, error) {
	if request.Evidence == nil {
		return runProgram(entry.prog, entry.requestRules, entry.usedVars, params, request)
	}
	evidence := request.Evidence
	if evidence.Version != 1 || evidence.ExpressionHash != ExprHashString(expression) {
		return 0, TraceResult{}, fmt.Errorf("request pricing evidence does not match expression")
	}
	for _, name := range []string{"hour", "minute", "weekday", "month", "day"} {
		if entry.usedVars[name] && (request.EvaluationTime == nil || request.EvaluationTime.IsZero()) {
			return 0, TraceResult{}, fmt.Errorf("request pricing replay requires evaluation time")
		}
	}
	_, body := ParseExprVersion(expression)
	tree, err := parser.Parse(body)
	if err != nil {
		return 0, TraceResult{}, err
	}
	trace := &requestRulePatcher{}
	ast.Walk(&tree.Node, trace)
	patcher := &requestEvidencePatcher{evidence: evidence}
	ast.Walk(&tree.Node, patcher)
	if unretainedRequestProbe(tree.Node) != nil {
		return 0, TraceResult{}, fmt.Errorf("request pricing evidence is incomplete")
	}
	// Compile from the original expression so trace indices and original condition
	// labels survive partial evaluation. The shared cache never stores user facts.
	trace = &requestRulePatcher{}
	program, err := expr.Compile(body, expr.Env(getCompileEnv(entry.version)), expr.Patch(trace), expr.Patch(patcher), expr.AsFloat64())
	if err != nil {
		return 0, TraceResult{}, fmt.Errorf("request pricing evidence cannot be replayed")
	}
	request.Body = nil
	request.Headers = nil
	return runProgram(program, entry.requestRules, entry.usedVars, params, request)
}
