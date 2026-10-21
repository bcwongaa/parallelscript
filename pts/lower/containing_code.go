package lower

// containingCode records all occurrences of a pairing, without changing its identity.
// An occurrence needs both the demand's code and the value's code; another occurrence
// can keep the same pairing alive. Compute levels only after every instance user is known.
type containingCode struct {
	pairings  map[pairing]map[containingContext]bool
	contexts  int
	exhausted bool
}

type containingContext struct {
	demand, value *substitution
}

// Bound both recording and the completed availability graph. Exhaustion keeps intrinsic
// levels, never a proof made from an incomplete set of occurrences or users.
const maxContainingSteps = 100000

func (c *containingCode) record(p pairing, demand, value *substitution) {
	if c.exhausted {
		return
	}
	context := containingContext{instanceUse(demand).subst, instanceUse(value).subst}
	if c.pairings[p][context] {
		return
	}
	if c.contexts >= maxContainingSteps {
		c.pairings, c.exhausted = nil, true
		return
	}
	if c.pairings == nil {
		c.pairings = map[pairing]map[containingContext]bool{}
	}
	if c.pairings[p] == nil {
		c.pairings[p] = map[containingContext]bool{}
	}
	c.pairings[p][context] = true
	c.contexts++
}

// levels is the last fallback that can make each omitted pairing. This is a positive
// graph: users join by OR, enclosing code by AND. Start with no speculative code and
// propagate from program-owned/unknown sources, so an unseeded cycle cannot make itself.
func (c *containingCode) levels(omitted map[pairing]bool) map[pairing]Specialization {
	if c.exhausted {
		return nil
	}
	g := availabilityGraph{
		pairings: map[pairing]*availability{}, substitutions: map[*substitution]*availability{},
	}
	g.own = &availability{level: DeclaredInstances}
	g.nodes = append(g.nodes, g.own)
	for p := range omitted {
		g.pairing(p)
		if g.exhausted {
			return nil
		}
	}
	for i := 1; i < len(g.nodes); i++ {
		n := g.nodes[i]
		if n.isPairing {
			contexts := c.pairings[n.pairing]
			if len(contexts) == 0 {
				g.require(n, g.own, nil, n.pairing.level) // unknown occurrence
			}
			for context := range contexts {
				g.require(n, g.substitution(context.demand), g.substitution(context.value), n.pairing.level)
				if g.exhausted {
					return nil
				}
			}
		} else {
			s := n.subst
			outer := g.substitution(s.outer)
			if s.ownUse {
				g.require(n, g.own, outer, DeclaredInstances)
			}
			for _, u := range s.users {
				switch {
				case u.statement:
					// statementSites attributes reports, not actual containment.
					g.require(n, g.own, outer, DeclaredInstances)
				case u.pairings != nil:
					for _, p := range u.pairings {
						g.require(n, g.pairing(p), outer, DeclaredInstances)
						if g.exhausted {
							return nil
						}
					}
				case u.subst != nil:
					g.require(n, g.substitution(u.subst), outer, DeclaredInstances)
				default:
					g.require(n, g.own, outer, DeclaredInstances)
				}
				if g.exhausted {
					return nil
				}
			}
		}
		if g.exhausted {
			return nil
		}
	}
	ready := []*availability{g.own}
	for i := 0; i < len(ready); i++ {
		n := ready[i]
		for _, rule := range n.next {
			level := min(n.level, rule.ceiling)
			if rule.other != nil {
				level = min(level, rule.other.level)
			}
			if level > rule.target.level {
				rule.target.level = level
				ready = append(ready, rule.target)
			}
		}
	}
	levels := map[pairing]Specialization{}
	for p := range omitted {
		// Absent even at AllInstances is no stronger a cap for Within's later fallbacks.
		levels[p] = max(AllInstances, g.pairings[p].level)
	}
	return levels
}

type availability struct {
	level     Specialization // -1 until a source makes it
	pairing   pairing
	isPairing bool
	subst     *substitution
	next      []availabilityRule
}

type availabilityRule struct {
	other, target *availability
	ceiling       Specialization
}

type availabilityGraph struct {
	own           *availability
	nodes         []*availability
	pairings      map[pairing]*availability
	substitutions map[*substitution]*availability
	rules         int
	exhausted     bool
}

func (g *availabilityGraph) add(n *availability) *availability {
	if g.exhausted || len(g.nodes)+g.rules >= maxContainingSteps {
		g.exhausted = true
		return g.own
	}
	n.level = -1
	g.nodes = append(g.nodes, n)
	return n
}

func (g *availabilityGraph) pairing(p pairing) *availability {
	if n := g.pairings[p]; n != nil {
		return n
	}
	n := g.add(&availability{pairing: p, isPairing: true})
	if !g.exhausted {
		g.pairings[p] = n
	}
	return n
}

func (g *availabilityGraph) substitution(s *substitution) *availability {
	s = instanceUse(s).subst
	if s == nil {
		return g.own
	}
	if n := g.substitutions[s]; n != nil {
		return n
	}
	n := g.add(&availability{subst: s})
	if !g.exhausted {
		g.substitutions[s] = n
	}
	return n
}

// require adds a rule: both a and b (if any) make target, up to ceiling. Each node's
// level rises at most three times, so propagation takes linear work in this bounded graph.
func (g *availabilityGraph) require(target, a, b *availability, ceiling Specialization) {
	rules := 1
	if b != nil && b != a {
		rules++
	}
	if g.exhausted || len(g.nodes)+g.rules+rules > maxContainingSteps {
		g.exhausted = true
		return
	}
	a.next = append(a.next, availabilityRule{other: b, target: target, ceiling: ceiling})
	if rules == 2 {
		b.next = append(b.next, availabilityRule{other: a, target: target, ceiling: ceiling})
	}
	g.rules += rules
}
