package lower

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/microsoft/typescript-go/pts/ir"
)

func TestATrialsDependenciesFollowItsReusedInstance(t *testing.T) {
	for _, where := range []string{"signature", "body"} {
		t.Run(where, func(t *testing.T) {
			fn, root, dependency := &ir.Func{}, &substitution{used: true}, &substitution{}
			first := pairing{name: "first", level: AllInstances}
			second := pairing{name: "second", level: AllInstances}
			trial := &trial{root: root, pairing: first, funcs: map[*ir.Func]bool{fn: true}}
			if where == "signature" {
				trial.making = 1
			}
			l := &lowerer{trial: trial, subst: root, function: fn}
			l.use(root)
			l.use(dependency)
			l.trial = nil
			generic := &genericFunc{instances: []*instance{{fn: fn, subst: root}}}
			l.useListedInstance(&demand{}, generic, second, fn)
			b, _ := l.blameOf(use{subst: dependency}, newBlames())
			if b.own || !slices.Equal(b.pairings, []pairing{first, second}) {
				t.Fatalf("dependency must follow every use of the trial's instance: %+v", b)
			}
		})
	}
}

func TestDescriptorHitsKeepEveryPairingThatUsesTheInstance(t *testing.T) {
	fn, subst := &ir.Func{}, &substitution{}
	generic := &genericFunc{instances: []*instance{{fn: fn, subst: subst}}}
	l := &lowerer{}
	first := pairing{name: "first", level: AllInstances}
	second := pairing{name: "second", level: ValueInstances}
	l.useListedInstance(&demand{}, generic, first, fn)
	l.useListedInstance(&demand{}, generic, second, fn)
	l.useListedInstance(&demand{}, generic, second, fn)
	b, _ := l.blameOf(use{subst: subst}, newBlames())
	if b.own || !slices.Equal(b.pairings, []pairing{first, second}) || len(subst.users) != 2 {
		t.Fatalf("descriptor reuse lost a pairing or duplicated its use: %+v, users=%d", b, len(subst.users))
	}
}

func TestADeclaredDescriptorHitBelongsToItsDemandInstance(t *testing.T) {
	fn, subst := &ir.Func{}, &substitution{}
	generic := &genericFunc{instances: []*instance{{fn: fn, subst: subst}}}
	p := pairing{name: "caller", level: AllInstances}
	caller := &substitution{used: true, users: []use{{pairings: []pairing{p}}}}
	l := &lowerer{}
	declared := pairing{name: "declared", level: DeclaredInstances}
	l.useListedInstance(&demand{subst: caller}, generic, declared, fn)
	b, _ := l.blameOf(use{subst: subst}, newBlames())
	if b.own || !slices.Equal(b.pairings, []pairing{p}) {
		t.Fatalf("declared pairing must belong to the instance containing its call: %+v", b)
	}
	l.useListedInstance(&demand{}, generic, declared, fn)
	b, _ = l.blameOf(use{subst: subst}, newBlames())
	if !b.own {
		t.Fatalf("program-owned descriptor hit must not be removed: %+v", b)
	}
}

func TestAnErasedDescriptorEntryMakesNoInstance(t *testing.T) {
	fn, subst := &ir.Func{}, &substitution{}
	generic := &genericFunc{instances: []*instance{{fn: fn, subst: subst}}}
	(&lowerer{}).useListedInstance(&demand{}, generic, pairing{}, nil)
	if subst.used {
		t.Fatal("a descriptor entry with no function cannot keep an instance")
	}
}

func TestBlameOfAnOuterCycleKeepsTheProgramsOwnUse(t *testing.T) {
	p := pairing{name: "speculative", level: AllInstances}
	class := &substitution{used: true}
	helper := &substitution{used: true}
	method := &substitution{used: true, ownUse: true, outer: class}
	own := &substitution{used: true, ownUse: true}
	root := &substitution{used: true, outer: class, users: []use{{subst: helper}}}
	helper.users = []use{{subst: method}, {subst: own}}
	class.users = []use{{pairings: []pairing{p}}, {subst: helper}}

	// Searching method's outer caches class's partial {p} before helper reaches own.
	// own still makes helper and class without p; that stale result cannot remove root.
	b, _ := (&lowerer{}).blameOf(use{subst: root}, newBlames())
	if !b.own {
		t.Fatalf("program-owned class blamed on speculative pairings: %+v", b)
	}
}

func TestBlameOfAClassCanRemoveItsProgramUsedMethod(t *testing.T) {
	p := pairing{name: "speculative", level: AllInstances}
	class := &substitution{used: true, users: []use{{pairings: []pairing{p}}}}
	method := &substitution{used: true, ownUse: true, outer: class}
	b, _ := (&lowerer{}).blameOf(use{subst: method}, newBlames())
	if b.own || !slices.Equal(b.pairings, []pairing{p}) {
		t.Fatalf("method need not exist without its class: %+v", b)
	}
}

func TestBlameOfCutsActuallyRemoveTheReportedCode(t *testing.T) {
	random := rand.New(rand.NewPCG(3, 7))
	pairings := []pairing{{name: "all0", level: AllInstances}, {name: "value0", level: ValueInstances},
		{name: "all1", level: AllInstances}, {name: "value1", level: ValueInstances}}
	for trial := range 10000 {
		nodes := make([]*substitution, 2+random.IntN(7))
		for i := range nodes {
			nodes[i] = &substitution{used: true}
		}
		for i, node := range nodes {
			node.ownUse = random.IntN(6) == 0
			if !node.ownUse {
				for range random.IntN(4) {
					if random.IntN(3) == 0 {
						node.users = append(node.users, use{pairings: []pairing{pairings[random.IntN(len(pairings))]}})
					} else if j := random.IntN(len(nodes)); j != i {
						node.users = append(node.users, use{subst: nodes[j]})
					}
				}
			}
			if trial%2 == 0 && i+1 < len(nodes) && random.IntN(3) == 0 {
				node.outer = nodes[i+1+random.IntN(len(nodes)-i-1)] // enclosing classes cannot contain themselves
			}
		}
		root := nodes[0]
		if !madeWithout(nodes, nil)[root] {
			continue // code never made cannot report
		}
		b, _ := (&lowerer{}).blameOf(use{subst: root}, newBlames())
		if !b.own && madeWithout(nodes, b.pairings)[root] {
			t.Fatalf("trial %d: reported code still exists without its blame: %+v", trial, b)
		}
	}
}

// madeWithout is an independent, small-graph oracle: a node exists if any user makes it
// and its enclosing class exists. Iterate from nothing, so a cycle cannot make itself.
func madeWithout(nodes []*substitution, removed []pairing) map[*substitution]bool {
	made := map[*substitution]bool{}
	for changed := true; changed; {
		changed = false
		for _, node := range nodes {
			if made[node] || node.outer != nil && !made[node.outer] {
				continue
			}
			used := node.ownUse
			for _, user := range node.users {
				switch {
				case user.pairings != nil:
					for _, p := range user.pairings {
						used = used || !slices.Contains(removed, p)
					}
				case user.subst != nil:
					used = used || made[user.subst]
				default:
					used = true
				}
			}
			if used {
				made[node], changed = true, true
			}
		}
	}
	return made
}
