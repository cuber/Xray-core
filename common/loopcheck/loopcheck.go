// Package loopcheck finds the cycles of the configuration graphs that Core refuses before they run:
// balancer fallbacks (Core spec017 L1) and outbound dialerProxy chains (Core spec018 S1).
package loopcheck

import (
	"maps"
	"slices"
	"strings"
)

// Functional returns every cycle of a graph in which each vertex has at most one edge, next[v], a
// self-reference included. A vertex that is no key of next ends a walk. Each cycle is printed from
// its smallest vertex back to it ("a -> b -> a"), and the cycles come in that order.
func Functional(next map[string]string) []string {
	const (
		unvisited = iota
		walking
		done
	)
	state := make(map[string]int, len(next))
	var cycles []string
	for _, start := range slices.Sorted(maps.Keys(next)) {
		var walk []string
		for v := start; state[v] != done; {
			if state[v] == walking {
				cycles = append(cycles, Format(walk[slices.Index(walk, v):]))
				break
			}
			state[v] = walking
			walk = append(walk, v)
			w, ok := next[v]
			if !ok {
				break
			}
			v = w
		}
		for _, v := range walk {
			state[v] = done
		}
	}
	slices.Sort(cycles)
	return cycles
}

// Format prints a cycle from its smallest vertex back to it: "a -> b -> a".
func Format(cycle []string) string {
	first := slices.Index(cycle, slices.Min(cycle))
	vertices := append(slices.Clone(cycle[first:]), cycle[:first]...)
	return strings.Join(append(vertices, vertices[0]), " -> ")
}
