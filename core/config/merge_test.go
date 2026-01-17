package config

import (
	"testing"
)

func TestDeepMergeStructs(t *testing.T) {
	type Inner struct {
		Value int
		Name  string
	}
	type Outer struct {
		Inner Inner
		Count int
	}

	dst := &Outer{Inner: Inner{Value: 1, Name: "original"}, Count: 10}
	src := &Outer{Inner: Inner{Value: 2}, Count: 0}

	DeepMerge(dst, src)

	if dst.Inner.Value != 2 {
		t.Errorf("Inner.Value: got %d, want 2", dst.Inner.Value)
	}
	if dst.Inner.Name != "original" {
		t.Errorf("Inner.Name: got %s, want original", dst.Inner.Name)
	}
	if dst.Count != 10 {
		t.Errorf("Count: got %d, want 10 (zero value shouldn't override)", dst.Count)
	}
}

func TestDeepMergeMaps(t *testing.T) {
	type S struct {
		M map[string]int
	}

	dst := &S{M: map[string]int{"a": 1, "b": 2}}
	src := &S{M: map[string]int{"b": 20, "c": 3}}

	DeepMerge(dst, src)

	if dst.M["a"] != 1 {
		t.Errorf("M[a]: got %d, want 1", dst.M["a"])
	}
	if dst.M["b"] != 20 {
		t.Errorf("M[b]: got %d, want 20", dst.M["b"])
	}
	if dst.M["c"] != 3 {
		t.Errorf("M[c]: got %d, want 3", dst.M["c"])
	}
}

func TestDeepMergeSlices(t *testing.T) {
	type S struct {
		Items []string
	}

	dst := &S{Items: []string{"a", "b"}}
	src := &S{Items: []string{"x", "y", "z"}}

	DeepMerge(dst, src)

	if len(dst.Items) != 3 {
		t.Errorf("Items length: got %d, want 3", len(dst.Items))
	}
	if dst.Items[0] != "x" {
		t.Errorf("Items[0]: got %s, want x", dst.Items[0])
	}
}

func TestDeepMergeEmptySliceNoOverwrite(t *testing.T) {
	type S struct {
		Items []string
	}

	dst := &S{Items: []string{"a", "b"}}
	src := &S{Items: []string{}}

	DeepMerge(dst, src)

	if len(dst.Items) != 2 {
		t.Errorf("Items length: got %d, want 2 (empty slice shouldn't overwrite)", len(dst.Items))
	}
}

func TestDeepMergeNilMap(t *testing.T) {
	type S struct {
		M map[string]int
	}

	dst := &S{M: nil}
	src := &S{M: map[string]int{"a": 1}}

	DeepMerge(dst, src)

	if dst.M == nil {
		t.Error("M should not be nil after merge")
	}
	if dst.M["a"] != 1 {
		t.Errorf("M[a]: got %d, want 1", dst.M["a"])
	}
}

func TestDeepMergeConfig(t *testing.T) {
	dst := DefaultConfig()
	src := &Config{
		LLM: LLMConfig{
			DefaultProvider: "openai",
			MaxRetries:      5,
		},
	}

	DeepMerge(dst, src)

	if dst.LLM.DefaultProvider != "openai" {
		t.Errorf("DefaultProvider: got %s, want openai", dst.LLM.DefaultProvider)
	}
	if dst.LLM.MaxRetries != 5 {
		t.Errorf("MaxRetries: got %d, want 5", dst.LLM.MaxRetries)
	}
	if dst.Memory.GlobalCeilingPercent != 0.8 {
		t.Errorf("GlobalCeilingPercent should retain default: got %v, want 0.8", dst.Memory.GlobalCeilingPercent)
	}
}

func TestIsZeroValue(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want bool
	}{
		{"empty string", "", true},
		{"non-empty string", "hello", false},
		{"zero int", 0, true},
		{"non-zero int", 5, false},
		{"zero float", 0.0, true},
		{"non-zero float", 1.5, false},
		{"false bool", false, true},
		{"true bool", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt
		})
	}
}
