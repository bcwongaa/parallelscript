package lower

import (
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/pts/diagnostic"
	"github.com/microsoft/typescript-go/pts/ir"
)

// A build's lowering may fail in code that only its speculative instances make, in ways a
// trial can't take back (D-190): a generic class instance a trial made, whose body is
// lowered later; a relaxed cast in a kept instance, checked after lowering; a call whose
// demand doesn't lower at its own site, or whose result doesn't fit where it goes. The
// driver then lowers the program again leaving out only what the failures came from (C176,
// D-199): each report is blamed on the pairings of demands with erased values or generic
// methods whose trials made, or found, what the code that reported belongs to, or on the
// call sites whose demands reported or made what the statement reporting uses, where a
// later fallback doesn't make them. A report of the program's own code, or of code every
// fallback makes, stands, as before.

// pairing is a demand's call (and for a demand by name, its name) paired with an erased
// value or generic method, by declaration; or a call site (site) whose demand a later
// fallback doesn't make, with nothing. level is the last of Fallbacks that makes it.
type pairing struct {
	call  *ast.Node
	name  string
	value *ast.Node
	site  siteKind
	level Specialization
}

// siteKind is what a call site that a narrower lowering lowers as a later fallback does
// lowers otherwise: none for a pairing.
type siteKind int

const (
	notASite        siteKind = iota
	byNameSite               // a demand by name (keyedInstanceCall, D-191, D-194): AllInstances only
	constantKeySite          // a constant key's call lowered as its dotted call (constantKeyCall, C185)
	demandSite               // a value's or dispatched method's demand with a rest parameter or a dynamic result (siteDemandAt)
)

// siteLevels are the last of Fallbacks that make each kind of call site's demand.
var siteLevels = map[siteKind]Specialization{byNameSite: AllInstances, constantKeySite: ValueInstances, demandSite: ValueInstances}

// Narrowing is what a lowering leaves out of the instances it would make: the pairings
// whose trials specialize skips, and the call sites that make no demand, lowering as the
// first later fallback that doesn't make it does. The zero Narrowing leaves out nothing.
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

