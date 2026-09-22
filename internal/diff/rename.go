package diff

import "reflect"

// PairRenames folds a delete and a create that differ only by name into the
// single update they most likely were meant to be.
//
// A settings file says what should be there, not how to get there, so a label
// declared as `defect` where GitHub reports `bug` reads as two unrelated
// facts: `bug` is gone, `defect` is new. Applied literally that is a delete
// and a create, and for most collections the two are the same thing -- a
// ruleset created with the settings of the one just deleted is that ruleset.
//
// For labels it is not. Deleting a label takes it off every issue and pull
// request that carried it, and creating one under the new name does not put it
// back. GitHub can rename in place, so the collections that can say so -- see
// resource.Renamer -- have their matches passed through here first.
//
// Only an unambiguous pairing is folded. Where one create equals two deletes,
// or one delete equals two creates, there is no way to tell which was meant,
// and the literal reading stands: the plan shows the delete and the create,
// and apply does exactly that.
func PairRenames(matches []ElementMatch) []ElementMatch {
	renamed := make(map[int]int, len(matches))
	claimed := make(map[int]int, len(matches))
	for create := range matches {
		if matches[create].Action != ActionCreate {
			continue
		}
		deleted, found := soleMatch(matches, create)
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
// create declares, name aside. It reports false where none does, or more than
// one does.
func soleMatch(matches []ElementMatch, create int) (int, bool) {
	found, count := 0, 0
	for i := range matches {
		if matches[i].Action != ActionDelete {
			continue
		}
		if !equalApartFromName(matches[i].Current, matches[create].Desired) {
			continue
		}
		found, count = i, count+1
	}
	return found, count == 1
}

// equalApartFromName reports whether a reported element carries every field the
// declaration asks for, name aside, with the value it asks for.
//
// Only the declared fields are compared, because only they are managed: an id
// or a description the file says nothing about is not a difference, here or
// anywhere else in a plan.
func equalApartFromName(current, desired map[string]any) bool {
	if current == nil || desired == nil {
		return false
	}
	for field, value := range desired {
		if field == NameField {
			continue
		}
		if !reflect.DeepEqual(current[field], value) {
			return false
		}
	}
	return true
}
