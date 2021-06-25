package startf

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

func analyzeScriptFile(thread *starlark.Thread, filename string) {
	err := doAnalyze(filename)
	if err != nil {
		panic(err)
	}
}

func doAnalyze(filename string) error {
	// ExecFile(thread *Thread, filename string, src interface{}, predeclared StringDict)
	// SourceProgram(filename string, src interface{}, isPredeclared func(string) bool)
	// f, err := syntax.Parse(filename string, src interface{}, 0 ?)
	fmt.Printf("analyze: %s\n", filename)

	f, err := syntax.Parse(filename, nil, 0)
	if err != nil {
		return err
	}

	fmt.Printf("Parsed successfully!\n")
	data, err := json.MarshalIndent(f, "", " ")
	if err != nil {
		return err
	}

	functions := []*FuncResult{}

	text := string(data)
	fmt.Printf("%s\n================================\n\n", text)

	for i, stmt := range f.Stmts {
		switch item := stmt.(type) {
		case *syntax.DefStmt:
			res, err := analyzeFunction(item)
			if err != nil {
				return err
			}
			functions = append(functions, res)
		default:
			fmt.Printf("%d: other top-level stmt\n", i)
		}
	}

	fmt.Printf("----------------------------------------\n")

	// Build a graph of all calls
	// Detect unused functions
	callGraph := buildCallGraph(functions)
	displayCallGraph(callGraph)

	fmt.Printf("----------------------------------------\n")

	analyzeSingleFunction(callGraph, "first_func")
/*
	fmt.Printf("----------------------------------------\n")
	for _, f := range functions {
		fmt.Printf("def %s(%s)\n", f.name, f.params)
		for _, c := range f.calls {
			fmt.Printf(" %s()\n", c)
		}
		fmt.Printf("\n")
	}
*/

	return nil
}

func analyzeFunction(def *syntax.DefStmt) (*FuncResult, error) {
	fmt.Printf("def func: %q\n", def.Name.Name)

	numParams := len(def.Params)
	_ = numParams
	params := make([]string, numParams)
	for k, param := range def.Params {
		p := parameterName(param)
		params[k] = p
	}
	fmt.Printf(" params: (%s)\n", strings.Join(params, ","))

	res, err := analyzeFuncBody(def.Body)
	if err != nil {
		return nil, err
	}
	res.name = def.Name.Name
	res.params = strings.Join(params, ",")
	res.body = def.Body

	fmt.Printf("\n")

	return res, nil
}

func parameterName(e syntax.Expr) string {
	id, ok := e.(*syntax.Ident)
	if !ok {
		return fmt.Sprintf("<UNKNOWN: %s>", reflect.TypeOf(e))
	}
	return id.Name
}

type FuncResult struct {
	name   string
	params string
	calls  []string
	body   []syntax.Stmt
}

func NewFuncResult() *FuncResult {
	return &FuncResult{calls: []string{}}
}

func analyzeFuncBody(body []syntax.Stmt) (*FuncResult, error) {
	result := NewFuncResult()
	for k, stmt := range body {
		data, err := json.Marshal(stmt)
		if err != nil {
			return nil, err
		}
		text := string(data)
		//fmt.Printf("%d: %s\n", k, text)
		_ = text

		switch item := stmt.(type) {
		case *syntax.AssignStmt:
			// Is the rhs a function call
			calls := getFuncCallsInExpr(item.RHS)
			result.calls = append(result.calls, calls...)

		case *syntax.BranchStmt:
			// pass
			fmt.Printf("TODO func body %d: branch\n", k)
		case *syntax.DefStmt:
			// pass
			fmt.Printf("TODO func body %d: def\n", k)
		case *syntax.ExprStmt:
			// pass
			// Is this a function call? (almost *certainly* it is)
			calls := getFuncCallsInExpr(item.X)
			result.calls = append(result.calls, calls...)

		case *syntax.ForStmt:
			// pass
			fmt.Printf("TODO func body %d: for\n", k)
		case *syntax.WhileStmt:
			// pass
			fmt.Printf("TODO func body %d: while\n", k)
		case *syntax.IfStmt:
			// pass
			fmt.Printf("TODO func body %d: if\n", k)
		case *syntax.LoadStmt:
			// pass
			fmt.Printf("TODO func body %d: load\n", k)
		case *syntax.ReturnStmt:
			// pass
			calls := getFuncCallsInExpr(item.Result)
			result.calls = append(result.calls, calls...)

		default:
			// pass
		}
	}
	return result, nil
}

// Stmt:
//  AssignStmt step
//  BranchStmt control-flow -> jump (BREAK | CONTINUE | PASS)
//  DefStmt    ?
//  ExprStmt   step
//  ForStmt    control-flow -> loop
//  WhileStmt  control-flow -> loop
//  IfStmt     control-flow -> branch
//  LoadStmt   ?
//  ReturnStmt control-flow -> termination

