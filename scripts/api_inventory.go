// api_inventory reads Go syntax without starting OpenList or contacting storage.
// Run from the repository root: go run scripts/api_inventory.go > inventory.json
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type source struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Code string `json:"code"`
}
type field struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	JSON     string `json:"json"`
	Form     string `json:"form"`
	Binding  string `json:"binding"`
	Embedded bool   `json:"embedded"`
}
type definition struct {
	Source source  `json:"source"`
	Type   string  `json:"type"`
	Fields []field `json:"fields,omitempty"`
}
type parameter struct {
	In      string `json:"in"`
	Name    string `json:"name"`
	Default string `json:"default,omitempty"`
}
type binding struct {
	Method string `json:"method"`
	Type   string `json:"type"`
}
type contract struct {
	Source     source      `json:"source"`
	Bindings   []binding   `json:"bindings"`
	Parameters []parameter `json:"parameters"`
	Responses  []string    `json:"responses"`
	Calls      []string    `json:"calls"`
	Types      []string    `json:"types"`
}
type route struct {
	Method     string   `json:"method"`
	Path       string   `json:"path"`
	Handler    string   `json:"handler"`
	Middleware []string `json:"middleware"`
	Source     source   `json:"source"`
}
type group struct {
	path string
	mw   []string
}
type unit struct {
	file    string
	pkg     string
	imports map[string]string
	ast     *ast.File
}
type function struct {
	u    *unit
	decl *ast.FuncDecl
}

var fset = token.NewFileSet()
var functions = map[string]function{}
var types = map[string]definition{}
var contracts = map[string]contract{}
var routes []route
var sourceFiles []string

func text(n ast.Node) string {
	var b bytes.Buffer
	if n != nil {
		if err := printer.Fprint(&b, fset, n); err != nil {
			panic(err)
		}
	}
	return b.String()
}
func location(u *unit, n ast.Node) source {
	return source{u.file, fset.Position(n.Pos()).Line, text(n)}
}
func qualified(u *unit, expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		switch x.Name {
		case "string", "bool", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64", "byte", "any", "error":
			return x.Name
		}
		return u.pkg + "." + x.Name
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			if p, ok := u.imports[id.Name]; ok {
				return p + "." + x.Sel.Name
			}
		}
		if strings.HasPrefix(text(x), "message.HttpInstance.") {
			return "internal/message." + x.Sel.Name
		}
	case *ast.StarExpr:
		return "*" + qualified(u, x.X)
	case *ast.ArrayType:
		return "[]" + qualified(u, x.Elt)
	case *ast.MapType:
		return "map[" + qualified(u, x.Key) + "]" + qualified(u, x.Value)
	case *ast.InterfaceType:
		return "any"
	case *ast.IndexExpr:
		return qualified(u, x.X) + "[" + qualified(u, x.Index) + "]"
	}
	return text(expr)
}

