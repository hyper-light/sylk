package main

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestNoGoAnalyzer(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, Analyzer, "forbidden", "allowed", "core/concurrency")
}