func getFuncCallsInExpr(expr syntax.Expr) []string {
	switch item := expr.(type) {
	case *syntax.BinaryExpr:
		return append(getFuncCallsInExpr(item.X), getFuncCallsInExpr(item.Y)...)

	case *syntax.CallExpr:
		funcName := simpleExprToFuncName(item.Fn)
		result := make([]string, 0, 1 + len(item.Args))
		result = append(result, funcName)
		for _, arg := range item.Args {
			result = append(result, getFuncCallsInExpr(arg)...)
		}
		return result

	case *syntax.Comprehension:
		panic("not implemented")

	case *syntax.CondExpr:
		result := getFuncCallsInExpr(item.Cond)
		result = append(result, getFuncCallsInExpr(item.True)...)
		result = append(result, getFuncCallsInExpr(item.False)...)
		return result

	case *syntax.DictEntry:
		return append(getFuncCallsInExpr(item.Key), getFuncCallsInExpr(item.Value)...)

	case *syntax.DictExpr:
		result := make([]string, 0, len(item.List))
		for _, elem := range item.List {
			result = append(result, getFuncCallsInExpr(elem)...)
		}
		return result

	case *syntax.DotExpr:
		panic("not implemented")

	case *syntax.Ident:
		// I think that this is correct?
		//fmt.Printf("Ident is not a FuncCall I think?\n")
		return []string{}

	case *syntax.IndexExpr:
		return append(getFuncCallsInExpr(item.X), getFuncCallsInExpr(item.Y)...)

	case *syntax.LambdaExpr:
		result := make([]string, 0, 1 + len(item.Params))
		result = append(result, getFuncCallsInExpr(item.Body)...)
		for _, elem := range item.Params {
			result = append(result, getFuncCallsInExpr(elem)...)
		}
		return result

	case *syntax.ListExpr:
		result := make([]string, 0, len(item.List))
		for _, elem := range item.List {
			result = append(result, getFuncCallsInExpr(elem)...)
		}
		return result

	case *syntax.Literal:
		return []string{}

	case *syntax.ParenExpr:
		return getFuncCallsInExpr(item.X)

	case *syntax.SliceExpr:
		result := getFuncCallsInExpr(item.X)
		result = append(result, getFuncCallsInExpr(item.Lo)...)
		result = append(result, getFuncCallsInExpr(item.Hi)...)
		result = append(result, getFuncCallsInExpr(item.Step)...)
		return result

	case *syntax.TupleExpr:
		result := make([]string, 0, len(item.List))
		for _, elem := range item.List {
			result = append(result, getFuncCallsInExpr(elem)...)
		}
		return result

	case *syntax.UnaryExpr:
		return getFuncCallsInExpr(item.X)

	}
	return nil
}

func simpleExprToFuncName(expr syntax.Expr) string {
	if item, ok := expr.(*syntax.Ident); ok {
		return item.Name
	}
	return fmt.Sprintf("<Unknown Name, Type: %q>", reflect.TypeOf(expr))
}

/*
	switch item := expr.(type) {
	case *syntax.BinaryExpr:
		// pass
	case *syntax.CallExpr:
		// pass
	case *syntax.ComprehensionExpr:
		// pass
	case *syntax.CondExpr:
		// pass
	case *syntax.DictEntry:
		// pass
	case *syntax.DictExpr:
		// pass
	case *syntax.DotExpr:
		// pass
	case *syntax.Ident:
		return item.Name
	case *syntax.IndexExpr:

	case *syntax.LambdaExpr:

	case *syntax.ListExpr:

	case *syntax.Literal:

	case *syntax.ParenExpr:

	case *syntax.SliceExpr:

	case *syntax.TupleExpr:

	case *syntax.UnaryExpr:

	}
}
*/

type CallGraph struct {
	root   *FuncNode
	nodes  []*FuncNode
	lookup map[string]*FuncNode
}

type FuncNode struct {
	name   string
	fn     *FuncResult
	outs   []*FuncNode
	reach  bool
	height int
}

func buildCallGraph(functions []*FuncResult) *CallGraph {
	symtable := map[string]*FuncResult{}
	for _, f := range functions {
		symtable[f.name] = f
	}
	symtable["print"] = &FuncResult{
		name: "print",
	}

	graph := &CallGraph{
		nodes:  make([]*FuncNode, 0, len(functions)),
		lookup: make(map[string]*FuncNode),
	}
	for _, f := range functions {
		addToCallGraph(f, graph, symtable)
	}

	for _, n := range graph.nodes {
		addCallHeight(n)
	}

	graph.root = graph.lookup["transform"]
	markReachable(graph.root)

	return graph
}

func addToCallGraph(f *FuncResult, graph *CallGraph, symtable map[string]*FuncResult) *FuncNode {
	me, ok := graph.lookup[f.name]
	if ok {
		return me
	}
	me = &FuncNode{
		name: f.name,
		fn:   f,
		outs: make([]*FuncNode, 0),
	}
	for _, call := range f.calls {
		child, ok := symtable[call]
		if !ok {
			//panic(fmt.Sprintf("not found: %s", call))
			fmt.Printf("not found: %s\n", call)
			continue
		}
		n := addToCallGraph(child, graph, symtable)
		me.outs = append(me.outs, n)
	}
	graph.lookup[f.name] = me
	graph.nodes = append(graph.nodes, me)
	return me
}