func callable(u *unit, expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.IndexExpr:
		return callable(u, x.X)
	case *ast.IndexListExpr:
		return callable(u, x.X)
	}
	return qualified(u, expr)
}
func define(u *unit, name string, expr ast.Expr, n ast.Node) {
	d := definition{Source: location(u, n), Type: qualified(u, expr)}
	if st, ok := expr.(*ast.StructType); ok {
		d.Type = "object"
		for _, f := range st.Fields.List {
			tag := reflect.StructTag("")
			if f.Tag != nil {
				value, err := strconv.Unquote(f.Tag.Value)
				if err != nil {
					panic(err)
				}
				tag = reflect.StructTag(value)
			}
			if len(f.Names) == 0 {
				d.Fields = append(d.Fields, field{Type: qualified(u, f.Type), JSON: tag.Get("json"), Form: tag.Get("form"), Binding: tag.Get("binding"), Embedded: true})
			} else {
				for _, id := range f.Names {
					if ast.IsExported(id.Name) {
						d.Fields = append(d.Fields, field{Name: id.Name, Type: qualified(u, f.Type), JSON: tag.Get("json"), Form: tag.Get("form"), Binding: tag.Get("binding")})
					}
				}
			}
		}
	}
	types[name] = d
}
func inspectFunction(name string, fn function) {
	inspectBody(name, fn.u, fn.decl.Body, fn.decl)
}
func inspectBody(name string, u *unit, body *ast.BlockStmt, node ast.Node) {
	if body == nil {
		return
	}
	c := contract{Source: location(u, node), Bindings: []binding{}, Parameters: []parameter{}, Responses: []string{}, Calls: []string{}, Types: []string{}}
	vars := map[string]string{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.TypeSpec:
			key := name + "." + x.Name.Name
			define(u, key, x.Type, x)
			vars[x.Name.Name] = key
			c.Types = append(c.Types, key)
		case *ast.CompositeLit:
			c.Types = append(c.Types, qualified(u, x.Type))
		case *ast.ValueSpec:
			if x.Type != nil {
				t := qualified(u, x.Type)
				if _, ok := x.Type.(*ast.StructType); ok {
					t = name + "." + x.Names[0].Name
					define(u, t, x.Type, x)
				}
				if id, ok := x.Type.(*ast.Ident); ok {
					if local, ok := vars[id.Name]; ok {
						t = local
					}
				}
				for _, id := range x.Names {
					vars[id.Name] = t
				}
				c.Types = append(c.Types, t)
			}
		case *ast.AssignStmt:
			if len(x.Lhs) == 1 && len(x.Rhs) == 1 {
				if id, ok := x.Lhs[0].(*ast.Ident); ok {
					expr := x.Rhs[0]
					if unary, ok := expr.(*ast.UnaryExpr); ok {
						expr = unary.X
					}
					if literal, ok := expr.(*ast.CompositeLit); ok {
						vars[id.Name] = qualified(u, literal.Type)
					}
				}
			}
		case *ast.CallExpr:
			callName := callable(u, x.Fun)
			if _, known := functions[callName]; known && (strings.HasPrefix(callName, "server/handles.") || strings.HasPrefix(callName, "internal/message.")) {
				c.Calls = append(c.Calls, callName)
			}
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				receiver, _ := sel.X.(*ast.Ident)
				if receiver != nil && (receiver.Name == "c" || receiver.Name == "ctx") {
					switch sel.Sel.Name {
					case "Data", "DataFromReader", "Status", "JSON", "String", "Redirect", "File", "FileAttachment", "XML", "HTML", "Header":
						c.Responses = append(c.Responses, text(x))
					}
					if strings.HasPrefix(sel.Sel.Name, "ShouldBind") || sel.Sel.Name == "BindJSON" {
						if len(x.Args) > 0 {
							arg := x.Args[0]
							if unary, ok := arg.(*ast.UnaryExpr); ok {
								arg = unary.X
							}
							if id, ok := arg.(*ast.Ident); ok {
								if t, ok := vars[id.Name]; ok {
									c.Bindings = append(c.Bindings, binding{sel.Sel.Name, t})
								}
							}
						}
					}
					in := ""
					switch sel.Sel.Name {
					case "Query", "DefaultQuery", "GetQuery", "QueryArray", "GetQueryArray":
						in = "query"
					case "GetHeader":
						in = "header"
					case "Param":
						in = "path"
					case "PostForm", "DefaultPostForm", "GetPostForm":
						in = "form"
					case "FormFile":
						in = "file"
					}
					if in != "" && len(x.Args) > 0 {
						if lit, ok := x.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							value, err := strconv.Unquote(lit.Value)
							if err != nil {
								panic(err)
							}
							p := parameter{In: in, Name: value}
							if len(x.Args) > 1 {
								p.Default = text(x.Args[1])
							}
							c.Parameters = append(c.Parameters, p)
						}
					}
				}
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "common" && (strings.Contains(sel.Sel.Name, "Resp") || sel.Sel.Name == "ErrorPage") {
					c.Responses = append(c.Responses, text(x))
				}
			}
		}
		return true
	})
	contracts[name] = c
}
func stringArg(expr ast.Expr) (string, bool) {
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			panic(err)
		}
		return s, true
	}
	return "", false
}
func evalGroup(expr ast.Expr, env map[string]group) (group, bool) {
	if id, ok := expr.(*ast.Ident); ok {
		g, ok := env[id.Name]
		return g, ok
	}
	if call, ok := expr.(*ast.CallExpr); ok {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Group" {
			g, ok := evalGroup(sel.X, env)
			if !ok {
				return group{}, false
			}
			p, ok := stringArg(call.Args[0])
			if !ok && text(call.Args[0]) == "conf.URL.Path" {
				p, ok = "", true
			}
			if !ok {
				panic("nonliteral group: " + text(expr))
			}
			g.path = strings.TrimRight(g.path, "/") + p
			g.mw = append([]string{}, g.mw...)
			for _, mw := range call.Args[1:] {
				g.mw = append(g.mw, text(mw))
			}
			return g, true
		}
	}
	return group{}, false
}
func registerBody(fn function, body *ast.BlockStmt, env map[string]group, canRetry bool) {
	for _, stmt := range body.List {
		switch x := stmt.(type) {
		case *ast.AssignStmt:
			if len(x.Lhs) == 1 && len(x.Rhs) == 1 {
				if id, ok := x.Lhs[0].(*ast.Ident); ok {
					if g, ok := evalGroup(x.Rhs[0], env); ok {
						env[id.Name] = g
					}
				}
			}
		case *ast.IfStmt:
			if text(x.Cond) == "canRetry" && !canRetry {
				continue
			}
			registerBody(fn, x.Body, env, canRetry)
			if x.Else != nil {
				panic("conditional alternate route registration needs explicit support: " + text(x))
			}
		case *ast.ExprStmt:
			call, ok := x.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				g, ok := evalGroup(sel.X, env)
				if ok {
					switch sel.Sel.Name {
					case "GET", "POST", "PUT", "PATCH", "HEAD", "OPTIONS", "DELETE", "Any":
						p, ok := stringArg(call.Args[0])
						if !ok {
							panic("nonliteral route: " + text(call))
						}
						path := strings.TrimRight(g.path, "/") + p
						if !strings.HasPrefix(path, "/api/") {
							continue
						}
						last := call.Args[len(call.Args)-1]
						handler := callable(fn.u, last)
						if literal, ok := last.(*ast.FuncLit); ok {
							handler = "server/handles.taskRoute" + p
							inspectBody(handler, fn.u, literal.Body, literal)
						}
						if factory, ok := last.(*ast.CallExpr); ok {
							handler = callable(fn.u, factory.Fun)
						}
						mw := append([]string{}, g.mw...)
						for _, middleware := range call.Args[1 : len(call.Args)-1] {
							mw = append(mw, text(middleware))
						}
						routes = append(routes, route{sel.Sel.Name, path, handler, mw, location(fn.u, call)})
					case "Use":
						id, ok := sel.X.(*ast.Ident)
						if !ok {
							panic("unsupported middleware receiver: " + text(call))
						}
						g.mw = append([]string{}, g.mw...)
						for _, mw := range call.Args {
							g.mw = append(g.mw, text(mw))
						}
						env[id.Name] = g
					default:
						panic("unsupported router group call: " + text(call))
					}
					continue
				}
			}
			callee := callable(fn.u, call.Fun)
			if len(call.Args) > 0 {
				if g, ok := evalGroup(call.Args[0], env); ok {
					if next, ok := functions[callee]; ok {
						if strings.Contains(callee, ".debug") || strings.Contains(callee, ".WebDav") || strings.Contains(callee, ".S3") || strings.Contains(callee, ".MCP") || callee == "server/static.Static" {
							continue
						}
						argName := next.decl.Type.Params.List[0].Names[0].Name
						retry := true
						if len(call.Args) > 2 && text(call.Args[2]) == "false" {
							retry = false
						}
						registerBody(next, next.decl.Body, map[string]group{argName: g}, retry)
					} else {
						panic("unresolved route helper: " + callee)
					}
				}
			}
		default:
			// Do not quietly drop routes introduced inside a new loop/switch style.
			ast.Inspect(stmt, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if _, ok := evalGroup(sel.X, env); ok {
							panic("unsupported route registration construct: " + text(stmt))
						}
					}
					if len(call.Args) > 0 {
						if _, ok := evalGroup(call.Args[0], env); ok {
							panic("unsupported route helper construct: " + text(stmt))
						}
					}
				}
				return true
			})
		}
	}
}
func main() {
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") && path != "." || entry.Name() == "vendor" || entry.Name() == "node_modules" || entry.Name() == "public" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasPrefix(path, "scripts/") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		u := &unit{file: filepath.ToSlash(path), pkg: filepath.ToSlash(filepath.Dir(path)), imports: map[string]string{}, ast: file}
		sourceFiles = append(sourceFiles, u.file)
		for _, imp := range file.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			alias := filepath.Base(p)
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			u.imports[alias] = strings.TrimPrefix(p, "github.com/OpenListTeam/OpenList/v4/")
		}
		for _, decl := range file.Decls {
			switch x := decl.(type) {
			case *ast.FuncDecl:
				if x.Recv == nil || u.pkg == "internal/message" {
					functions[u.pkg+"."+x.Name.Name] = function{u, x}
				}
			case *ast.GenDecl:
				for _, spec := range x.Specs {
					if t, ok := spec.(*ast.TypeSpec); ok {
						define(u, u.pkg+"."+t.Name.Name, t.Type, x)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for name, fn := range functions {
		if fn.u.pkg == "server/handles" || fn.u.pkg == "internal/message" {
			inspectFunction(name, fn)
		}
	}
	init, ok := functions["server.Init"]
	if !ok {
		panic("server.Init not found")
	}
	registerBody(init, init.decl.Body, map[string]group{"e": {path: ""}}, true)
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path == routes[j].Path {
			return routes[i].Method < routes[j].Method
		}
		return routes[i].Path < routes[j].Path
	})
	seen := map[string]bool{}
	for _, r := range routes {
		key := r.Method + " " + r.Path
		if seen[key] {
			panic("duplicate route: " + key)
		}
		seen[key] = true
		if _, ok := contracts[r.Handler]; !ok {
			panic("unresolved handler: " + r.Handler)
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"routes": routes, "contracts": contracts, "types": types, "source_files": sourceFiles}); err != nil {
		panic(err)
	}
}
