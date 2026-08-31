package diff

// Plan is what apply would do at one node of the settings file, and at every
// node below it.
//
// It has the shape of the file it came from, so that reporting it is a matter
// of walking it rather than of reading paths back apart.
type Plan struct {
	// Name is the key this node is written under, empty for the root.
	Name string

	// Path is where the node sits in the settings file, empty for the root.
	Path string

	// Collection reports whether the node holds elements rather than fields.
	Collection bool

	// Fields are the changes to this node's own fields. A collection has none:
	// its fields belong to its elements.
	Fields []Change

	// Elements are the changes to a collection's elements.
	Elements []ElementDiff

	// Children are the plans for the nodes written under this one.
	Children []*Plan
}

// ElementDiff is what apply would do about one element of a collection.
type ElementDiff struct {
	Name string
	Path string

	// Action is whether the element is arriving, going, or staying and
	// changing.
	Action Action

	// Fields are the changed fields of an element that stays. Values is the
	// whole of one that arrives or goes, which is what there is to review
	// about it.
	Fields []Change
	Values map[string]any

	// Current is the element as the API reports it, which is what addresses it
	// where GitHub issues the address rather than taking the name. It is nil
	// for an element being created.
	Current map[string]any

	// Children are the plans for the collections this element owns, such as
	// the variables of an environment.
	Children []*Plan
}

// Empty reports a plan that would do nothing.
func (p *Plan) Empty() bool {
	if len(p.Fields) > 0 {
		return false
	}
	for _, element := range p.Elements {
		if !element.Empty() {
			return false
		}
	}
	for _, child := range p.Children {
		if !child.Empty() {
			return false
		}
	}
	return true
}

// Empty reports an element that would not be touched.
func (e ElementDiff) Empty() bool {
	if e.Action != ActionUpdate {
		return false
	}
	if len(e.Fields) > 0 {
		return false
	}
	for _, child := range e.Children {
		if !child.Empty() {
			return false
		}
	}
	return true
}

// Prune returns the plan with everything that would do nothing left out, or nil
// when that is all of it.
//
// Reporting a node that does not differ would bury the changes among the
// settings that are already as declared.
func (p *Plan) Prune() *Plan {
	if p == nil || p.Empty() {
		return nil
	}

	pruned := &Plan{
		Name:       p.Name,
		Path:       p.Path,
		Collection: p.Collection,
		Fields:     p.Fields,
	}

	for _, element := range p.Elements {
		if element.Empty() {
			continue
		}
		kept := ElementDiff{
			Name:    element.Name,
			Path:    element.Path,
			Action:  element.Action,
			Fields:  element.Fields,
			Values:  element.Values,
			Current: element.Current,
		}
		for _, child := range element.Children {
			if child := child.Prune(); child != nil {
				kept.Children = append(kept.Children, child)
			}
		}
		pruned.Elements = append(pruned.Elements, kept)
	}

	for _, child := range p.Children {
		if child := child.Prune(); child != nil {
			pruned.Children = append(pruned.Children, child)
		}
	}

	return pruned
}

// Summarize counts what a plan amounts to, by object rather than by field: a
// node with four changed fields is one object to change, the same as an element
// with one.
func (p *Plan) Summarize() Summary {
	var s Summary
	p.summarize(&s)
	return s
}

func (p *Plan) summarize(s *Summary) {
	if p == nil {
		return
	}
	if len(p.Fields) > 0 {
		s.Changed++
	}
	for _, element := range p.Elements {
		switch element.Action {
		case ActionCreate:
			s.Created++
		case ActionDelete:
			s.Deleted++
		default:
			// An element counts as changed when anything about it differs,
			// including the collections it owns: adding a variable to an
			// environment changes that environment.
			//
			// Those collections are not walked into. Their entries are not
			// objects in their own right -- a variable is not a thing that
			// exists apart from its environment -- and the element that owns
			// them has just been counted.
			if !element.Empty() {
				s.Changed++
			}
		}
	}
	for _, child := range p.Children {
		child.summarize(s)
	}
}
