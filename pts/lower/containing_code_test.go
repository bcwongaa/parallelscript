package lower

import (
	"math/rand/v2"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/ir"
)

func TestContainingLevelsUseEveryLateDescriptorHit(t *testing.T) {
	for _, later := range []Specialization{ValueInstances, DeclaredInstances} {
		t.Run([]string{"all", "value", "declared"}[later], func(t *testing.T) {
			all := pairing{name: "all", level: AllInstances}
			inner := pairing{name: "inner", level: ValueInstances}
			root := &substitution{used: true, users: []use{{pairings: []pairing{all}}}}
			var c containingCode
			c.record(inner, root, nil)
			omitted := map[pairing]bool{inner: true}
			if got := c.levels(omitted)[inner]; got != AllInstances {
				t.Fatalf("initial containing code: got %v", got)
			}
			fn := &ir.Func{}
			generic := &genericFunc{instances: []*instance{{fn: fn, subst: root}}}
			(&lowerer{}).useListedInstance(&demand{}, generic, pairing{name: "later", level: later}, fn)
			if got := c.levels(omitted)[inner]; got != ValueInstances {
				t.Fatalf("late descriptor hit at %v must retain the inner pairing: got %v", later, got)
			}
		})
	}
}

func TestContainingLevelsNeedBothDemandAndValueCode(t *testing.T) {
	all := pairing{name: "all", level: AllInstances}
	p := pairing{name: "inner", level: ValueInstances}
	inside := &substitution{used: true, users: []use{{pairings: []pairing{all}}}}
	for _, valueOnly := range []bool{false, true} {
		var c containingCode
		if valueOnly {
			c.record(p, nil, inside)
		} else {
			c.record(p, inside, nil)
		}
		if got := c.levels(map[pairing]bool{p: true})[p]; got != AllInstances {
			t.Fatalf("valueOnly=%v: got %v", valueOnly, got)
		}
		c.record(p, nil, nil)
		if got := c.levels(map[pairing]bool{p: true})[p]; got != ValueInstances {
			t.Fatalf("a second program-owned occurrence must retain the pairing: got %v", got)
		}
	}
}

func TestContainingLevelsNeedTheEnclosingClass(t *testing.T) {
	all := pairing{name: "class", level: AllInstances}
	p := pairing{name: "method body", level: DeclaredInstances}
	class := &substitution{used: true, users: []use{{pairings: []pairing{all}}}}
	method := &substitution{used: true, ownUse: true, outer: class}
	var c containingCode
	c.record(p, method, nil)
	omitted := map[pairing]bool{p: true}
	if got := c.levels(omitted)[p]; got != AllInstances {
		t.Fatalf("method cannot exist without its class: got %v", got)
	}
	class.ownUse = true
	if got := c.levels(omitted)[p]; got != DeclaredInstances {
		t.Fatalf("a late own class use keeps its method: got %v", got)
	}
}

func TestContainingSitesKeepEveryContextAndTheirRawKey(t *testing.T) {
	node := &ast.Node{}
	p := pairing{call: node, site: demandSite, level: ValueInstances}
	all := pairing{name: "all", level: AllInstances}
	root := &substitution{used: true, users: []use{{pairings: []pairing{all}}}}
	l := &lowerer{subst: root, narrowing: (&Narrowing{}).with([]pairing{p})}
	if !l.leavesOutSite(node, demandSite) {
		t.Fatal("recording containment must not change the raw omitted-site key")
	}
	if got := l.containing.levels(l.narrowing.pairings)[p]; got != AllInstances {
		t.Fatalf("omitted sites still record their containing instance: got %v", got)
	}
	l.subst = nil
	l.sitePairing(node, demandSite)
	if got := l.containing.levels(l.narrowing.pairings)[p]; got != ValueInstances {
		t.Fatalf("the same AST site in own code must retain its raw level: got %v", got)
	}
	n := &Narrowing{pairings: map[pairing]bool{p: true}, levels: map[pairing]Specialization{p: AllInstances}}
	if !n.leavesOut(p) || !n.Within(nil, ValueInstances) {
		t.Fatal("effective levels may cap Within, never pairing identity")
	}
	n.levels = nil
	if n.Within(nil, ValueInstances) {
		t.Fatal("a missing proof must use the intrinsic level")
	}
}

func TestContainingLevelsDontTreatStatementBlameAsContainment(t *testing.T) {
	all := pairing{name: "site", level: AllInstances}
	p := pairing{name: "inner", level: ValueInstances}
	root := &substitution{}
	l := &lowerer{}
	l.useFrom(root, use{pairings: []pairing{all}})
	l.useFrom(root, use{pairings: []pairing{all}, statement: true})
	if len(root.users) != 2 {
		t.Fatal("a later report-only use must not be deduplicated against a containing use")
	}
	var c containingCode
	c.record(p, root, nil)
	if got := c.levels(map[pairing]bool{p: true})[p]; got != ValueInstances {
		t.Fatalf("statementSites is not proof that code disappears: got %v", got)
	}
	l.statementSites = []pairing{all}
	if !l.here().statement {
		t.Fatal("statement blame must retain its report-only provenance")
	}
}

