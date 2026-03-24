package shared

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

// lockMethodNames are method names that acquire locks.
var lockMethodNames = map[string]bool{
	"Lock":  true,
	"RLock": true,
}

// unlockMethodNames are method names that release locks.
var unlockMethodNames = map[string]bool{
	"Unlock":  true,
	"RUnlock": true,
}

// RunDeadlockDetector performs AST-based lock ordering analysis on Go files.
func RunDeadlockDetector(ctx context.Context, runner *ToolRunner, paths []string) []ValidationIssue {
	var issues []ValidationIssue
	idx := 0

	goFiles, err := normalizeGoFileExecutionTargets(ctx, runner, paths)
	if err != nil {
		return executionFailureIssues("deadlock", err)
	}

	fset := token.NewFileSet()
	for _, file := range goFiles {
		select {
		case <-ctx.Done():
			return issues
		default:
		}

		content, err := readAnalysisFile(ctx, runner, file)
		if err != nil {
			continue
		}
		f, err := parser.ParseFile(fset, file, content, 0)
		if err != nil {
			continue
		}

		fileIssues := analyzeLockOrdering(fset, f, file, idx)
		issues = append(issues, fileIssues...)
		idx += len(fileIssues)
	}

	return DeduplicateIssues(issues)
}

func analyzeLockOrdering(fset *token.FileSet, f *ast.File, filename string, startIdx int) []ValidationIssue {
	var issues []ValidationIssue

	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}

		// Track lock acquisitions within the function
		var locks []lockAcquisition
		for _, stmt := range fn.Body.List {
			inspectStmtForLocks(fset, stmt, &locks)
		}

		// Detect nested locks without intervening unlocks
		if nested := findNestedLocks(locks); len(nested) > 0 {
			for _, pair := range nested {
				pos := fset.Position(pair.pos)
				issues = append(issues, ValidationIssue{
					ID:       fmt.Sprintf("dl_%d", startIdx+len(issues)),
					Severity: Critical,
					File:     filename,
					Line:     pos.Line,
					Column:   pos.Column,
					Message: fmt.Sprintf(
						"potential deadlock: nested lock %s acquired while %s is held in %s",
						pair.inner, pair.outer, fn.Name.Name,
					),
					RuleID: "deadlock",
				})
			}
		}

		return true
	})

	return issues
}

type lockAcquisition struct {
	name   string
	isLock bool
	pos    token.Pos
}

type nestedLockPair struct {
	outer string
	inner string
	pos   token.Pos
}

func inspectStmtForLocks(fset *token.FileSet, stmt ast.Stmt, locks *[]lockAcquisition) {
	ast.Inspect(stmt, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		name := selectorReceiverName(sel)
		method := sel.Sel.Name

		if lockMethodNames[method] {
			*locks = append(*locks, lockAcquisition{name: name, isLock: true, pos: call.Pos()})
		} else if unlockMethodNames[method] {
			*locks = append(*locks, lockAcquisition{name: name, isLock: false, pos: call.Pos()})
		}

		return true
	})
}

func selectorReceiverName(sel *ast.SelectorExpr) string {
	switch x := sel.X.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return selectorReceiverName(x) + "." + x.Sel.Name
	default:
		return "<unknown>"
	}
}

func findNestedLocks(locks []lockAcquisition) []nestedLockPair {
	var pairs []nestedLockPair
	held := make(map[string]bool)

	for _, acq := range locks {
		if acq.isLock {
			if len(held) > 0 {
				for outer := range held {
					if outer != acq.name {
						pairs = append(pairs, nestedLockPair{
							outer: outer,
							inner: acq.name,
							pos:   acq.pos,
						})
					}
				}
			}
			held[acq.name] = true
		} else {
			delete(held, acq.name)
		}
	}

	return pairs
}
