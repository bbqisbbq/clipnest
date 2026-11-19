// rmdecl 从 Go 源文件里按名字删除顶层声明（函数/方法/类型/常量/变量）。
//
// 为什么不用正则/行号：一次性删除脚本用"第一个 `}`"判断函数结尾时会被一行函数
// （`func X() { return y }`）带偏，把后面整段代码一起吞掉。AST 没有这个风险。
//
// 用法：
//
//	go run ./scratch/rmdecl -file internal/clip/snippet.go -names DeleteSnippet,GetSnippets
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

func main() {
	file := flag.String("file", "", "要修改的 Go 文件")
	names := flag.String("names", "", "逗号分隔的顶层声明名")
	flag.Parse()
	if *file == "" || *names == "" {
		fmt.Println("需要 -file 与 -names")
		os.Exit(2)
	}
	want := map[string]bool{}
	for _, n := range strings.Split(*names, ",") {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}

	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, *file, nil, parser.ParseComments)
	if err != nil {
		fmt.Println("parse:", err)
		os.Exit(1)
	}

	removed := []string{}
	blocked := []string{}
	decls := make([]ast.Decl, 0, len(af.Decls))
	for _, d := range af.Decls {
		// 单条声明的常量块（const ( X = 1 )）可以整块删；多条的留给人工，
		// 否则会把同块里还在用的常量一起带走。
		if g, ok := d.(*ast.GenDecl); ok && len(g.Specs) > 1 {
			if name, ok := declName(d); ok && want[name] {
				blocked = append(blocked, name)
			}
			decls = append(decls, d)
			continue
		}
		if name, ok := declName(d); ok && want[name] {
			removed = append(removed, name)
			continue
		}
		decls = append(decls, d)
	}
	if len(blocked) > 0 {
		fmt.Printf("%s: 在常量块里，需人工删：%s\n", *file, strings.Join(blocked, ", "))
	}
	if len(removed) == 0 {
		fmt.Printf("未找到：%s\n", *names)
		os.Exit(3)
	}
	if len(removed) == 0 {
		fmt.Printf("未找到：%s\n", *names)
		os.Exit(3)
	}
	af.Decls = decls

	var buf strings.Builder
	if err := format.Node(&buf, fset, af); err != nil {
		fmt.Println("format:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*file, []byte(buf.String()), 0o644); err != nil {
		fmt.Println("write:", err)
		os.Exit(1)
	}
	fmt.Printf("%s: 删除 %s\n", *file, strings.Join(removed, ", "))
}

// declName 返回顶层声明的名字；方法名按 "Type.Method" 之外的短名返回。
func declName(d ast.Decl) (string, bool) {
	switch t := d.(type) {
	case *ast.FuncDecl:
		return t.Name.Name, true
	case *ast.GenDecl:
		for _, s := range t.Specs {
			switch spec := s.(type) {
			case *ast.TypeSpec:
				return spec.Name.Name, true
			case *ast.ValueSpec:
				if len(spec.Names) == 1 {
					return spec.Names[0].Name, true
				}
			}
		}
	}
	return "", false
}