func addCallHeight(node *FuncNode) {
	maxChild := -1
	for _, fn := range node.outs {
		addCallHeight(fn)
		if fn.height > maxChild {
			maxChild = fn.height
		}
	}
	node.height = maxChild + 1
}

func markReachable(node *FuncNode) {
	node.reach = true
	for _, call := range node.outs {
		markReachable(call)
	}
}

func displayCallGraph(graph *CallGraph) {
	fmt.Printf("Call Graph...\n")
	fmt.Printf("nodes: %d\n", len(graph.nodes))

	for _, f := range graph.nodes {
		displayFuncNode(f, 0)
	}
}

func displayFuncNode(node *FuncNode, depth int) {
	padding := strings.Repeat("  ", depth)
	extra := ""
	if !node.reach {
		extra = " *** DEAD CODE"
	}
	fmt.Printf("%s%s @ %d%s\n", padding, node.name, node.height, extra)
	for _, call := range node.outs {
		displayFuncNode(call, depth+1)
	}
}

func analyzeSingleFunction(graph *CallGraph, fname string) {
	f := graph.lookup[fname]
	body := f.fn.body
	controlFlow := []*CodeBlock{}
	for _, line := range body {
		controlFlow = append(controlFlow, buildControlFlow(line))
	}

	data, err := json.MarshalIndent(controlFlow, "", " ")
	if err != nil {
		panic(err)
	}
	fmt.Printf(string(data))
}

type CodeBlock struct {
	Code []string
	Outs []*CodeBlock
}

func newCodeBlock() *CodeBlock {
	return &CodeBlock{
		Code: []string{},
		Outs: []*CodeBlock{},
	}
}

func buildControlFlow(stmt syntax.Stmt) *CodeBlock {
	currBlock := newCodeBlock()

	switch item := stmt.(type) {
	case *syntax.AssignStmt:
		// TODO: Also record vars in LHS and RHS
		assignLine := assignmentToText(item)
		currBlock.Code = append(currBlock.Code, assignLine)

	case *syntax.BranchStmt:
		fmt.Printf("~~~ TODO: branch stmt\n")

	case *syntax.DefStmt:
		fmt.Printf("~~~ TODO: def stmt\n")

	case *syntax.ExprStmt:

		// TODO: Also record vars in Params
		funcCallLine := funcCallToText(item)
		currBlock.Code = append(currBlock.Code, funcCallLine)

	case *syntax.ForStmt:
		// Add new block, connect old one here
		newBlock := newCodeBlock()
		currBlock.Outs = append(currBlock.Outs, newBlock)
		currBlock = newBlock

		//
		// TODO: Iterate the body for the For block
		//


	case *syntax.WhileStmt:
		fmt.Printf("~~~ TODO: while stmt\n")

	case *syntax.IfStmt:
		fmt.Printf("~~~ TODO: if stmt\n")

	case *syntax.LoadStmt:
		fmt.Printf("~~~ TODO: load stmt\n")

	case *syntax.ReturnStmt:
		fmt.Printf("~~~ TODO: return stmt\n")

	}

	return currBlock
}

func assignmentToText(assign *syntax.AssignStmt) string {
	result := "set! "
	if ident, ok := assign.LHS.(*syntax.Ident); ok {
		result = result + ident.Name
	} else {
		result = result + "???"
	}
	result = result + " = "
	if ident, ok := assign.RHS.(*syntax.Ident); ok {
		result = result + ident.Name
	} else if val, ok := assign.RHS.(*syntax.Literal); ok {
		result = result + val.Raw
	} else {
		result = result + "???"
	}
	return result
}

func funcCallToText(expr *syntax.ExprStmt) string {
	e := expr.X
	switch item := e.(type) {
	case *syntax.BinaryExpr:
		return "binary()"

	case *syntax.CallExpr:
		fn := item.Fn
		funcCallIdent := fn.(*syntax.Ident)
		return fmt.Sprintf("%s()", funcCallIdent.Name)

	case *syntax.Comprehension:
		return "comp()"

	case *syntax.CondExpr:
		return "cond()"

	case *syntax.DictEntry:
		return "dictEntry()"

	case *syntax.DictExpr:
		return "dict()"

	case *syntax.DotExpr:
		return "dot()"

	case *syntax.Ident:
		return fmt.Sprintf("%s()", item.Name)

	case *syntax.IndexExpr:
		return "index()"

	case *syntax.LambdaExpr:
		return "lambda()"

	case *syntax.ListExpr:
		return "list()"

	case *syntax.Literal:
		return "literal()"

	case *syntax.ParenExpr:
		return "paren()"

	case *syntax.SliceExpr:
		return "slice()"

	case *syntax.TupleExpr:
		return "tuple()"

	case *syntax.UnaryExpr:
		return "unary()"

	}
	return "????()"
}
