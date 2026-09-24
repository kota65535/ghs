package diff

import "testing"

// labelsIn builds the matches a collection of labels produces, which is what
// PairRenames is handed.
func labelsIn(current, desired map[string]map[string]any) []ElementMatch {
	return MatchElements("labels", current, desired)
}

func TestPairRenamesFoldsTheUnambiguousPair(t *testing.T) {
	current := map[string]map[string]any{
		"bug": element(map[string]any{"id": float64(1), "name": "bug", "color": "d73a4a", "description": "Something is not working"}),
	}
	desired := map[string]map[string]any{
		"defect": element(map[string]any{"name": "defect", "color": "d73a4a", "description": "Something is not working"}),
	}

	folded := PairRenames(labelsIn(current, desired))

	if len(folded) != 1 {
		t.Fatalf("got %d matches, want the delete and the create folded into one: %+v", len(folded), folded)
	}
	match := folded[0]
	if match.Action != ActionUpdate {
		t.Errorf("action = %s, want %s", match.Action, ActionUpdate)
	}
	if match.Name != "defect" {
		t.Errorf("name = %q, want the declared one", match.Name)
	}
	// Update addresses the label as GitHub knows it, so the reported element
	// has to be the one being renamed rather than the declaration.
	if match.Current["name"] != "bug" {
		t.Errorf("current = %+v, want the reported label", match.Current)
	}
	if match.Desired["name"] != "defect" {
		t.Errorf("desired = %+v, want the declaration", match.Desired)
	}
}

func TestPairRenamesComparesOnlyDeclaredFields(t *testing.T) {
	// The file says nothing about the description, so it is not managed and
	// not a reason to call these different labels.
	current := map[string]map[string]any{
		"bug": element(map[string]any{"id": float64(1), "name": "bug", "color": "d73a4a", "description": "Reported by the API"}),
	}
	desired := map[string]map[string]any{
		"defect": element(map[string]any{"name": "defect", "color": "d73a4a"}),
	}

	folded := PairRenames(labelsIn(current, desired))

	if len(folded) != 1 || folded[0].Action != ActionUpdate {
		t.Fatalf("got %+v, want one update", folded)
	}
}

func TestPairRenamesLeavesADifferentLabelAlone(t *testing.T) {
	// A colour the file changes is a label it does not claim is the old one.
	current := map[string]map[string]any{
		"bug": element(map[string]any{"name": "bug", "color": "d73a4a"}),
	}
	desired := map[string]map[string]any{
		"defect": element(map[string]any{"name": "defect", "color": "0e8a16"}),
	}

	folded := PairRenames(labelsIn(current, desired))

	if len(folded) != 2 {
		t.Fatalf("got %d matches, want the delete and the create kept apart: %+v", len(folded), folded)
	}
	byName := map[string]Action{}
	for _, match := range folded {
		byName[match.Name] = match.Action
	}
	if byName["bug"] != ActionDelete || byName["defect"] != ActionCreate {
		t.Errorf("got %+v, want bug deleted and defect created", byName)
	}
}

func TestPairRenamesLeavesTheUndecidableAlone(t *testing.T) {
	// Two labels of the same colour deleted, two created: every pairing is as
	// good as the others, so none of them is the one that was meant.
	current := map[string]map[string]any{
		"bug":  element(map[string]any{"name": "bug", "color": "d73a4a"}),
		"task": element(map[string]any{"name": "task", "color": "d73a4a"}),
	}
	desired := map[string]map[string]any{
		"defect": element(map[string]any{"name": "defect", "color": "d73a4a"}),
		"chore":  element(map[string]any{"name": "chore", "color": "d73a4a"}),
	}

	folded := PairRenames(labelsIn(current, desired))

	if len(folded) != 4 {
		t.Fatalf("got %d matches, want all four left as they were: %+v", len(folded), folded)
	}
	for _, match := range folded {
		if match.Action == ActionUpdate {
			t.Errorf("%s was folded into an update, which no pairing justifies", match.Name)
		}
	}
}

func TestPairRenamesLeavesTwoCreatesOnOneDeleteAlone(t *testing.T) {
	// One label deleted, two created that equal it: which one it became is not
	// something the file says.
	current := map[string]map[string]any{
		"bug": element(map[string]any{"name": "bug", "color": "d73a4a"}),
	}
	desired := map[string]map[string]any{
		"defect": element(map[string]any{"name": "defect", "color": "d73a4a"}),
		"fault":  element(map[string]any{"name": "fault", "color": "d73a4a"}),
	}

	folded := PairRenames(labelsIn(current, desired))

	if len(folded) != 3 {
		t.Fatalf("got %d matches, want all three left as they were: %+v", len(folded), folded)
	}
	for _, match := range folded {
		if match.Action == ActionUpdate {
			t.Errorf("%s was folded into an update, which no pairing justifies", match.Name)
		}
	}
}

func TestPairRenamesLeavesOrdinaryChangesAlone(t *testing.T) {
	current := map[string]map[string]any{
		"bug":  element(map[string]any{"name": "bug", "color": "d73a4a"}),
		"gone": element(map[string]any{"name": "gone", "color": "ffffff"}),
	}
	desired := map[string]map[string]any{
		"bug": element(map[string]any{"name": "bug", "color": "0e8a16"}),
		"new": element(map[string]any{"name": "new", "color": "111111"}),
	}

	folded := PairRenames(labelsIn(current, desired))

	if len(folded) != 3 {
		t.Fatalf("got %d matches, want the update, the create and the delete: %+v", len(folded), folded)
	}
	byName := map[string]Action{}
	for _, match := range folded {
		byName[match.Name] = match.Action
	}
	want := map[string]Action{"bug": ActionUpdate, "new": ActionCreate, "gone": ActionDelete}
	for name, action := range want {
		if byName[name] != action {
			t.Errorf("%s = %s, want %s", name, byName[name], action)
		}
	}
}