// Within reports whether narrowing leaves out every pairing and call site that n leaves out
// and a lowering with at makes: a lowering that leaves out n then keeps all of the instances
// that a lowering with at, leaving out narrowing, keeps. A nil narrowing leaves out nothing.
func (n *Narrowing) Within(narrowing *Narrowing, at Specialization) bool {
	if n != nil {
		for p := range n.pairings {
			if p.level >= at && !narrowing.leavesOut(p) {
				return false
			}
		}
	}
	return true
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

// use is code that made or found an instance, or that reported (here): a trial's or a call
// site's, whose pairings a narrower lowering leaves out; or an instance's (subst), which is
// what that instance's own uses are; or the program's own (neither).
type use struct {
	pairings []pairing
	subst    *substitution
}

// here is the code being lowered, as a use: the call site whose demand is being lowered
// (site); the trial being run, for its own code (what an instance it is making declares, or
// a function it made); the call sites whose demands made what the statement being lowered
// uses (statementSites); or the instance being lowered (the first substitution out from it
// with uses: an instance's, a class instance's or an erased copy's); else the program's own.
// Trials and call sites that every fallback makes are none of these: their code is what the
// instances it is in are. A trial's report, of its own code, is taken back with the trial
// even where a call site in it reported: a later fallback may make the site and not the
// trial, and leaving the site out would leave it out where the program's own code lowers it.
func (l *lowerer) here() use {
	if l.site != nil {
		return use{pairings: []pairing{*l.site}}
	}
	if t := l.trial; t != nil && t.pairing.level < DeclaredInstances && (t.making > 0 || t.owns(l.function)) {
		return use{pairings: []pairing{t.pairing}}
	}
	if len(l.statementSites) > 0 {
		return use{pairings: slices.Clone(l.statementSites)}
	}
	for s := l.subst; s != nil; s = s.made {
		if s.used {
			return use{subst: s}
		}
	}
	return use{}
}

// use records that the code being lowered (here) makes or finds the instance of subst.
func (l *lowerer) use(subst *substitution) {
	subst.used = true
	if subst.ownUse {
		return // the program's own: what else uses it doesn't matter
	}
	u := l.here()
	if u.pairings == nil && u.subst == nil {
		subst.ownUse, subst.users = true, nil
		return
	}
	if u.subst == subst || slices.ContainsFunc(subst.users, func(other use) bool { return other.subst == u.subst && slices.Equal(other.pairings, u.pairings) }) {
		return
	}
	subst.users = append(subst.users, u)
}

// blames are what blameOf has found so far, for the substitutions with uses (nil for one
// being found), and how many uses it has followed, at most maxBlameSteps.
type blames struct {
	found map[*substitution]*blame
	steps int
}

// maxBlameSteps bounds blameOf's search: past it the code is taken to be the program's own.
const maxBlameSteps = 100000

func newBlames() *blames { return &blames{found: map[*substitution]*blame{}} }

// blameOf is what code that is u is blamed on: the pairings and call sites a narrower
// lowering leaves out so that none of it is made, or own if it can't (the program's own
// code makes it, or every fallback does). An instance of a method of a generic class's
// instance isn't made without the class instance either (outer): where its uses are own,
// or blamed on later fallbacks' code, the class instance's blame may be; and it adds
// nothing to the class instance's own blame. Instances that make each other are what
// makes the first: a use being found adds nothing (cut), which the use's own search adds
// where it merges its users, so a search from one use is a union over what reaches it, and
// each substitution is searched once (search is one query's: what a cut leaves out of a
// substitution's blame is the query's own use's).
func (l *lowerer) blameOf(u use, search *blames) (b blame, cut bool) {
	switch {
	case u.pairings != nil:
		return blame{pairings: u.pairings}, false
	case u.subst == nil:
		return blame{own: true}, false
	}
	if found, ok := search.found[u.subst]; ok {
		if found == nil {
			return blame{}, true
		}
		return *found, false
	}
	if outer := u.subst.outer; outer != nil {
		if found, ok := search.found[outer]; ok && found == nil {
			return blame{}, true
		}
	}
	if search.steps++; search.steps > maxBlameSteps {
		return blame{own: true}, false
	}
	search.found[u.subst] = nil
	b = blame{own: u.subst.ownUse}
	for _, user := range u.subst.users {
		if b.own {
			break
		}
		ub, c := l.blameOf(user, search)
		b, cut = b.merge(ub), cut || c
	}
	if outer := u.subst.outer; outer != nil {
		class, c := l.blameOf(use{subst: outer}, search)
		cut = cut || c
		if !class.own && len(class.pairings) > 0 && (b.own || len(b.pairings) == 0 || class.level() < b.level()) {
			b = class
		}
	}
	search.found[u.subst] = &b
	return b, cut
}

// blame is what a diagnostic's reports, or a cast's sources, are blamed on: the pairings
// or call sites a narrower lowering leaves out, unless one of them was of code that is the
// program's own, or that every fallback makes (own).
type blame struct {
	own      bool
	pairings []pairing
}

// with is b blamed on pairings too.
func (b blame) with(pairings []pairing) blame {
	for _, p := range pairings {
		if !slices.Contains(b.pairings, p) {
			b.pairings = append(slices.Clip(b.pairings), p)
		}
	}
	return b
}

// merge is what b and other are blamed on together: own if either is.
func (b blame) merge(other blame) blame {
	merged := b.with(other.pairings)
	merged.own = b.own || other.own
	return merged
}

// level is the latest of Fallbacks that makes some of what b is blamed on.
func (b blame) level() Specialization {
	level := AllInstances
	for _, p := range b.pairings {
		level = max(level, p.level)
	}
	return level
}

// reported is what lowering has reported so far, which code taken back restores: report
// only appends to errors and errorUses, and copies errorUses before it changes an entry.
type reported struct {
	errors         []diagnostic.Diagnostic
	uses           [][]use
	reports        int
	unexplained    *ast.Node
	unexplainedUse use
}

func (l *lowerer) reportedSoFar() reported {
	return reported{errors: l.errors, uses: l.errorUses, reports: l.reports, unexplained: l.unexplained, unexplainedUse: l.unexplainedUse}
}

func (l *lowerer) restoreReported(r reported) {
	l.errors, l.errorUses, l.reports, l.unexplained, l.unexplainedUse = r.errors, r.uses, r.reports, r.unexplained, r.unexplainedUse
}

// noteUse records that a report of errors[i] (a new one if i is len(errors)) was of u.
func (l *lowerer) noteUse(i int, u use) {
	if i == len(l.errorUses) {
		l.errorUses = append(l.errorUses, nil)
	} else {
		l.errorUses = slices.Clone(l.errorUses) // saved reported values keep theirs
	}
	l.errorUses[i] = append(slices.Clip(l.errorUses[i]), u)
}

// narrower is the narrowing that leaves out what the errors are blamed on, besides what
// l.narrowing leaves out, if every error is blamed on something it doesn't leave out yet;
// else nil. Blames are found once the program is lowered, when every trial that found an
// instance has run.
func (l *lowerer) narrower() *Narrowing {
	if len(l.errors) == 0 {
		return nil
	}
	var more []pairing
	for _, uses := range l.errorUses {
		var b blame
		for _, u := range uses {
			ub, _ := l.blameOf(u, newBlames())
			b = b.merge(ub)
		}
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

// blamingSite makes what is lowered next, the part of the call at node that only its
// demand (kind) lowers, the call site's, until restore.
func (l *lowerer) blamingSite(node *ast.Node, kind siteKind) (restore func()) {
	outer := l.site
	l.site = &pairing{call: node, site: kind, level: siteLevels[kind]}
	return func() { l.site = outer }
}

// usedSite records that the statement being lowered uses what the demand of the call at
// node (kind) made: the rest of the statement is the call site's code too (here), a result
// that doesn't fit where it goes among it.
func (l *lowerer) usedSite(node *ast.Node, kind siteKind) {
	if p := (pairing{call: node, site: kind, level: siteLevels[kind]}); !slices.Contains(l.statementSites, p) {
		l.statementSites = append(slices.Clip(l.statementSites), p)
	}
}

// leavesOutSite reports whether the call at node is lowered as the first later fallback
// that doesn't make its demand (kind) lowers it (Narrowing).
func (l *lowerer) leavesOutSite(node *ast.Node, kind siteKind) bool {
	return l.narrowing.leavesOut(pairing{call: node, site: kind, level: siteLevels[kind]})
}

// pairingOf is a demand's pairing with an erased value or generic method declared at
// value, which the last fallback that makes both makes (level is the value's).
func pairingOf(d *demand, value *ast.Node, level Specialization) pairing {
	return pairing{call: d.node, name: d.name, value: value, level: min(d.level(), level)}
}

// valueLevel is the last of Fallbacks that lists instances in v: AllInstances for an
// object literal's generic method (D-191) or a wrapper (D-194), ValueInstances for a
// generic arrow function's or function expression's erased copy (D-190), else (a
// declaration's) DeclaredInstances.
func valueLevel(v *erasedValue) Specialization {
	switch {
	case v.forward != nil || v.generic.field != nil:
		return AllInstances
	case v.generic.closure != nil:
		return ValueInstances
	}
	return DeclaredInstances
}

// classUse is the code that made class's objects exist where a relaxed cast meets them: the
// instance a generic class's instance is, else the program's own (relaxedCastBlame).
func (l *lowerer) classUse(class *ir.Class) use {
	if inst := l.classInstances[class]; inst != nil && inst.subst.used {
		return use{subst: inst.subst}
	}
	return use{}
}