func TestContainingLevelsPropagateThroughCyclesFromLiveSources(t *testing.T) {
	p := pairing{name: "inner", level: DeclaredInstances}
	a, b := &substitution{used: true}, &substitution{used: true}
	a.users, b.users = []use{{subst: b}}, []use{{subst: a}}
	var c containingCode
	c.record(p, a, nil)
	omitted := map[pairing]bool{p: true}
	if got := c.levels(omitted)[p]; got != AllInstances {
		t.Fatalf("unseeded cycle cannot make code: got %v", got)
	}
	value := pairing{name: "value", level: ValueInstances}
	b.users = append(b.users, use{pairings: []pairing{value}})
	if got := c.levels(omitted)[p]; got != ValueInstances {
		t.Fatalf("ValueInstances seed must propagate through the cycle: got %v", got)
	}
	b.ownUse = true
	if got := c.levels(omitted)[p]; got != DeclaredInstances {
		t.Fatalf("a late own seed must propagate through the cycle: got %v", got)
	}
}

func TestContainingLevelsUnknownAndExhaustedProofsKeepIntrinsicLevels(t *testing.T) {
	p := pairing{name: "inner", level: ValueInstances}
	omitted := map[pairing]bool{p: true}
	var c containingCode
	if got := c.levels(omitted)[p]; got != p.level {
		t.Fatalf("unrecorded pairing: got %v", got)
	}
	c.record(p, &substitution{}, nil)
	if got := c.levels(omitted)[p]; got != p.level {
		t.Fatalf("unknown containing code: got %v", got)
	}
	// Exercise the storage boundary without allocating 100,000 distinct contexts.
	c.contexts = maxContainingSteps
	c.record(p, nil, nil)
	if c.exhausted {
		t.Fatal("an existing context consumes no more budget")
	}
	c.record(p, &substitution{used: true, ownUse: true}, nil)
	if !c.exhausted || c.levels(omitted) != nil {
		t.Fatal("an incomplete occurrence set must disable the entire cap proof")
	}
	all := pairing{name: "all", level: AllInstances}
	root := &substitution{used: true, users: make([]use, maxContainingSteps)}
	for i := range root.users {
		root.users[i] = use{pairings: []pairing{all}}
	}
	var large containingCode
	large.record(p, root, nil)
	if large.levels(omitted) != nil {
		t.Fatal("graph exhaustion must not publish a partial cap")
	}
}

func TestContainingLevelsMatchAnIndependentSmallGraphOracle(t *testing.T) {
	random := rand.New(rand.NewPCG(19, 23))
	for trial := range 5000 {
		pairings := make([]pairing, 2+random.IntN(5))
		omitted := map[pairing]bool{}
		for i := range pairings {
			pairings[i] = pairing{call: &ast.Node{}, level: Specialization(random.IntN(3))}
			omitted[pairings[i]] = true
		}
		nodes := make([]*substitution, 2+random.IntN(7))
		for i := range nodes {
			nodes[i] = &substitution{used: true, ownUse: random.IntN(7) == 0}
		}
		for i, node := range nodes {
			for range random.IntN(4) {
				if random.IntN(2) == 0 {
					node.users = append(node.users, use{pairings: []pairing{pairings[random.IntN(len(pairings))]}, statement: random.IntN(8) == 0})
				} else {
					node.users = append(node.users, use{subst: nodes[random.IntN(len(nodes))]})
				}
			}
			if i+1 < len(nodes) && random.IntN(3) == 0 {
				node.outer = nodes[i+1+random.IntN(len(nodes)-i-1)]
			}
		}
		var c containingCode
		context := func() *substitution {
			if random.IntN(4) == 0 {
				return nil
			}
			return nodes[random.IntN(len(nodes))]
		}
		for _, p := range pairings {
			for range random.IntN(4) {
				c.record(p, context(), context())
			}
		}
		levels := c.levels(omitted)
		for _, at := range []Specialization{ValueInstances, DeclaredInstances} {
			made := containingAt(c, pairings, nodes, at)
			for _, p := range pairings {
				if (levels[p] >= at) != made[p] {
					t.Fatalf("trial %d at %v: intrinsic %v, got cap %v, expected available=%v", trial, at, p.level, levels[p], made[p])
				}
			}
		}
	}
}

// containingAt is intentionally iterative and boolean, independent of the production
// worklist/level lattice. Repeatedly apply the source rules at one fallback, from nothing.
func containingAt(c containingCode, pairings []pairing, nodes []*substitution, at Specialization) map[pairing]bool {
	made := map[pairing]bool{}
	code := map[*substitution]bool{nil: true}
	for changed := true; changed; {
		changed = false
		for _, p := range pairings {
			if made[p] || p.level < at {
				continue
			}
			available := len(c.pairings[p]) == 0
			for context := range c.pairings[p] {
				available = available || code[context.demand] && code[context.value]
			}
			if available {
				made[p], changed = true, true
			}
		}
		for _, s := range nodes {
			if code[s] || !code[s.outer] {
				continue
			}
			available := s.ownUse
			for _, u := range s.users {
				switch {
				case u.statement:
					available = true
				case u.pairings != nil:
					for _, p := range u.pairings {
						available = available || made[p]
					}
				default:
					available = available || code[u.subst]
				}
			}
			if available {
				code[s], changed = true, true
			}
		}
	}
	return made
}
