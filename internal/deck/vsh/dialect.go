package vsh

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// interpBuiltins are the interpreter's own builtins that scripts may call.
var interpBuiltins = map[string]bool{
	"echo": true, "printf": true, "true": true, "false": true, ":": true, "test": true, "[": true,
}

const maxScript = 64 << 10

// allowed reports whether name is an allowed builtin or a shim.
func allowed(name string, shims map[string]*shim) bool {
	return interpBuiltins[name] || goBuiltins[name] != nil || shims[name] != nil
}

// parse parses script and checks that it stays inside the dialect.
func parse(script string, shims map[string]*shim) (*syntax.File, error) {
	if len(script) > maxScript {
		return nil, refusef("the script is longer than 64 KiB")
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(script), "")
	if err != nil {
		return nil, refusef("parse error: %v", err)
	}
	return f, checkStmts(f.Stmts, shims)
}

func checkStmts(stmts []*syntax.Stmt, shims map[string]*shim) error {
	for _, s := range stmts {
		if err := checkStmt(s, shims); err != nil {
			return err
		}
	}
	return nil
}

func checkStmt(s *syntax.Stmt, shims map[string]*shim) error {
	if s.Background || s.Coprocess {
		return refusef("background jobs (&) are not allowed")
	}
	for _, r := range s.Redirs {
		if err := checkRedir(r); err != nil {
			return err
		}
	}
	switch c := s.Cmd.(type) {
	case nil:
		return nil
	case *syntax.CallExpr:
		if len(c.Assigns) > 0 {
			return refusef("variables are not allowed")
		}
		name, ok := literal(c.Args[0])
		if !ok {
			return refusef("command names must be plain words")
		}
		if !allowed(name, shims) {
			return refusef("%q: not permitted by contract", name)
		}
		for _, w := range c.Args[1:] {
			if err := checkWord(w); err != nil {
				return err
			}
		}
		return nil
	case *syntax.BinaryCmd:
		if err := checkStmt(c.X, shims); err != nil {
			return err
		}
		return checkStmt(c.Y, shims)
	case *syntax.Subshell:
		return checkStmts(c.Stmts, shims)
	case *syntax.Block:
		return checkStmts(c.Stmts, shims)
	case *syntax.IfClause:
		for ; c != nil; c = c.Else {
			if err := checkStmts(c.Cond, shims); err != nil {
				return err
			}
			if err := checkStmts(c.Then, shims); err != nil {
				return err
			}
		}
		return nil
	case *syntax.WhileClause, *syntax.ForClause:
		return refusef("loops are not allowed")
	case *syntax.FuncDecl:
		return refusef("function definitions are not allowed")
	case *syntax.DeclClause:
		return refusef("%s is not allowed", c.Variant.Value)
	case *syntax.CaseClause:
		return refusef("case is not allowed")
	case *syntax.TestClause:
		return refusef("[[ ]] is not allowed; use [ ] or test")
	case *syntax.ArithmCmd, *syntax.LetClause:
		return refusef("arithmetic is not allowed")
	case *syntax.TimeClause:
		return refusef("time is not allowed")
	case *syntax.CoprocClause:
		return refusef("coproc is not allowed")
	default:
		return refusef("%T is not allowed", c)
	}
}

// literal returns the word when it is one unquoted literal.
func literal(w *syntax.Word) (string, bool) {
	if len(w.Parts) != 1 {
		return "", false
	}
	l, ok := w.Parts[0].(*syntax.Lit)
	if !ok {
		return "", false
	}
	return l.Value, true
}

func checkRedir(r *syntax.Redirect) error {
	if r.N != nil && r.N.Value != "0" && r.N.Value != "1" && r.N.Value != "2" {
		return refusef("only file descriptors 0, 1 and 2 may be redirected")
	}
	switch r.Op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrIn, syntax.RdrAll, syntax.AppAll:
		return checkWord(r.Word)
	case syntax.DplOut:
		if v, _ := literal(r.Word); v != "1" && v != "2" {
			return refusef("only >&1 and >&2 are allowed")
		}
		return nil
	case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
		return refusef("here-docs and here-strings are not allowed")
	default:
		return refusef("the %s redirection is not allowed", r.Op)
	}
}

func checkWord(w *syntax.Word) error {
	for _, p := range w.Parts {
		if err := checkPart(p, false); err != nil {
			return err
		}
	}
	return nil
}

func checkPart(p syntax.WordPart, quoted bool) error {
	switch p := p.(type) {
	case *syntax.Lit:
		if !quoted && strings.ContainsAny(p.Value, "{}") {
			return refusef("brace expansion is not allowed; quote words that contain { or }")
		}
	case *syntax.SglQuoted:
	case *syntax.DblQuoted:
		for _, q := range p.Parts {
			if err := checkPart(q, true); err != nil {
				return err
			}
		}
	case *syntax.ParamExp:
		return refusef("variables are not allowed")
	case *syntax.CmdSubst:
		return refusef("command substitution is not allowed")
	case *syntax.ArithmExp:
		return refusef("arithmetic expansion is not allowed")
	case *syntax.ProcSubst:
		return refusef("process substitution is not allowed")
	case *syntax.ExtGlob:
		return refusef("extended globs are not allowed")
	default:
		return refusef("this expansion is not allowed")
	}
	return nil
}
