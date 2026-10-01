package diff

// PairRenames folds a delete and a create that differ only by key into the
// single update they most likely were meant to be. key is the field the
// collection matches its elements on, and arrays what identifies the elements
// of the arrays among an element's fields, as Compute takes it.
//
// A settings file says what should be there, not how to get there, so a label
// declared as `defect` where GitHub reports `bug` reads as two unrelated
// facts: `bug` is gone, `defect` is new. Applied literally that is a delete
// and a create, and that loses what GitHub keeps about the element besides its
// settings: a deleted label comes off every issue and pull request that
// carried it, and a ruleset created anew gets a new id and history. Where the
// API renames in place -- the schema says so with Rename -- the matches are
// passed through here first.
//
// A declaration that holds nothing but its key is left alone: it equals every
// reported element apart from the key, so it says nothing about which one it
// was.
//
// Only an unambiguous pairing is folded. Where one create equals two deletes,
// or one delete equals two creates, there is no way to tell which was meant,
// and the literal reading stands: the plan shows the delete and the create,
// and apply does exactly that.
func PairRenames(matches []ElementMatch, key string, arrays Matches) []ElementMatch {
	renamed := make(map[int]int, len(matches))
	claimed := make(map[int]int, len(matches))
	for create := range matches {
		if matches[create].Action != ActionCreate {
			continue
		}
		deleted, found := soleMatch(matches, create, key, arrays)
		if !found {
			continue
		}
		renamed[create] = deleted
		// A delete equalled by two creates is as undecidable as the other way
		// round, so finding it from one side settles nothing on its own.
		claimed[deleted]++
	}

	dropped := make(map[int]bool, len(renamed))
	for create, deleted := range renamed {
		if claimed[deleted] > 1 {
			continue
		}
		matches[create].Action = ActionUpdate
		matches[create].Current = matches[deleted].Current
		dropped[deleted] = true
	}

	folded := make([]ElementMatch, 0, len(matches))
	for i := range matches {
		if !dropped[i] {
			folded = append(folded, matches[i])
		}
	}
	return folded
}

// soleMatch returns the one delete whose reported element equals what the
// create declares, key aside. It reports false where none does, or more than
// one does.
func soleMatch(matches []ElementMatch, create int, key string, arrays Matches) (int, bool) {
	if len(matches[create].Desired) < 2 {
		// The key alone, or less: nothing to tell one element from another.
		return 0, false
	}
	found, count := 0, 0
	for i := range matches {
		if matches[i].Action != ActionDelete {
			continue
		}
		if !equalApartFrom(key, matches[i].Current, matches[create].Desired, arrays) {
			continue
		}
		found, count = i, count+1
	}
	return found, count == 1
}

// equalApartFrom reports whether a reported element is what the declaration
// asks for, key aside.
//
// It is the comparison a plan makes of any element, so that a rename is
// recognised exactly where the plan would report the key and nothing else:
// only the declared fields are compared, a default GitHub fills in is not a
// difference, and the elements of a keyed array are paired whatever order
// GitHub reports them in.
func equalApartFrom(key string, current, desired map[string]any, arrays Matches) bool {
	if current == nil || desired == nil {
		return false
	}
	rest := make(map[string]any, len(desired))
	for field, value := range desired {
		if field != key {
			rest[field] = value
		}
	}
	return len(Compute(current, rest, arrays)) == 0
}
