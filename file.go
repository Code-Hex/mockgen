package funcy

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
)

type file struct {
	*ast.File
	pkg  string
	info *types.Info
	data map[string][]*Interface
}

var reserved = map[string]string{
	"bool":        "bval",
	"int":         "ival",
	"int8":        "i8val",
	"int16":       "i16val",
	"int32":       "i32val",
	"int64":       "i64val",
	"uint":        "uival",
	"uint8":       "ui8val",
	"uint16":      "ui16val",
	"uint32":      "ui32val",
	"uint64":      "ui64val",
	"float32":     "f32val",
	"float64":     "f64val",
	"complex64":   "cmplx64val",
	"complex128":  "cmplx128val",
	"string":      "strval",
	"struct{}":    "structval",
	"interface{}": "ifaceval",
}

var reservedPrefix = map[string]string{
	"map": "map",
	"[]":  "s",
}

func parse(fi string) (*file, error) {
	dir := filepath.Dir(fi)
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, fi, nil, parser.AllErrors)
	if err != nil {
		return nil, err
	}
	astFiles, err := loadStdlib(fset, af, fi, dir)
	if err != nil {
		return nil, err
	}
	info, err := check(fset, astFiles, dir)
	if err != nil {
		return nil, err
	}
	return &file{
		File: af,
		pkg:  af.Name.Name,
		info: info,
		data: make(map[string][]*Interface),
	}, nil
}

func check(fset *token.FileSet, astFiles []*ast.File, dir string) (*types.Info, error) {
	info := &types.Info{
		Uses: map[*ast.Ident]types.Object{},
	}
	var conf types.Config
	conf.Importer = importer.Default()
	if _, err := conf.Check(dir, fset, astFiles, info); err != nil {
		return nil, err
	}
	return info, nil
}

func loadStdlib(fset *token.FileSet, af *ast.File, path, dir string) ([]*ast.File, error) {
	buildPkg, err := build.ImportDir(dir, 0)
	if err != nil {
		return nil, err
	}
	base := filepath.Base(path)
	astFiles := make([]*ast.File, 0, 1+len(buildPkg.GoFiles)+len(buildPkg.CgoFiles))
	astFiles = append(astFiles, af)
	for _, files := range [...][]string{buildPkg.GoFiles, buildPkg.CgoFiles} {
		for _, file := range files {
			if file == base {
				// already parsed this file above
				continue
			}
			file, err := parser.ParseFile(fset, filepath.Join(dir, file), nil, 0)
			if err != nil {
				return nil, err
			}
			astFiles = append(astFiles, file)
		}
	}
	return astFiles, nil
}

func (f *file) getInterfaces() {
	name := "" // interface name
	result := make([]*Interface, 0)
	f.walk(func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.TypeSpec:
			if _, ok := v.Type.(*ast.InterfaceType); ok {
				name = v.Name.Name
			}
			result = result[:0]
		case *ast.InterfaceType:
			for _, x := range v.Methods.List {
				switch v := x.Type.(type) {
				case *ast.FuncType:
					i := &Interface{
						Name:   x.Names[0].Name,
						Param:  f.makeParam(v.Params.List),
						Return: f.makeReturn(v.Results.List),
					}
					result = append(result, i)
				}
			}
			f.data[name] = result
		}
		return true
	})
}

func (f *file) walk(fn func(ast.Node) bool) {
	ast.Walk(walker(fn), f.File)
}

func (f *file) makeReturn(list []*ast.Field) *Return {
	return &Return{
		Type:  f.getReturnFields(list),
		Value: f.getDefaultValues(list),
	}
}

func (f *file) makeParam(list []*ast.Field) *Param {
	field, names := f.getParamField(list)
	return &Param{
		TypeOnly: f.getParamTypes(list),
		NameOnly: names,
		Field:    field,
	}
}

func (f *file) getParamTypes(list []*ast.Field) string {
	params := make([]string, 0, len(list))
	for _, p := range list {
		params = append(params, f.getType(p.Type))
	}
	return strings.Join(params, ", ")
}

