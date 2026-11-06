package loopcheck_test

import (
	"slices"
	"testing"

	"github.com/xtls/xray-core/common/loopcheck"
)

func TestFunctional(t *testing.T) {
	cases := []struct {
		name string
		next map[string]string
		want []string
	}{
		{"none", map[string]string{"a": "b", "b": "out", "c": "b"}, nil},
		{"self", map[string]string{"a": "a"}, []string{"a -> a"}},
		{"two", map[string]string{"b": "a", "a": "b"}, []string{"a -> b -> a"}},
		{"three from the smallest", map[string]string{"c": "a", "a": "b", "b": "c"}, []string{"a -> b -> c -> a"}},
		{"a tail into a cycle", map[string]string{"t": "x", "x": "y", "y": "x"}, []string{"x -> y -> x"}},
		{
			"two cycles in order",
			map[string]string{"q": "p", "p": "q", "b": "a", "a": "b"},
			[]string{"a -> b -> a", "p -> q -> p"},
		},
	}
	for _, c := range cases {
		if got := loopcheck.Functional(c.next); !slices.Equal(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFunctionalLongChain(t *testing.T) {
	next := map[string]string{}
	for i := byte(0); i < 63; i++ {
		next[string(rune('A'+i))] = string(rune('A' + i + 1))
	}
	if got := loopcheck.Functional(next); got != nil {
		t.Fatalf("a 64-vertex chain: %q", got)
	}
}
