package lower

import (
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/diagnostic"
)

// A build's lowering may fail in code that only its speculative instances make, in ways a
// trial can't take back (D-190): a generic class instance a trial made, whose body is
// lowered later; a relaxed cast in a kept instance, checked after lowering; a call through
// an interface whose demand by name doesn't lower at its own site. The driver then lowers
// the program again leaving out only what the failures came from (C176): each report is
// blamed on the pairing of a demand with an erased value or generic method whose trial
// made the code that reported, or on the call site whose demand by name reported, where
// the last fallback (DeclaredInstances) doesn't make that pairing or demand. A report of
// the program's own code, or of a pairing every fallback makes, stands, as before.

// pairing is a demand's call (and for a demand by name, its name) paired with an erased
// value or generic method, by declaration, or for a call site whose demand by name
// reported (site), with nothing.
type pairing struct {
	call  *ast.Node
	name  string
	value *ast.Node
}

// Narrowing is what a lowering leaves out of the instances it would make: the pairings
// whose trials specialize skips, and the call sites that make no demand by name, lowering
// as ValueInstances lowers them. The zero Narrowing leaves out nothing.
type Narrowing struct {
	pairings map[pairing]bool
}

// Len is how many pairings and call sites n leaves out.
func (n *Narrowing) Len() int {
	if n == nil {
		return 0
	}
	return len(n.pairings)
}

func (n *Narrowing) leavesOut(p pairing) bool {
	return n != nil && n.pairings[p]
}

// with is n and the pairings more.
func (n *Narrowing) with(more []pairing) *Narrowing {
	next := &Narrowing{pairings: map[pairing]bool{}}
	if n != nil {
		for p := range n.pairings {
			next.pairings[p] = true
		}
	}
	for _, p := range more {
		next.pairings[p] = true
	}
	return next
}

// blame is what a diagnostic's reports are blamed on: the pairings or call sites a
// narrower lowering leaves out, unless one of its reports was of code that is the
// program's own, or that every fallback makes (own).
type blame struct {
	own      bool
	pairings []pairing
}

// reported is what lowering has reported so far, which code taken back restores: report
// only appends to errors and blames, and copies blames before it changes one.
type reported struct {
	errors           []diagnostic.Diagnostic
	blames           []blame
	reports          int
	unexplained      *ast.Node
	unexplainedBlame *pairing
}

func (l *lowerer) reportedSoFar() reported {
	return reported{errors: l.errors, blames: l.blames, reports: l.reports, unexplained: l.unexplained, unexplainedBlame: l.unexplainedBlame}
}

func (l *lowerer) restoreReported(r reported) {
	l.errors, l.blames, l.reports, l.unexplained, l.unexplainedBlame = r.errors, r.blames, r.reports, r.unexplained, r.unexplainedBlame
}

// blameHere is what a report of the code being lowered is blamed on, or nil if it stands:
// the call site whose demand by name is being lowered (site), or else the innermost
// pairing that a narrower lowering leaves out whose trial made the code, the trial being
// run or one that made the instance being lowered, or one the instance was made in.
func (l *lowerer) blameHere() *pairing {
	if l.site != nil {
		return l.site
	}
	if t := l.trial; t != nil && t.narrowable {
		return &t.pairing
	}
	for s := l.subst; s != nil; s = s.made {
		if t := s.trial; t != nil && t.narrowable {
			return &t.pairing
		}
	}
	return nil
}

// blameReport records what a report of d, at index i of errors (a new one if i is
// len(errors)), is blamed on: b, or the program's own code if b is nil.
func (l *lowerer) blameReport(i int, b *pairing) {
	if i == len(l.blames) {
		l.blames = append(l.blames, blame{})
	} else {
		l.blames = slices.Clone(l.blames) // saved reported values keep theirs
	}
	entry := l.blames[i]
	switch {
	case b == nil:
		entry.own = true
	case !slices.Contains(entry.pairings, *b):
		entry.pairings = append(slices.Clone(entry.pairings), *b)
	}
	l.blames[i] = entry
}

// narrower is the narrowing that leaves out what the errors are blamed on, besides what
// l.narrowing leaves out, if every error is blamed on something it doesn't leave out yet;
// else nil.
func (l *lowerer) narrower() *Narrowing {
	if len(l.errors) == 0 {
		return nil
	}
	var more []pairing
	for _, b := range l.blames {
		if b.own || len(b.pairings) == 0 {
			return nil
		}
		for _, p := range b.pairings {
			if l.narrowing.leavesOut(p) {
				return nil // left out already: it can't be what failed
			}
			if !slices.Contains(more, p) {
				more = append(more, p)
			}
		}
	}
	return l.narrowing.with(more)
}

// blamingSite blames the reports of what is lowered next, the part of the call at node that
// only its demand by name lowers, on the call site (pairing{call: node}), until restore.
func (l *lowerer) blamingSite(node *ast.Node) (restore func()) {
	outer := l.site
	l.site = &pairing{call: node}
	return func() { l.site = outer }
}

// leavesOutSite reports whether the call at node makes no demand by name (Narrowing).
func (l *lowerer) leavesOutSite(node *ast.Node) bool {
	return l.narrowing.leavesOut(pairing{call: node})
}

// leavesOut reports whether specialize skips the pairing of a demand with an erased value
// or generic method declared at value (Narrowing).
func (l *lowerer) leavesOut(d *demand, value *ast.Node) bool {
	return l.narrowing.leavesOut(pairing{call: d.node, name: d.name, value: value})
}

// valueNarrowable reports whether the last fallback doesn't list instances in v: a generic
// arrow function's or function expression's erased copy (D-190), an object literal's
// generic method (D-191) or a wrapper (D-194).
func valueNarrowable(v *erasedValue) bool {
	return v.forward != nil || v.generic.closure != nil || v.generic.field != nil
}