func (f *file) getParamField(list []*ast.Field) (string, string) {
	m := make(map[string]uint, 0)
	params := make([]string, 0, len(list))
	names := make([]string, 0, len(list))
	for _, p := range list {
		if len(p.Names) > 0 {
			// strings.Join()
			params = append(params, p.Names[0].Name+" "+f.getType(p.Type))
			names = append(names, p.Names[0].Name)
		} else {
			t := f.getType(p.Type)
			lt := strings.ToLower(t)
			key := makeIdentName(lt)
			if i, ok := m[key]; ok {
				params = append(params, fmt.Sprintf("%s%d %s", key, i, t))
				names = append(names, fmt.Sprintf("%s%d", key, i))
				m[key]++
			} else {
				params = append(params, fmt.Sprintf("%s %s", key, t))
				names = append(names, fmt.Sprintf("%s", key))
				m[key] = 0
			}
		}
	}
	return strings.Join(params, ", "), strings.Join(names, ", ")
}

func makeIdentName(lower string) string {
	ident := firstStep(lower)
	if v, ok := reserved[ident]; ok {
		return v
	}
	for k, v := range reservedPrefix {
		if strings.HasPrefix(ident, k) {
			idx := strings.Index(ident, "]")
			if idx != -1 {
				return makeIdentName(ident[idx+1:]) + v
			}
		}
	}
	return ident
}

func firstStep(lower string) string {
	for k := range reservedPrefix {
		if strings.HasPrefix(lower, k) {
			return lower
		}
	}
	idx := strings.Index(lower, ".")
	if idx != -1 {
		typ := lower[idx+1:]
		if typ == "context" {
			return "ctx"
		}
		return typ
	}
	return lower
}

func (f *file) getReturnFields(list []*ast.Field) string {
	params := make([]string, 0, len(list))
	for _, p := range list {
		if len(p.Names) > 0 {
			params = append(params, p.Names[0].Name+" "+f.getType(p.Type))
		} else {
			params = append(params, f.getType(p.Type))
		}
	}
	if len(params) > 1 {
		return "(" + strings.Join(params, ", ") + ")"
	}
	return params[0]
}

func (f *file) getDefaultValues(list []*ast.Field) string {
	params := make([]string, 0, len(list))
	for _, p := range list {
		params = append(params, f.getZeroValue(p.Type))
	}
	return strings.Join(params, ", ")
}

func (f *file) getZeroValue(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.StarExpr, *ast.SliceExpr, *ast.ArrayType, *ast.MapType, *ast.FuncType,
		*ast.ChanType, *ast.StructType, *ast.InterfaceType:
		return "nil"
	case *ast.SelectorExpr:
		return f.getZeroValue(v.Sel)
	case *ast.Ident:
		return f.getBuiltinZeroValue(v)
	}
	return "nil"
}

func (f *file) getBuiltinZeroValue(ident *ast.Ident) string {
	switch f.info.TypeOf(ident).Underlying().String() {
	case "uint8", "uint16", "uint32", "uint64", "uint", "uintptr",
		"int8", "int16", "int32", "int64", "int", "byte", "rune",
		"float32", "float64",
		"complex64", "complex128":
		return "0"
	case "bool":
		return "false"
	case "string":
		return `""`
	default:
		return "nil"
	}
}

func (f *file) getType(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return f.getType(v.X) + "." + v.Sel.Name
	case *ast.StarExpr:
		return "*" + f.getType(v.X)
	case *ast.ArrayType:
		return "[]" + f.getType(v.Elt)
	case *ast.MapType:
		return "map[" + f.getType(v.Key) + "]" + f.getType(v.Value)
	case *ast.FuncType:
		return "func(" + f.getParamTypes(v.Params.List) + ") " + f.getReturnFields(v.Results.List)
	case *ast.ChanType:
		ch := "chan "
		switch types.ChanDir(v.Dir) {
		case types.SendRecv:
		case types.SendOnly:
			ch += "<-"
		case types.RecvOnly:
			ch = "<-" + ch
		}
		return ch + f.getType(v.Value)
	case *ast.StructType:
		return "struct{}"
	case *ast.InterfaceType:
		return "interface{}"
	}
	return "nil"
}
